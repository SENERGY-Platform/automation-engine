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

package controller

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/prepare"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// SetParameters enables start parameters with options (SMS-08); userToken serves starts without
// a caller, e.g. by the watcher.
func (this *Controller) SetParameters(describer *prepare.Describer, userToken auth.UserTokenSource) {
	this.parameters = describer
	this.parametersUserToken = userToken
}

// DeploymentParameters describes the start parameters of a deployment with their options; it
// needs execute on the deployment, as before for releases (SMS-08).
func (this *Controller) DeploymentParameters(ctx context.Context, token string, namespace string, id string) ([]model.ParameterDescription, error) {
	var start *bpmn.Element
	err := this.store.Tx(ctx, func(tx *store.Tx) error {
		d, err := this.readDeployment(ctx, tx, token, namespace, id, Execute, false)
		if err != nil {
			return err
		}
		proc, err := this.engine.Process(tx, d.Id, d.Version)
		start = proc.NoneStartEvent()
		return err
	})
	if err != nil {
		return nil, err
	}
	return this.describe(ctx, token, start)
}

// usableConcurrency bounds the deployments DeploymentsUsable describes at once; each asks
// device-selection and device-repository
const usableConcurrency = 16

// usableTTL is how long a computed usable flag is served without asking again; a device the
// user adds shows in the flag after this time at the latest, the parameters of a single
// deployment are always described fresh
const usableTTL = 5 * time.Minute

type usableEntry struct {
	usable bool
	at     time.Time
}

// usableFlags keeps computed usable flags per user, deployment and version
type usableFlags struct {
	mux     sync.Mutex
	entries map[string]usableEntry
}

func (this *usableFlags) get(key string, now time.Time) (bool, bool) {
	this.mux.Lock()
	defer this.mux.Unlock()
	entry, ok := this.entries[key]
	if !ok || now.Sub(entry.at) > usableTTL {
		return false, false
	}
	return entry.usable, true
}

func (this *usableFlags) set(key string, usable bool, now time.Time) {
	this.mux.Lock()
	defer this.mux.Unlock()
	if this.entries == nil {
		this.entries = map[string]usableEntry{}
	}
	for k, entry := range this.entries {
		if now.Sub(entry.at) > usableTTL {
			delete(this.entries, k)
		}
	}
	this.entries[key] = usableEntry{usable: usable, at: now}
}

// DeploymentsUsable reports per deployment whether every start parameter of its current
// version has an option for the caller, the usable flag of release lists (SMS-08). Deployments
// without the execute right, or unknown ones, are not usable. It checks the rights once and
// holds no database connection while it asks other services; each description costs
// device-selection about 100 ms of work, so a list of a few dozen releases takes seconds.
// A flag that is not computed when ctx ends is missing from the result; its computation goes
// on and serves the next call from a cache for a few minutes.
func (this *Controller) DeploymentsUsable(ctx context.Context, token string, namespace string, ids []string) (map[string]bool, error) {
	user, _, err := this.begin(token, namespace)
	if err != nil {
		return nil, err
	}
	access, err := this.auth.Access(ctx, user, namespace, KindDeployment, Execute)
	if err != nil {
		return nil, err
	}
	type pending struct {
		id, key string
		start   *bpmn.Element
	}
	result := map[string]bool{}
	var todo []pending
	now := time.Now()
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		for _, id := range ids {
			result[id] = false
			d, err := tx.GetDeployment(id, false)
			if err != nil || d.Namespace != namespace {
				continue
			}
			if !access.Admin && d.Owner != access.Owner && !slices.Contains(access.Ids, d.Id) {
				continue
			}
			key := user.GetUserId() + "|" + d.Id + "|" + strconv.Itoa(d.Version)
			if usable, ok := this.usable.get(key, now); ok {
				result[id] = usable
				continue
			}
			proc, err := this.engine.Process(tx, d.Id, d.Version)
			if err != nil {
				return err
			}
			delete(result, id)
			todo = append(todo, pending{id: id, key: key, start: proc.NoneStartEvent()})
		}
		return nil
	})
	if err != nil || len(todo) == 0 {
		return result, err
	}
	// the work outlives ctx, so that a flag missing now is cached for the next call
	work := prepare.WithSelectablesCache(context.WithoutCancel(ctx))
	var mux sync.Mutex
	computed := map[string]bool{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		slots := make(chan struct{}, usableConcurrency)
		for _, p := range todo {
			wg.Add(1)
			slots <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-slots }()
				parameters, err := this.describe(work, token, p.start)
				if err != nil {
					return
				}
				usable := !slices.ContainsFunc(parameters, func(d model.ParameterDescription) bool { return d.HasNoValidOption })
				this.usable.set(p.key, usable, time.Now())
				mux.Lock()
				computed[p.id] = usable
				mux.Unlock()
			}()
		}
		wg.Wait()
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	mux.Lock()
	defer mux.Unlock()
	for id, usable := range computed {
		result[id] = usable
	}
	return result, nil
}

// MaintenanceParameters describes the parameters of a maintenance procedure (SMS-13).
func (this *Controller) MaintenanceParameters(ctx context.Context, token string, namespace string, id string, procedure string) ([]model.ParameterDescription, error) {
	var start *bpmn.Element
	err := this.store.Tx(ctx, func(tx *store.Tx) error {
		inst, err := this.readInstance(ctx, tx, token, namespace, id, Read)
		if err != nil {
			return err
		}
		proc, err := this.engine.Process(tx, inst.DeploymentId, inst.DeploymentVersion)
		if err != nil {
			return err
		}
		for _, p := range procedures(proc) {
			if p.Id == procedure {
				start = proc.Element(p.ElementId)
			}
		}
		if start == nil {
			return model.NotFound("instance %v has no maintenance procedure %v", id, procedure)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return this.describe(ctx, token, start)
}

func (this *Controller) describe(ctx context.Context, token string, start *bpmn.Element) ([]model.ParameterDescription, error) {
	if this.parameters == nil {
		return prepare.NewDescriber("", nil).Describe(ctx, token, start)
	}
	return this.parameters.Describe(ctx, token, start)
}

// withAutoSelected adds the values of auto_select_all fields of a start event: every option
// the token may select; given values stay
func (this *Controller) withAutoSelected(ctx context.Context, token string, deploymentId string, version int, elementId string, variables map[string]any) (map[string]any, error) {
	if this.parameters == nil {
		return variables, nil
	}
	var start *bpmn.Element
	err := this.store.Tx(ctx, func(tx *store.Tx) error {
		if version == 0 {
			d, err := tx.GetDeployment(deploymentId, false)
			if err != nil {
				return err
			}
			version = d.Version
		}
		proc, err := this.engine.Process(tx, deploymentId, version)
		if err != nil {
			return err
		}
		if elementId == "" {
			start = proc.NoneStartEvent()
		} else {
			start = proc.Element(elementId)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	auto, err := this.parameters.AutoSelected(ctx, token, start)
	if err != nil || len(auto) == 0 {
		return variables, err
	}
	result := maps.Clone(auto)
	maps.Copy(result, variables)
	return result, nil
}
