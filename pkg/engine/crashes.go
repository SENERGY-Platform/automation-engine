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

	"github.com/SENERGY-Platform/automation-engine/pkg/crashbrake"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// ApplyCrashDecisions stops what the crash brake found to have ended the previous process with a
// fatal error, so that it does not run again after every restart (EXPR-03):
//   - a script of an instance: an incident at its element;
//   - the condition of an event: the description is suspended, and the instances waiting for it
//     get an incident;
//   - the expression of a start timer, or a script of an instance whose creation the crash rolled
//     back, e.g. started by a message or a timer: the deployment version is quarantined, which
//     disables the deployment, also when it is created only later (applyQuarantine).
//
// The deployment is marked faulty, which pauses automatic restarts, and its owner is notified
// once. A reset of the restarts lifts all of it, an update the suspension and the mark, a newer
// version or enabling the deployment the quarantine. It returns the decisions it could not apply,
// for the next start.
func (this *Engine) ApplyCrashDecisions(ctx context.Context, decisions []crashbrake.Decision) (remaining []crashbrake.Decision) {
	for _, decision := range decisions {
		err := this.store.Tx(ctx, func(tx *store.Tx) error {
			return this.applyCrash(tx, decision)
		})
		if err != nil {
			this.logger.Error("unable to stop the script that ended the previous process, retried at the next start", "kind", decision.Unit.Kind, "id", decision.Unit.Id, "error", err)
			remaining = append(remaining, decision)
			continue
		}
		this.logger.Error("a script ended the previous process with a fatal error and was stopped", "kind", decision.Unit.Kind, "id", decision.Unit.Id,
			"element_id", decision.Unit.Element, "deployment_id", decision.Unit.Deployment, "error", decision.Error, "report", decision.Report)
	}
	this.signal()
	return remaining
}

func (this *Engine) applyCrash(tx *store.Tx, decision crashbrake.Decision) error {
	unit := decision.Unit
	switch unit.Kind {
	case crashbrake.KindInstance:
		inst, err := tx.GetInstance(unit.Id, true)
		if errors.Is(err, model.ErrNotFound) {
			if unit.Version == 0 {
				// a module script of an instance deleted since: nothing runs it again
				return nil
			}
			// the crash rolled back the creation of the instance; the trigger would create it again
			return this.quarantine(tx, store.Quarantine{DeploymentId: unit.Deployment, Version: unit.Version, ElementId: unit.Element, Error: decision.Error, Since: this.now()},
				fmt.Sprintf("A script of element %v ended the engine with %v while a new instance started.", unit.Element, decision.Error))
		}
		if err != nil {
			return err
		}
		if inst.State != model.StateRunning {
			if inst.RestartAt == nil {
				return nil
			}
			// the crash rolled back the automatic restart after an incident, which would run again
			inst.RestartAt = nil
			if err = tx.UpdateInstance(inst); err != nil {
				return err
			}
			return this.markCrashed(tx, inst.DeploymentId, fmt.Sprintf("A script of element %v ended the engine with %v when instance %v restarted after an incident. The automatic restart is cancelled.", unit.Element, decision.Error, inst.Id))
		}
		stopped, err := this.crashIncident(tx, inst.Id, unit.Element, decision.Error)
		if err != nil || !stopped {
			return err
		}
		return this.markCrashed(tx, inst.DeploymentId, fmt.Sprintf("A script of element %v of instance %v ended the engine with %v. The instance stopped with an incident.", unit.Element, inst.Id, decision.Error))
	case crashbrake.KindEvent:
		d, suspended, err := tx.SuspendEventDescription(unit.Id, decision.Error)
		if err != nil || !suspended {
			return ignoreNotFound(err)
		}
		subscriptions, err := tx.MessageSubscriptions(d.MessageName, "", "", d.Version)
		if err != nil {
			return err
		}
		stopped := map[string]bool{}
		for _, s := range subscriptions {
			if stopped[s.InstanceId] {
				continue
			}
			if stopped[s.InstanceId], err = this.crashIncident(tx, s.InstanceId, s.ElementId, decision.Error); err != nil {
				return err
			}
		}
		return this.markCrashed(tx, d.DeploymentId, fmt.Sprintf("The condition of event %v ended the engine with %v. It is no longer evaluated, and the %v instances waiting for it stopped with an incident.", d.ElementId, decision.Error, len(stopped)))
	case crashbrake.KindDeployment:
		return this.quarantine(tx, store.Quarantine{DeploymentId: unit.Id, Version: unit.Version, ElementId: unit.Element, Error: decision.Error, Since: this.now()},
			fmt.Sprintf("The timer expression of start event %v ended the engine with %v.", unit.Element, decision.Error))
	}
	return fmt.Errorf("unknown kind of crash unit: %v", unit.Kind)
}

// crashIncident ends the run of a running instance with an incident at the element whose script
// ended the process, and reports whether it did
func (this *Engine) crashIncident(tx *store.Tx, instanceId string, elementId string, crash string) (bool, error) {
	r, err := this.lockRunning(tx, instanceId)
	if errors.Is(err, model.ErrConflict) || errors.Is(err, model.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = r.incident(elementId, fmt.Sprintf("the engine ended with %v while this element evaluated a script; it is not run again automatically", crash)); err != nil {
		return false, err
	}
	return true, r.finish()
}

// quarantine records the quarantine of a deployment version and disables the deployment, if it
// exists already; the first quarantine counts
func (this *Engine) quarantine(tx *store.Tx, q store.Quarantine, what string) error {
	if q.DeploymentId == "" {
		return nil
	}
	added, err := tx.QuarantineDeployment(q)
	if err != nil || !added {
		return err
	}
	deployment, err := tx.GetDeployment(q.DeploymentId, true)
	if err != nil {
		// created later, e.g. applied again by a gateway: ActivateVersion disables it then
		return ignoreNotFound(err)
	}
	return this.enforceQuarantine(tx, deployment, what)
}

func (this *Engine) enforceQuarantine(tx *store.Tx, deployment model.Deployment, what string) error {
	if err := tx.SetDeploymentDisabled(deployment.Id, true); err != nil {
		return err
	}
	return this.markCrashed(tx, deployment.Id, what+" The deployment is disabled and starts no instances.")
}

// applyQuarantine is part of the activation of a version: a quarantined deployment stays disabled
// while the version is not newer than the quarantined one, e.g. when a gateway applies it again
// after the crash, and is disabled if it was created only after the crash; a newer version may
// have fixed the script and lifts it. It reports whether the deployment is quarantined.
func (this *Engine) applyQuarantine(tx *store.Tx, deployment model.Deployment, version int) (bool, error) {
	q, err := tx.GetQuarantine(deployment.Id)
	if err != nil || q == nil {
		return false, err
	}
	if version > q.Version {
		_, err = this.LiftQuarantine(tx, deployment.Id)
		return false, err
	}
	if deployment.Disabled {
		return true, nil
	}
	return true, this.enforceQuarantine(tx, deployment, fmt.Sprintf("A script of element %v ended the engine with %v before this version was activated.", q.ElementId, q.Error))
}

// LiftQuarantine removes the quarantine of a deployment and enables it again; it reports whether
// there was one. The caller activates the version again, which computes the start timers.
func (this *Engine) LiftQuarantine(tx *store.Tx, deploymentId string) (bool, error) {
	lifted, err := tx.LiftQuarantine(deploymentId)
	if err != nil || !lifted {
		return false, err
	}
	return true, tx.SetDeploymentDisabled(deploymentId, false)
}

// markCrashed marks the deployment faulty, which pauses automatic restarts of its instances, and
// tells its owner what happened and how to resume
func (this *Engine) markCrashed(tx *store.Tx, deploymentId string, what string) error {
	deployment, err := tx.GetDeployment(deploymentId, true)
	if err != nil {
		return ignoreNotFound(err)
	}
	stats, err := tx.GetRestartStats(deploymentId)
	if err != nil {
		return err
	}
	if stats.FaultySince == nil {
		now := this.now()
		stats.FaultySince = &now
		if err = tx.SetRestartStats(deploymentId, stats); err != nil {
			return err
		}
	}
	return this.queueNotification(tx, deployment.Owner, deployment.Namespace, "Script stopped after it ended the engine: "+deployment.Name,
		what+" Automatic restarts of the deployment are paused. Fix the script and update the deployment, or reset its restarts, to run it again.")
}
