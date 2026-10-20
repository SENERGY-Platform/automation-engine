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

package migrate

import (
	"context"
	"errors"
	"maps"

	"github.com/SENERGY-Platform/automation-engine/pkg/controller"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
)

// Rights reads and writes resources of permissions-v2 as admin.
type Rights interface {
	Rights(ctx context.Context, topic string, id string) (permmodel.ResourcePermissions, bool, error)
	SetRights(ctx context.Context, topic string, id string, permissions permmodel.ResourcePermissions) error
}

// Namespaces knows the namespaces of the engine.
type Namespaces interface {
	Get(name string) (model.Namespace, bool)
}

// Target is the engine the data goes into. Writes go through the controller with an admin
// token, so that deployments are activated and instances started as by the API; resources the
// API creates with new ids (models, schedules, IO values) go into the store with their ids.
type Target struct {
	Store      *store.Store
	Engine     *engine.Engine
	Controller *controller.Controller
	Namespaces Namespaces
	// an admin token, fetched per call since a migration may outlast a token
	AdminToken func() (string, error)
	// nil leaves rights alone, e.g. without permissions-v2
	Rights Rights
}

func (this *Target) token() string {
	token, err := this.AdminToken()
	if err != nil {
		return ""
	}
	return token
}

func (this *Target) exists(ctx context.Context, read func(tx *store.Tx) error) (bool, error) {
	err := this.Store.Tx(ctx, read)
	if errors.Is(err, model.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (this *Target) modelExists(ctx context.Context, id string) (bool, error) {
	return this.exists(ctx, func(tx *store.Tx) error { _, err := tx.GetModel(id, false); return err })
}

func (this *Target) deploymentExists(ctx context.Context, id string) (bool, error) {
	return this.exists(ctx, func(tx *store.Tx) error { _, err := tx.GetDeployment(id, false); return err })
}

// instance reads an instance of the engine; found is false if there is none
func (this *Target) instance(ctx context.Context, id string) (result model.Instance, found bool, err error) {
	found, err = this.exists(ctx, func(tx *store.Tx) error {
		i, err := tx.GetInstance(id, false)
		result = i.Instance
		return err
	})
	return result, found, err
}

func (this *Target) instanceExists(ctx context.Context, id string) (bool, error) {
	return this.exists(ctx, func(tx *store.Tx) error { _, err := tx.GetInstance(id, false); return err })
}

// merge adds the rights of from to into; nobody loses a right
func merge(into permmodel.ResourcePermissions, from permmodel.ResourcePermissions) permmodel.ResourcePermissions {
	or := func(a, b permmodel.PermissionsMap) permmodel.PermissionsMap {
		return permmodel.PermissionsMap{Read: a.Read || b.Read, Write: a.Write || b.Write, Execute: a.Execute || b.Execute, Administrate: a.Administrate || b.Administrate}
	}
	combine := func(a, b map[string]permmodel.PermissionsMap) map[string]permmodel.PermissionsMap {
		result := maps.Clone(a)
		if result == nil {
			result = map[string]permmodel.PermissionsMap{}
		}
		for k, v := range b {
			result[k] = or(result[k], v)
		}
		return result
	}
	return permmodel.ResourcePermissions{
		UserPermissions:  combine(into.UserPermissions, from.UserPermissions),
		GroupPermissions: combine(into.GroupPermissions, from.GroupPermissions),
		RolePermissions:  combine(into.RolePermissions, from.RolePermissions),
	}
}

// copyRights takes the rights of a resource from a topic of the old services into the topic of
// a namespace level, possibly under another id, with all rights for the owner (MIG-08, SEC-10). Users with an empty id,
// which a release creator can have, are dropped.
func (this *migration) copyRights(ctx context.Context, from string, fromId string, namespace string, kind model.Kind, id string, owner string) error {
	if this.target.Rights == nil || !this.options.Apply {
		return nil
	}
	ns, ok := this.target.Namespaces.Get(namespace)
	if !ok || !ns.Level(kind).Registered() {
		return nil
	}
	old, found, err := this.sources.Rights(ctx, from, fromId)
	if err != nil || !found {
		return err
	}
	topic := ns.Topic(kind)
	current, _, err := this.target.Rights.Rights(ctx, topic, id)
	if err != nil {
		return err
	}
	result := merge(current, old)
	delete(result.UserPermissions, "")
	result.UserPermissions[owner] = permmodel.PermissionsMap{Read: true, Write: true, Execute: true, Administrate: true}
	return this.target.Rights.SetRights(ctx, topic, id, result)
}
