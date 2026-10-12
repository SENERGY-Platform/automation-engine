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
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/google/uuid"
)

// ListSchedules lists the schedules of the caller; admins see all (RUN-07).
func (this *Controller) ListSchedules(ctx context.Context, token string, namespace string, options model.ScheduleListOptions) (result model.Page[model.Schedule], err error) {
	user, _, err := this.begin(token, namespace)
	if err != nil {
		return result, err
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		result, err = tx.ListSchedules(namespace, options, store.Access{Admin: user.IsAdmin(), Owner: user.GetUserId()})
		return err
	})
	return result, err
}

func (this *Controller) readSchedule(tx *store.Tx, token string, namespace string, id string) (model.Schedule, error) {
	user, _, err := this.begin(token, namespace)
	if err != nil {
		return model.Schedule{}, err
	}
	s, err := tx.GetSchedule(id)
	if err != nil {
		return s, err
	}
	if s.Namespace != namespace || (!user.IsAdmin() && s.Owner != user.GetUserId()) {
		return s, model.NotFound("schedule %v not found", id)
	}
	return s, nil
}

func (this *Controller) GetSchedule(ctx context.Context, token string, namespace string, id string) (result model.Schedule, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		result, err = this.readSchedule(tx, token, namespace, id)
		return err
	})
	return result, err
}

// CreateSchedule needs the execute right on the deployment; the starts run as its owner.
func (this *Controller) CreateSchedule(ctx context.Context, token string, namespace string, input model.ScheduleInput) (result model.Schedule, err error) {
	user, _, err := this.begin(token, namespace)
	if err != nil {
		return result, err
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		if _, err := this.readDeployment(ctx, tx, token, namespace, input.DeploymentId, Execute, false); err != nil {
			return err
		}
		now := time.Now().UTC()
		result = model.Schedule{Id: uuid.NewString(), Namespace: namespace, DeploymentId: input.DeploymentId, Owner: user.GetUserId(),
			Cron: input.Cron, Alias: input.Alias, Disabled: input.Disabled, CreatedAt: now, UpdatedAt: now}
		if result.NextDue, err = this.engine.NextScheduleDue(model.Schedule{Cron: input.Cron}, this.engine.Now()); err != nil {
			return err
		}
		if result.Disabled {
			result.NextDue = nil
		}
		return tx.SaveSchedule(result)
	})
	return result, err
}

func (this *Controller) UpdateSchedule(ctx context.Context, token string, namespace string, id string, input model.ScheduleInput) (result model.Schedule, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		result, err = this.readSchedule(tx, token, namespace, id)
		if err != nil {
			return err
		}
		if input.DeploymentId != "" && input.DeploymentId != result.DeploymentId {
			if _, err := this.readDeployment(ctx, tx, token, namespace, input.DeploymentId, Execute, false); err != nil {
				return err
			}
			result.DeploymentId = input.DeploymentId
		}
		result.Cron, result.Alias, result.Disabled = input.Cron, input.Alias, input.Disabled
		result.UpdatedAt = time.Now().UTC()
		if result.NextDue, err = this.engine.NextScheduleDue(model.Schedule{Cron: input.Cron}, this.engine.Now()); err != nil {
			return err
		}
		if result.Disabled {
			result.NextDue = nil
		}
		return tx.SaveSchedule(result)
	})
	return result, err
}

func (this *Controller) DeleteSchedule(ctx context.Context, token string, namespace string, id string) error {
	return this.store.Tx(ctx, func(tx *store.Tx) error {
		if _, err := this.readSchedule(tx, token, namespace, id); err != nil {
			return err
		}
		return tx.DeleteSchedule(id)
	})
}

func (this *Controller) ioUser(token string) (string, bool, error) {
	user, err := this.parseToken(token)
	if err != nil {
		return "", false, err
	}
	if this.io == nil {
		return "", false, model.Unavailable("process-io is not configured")
	}
	return user.GetUserId(), user.IsAdmin(), nil
}

// ListIoValues lists the process-io values of the caller (IO-02).
func (this *Controller) ListIoValues(ctx context.Context, token string, options model.IoListOptions) (model.Page[model.IoValue], error) {
	user, _, err := this.ioUser(token)
	if err != nil {
		return model.Page[model.IoValue]{}, err
	}
	return this.io.List(ctx, user, options)
}

func (this *Controller) GetIoValue(ctx context.Context, token string, key string) (model.IoValue, error) {
	user, _, err := this.ioUser(token)
	if err != nil {
		return model.IoValue{}, err
	}
	v, ok, err := this.io.Get(ctx, user, key)
	if err == nil && !ok {
		return v, model.NotFound("key %v not found", key)
	}
	return v, err
}

func (this *Controller) SetIoValue(ctx context.Context, token string, key string, input model.IoValueInput) error {
	user, admin, err := this.ioUser(token)
	if err != nil {
		return err
	}
	return this.io.Set(ctx, user, admin, model.IoValue{Key: key, Value: input.Value, DeploymentId: input.DeploymentId, InstanceId: input.InstanceId})
}

func (this *Controller) DeleteIoValue(ctx context.Context, token string, key string) error {
	user, _, err := this.ioUser(token)
	if err != nil {
		return err
	}
	return this.io.Delete(ctx, user, key)
}

func (this *Controller) IoBulk(ctx context.Context, token string, bulk model.IoBulk) ([]model.IoValue, error) {
	user, admin, err := this.ioUser(token)
	if err != nil {
		return nil, err
	}
	return this.io.Bulk(ctx, user, admin, bulk)
}
