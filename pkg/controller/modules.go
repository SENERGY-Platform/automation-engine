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
	"errors"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// ListModules lists the modules of the instances a user may read (SMS-12).
func (this *Controller) ListModules(ctx context.Context, token string, namespace string, options model.ModuleListOptions) (result model.Page[model.Module], err error) {
	user, _, err := this.begin(token, namespace)
	if err != nil {
		return result, err
	}
	access, err := this.auth.Access(ctx, user, namespace, KindInstance, Read)
	if err != nil {
		return result, err
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		result, err = tx.ListModules(namespace, options, access)
		return err
	})
	return result, err
}

func (this *Controller) GetModule(ctx context.Context, token string, namespace string, id string) (result model.Module, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		result, err = this.readModule(ctx, tx, token, namespace, id, Read)
		return err
	})
	return result, err
}

// DeleteModule removes a module; its resource is removed through the outbox (SMS-06).
func (this *Controller) DeleteModule(ctx context.Context, token string, namespace string, id string) error {
	return this.store.Tx(ctx, func(tx *store.Tx) error {
		if _, err := this.readModule(ctx, tx, token, namespace, id, Administrate); err != nil {
			return err
		}
		return tx.DeleteModule(id)
	})
}

// readModule checks a right on the instance the module belongs to
func (this *Controller) readModule(ctx context.Context, tx *store.Tx, token string, namespace string, id string, right Right) (model.Module, error) {
	m, err := tx.GetModule(id)
	if err != nil {
		return m, err
	}
	if m.Namespace != namespace {
		return m, model.NotFound("module %v not found", id)
	}
	if _, err = this.readInstance(ctx, tx, token, namespace, m.InstanceId, right); err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return m, model.NotFound("module %v not found", id)
		}
		return m, err
	}
	return m, nil
}
