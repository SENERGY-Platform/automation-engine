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
)

// RetentionTarget is a deployment as the retention pass sees it.
type RetentionTarget struct {
	Id            string
	Namespace     string
	RetentionDays int
}

// DeploymentsAfter pages through all deployments by id, for passes in small steps (LONG-05).
func (this *Tx) DeploymentsAfter(afterId string, limit int) (result []RetentionTarget, err error) {
	rows, err := this.query("SELECT id, namespace, retention_days FROM deployments WHERE id > ? ORDER BY id LIMIT ?", afterId, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t RetentionTarget
		if err = rows.Scan(&t.Id, &t.Namespace, &t.RetentionDays); err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, rows.Err()
}

func (this *Tx) ModelsAfter(afterId string, limit int) (result []string, err error) {
	rows, err := this.query("SELECT id FROM models WHERE id > ? ORDER BY id LIMIT ?", afterId, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

// DeleteFinishedInstances removes up to limit instances whose run ended before the cutoff,
// selected by column (deployment_id or owner); their history goes with them (HIST-04).
func (this *Tx) DeleteFinishedInstances(column string, value string, before time.Time, limit int) (int64, error) {
	// instances with modules stay: their resources keep running, e.g. a deployed process (SMS-12)
	selection := "id IN (SELECT id FROM instances WHERE " + column + " = ? AND state <> 'running' AND restart_at IS NULL AND ended_at < ? " +
		"AND NOT EXISTS (SELECT 1 FROM modules m WHERE m.instance_id = instances.id) AND " + unsynced + " ORDER BY ended_at LIMIT ?)"
	args := []any{value, millis(before), limit}
	if err := this.queueRemovals("instances", selection, args...); err != nil {
		return 0, err
	}
	result, err := this.exec("DELETE FROM instances WHERE "+selection, args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// TrimHistory removes history older than the cutoff from instances that are kept, in
// particular from running instances, whose history must not grow with their runtime (HIST-06).
// column selects the instances by deployment_id or owner.
func (this *Tx) TrimHistory(column string, value string, before time.Time) error {
	instances := "SELECT id FROM instances WHERE " + column + " = ?"
	statements := []string{
		"DELETE FROM activities WHERE instance_id IN (" + instances + ") AND ts < ?",
		"DELETE FROM incidents WHERE instance_id IN (" + instances + ") AND ts < ?",
		"DELETE FROM runs WHERE instance_id IN (" + instances + ") AND ended_at < ? AND run < (SELECT i.run FROM instances i WHERE i.id = runs.instance_id)",
		"DELETE FROM element_stats WHERE instance_id IN (" + instances + ") AND last_at < ? AND run < (SELECT i.run FROM instances i WHERE i.id = element_stats.instance_id)",
	}
	for _, statement := range statements {
		if _, err := this.exec(statement, value, millis(before)); err != nil {
			return err
		}
	}
	// keeps the truncation marker of the activity log right (LONG-03)
	_, err := this.exec("UPDATE instances SET activity_min_seq = COALESCE((SELECT MIN(a.seq) FROM activities a WHERE a.instance_id = instances.id), activity_seq + 1) WHERE "+column+" = ?", value)
	if err != nil {
		return err
	}
	// snapshots before the oldest kept entry cannot start a reconstruction anymore (DBG-14)
	_, err = this.exec("DELETE FROM history_snapshots WHERE instance_id IN ("+instances+") AND seq < (SELECT i.activity_min_seq - 1 FROM instances i WHERE i.id = history_snapshots.instance_id)", value)
	return err
}

// TrimModelVersions keeps the newest versions of a model and every version a deployment refers to (MOD-07).
func (this *Tx) TrimModelVersions(modelId string, keep int) error {
	_, err := this.exec(`DELETE FROM model_versions WHERE model_id = ?
		AND version <= (SELECT m.version FROM models m WHERE m.id = ?) - ?
		AND version NOT IN (SELECT d.model_version FROM deployments d WHERE d.model_id = ?)
		AND version NOT IN (SELECT dv.model_version FROM deployment_versions dv JOIN deployments d ON d.id = dv.deployment_id WHERE d.model_id = ?)`,
		modelId, modelId, keep, modelId, modelId)
	return err
}
