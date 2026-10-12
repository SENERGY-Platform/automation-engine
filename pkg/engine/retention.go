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
)

const retentionBatch = 50

// Retention returns the effective retention in days of a deployment's history: the deployment
// value or the default, capped by the namespace and the global maximum (HIST-04). The cap of the
// instance owner is applied separately.
func (this *Engine) Retention(namespace string, deploymentDays int) int {
	ns, _ := this.namespaces.Get(namespace)
	days := deploymentDays
	if days <= 0 {
		days = ns.Retention.DefaultDays
	}
	if days <= 0 {
		days = this.config.RetentionDefaultDays
	}
	for _, maximum := range []int{ns.Retention.MaxDays, this.config.RetentionMaxDays} {
		if maximum > 0 && days > maximum {
			days = maximum
		}
	}
	return days
}

// MaxRetention is the highest value a deployment of owner may set; 0 means unlimited.
func (this *Engine) MaxRetention(tx *store.Tx, namespace string, owner string) (int, error) {
	ns, _ := this.namespaces.Get(namespace)
	result := 0
	settings, err := tx.GetUserSettings(owner)
	if err != nil {
		return 0, err
	}
	for _, maximum := range []int{ns.Retention.MaxDays, this.config.RetentionMaxDays, settings.MaxRetentionDays} {
		if maximum > 0 && (result == 0 || maximum < result) {
			result = maximum
		}
	}
	return result, nil
}

func days(n int) time.Duration {
	return time.Duration(n) * 24 * time.Hour
}

// enforceRetention removes finished instances and old history of running ones, for a batch of
// deployments per call and then for every user with a lower limit. Lowered limits apply at once,
// also to existing data (HIST-04, HIST-06, LONG-03, LONG-05).
func (this *Engine) enforceRetention(ctx context.Context, now time.Time) error {
	var targets []store.RetentionTarget
	err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		targets, err = tx.DeploymentsAfter(this.retentionCursor, retentionBatch)
		return err
	})
	if err != nil {
		return err
	}
	if len(targets) < retentionBatch {
		this.retentionCursor = ""
	} else {
		this.retentionCursor = targets[len(targets)-1].Id
	}
	var errs []error
	for _, target := range targets {
		cutoff := now.Add(-days(this.Retention(target.Namespace, target.RetentionDays)))
		errs = append(errs, this.trim(ctx, "deployment_id", target.Id, cutoff))
	}
	var caps []model.UserSettings
	err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		caps, err = tx.UserRetentionCaps()
		return err
	})
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, c := range caps {
		errs = append(errs, this.trim(ctx, "owner", c.UserId, now.Add(-days(c.MaxRetentionDays))))
	}
	return errors.Join(errs...)
}

// trim deletes finished instances before the cutoff in small transactions, then old history
func (this *Engine) trim(ctx context.Context, column string, value string, cutoff time.Time) error {
	for {
		var deleted int64
		err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
			deleted, err = tx.DeleteFinishedInstances(column, value, cutoff, this.config.BatchSize)
			return err
		})
		if err != nil {
			return err
		}
		if deleted < int64(this.config.BatchSize) {
			break
		}
	}
	return this.store.Tx(ctx, func(tx *store.Tx) error {
		return tx.TrimHistory(column, value, cutoff)
	})
}

func (this *Engine) trimModelVersions(ctx context.Context) error {
	var ids []string
	err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		ids, err = tx.ModelsAfter(this.modelCursor, retentionBatch)
		return err
	})
	if err != nil {
		return err
	}
	if len(ids) < retentionBatch {
		this.modelCursor = ""
	} else {
		this.modelCursor = ids[len(ids)-1]
	}
	return this.store.Tx(ctx, func(tx *store.Tx) error {
		for _, id := range ids {
			if err := tx.TrimModelVersions(id, this.config.ModelVersionsKept); err != nil {
				return err
			}
		}
		return nil
	})
}
