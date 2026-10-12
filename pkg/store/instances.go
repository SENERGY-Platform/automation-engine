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
	"database/sql"
	"errors"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// Instance is the stored form of an instance with the counters of its activity log.
type Instance struct {
	model.Instance
	ActivitySeq    int64
	ActivityMinSeq int64
	// activity sequence and time of the latest history snapshot (DBG-14)
	SnapshotSeq int64
	SnapshotAt  time.Time
}

const instanceColumns = "id, namespace, owner, deployment_id, deployment_name, deployment_version, model_id, model_version, run, state, end_reason, start_variables, created_at, started_at, ended_at, last_activity_at, activity_seq, activity_min_seq, restart_at, snapshot_seq, snapshot_at, parent_id, network_id"

func scanInstance(row interface{ Scan(...any) error }) (i Instance, err error) {
	var state, startVariables string
	var created, started, lastActivity, snapshotAt int64
	var ended, restartAt sql.NullInt64
	err = row.Scan(&i.Id, &i.Namespace, &i.Owner, &i.DeploymentId, &i.DeploymentName, &i.DeploymentVersion, &i.ModelId, &i.ModelVersion, &i.Run, &state, &i.EndReason, &startVariables, &created, &started, &ended, &lastActivity, &i.ActivitySeq, &i.ActivityMinSeq, &restartAt, &i.SnapshotSeq, &snapshotAt, &i.ParentId, &i.NetworkId)
	if snapshotAt > 0 {
		i.SnapshotAt = fromMillis(snapshotAt)
	}
	i.RestartAt = fromNullMillis(restartAt)
	i.State = model.InstanceState(state)
	i.StartVariables = fromJson[map[string]any](startVariables)
	i.CreatedAt = fromMillis(created)
	i.StartedAt = fromMillis(started)
	i.EndedAt = fromNullMillis(ended)
	i.LastActivityAt = fromMillis(lastActivity)
	return i, err
}

func (this *Tx) InsertInstance(i Instance) error {
	_, err := this.exec("INSERT INTO instances ("+instanceColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		i.Id, i.Namespace, i.Owner, i.DeploymentId, i.DeploymentName, i.DeploymentVersion, i.ModelId, i.ModelVersion, i.Run, string(i.State), i.EndReason, toJson(i.StartVariables),
		millis(i.CreatedAt), millis(i.StartedAt), nullMillis(i.EndedAt), millis(i.LastActivityAt), i.ActivitySeq, i.ActivityMinSeq, nullMillis(i.RestartAt), i.SnapshotSeq, snapshotMillis(i.SnapshotAt), i.ParentId, i.NetworkId)
	if err != nil {
		return err
	}
	return this.changed(i.Id, i.Namespace, i.ActivitySeq, false)
}

func snapshotMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return millis(t)
}

func (this *Tx) UpdateInstance(i Instance) error {
	_, err := this.exec("UPDATE instances SET owner = ?, deployment_name = ?, deployment_version = ?, run = ?, state = ?, end_reason = ?, started_at = ?, ended_at = ?, last_activity_at = ?, activity_seq = ?, activity_min_seq = ?, restart_at = ?, snapshot_seq = ?, snapshot_at = ? WHERE id = ?",
		i.Owner, i.DeploymentName, i.DeploymentVersion, i.Run, string(i.State), i.EndReason, millis(i.StartedAt), nullMillis(i.EndedAt), millis(i.LastActivityAt), i.ActivitySeq, i.ActivityMinSeq, nullMillis(i.RestartAt), i.SnapshotSeq, snapshotMillis(i.SnapshotAt), i.Id)
	if err != nil {
		return err
	}
	return this.changed(i.Id, i.Namespace, i.ActivitySeq, false)
}

// GetInstance reads an instance; with lock, the row stays locked until the transaction ends,
// which serializes all state transitions of the instance (RUN-08).
func (this *Tx) GetInstance(id string, lock bool) (Instance, error) {
	i, err := scanInstance(this.queryRow("SELECT "+instanceColumns+" FROM instances WHERE id = ?"+this.forUpdate(lock), id))
	return i, notFound(err, "instance %v not found", id)
}

func (this *Tx) DeleteInstance(id string) error {
	// maintenance procedures go with their instance (SMS-13)
	children, err := this.ChildInstances(id)
	if err != nil {
		return err
	}
	for _, child := range children {
		if err = this.DeleteInstance(child); err != nil {
			return err
		}
	}
	if err := this.queueRemovals("instances", "id = ?", id); err != nil {
		return err
	}
	if err := this.queueModuleRemovals("instance_id = ?", id); err != nil {
		return err
	}
	var namespace string
	var activitySeq int64
	err = this.queryRow("SELECT namespace, activity_seq FROM instances WHERE id = ?", id).Scan(&namespace, &activitySeq)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = this.exec("DELETE FROM instances WHERE id = ?", id); err != nil {
		return err
	}
	if namespace == "" {
		return nil
	}
	return this.changed(id, namespace, activitySeq, true)
}

func (this *Tx) ListInstances(namespace string, options model.InstanceListOptions, access Access) (result model.Page[model.Instance], err error) {
	w := &where{}
	w.add("namespace = ?", namespace)
	w.access("", access)
	switch options.State {
	case "":
	case "running":
		w.add("state = ?", string(model.StateRunning))
	case "finished":
		w.add("state NOT IN (?, ?)", string(model.StateRunning), string(model.StatePending))
	default:
		w.add("state = ?", options.State)
	}
	if options.DeploymentId != "" {
		w.add("deployment_id = ?", options.DeploymentId)
	}
	w.search("deployment_name", options.DeploymentName)
	if options.ModelId != "" {
		w.add("model_id = ?", options.ModelId)
	}
	if len(options.Ids) > 0 {
		w.in("id", options.Ids)
	}
	if options.Owner != "" {
		w.add("owner = ?", options.Owner)
	}
	w.network(options.NetworkId)
	order, err := orderBy(options.Sort, map[string]string{
		"started_at":      "started_at",
		"ended_at":        "ended_at",
		"created_at":      "created_at",
		"deployment_name": "LOWER(deployment_name)",
		"state":           "state",
	}, "started_at DESC, id ASC")
	if err != nil {
		return result, err
	}
	if result.Total, err = this.count("SELECT COUNT(*) FROM instances"+w.String(), w.args...); err != nil {
		return result, err
	}
	limit, limitArgs := limitOffset(options.PageOptions)
	rows, err := this.query("SELECT "+instanceColumns+" FROM instances"+w.String()+order+limit, append(w.args, limitArgs...)...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	result.Items = []model.Instance{}
	for rows.Next() {
		i, err := scanInstance(rows)
		if err != nil {
			return result, err
		}
		result.Items = append(result.Items, i.Instance)
	}
	page := options.PageOptions.Normalized()
	result.Limit, result.Offset = page.Limit, page.Offset
	return result, rows.Err()
}

func (this *Tx) InstanceIdsOfDeployment(deploymentId string, state model.InstanceState) (result []string, err error) {
	rows, err := this.query("SELECT id FROM instances WHERE deployment_id = ? AND state = ? ORDER BY id", deploymentId, string(state))
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

// RunningVersions returns the deployment versions that still have running instances.
func (this *Tx) RunningVersions(deploymentId string) (result []int, err error) {
	rows, err := this.query("SELECT DISTINCT deployment_version FROM instances WHERE deployment_id = ? AND state = ? ORDER BY deployment_version", deploymentId, string(model.StateRunning))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v int
		if err = rows.Scan(&v); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

func (this *Tx) InsertRun(r model.Run) error {
	_, err := this.exec("INSERT INTO runs (instance_id, run, deployment_version, cause, state, end_reason, started_at, ended_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		r.InstanceId, r.Run, r.DeploymentVersion, string(r.Cause), string(r.State), r.EndReason, millis(r.StartedAt), nullMillis(r.EndedAt))
	return err
}

func (this *Tx) UpdateRun(r model.Run) error {
	_, err := this.exec("UPDATE runs SET state = ?, end_reason = ?, ended_at = ? WHERE instance_id = ? AND run = ?",
		string(r.State), r.EndReason, nullMillis(r.EndedAt), r.InstanceId, r.Run)
	return err
}

func (this *Tx) GetRun(instanceId string, run int) (r model.Run, err error) {
	runs, err := this.listRuns("WHERE instance_id = ? AND run = ?", instanceId, run)
	if err != nil {
		return r, err
	}
	if len(runs) == 0 {
		return r, model.NotFound("run %v of instance %v not found", run, instanceId)
	}
	return runs[0], nil
}

func (this *Tx) ListRuns(instanceId string) ([]model.Run, error) {
	return this.listRuns("WHERE instance_id = ? ORDER BY run", instanceId)
}

func (this *Tx) listRuns(condition string, args ...any) (result []model.Run, err error) {
	rows, err := this.query("SELECT instance_id, run, deployment_version, cause, state, end_reason, started_at, ended_at FROM runs "+condition, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result = []model.Run{}
	for rows.Next() {
		var r model.Run
		var cause, state string
		var started int64
		var ended sql.NullInt64
		if err = rows.Scan(&r.InstanceId, &r.Run, &r.DeploymentVersion, &cause, &state, &r.EndReason, &started, &ended); err != nil {
			return nil, err
		}
		r.Cause = model.RunCause(cause)
		r.State = model.InstanceState(state)
		r.StartedAt = fromMillis(started)
		r.EndedAt = fromNullMillis(ended)
		result = append(result, r)
	}
	return result, rows.Err()
}

// SetVariable stores a JSON value; size is the length of its JSON form.
func (this *Tx) SetVariable(instanceId string, name string, value any, elementId string, at time.Time) error {
	_, err := this.exec("INSERT INTO variables (instance_id, name, value, updated_at, element_id) VALUES (?, ?, ?, ?, ?) ON CONFLICT (instance_id, name) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at, element_id = excluded.element_id",
		instanceId, name, toJson(value), millis(at), elementId)
	return err
}

func (this *Tx) GetVariables(instanceId string) (result []model.Variable, err error) {
	rows, err := this.query("SELECT name, value, updated_at, element_id FROM variables WHERE instance_id = ? ORDER BY name", instanceId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result = []model.Variable{}
	for rows.Next() {
		var v model.Variable
		var raw string
		var updated int64
		if err = rows.Scan(&v.Name, &raw, &updated, &v.ElementId); err != nil {
			return nil, err
		}
		v.Value = fromJson[any](raw)
		v.Type = model.ValueType(v.Value)
		v.UpdatedAt = fromMillis(updated)
		result = append(result, v)
	}
	return result, rows.Err()
}

// VariablesSize sums the JSON size of all variables of an instance (LONG-08).
func (this *Tx) VariablesSize(instanceId string) (int, error) {
	return this.count("SELECT COALESCE(SUM(LENGTH(value)), 0) FROM variables WHERE instance_id = ?", instanceId)
}

func (this *Tx) DeleteVariables(instanceId string) error {
	_, err := this.exec("DELETE FROM variables WHERE instance_id = ?", instanceId)
	return err
}

func (this *Tx) DeleteVariable(instanceId string, name string) error {
	_, err := this.exec("DELETE FROM variables WHERE instance_id = ? AND name = ?", instanceId, name)
	return err
}

// SetStartVariables replaces the stored start variables, e.g. after a parameter change (SMS-09).
func (this *Tx) SetStartVariables(instanceId string, variables map[string]any) error {
	_, err := this.exec("UPDATE instances SET start_variables = ? WHERE id = ?", toJson(variables), instanceId)
	return err
}
