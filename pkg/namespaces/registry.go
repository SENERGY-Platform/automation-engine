/*
 * Copyright 2026 InfAI (CC SES)
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *    http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package namespaces keeps the namespaces in the database and in a cache (NS-02).
package namespaces

import (
	"context"
	"log/slog"
	"reflect"
	"sync"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// Topics creates and removes the permissions-v2 topics of a namespace (NS-05).
type Topics interface {
	EnsureTopics(ctx context.Context, ns model.Namespace) error
	RemoveTopics(ctx context.Context, name string) error
}

type Registry struct {
	store  *store.Store
	topics Topics
	logger *slog.Logger
	now    func() time.Time

	mux    sync.RWMutex
	byName map[string]model.Namespace
	order  []string
	// topics already ensured, by namespace and configuration
	ensured map[string]model.Namespace
}

// New stores the seed namespaces that do not exist yet and loads all namespaces.
func New(ctx context.Context, s *store.Store, seed []model.Namespace, topics Topics, logger *slog.Logger) (*Registry, error) {
	r := &Registry{store: s, topics: topics, logger: logger, now: time.Now, byName: map[string]model.Namespace{}, ensured: map[string]model.Namespace{}}
	err := s.Tx(ctx, func(tx *store.Tx) error {
		for _, ns := range seed {
			ns = ns.WithDefaults()
			if err := ns.Validate(); err != nil {
				return err
			}
			if _, err := tx.InsertNamespace(ns, r.now()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err = r.Reload(ctx); err != nil {
		return nil, err
	}
	// once at start, so that shares work from the first request; failures are retried in Run
	r.ensureTopics(ctx)
	return r, nil
}

func (this *Registry) Reload(ctx context.Context) error {
	var list []model.Namespace
	err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		list, err = tx.ListNamespaces()
		return err
	})
	if err != nil {
		return err
	}
	byName := map[string]model.Namespace{}
	order := make([]string, 0, len(list))
	for _, ns := range list {
		ns = ns.WithDefaults()
		byName[ns.Name] = ns
		order = append(order, ns.Name)
	}
	this.mux.Lock()
	this.byName, this.order = byName, order
	this.mux.Unlock()
	return nil
}

func (this *Registry) Get(name string) (model.Namespace, bool) {
	this.mux.RLock()
	defer this.mux.RUnlock()
	ns, ok := this.byName[name]
	return ns, ok
}

func (this *Registry) List() []model.Namespace {
	this.mux.RLock()
	defer this.mux.RUnlock()
	result := make([]model.Namespace, 0, len(this.order))
	for _, name := range this.order {
		result = append(result, this.byName[name])
	}
	return result
}

// Put creates or updates a namespace; its topics are written first, so that a namespace never
// exists without them. A level that becomes registered gets its resources registered (NS-05).
func (this *Registry) Put(ctx context.Context, ns model.Namespace) (result model.Namespace, created bool, err error) {
	ns = ns.WithDefaults()
	if err = ns.Validate(); err != nil {
		return ns, false, err
	}
	if this.topics != nil {
		if err = this.topics.EnsureTopics(ctx, ns); err != nil {
			return ns, false, model.Unavailable("unable to write permissions-v2 topics: %v", err)
		}
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		now := this.now()
		old, existed := this.Get(ns.Name)
		created, err = tx.InsertNamespace(ns, now)
		if err != nil || created {
			return err
		}
		if err = tx.UpdateNamespace(ns, now); err != nil {
			return err
		}
		for _, kind := range model.Kinds {
			if existed && !old.Level(kind).Registered() && ns.Level(kind).Registered() {
				if err = tx.MarkNamespaceUnregistered(kind, ns.Name); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return ns, false, err
	}
	this.markEnsured(ns)
	if err = this.Reload(ctx); err != nil {
		return ns, created, err
	}
	result, _ = this.Get(ns.Name)
	return result, created, nil
}

// Delete removes a namespace without resources together with its topics (NS-05).
func (this *Registry) Delete(ctx context.Context, name string) error {
	err := this.store.Tx(ctx, func(tx *store.Tx) error {
		if _, ok := this.Get(name); !ok {
			return model.NotFound("namespace %v not found", name)
		}
		inUse, err := tx.NamespaceInUse(name)
		if err != nil {
			return err
		}
		if inUse {
			return model.Conflict("namespace %v still has models, deployments or instances", name)
		}
		return tx.DeleteNamespace(name)
	})
	if err != nil {
		return err
	}
	if this.topics != nil {
		if err = this.topics.RemoveTopics(ctx, name); err != nil {
			this.logger.Warn("unable to remove permissions-v2 topics of a deleted namespace", "namespace", name, "error", err)
		}
	}
	return this.Reload(ctx)
}

func (this *Registry) markEnsured(ns model.Namespace) {
	this.mux.Lock()
	defer this.mux.Unlock()
	this.ensured[ns.Name] = ns
}

// ensureTopics writes topics of namespaces whose configuration this replica has not written
// yet, e.g. seeded ones or ones changed by another replica
func (this *Registry) ensureTopics(ctx context.Context) {
	if this.topics == nil {
		return
	}
	for _, ns := range this.List() {
		this.mux.RLock()
		done, ok := this.ensured[ns.Name]
		this.mux.RUnlock()
		if ok && reflect.DeepEqual(done, ns) {
			continue
		}
		if err := this.topics.EnsureTopics(ctx, ns); err != nil {
			this.logger.Warn("unable to write permissions-v2 topics", "namespace", ns.Name, "error", err)
			continue
		}
		this.markEnsured(ns)
	}
}

// Run reloads the namespaces periodically, so that changes of other replicas arrive, and
// writes missing topics.
func (this *Registry) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		this.ensureTopics(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := this.Reload(ctx); err != nil && ctx.Err() == nil {
				this.logger.Warn("unable to reload namespaces", "error", err)
			}
		}
	}
}
