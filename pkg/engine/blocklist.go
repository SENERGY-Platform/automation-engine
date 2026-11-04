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

	"github.com/SENERGY-Platform/automation-engine/pkg/crashbrake"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/script"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// Blocklist returns the scripts the engine refuses to save, deploy and run (EXPR-03).
func (this *Engine) Blocklist() *script.Blocklist {
	return this.blocklist
}

// blocked is a script a crash put on the blocklist, with what the log names for the admins
type blocked struct {
	added      bool
	entry      model.BlockedScript
	deployment model.Deployment // empty if gone
	version    int
	element    string
	task       string // the name of the element
}

// blockScript stores the script a crash decision names on the blocklist, with its source if the
// deployment version is still stored; the caller adds it to the blocklist in memory and logs it
// after the commit. ran is empty when the unit is gone; the unit names the deployment version
// then.
func (this *Engine) blockScript(tx *store.Tx, decision crashbrake.Decision, ran ranAt) (result blocked, err error) {
	if ran.deploymentId == "" {
		ran.deploymentId, ran.version = decision.Unit.Deployment, decision.Unit.Version
		if decision.Unit.Kind == crashbrake.KindDeployment {
			ran.deploymentId = decision.Unit.Id
		}
	}
	result.version, result.element = ran.version, decision.Unit.Element
	if ran.element != "" {
		result.element = ran.element
	}
	if ran.deploymentId != "" {
		if result.deployment, err = tx.GetDeployment(ran.deploymentId, false); err != nil {
			if err = ignoreNotFound(err); err != nil {
				return result, err
			}
			result.deployment = model.Deployment{Id: ran.deploymentId}
		}
	}
	source := ran.source
	if ran.deploymentId != "" && ran.version > 0 {
		if proc, err := this.process(tx, ran.deploymentId, ran.version); err == nil {
			if e := proc.Element(result.element); e != nil {
				result.task = e.Name
			}
			for _, s := range proc.Scripts() {
				if source != "" {
					break
				}
				if script.Fingerprint(s.Source) == decision.Unit.Script {
					source = s.Source
				}
			}
		}
	}
	origin := fmt.Sprintf("%v %v of deployment %v, version %v", decision.Unit.Kind, result.element, ran.deploymentId, ran.version)
	if decision.Unit.Kind == crashbrake.KindEvent {
		origin = fmt.Sprintf("condition of event description %v of deployment %v, version %v", decision.Unit.Id, ran.deploymentId, ran.version)
	}
	result.entry = model.BlockedScript{Fingerprint: decision.Unit.Script, Source: source, Reason: decision.Error, Origin: origin, CreatedAt: this.now()}
	result.added, err = tx.BlockScript(result.entry)
	return result, err
}

// logBlocked tells the admins, at level error, that a script is blocked from now on: which
// process, task and owner it came from, and what follows for the copies
func (this *Engine) logBlocked(decision crashbrake.Decision, b blocked) {
	this.logger.Error("a script ended the engine and is on the blocklist now; deployments with a copy are quarantined, their instances stopped, and a smart service that deployed it is stopped",
		"process", b.deployment.Name, "deployment_id", b.deployment.Id, "version", b.version, "namespace", b.deployment.Namespace,
		"task", b.task, "element_id", b.element, "owner", b.deployment.Owner, "managed_by", b.deployment.ManagedBy,
		"kind", decision.Unit.Kind, "unit_id", decision.Unit.Id, "fingerprint", b.entry.Fingerprint, "reason", b.entry.Reason)
}

// UnblockScript removes a script from the blocklist and applies it on this replica at once, the
// others when they see the new revision. Quarantined deployments and stopped instances stay as
// they are.
func (this *Engine) UnblockScript(ctx context.Context, fingerprint string, by string) error {
	err := this.store.Tx(ctx, func(tx *store.Tx) error {
		removed, err := tx.UnblockScript(fingerprint)
		if err == nil && !removed {
			err = model.NotFound("script %v is not blocked", fingerprint)
		}
		return err
	})
	if err != nil {
		return err
	}
	this.logger.Warn("a script was removed from the blocklist and runs again", "fingerprint", fingerprint, "by", by)
	_, err = this.LoadBlocklist(ctx)
	return err
}

// blockedKeys returns the fingerprints under which an entry blocks: the stored one, and that of
// its source in the current mode, which differs if SCRIPT_FINGERPRINT changed since
func blockedKeys(b model.BlockedScript) []string {
	keys := []string{b.Fingerprint}
	if b.Source != "" {
		if f := script.Fingerprint(b.Source); f != b.Fingerprint {
			keys = append(keys, f)
		}
	}
	return keys
}

// stopOriginator stops the smart service instance that created a deployment through a module
// (managed_by) with an incident, and quarantines every deployment it manages and stops their
// running instances: it would deploy the script again, also changed beyond what its fingerprint
// catches. managedBy names the instance when the crash rolled the deployment back. An instance
// waiting for its automatic restart after an incident does not restart.
func (this *Engine) stopOriginator(tx *store.Tx, deploymentId string, managedBy string, crash string) error {
	name := deploymentId
	deployment, err := tx.GetDeployment(deploymentId, false)
	if err == nil {
		managedBy, name = deployment.ManagedBy, deployment.Name
	} else if err = ignoreNotFound(err); err != nil {
		return err
	}
	root := managedBy
	if root == "" {
		return nil
	}
	rootInstance, err := tx.GetInstance(root, true)
	if err != nil {
		return ignoreNotFound(err)
	}
	stopped, err := this.stopWithIncident(tx, root, "", fmt.Sprintf("deployment %v, which this instance manages, ran a script that ended the engine with %v", name, crash))
	if err != nil {
		return err
	}
	if !stopped && rootInstance.RestartAt != nil {
		rootInstance.RestartAt = nil
		if err = tx.UpdateInstance(rootInstance); err != nil {
			return err
		}
		stopped = true
	}
	if stopped {
		if err = this.markCrashed(tx, rootInstance.DeploymentId, fmt.Sprintf("Instance %v deployed %v, which ran a script that ended the engine with %v. The instance stopped with an incident, and the deployments it manages are disabled.", root, name, crash)); err != nil {
			return err
		}
	}
	managed, err := tx.ManagedDeployments(root)
	if err != nil {
		return err
	}
	for _, id := range managed {
		d, err := tx.GetDeployment(id, true)
		if errors.Is(err, model.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if d.NetworkId != "" {
			// runs on a gateway, which stops it with its own blocklist
			continue
		}
		q := store.Quarantine{DeploymentId: id, Version: d.Version, ElementId: "", Error: crash, Since: this.now()}
		if err = this.quarantine(tx, q, fmt.Sprintf("Instance %v, which manages this deployment, deployed a script that ended the engine with %v.", root, crash)); err != nil {
			return err
		}
		if err = this.stopRunning(tx, d, "", fmt.Sprintf("the instance %v that manages this deployment deployed a script that ended the engine with %v", root, crash)); err != nil {
			return err
		}
	}
	return nil
}

// stopRunning stops every running instance of a deployment with an incident
func (this *Engine) stopRunning(tx *store.Tx, d model.Deployment, elementId string, message string) error {
	for version := 1; version <= d.Version; version++ {
		if err := this.stopVersion(tx, d.Id, version, elementId, message); err != nil {
			return err
		}
	}
	return nil
}

// stopVersion stops the running instances of a deployment version with an incident
func (this *Engine) stopVersion(tx *store.Tx, deploymentId string, version int, elementId string, message string) error {
	ids, err := tx.RunningInstancesOf(deploymentId, version)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = this.stopWithIncident(tx, id, elementId, message); err != nil {
			return err
		}
	}
	return nil
}

// LoadBlocklist reads the blocklist from the store.
func (this *Engine) LoadBlocklist(ctx context.Context) (revision int64, err error) {
	var list []model.BlockedScript
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		if revision, err = tx.BlocklistRevision(); err != nil {
			return err
		}
		list, err = tx.ListBlockedScripts()
		return err
	})
	if err != nil {
		return revision, err
	}
	entries := map[string]string{}
	for _, b := range list {
		for _, key := range blockedKeys(b) {
			entries[key] = b.Reason
		}
	}
	this.blocklist.Replace(entries)
	return revision, nil
}

// WatchBlocklist reloads the blocklist when another replica changed it, and searches the stored
// deployments for scripts blocked since the last search, until ctx is done.
func (this *Engine) WatchBlocklist(ctx context.Context, interval time.Duration) {
	var known int64 = -1
	scan := true
	for {
		var revision int64
		err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
			revision, err = tx.BlocklistRevision()
			return err
		})
		if err == nil && revision != known {
			if known, err = this.LoadBlocklist(ctx); err == nil {
				scan = true
			}
		}
		if err == nil && scan {
			if err = this.ScanBlockedScripts(ctx); err == nil {
				scan = false
			}
		}
		if err != nil && ctx.Err() == nil {
			this.logger.Warn("unable to update the blocklist of scripts", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// ScanBlockedScripts searches the stored deployments for the scripts blocked since the last
// search (EXPR-03): a deployment whose current version contains one is quarantined, the running
// instances of every version that contains one are stopped with an incident, conditions with one
// are suspended, and the smart service instance that created such a deployment is stopped.
// Deployments of gateways are left to the gateway, which runs their instances.
func (this *Engine) ScanBlockedScripts(ctx context.Context) error {
	var unscanned []model.BlockedScript
	err := this.store.Tx(ctx, func(tx *store.Tx) error {
		list, err := tx.ListBlockedScripts()
		for _, b := range list {
			if b.ScannedAt == nil {
				unscanned = append(unscanned, b)
			}
		}
		return err
	})
	if err != nil || len(unscanned) == 0 {
		return err
	}
	blocked := map[string]string{}
	for _, b := range unscanned {
		for _, key := range blockedKeys(b) {
			blocked[key] = b.Reason
		}
	}
	// a smart service that deployed the script many times is stopped once
	originators := map[string]bool{}
	// a deployment that fails does not keep the others from being searched; all are searched
	// again in the next round, which is harmless for those done
	var failed error
	after := ""
	for {
		var page []model.Deployment
		err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
			page, err = tx.AllDeployments(after, 100)
			return err
		})
		if err != nil {
			return err
		}
		for _, d := range page {
			if d.NetworkId != "" {
				continue
			}
			if err = this.store.Tx(ctx, func(tx *store.Tx) error {
				return this.scanDeployment(tx, d.Id, blocked, originators)
			}); err != nil {
				if ctx.Err() != nil {
					return err
				}
				this.logger.Warn("unable to search a deployment for blocked scripts", "deployment_id", d.Id, "error", err)
				failed = err
			}
		}
		if len(page) < 100 {
			break
		}
		after = page[len(page)-1].Id
	}
	if failed != nil {
		return failed
	}
	return this.store.Tx(ctx, func(tx *store.Tx) error {
		for _, b := range unscanned {
			if err := tx.SetBlockedScriptScanned(b.Fingerprint, this.now()); err != nil {
				return err
			}
		}
		return nil
	})
}

func (this *Engine) scanDeployment(tx *store.Tx, deploymentId string, blocked map[string]string, originators map[string]bool) error {
	d, err := tx.GetDeployment(deploymentId, true)
	if err != nil {
		return ignoreNotFound(err)
	}
	versions, err := tx.ListDeploymentVersions(d.Id, model.PageOptions{Limit: 100000})
	if err != nil {
		return err
	}
	reason, matched := "", false
	for _, v := range versions.Items {
		proc, err := this.process(tx, d.Id, v.Version)
		if err != nil {
			continue
		}
		for _, s := range proc.Scripts() {
			r, ok := blocked[script.Fingerprint(s.Source)]
			if !ok {
				continue
			}
			reason, matched = r, true
			if v.Version == d.Version {
				q := store.Quarantine{DeploymentId: d.Id, Version: d.Version, ElementId: s.ElementId, Error: r, Since: this.now()}
				if err = this.quarantine(tx, q, fmt.Sprintf("Element %v contains a script that is blocked, because a copy of it ended the engine with %v.", s.ElementId, r)); err != nil {
					return err
				}
			}
			if err = this.stopVersion(tx, d.Id, v.Version, s.ElementId, fmt.Sprintf("the script of this element is blocked, because a copy of it ended the engine with %v", r)); err != nil {
				return err
			}
		}
	}
	descriptions, err := tx.ListEventDescriptions(d.Id, 0)
	if err != nil {
		return err
	}
	for _, description := range descriptions {
		if r, ok := blocked[script.Fingerprint(description.Script)]; ok {
			if _, _, err = tx.SuspendEventDescription(description.Id, r); err != nil {
				return err
			}
			reason, matched = r, true
		}
	}
	if !matched || d.ManagedBy == "" || originators[d.ManagedBy] {
		return nil
	}
	originators[d.ManagedBy] = true
	return this.stopOriginator(tx, d.Id, d.ManagedBy, reason)
}
