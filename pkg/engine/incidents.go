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
	"fmt"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/google/uuid"
)

const NotificationTopic = "incident"

// handleIncident applies the incident handling of the deployment version (INC-02): it queues
// a notification and schedules a restart after a backoff that grows with consecutive incidents.
// Whether the restart happens is decided when it is due, see processRestarts.
func (this *runner) handleIncident(message string) error {
	handling, err := this.tx.GetIncidentHandling(this.inst.DeploymentId, this.inst.DeploymentVersion)
	if err != nil {
		return err
	}
	if handling.Restart {
		n, err := this.tx.ConsecutiveIncidentRuns(this.inst.Id)
		if err != nil {
			return err
		}
		due := this.now.Add(this.e.restartBackoff(this.inst.Namespace, n))
		this.inst.RestartAt = &due
		this.scheduled = true
		if err = this.log(model.ActivityRestartScheduled, nil, map[string]any{"due": due}); err != nil {
			return err
		}
	}
	if handling.Notify && this.inst.Owner != "" {
		text := message
		if handling.Restart {
			text += "\n\nprocess will be restarted"
		}
		return this.e.queueNotification(this.tx, this.inst.Owner, this.inst.Namespace, "Process-Incident in "+this.inst.DeploymentName, text)
	}
	return nil
}

func (this *Engine) restartBackoff(namespace string, consecutive int) time.Duration {
	ns, _ := this.namespaces.Get(namespace)
	_, initial, maximum := ns.WithDefaults().RestartPolicy.Durations()
	backoff := initial
	for i := 1; i < consecutive && backoff < maximum; i++ {
		backoff *= 2
	}
	return min(backoff, maximum)
}

// QueueNotification queues a notification to a user, sent after commit with retries; namespace
// is the one of the resource it is about, empty for none.
func (this *Engine) QueueNotification(ctx context.Context, userId string, namespace string, title string, message string) error {
	return this.store.Tx(ctx, func(tx *store.Tx) error {
		return this.queueNotification(tx, userId, namespace, title, message)
	})
}

func (this *Engine) queueNotification(tx *store.Tx, userId string, namespace string, title string, message string) error {
	return tx.InsertNotification(store.Notification{Id: uuid.NewString(), UserId: userId, Title: title, Message: message, Topic: NotificationTopic, Namespace: namespace, CreatedAt: this.now()})
}

// processRestarts restarts instances whose restart after an incident is due. It counts
// automatic restarts per deployment and marks the deployment faulty above the restart policy;
// a faulty deployment gets no automatic restarts until it is reset (INC-04). The deployment is
// locked before the instance, in the order deployment updates use.
func (this *Engine) processRestarts(ctx context.Context, now time.Time) error {
	var due []string
	err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		due, err = tx.DueRestarts(now, this.config.BatchSize)
		return err
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range due {
		errs = append(errs, this.store.Tx(ctx, func(tx *store.Tx) error {
			peek, err := tx.GetInstance(id, false)
			if err != nil {
				return ignoreNotFound(err)
			}
			deployment, err := tx.GetDeployment(peek.DeploymentId, true)
			if err != nil {
				return ignoreNotFound(err)
			}
			inst, err := tx.GetInstance(id, true)
			if err != nil {
				return ignoreNotFound(err)
			}
			if inst.RestartAt == nil || inst.RestartAt.After(now) || inst.State == model.StateRunning {
				return nil
			}
			inst.RestartAt = nil
			if err = tx.UpdateInstance(inst); err != nil {
				return err
			}
			allowed, err := this.countRestart(tx, deployment, now)
			if err != nil {
				return err
			}
			this.metrics.Restart(inst.Namespace, inst.DeploymentId, allowed)
			if !allowed {
				return nil
			}
			_, err = this.restartInstance(tx, id, 0, model.CauseIncident)
			if restartRefused(err) {
				// the restart itself is not possible, e.g. the current version has neither the start
				// event of the failed run nor one without definition (see restartStart); the instance
				// stays ended with its incident and the owner is told
				this.logger.Warn("restart after incident not possible", "instance_id", inst.Id, "deployment_id", inst.DeploymentId, "error", err)
				return this.queueNotification(tx, inst.Owner, inst.Namespace, "ERROR: unable to restart process after incident in: "+inst.DeploymentName, fmt.Sprintf("Restart-Error: %v\n\nIncident: %v", err, inst.EndReason))
			}
			return err
		}))
	}
	return errors.Join(errs...)
}

// countRestart counts an automatic restart in the window of the deployment and reports whether
// it may happen
func (this *Engine) countRestart(tx *store.Tx, deployment model.Deployment, now time.Time) (bool, error) {
	stats, err := tx.GetRestartStats(deployment.Id)
	if err != nil {
		return false, err
	}
	if stats.FaultySince != nil {
		return false, nil
	}
	ns, _ := this.namespaces.Get(deployment.Namespace)
	policy := ns.WithDefaults().RestartPolicy
	window, _, _ := policy.Durations()
	if now.Sub(stats.WindowStart) > window {
		stats.WindowStart = now
		stats.Count = 0
	}
	stats.Count++
	if stats.Count > policy.MaxRestarts {
		stats.FaultySince = &now
		if err = tx.SetRestartStats(deployment.Id, stats); err != nil {
			return false, err
		}
		this.logger.Warn("deployment marked faulty after restart loop", "deployment_id", deployment.Id, "restarts", policy.MaxRestarts, "window", policy.Window)
		return false, this.queueNotification(tx, deployment.Owner, deployment.Namespace, "Deployment stopped restarting: "+deployment.Name,
			fmt.Sprintf("Instances of this deployment were restarted %v times within %v after incidents. Automatic restarts are paused until the deployment is updated or reset.", policy.MaxRestarts, policy.Window))
	}
	return true, tx.SetRestartStats(deployment.Id, stats)
}

// ResetRestarts clears the faulty mark and the restart count of a deployment.
func (this *Engine) ResetRestarts(tx *store.Tx, deploymentId string) error {
	return tx.SetRestartStats(deploymentId, store.RestartStats{})
}

// processNotifications sends queued notifications; failures are retried with backoff and
// dropped after a day, so that a notifier outage does not fill the database
func (this *Engine) processNotifications(ctx context.Context, now time.Time) error {
	var due []store.Notification
	err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		due, err = tx.DueNotifications(now, this.config.BatchSize)
		return err
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, n := range due {
		var sendErr error
		if this.notifier != nil {
			sendErr = this.notifier.Notify(ctx, n)
		}
		errs = append(errs, this.store.Tx(ctx, func(tx *store.Tx) error {
			if sendErr == nil || now.Sub(n.CreatedAt) > 24*time.Hour {
				return tx.DeleteNotification(n.Id)
			}
			backoff := time.Duration(min(1<<min(n.Attempts, 10), 3600)) * time.Second
			return tx.RetryNotification(n.Id, now.Add(backoff), sendErr.Error())
		}))
		if sendErr != nil {
			errs = append(errs, fmt.Errorf("notification %v: %w", n.Id, sendErr))
		}
	}
	return errors.Join(errs...)
}
