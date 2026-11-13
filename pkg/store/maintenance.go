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
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// MaintenanceRerun is a run of a maintenance procedure owed to an instance: a watch fired while
// a run of the procedure was running (SNRGY-5143).
type MaintenanceRerun struct {
	InstanceId string
	Procedure  string
	Variables  map[string]any
	// identifies the variables within the reruns of a procedure, see RerunHash
	VariablesHash string
	RequestedAt   time.Time
}

// RerunHash hashes the variables of a procedure start as JSON with sorted keys; no variables
// hash as empty ones.
func RerunHash(variables map[string]any) string {
	if variables == nil {
		variables = map[string]any{}
	}
	sum := sha256.Sum256([]byte(toJson(variables)))
	return hex.EncodeToString(sum[:])
}

// MaintenanceRunning reports whether a run of the procedure of the instance is running; one that
// ended is not, unless it is an incident with a restart pending, which comes back on its own.
func (this *Tx) MaintenanceRunning(instanceId string, procedure string) (bool, error) {
	var found int
	err := this.queryRow("SELECT 1 FROM instances WHERE parent_id = ? AND procedure_name = ? AND (state = ? OR restart_at IS NOT NULL) LIMIT 1", instanceId, procedure, string(model.StateRunning)).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// QueueMaintenanceRerun records a rerun, due at once; a rerun queued before with the same
// variables takes the new request time.
func (this *Tx) QueueMaintenanceRerun(r MaintenanceRerun) error {
	_, err := this.exec("INSERT INTO maintenance_reruns (instance_id, procedure_name, variables_hash, variables, requested_at, next_at) VALUES (?, ?, ?, ?, ?, 0) "+
		"ON CONFLICT (instance_id, procedure_name, variables_hash) DO UPDATE SET requested_at = excluded.requested_at, next_at = 0",
		r.InstanceId, r.Procedure, r.VariablesHash, toJson(r.Variables), millis(r.RequestedAt))
	return err
}

// ClaimMaintenanceReruns takes due reruns of procedures without a running run for one replica
// until until; another replica claiming at the same time skips them, and they are due again at
// until unless started before.
func (this *Tx) ClaimMaintenanceReruns(now time.Time, until time.Time, limit int) (result []MaintenanceRerun, err error) {
	rows, err := this.query("UPDATE maintenance_reruns SET next_at = ? WHERE (instance_id, procedure_name, variables_hash) IN "+
		"(SELECT r.instance_id, r.procedure_name, r.variables_hash FROM maintenance_reruns r WHERE r.next_at <= ? AND NOT EXISTS "+
		"(SELECT 1 FROM instances c WHERE c.parent_id = r.instance_id AND c.procedure_name = r.procedure_name AND (c.state = ? OR c.restart_at IS NOT NULL)) "+
		"ORDER BY r.next_at, r.requested_at LIMIT ?"+this.skipLocked()+") RETURNING instance_id, procedure_name, variables_hash, variables, requested_at",
		millis(until), millis(now), string(model.StateRunning), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r MaintenanceRerun
		var variables string
		var requested int64
		if err = rows.Scan(&r.InstanceId, &r.Procedure, &r.VariablesHash, &variables, &requested); err != nil {
			return nil, err
		}
		r.Variables, r.RequestedAt = fromJson[map[string]any](variables), fromMillis(requested)
		result = append(result, r)
	}
	return result, rows.Err()
}

// MaintenanceRerunQueued reports whether the rerun is still queued, i.e. not started by another
// replica.
func (this *Tx) MaintenanceRerunQueued(instanceId string, procedure string, variablesHash string) (bool, error) {
	n, err := this.count("SELECT COUNT(*) FROM maintenance_reruns WHERE instance_id = ? AND procedure_name = ? AND variables_hash = ?", instanceId, procedure, variablesHash)
	return n > 0, err
}

// DeleteMaintenanceRerun removes a rerun, once a run of the procedure with its variables started.
func (this *Tx) DeleteMaintenanceRerun(instanceId string, procedure string, variablesHash string) error {
	_, err := this.exec("DELETE FROM maintenance_reruns WHERE instance_id = ? AND procedure_name = ? AND variables_hash = ?", instanceId, procedure, variablesHash)
	return err
}
