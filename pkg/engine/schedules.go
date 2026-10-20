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

package engine

import (
	"context"
	"errors"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/timer"
)

// NextScheduleDue computes the next start of a schedule after from; nil if disabled. Starts are
// at least MinTimerDuration apart, counted from the due time s had if it has passed.
func (this *Engine) NextScheduleDue(s model.Schedule, from time.Time) (*time.Time, error) {
	if s.Disabled {
		return nil, nil
	}
	schedule, err := timer.ParseSchedule(s.Cron, this.config.Location)
	if err != nil {
		return nil, model.BadRequest("%v", err)
	}
	next := timer.NextScheduled(schedule, from, this.earliestDue(from, s.NextDue))
	if next.IsZero() {
		return nil, nil
	}
	return &next, nil
}

// fireSchedules starts instances of due schedules. Like timer start events, the deployment is
// locked and the due time read again, so that each start happens once across replicas; missed
// starts after a downtime happen once (RUN-07, LONG-12).
func (this *Engine) fireSchedules(ctx context.Context, now time.Time) error {
	var due []model.Schedule
	err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		due, err = tx.DueSchedules(now, this.config.BatchSize)
		return err
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, candidate := range due {
		errs = append(errs, this.store.Tx(ctx, func(tx *store.Tx) error {
			deployment, err := tx.GetDeployment(candidate.DeploymentId, true)
			if err != nil {
				return ignoreNotFound(err)
			}
			s, err := tx.GetSchedule(candidate.Id)
			if err != nil {
				return ignoreNotFound(err)
			}
			if s.Disabled || s.NextDue == nil || s.NextDue.After(now) {
				return nil
			}
			s.NextDue, err = this.NextScheduleDue(s, now)
			if err != nil {
				s.NextDue = nil
			}
			s.UpdatedAt = now
			if err = tx.SaveSchedule(s); err != nil {
				return err
			}
			if deployment.Disabled {
				this.logger.InfoContext(ctx, "scheduled start skipped, the deployment is disabled", "schedule_id", s.Id, "deployment_id", deployment.Id)
				return nil
			}
			_, _, err = this.startInstance(tx, StartRequest{DeploymentId: deployment.Id, Owner: deployment.Owner}, model.CauseStart)
			var modelErr *model.Error
			if errors.As(err, &modelErr) && modelErr.Kind != model.KindInternal && modelErr.Kind != model.KindUnavailable {
				this.logger.WarnContext(ctx, "scheduled start failed", "schedule_id", s.Id, "deployment_id", deployment.Id, "error", err)
				return tx.SaveSchedule(s)
			}
			return err
		}))
	}
	return errors.Join(errs...)
}
