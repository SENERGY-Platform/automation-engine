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

package store

import (
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

func (this *Tx) nextBlocklistRevision() error {
	_, err := this.exec("UPDATE counters SET value = value + 1 WHERE name = 'blocked_scripts'")
	return err
}

// BlockScript adds a script to the blocklist and reports false if it was there already. Replicas
// that crashed on the same script add it at the same time; the second finds it there.
func (this *Tx) BlockScript(b model.BlockedScript) (bool, error) {
	result, err := this.exec("INSERT INTO blocked_scripts (fingerprint, source, reason, origin, created_at, scanned_at) VALUES (?, ?, ?, ?, ?, 0) ON CONFLICT (fingerprint) DO NOTHING",
		b.Fingerprint, b.Source, b.Reason, b.Origin, millis(b.CreatedAt))
	if err != nil {
		return false, err
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	return true, this.nextBlocklistRevision()
}

// UnblockScript removes a script from the blocklist and reports whether it was there.
func (this *Tx) UnblockScript(fingerprint string) (bool, error) {
	result, err := this.exec("DELETE FROM blocked_scripts WHERE fingerprint = ?", fingerprint)
	if err != nil {
		return false, err
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	return true, this.nextBlocklistRevision()
}

// BlocklistRevision changes whenever a script is blocked or unblocked.
func (this *Tx) BlocklistRevision() (revision int64, err error) {
	err = this.queryRow("SELECT value FROM counters WHERE name = 'blocked_scripts'").Scan(&revision)
	return revision, err
}

// ListBlockedScripts returns the blocklist, the oldest first.
func (this *Tx) ListBlockedScripts() (result []model.BlockedScript, err error) {
	rows, err := this.query("SELECT fingerprint, source, reason, origin, created_at, scanned_at FROM blocked_scripts ORDER BY created_at, fingerprint")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result = []model.BlockedScript{}
	for rows.Next() {
		var b model.BlockedScript
		var created, scanned int64
		if err = rows.Scan(&b.Fingerprint, &b.Source, &b.Reason, &b.Origin, &created, &scanned); err != nil {
			return nil, err
		}
		b.CreatedAt = fromMillis(created)
		if scanned > 0 {
			at := fromMillis(scanned)
			b.ScannedAt = &at
		}
		result = append(result, b)
	}
	return result, rows.Err()
}

// SetBlockedScriptScanned records that the stored deployments were searched for the script.
func (this *Tx) SetBlockedScriptScanned(fingerprint string, at time.Time) error {
	_, err := this.exec("UPDATE blocked_scripts SET scanned_at = ? WHERE fingerprint = ?", millis(at), fingerprint)
	return err
}

// ManagedDeployments returns the ids of the deployments an instance manages through a module.
func (this *Tx) ManagedDeployments(instanceId string) (result []string, err error) {
	return this.ids("SELECT id FROM deployments WHERE managed_by = ? ORDER BY id", instanceId)
}

// RunningInstancesOf returns the ids of the running instances on a version of a deployment.
func (this *Tx) RunningInstancesOf(deploymentId string, version int) (result []string, err error) {
	return this.ids("SELECT id FROM instances WHERE deployment_id = ? AND deployment_version = ? AND state = ? ORDER BY id", deploymentId, version, model.StateRunning)
}
