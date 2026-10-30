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
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// InsertActivity appends an entry to the activity log; the caller holds the instance lock
// and passes the sequence number taken from the instance.
func (this *Tx) InsertActivity(a model.Activity) error {
	data := ""
	if len(a.Data) > 0 {
		data = toJson(a.Data)
	}
	_, err := this.exec("INSERT INTO activities (instance_id, seq, run, ts, kind, element_id, element_type, data) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		a.InstanceId, a.Seq, a.Run, millis(a.Time), string(a.Kind), a.ElementId, a.ElementType, data)
	return err
}

// TrimActivities removes entries with a sequence number below minSeq (LONG-03).
func (this *Tx) TrimActivities(instanceId string, minSeq int64) error {
	_, err := this.exec("DELETE FROM activities WHERE instance_id = ? AND seq < ?", instanceId, minSeq)
	return err
}

func (this *Tx) ListActivities(instanceId string, options model.ActivityListOptions) (result []model.Activity, err error) {
	w := &where{}
	w.add("instance_id = ?", instanceId)
	if options.Run > 0 {
		w.add("run = ?", options.Run)
	}
	if options.AfterSeq > 0 {
		w.add("seq > ?", options.AfterSeq)
	}
	if options.Kind != "" {
		w.add("kind = ?", options.Kind)
	}
	if options.ElementId != "" {
		w.add("element_id = ?", options.ElementId)
	}
	limit := options.Limit
	if limit <= 0 || limit > model.MaxPageLimit {
		limit = model.MaxPageLimit
	}
	// the search looks into the data, which is JSON text; it reads the variable entries and
	// filters them here, bounded by the entries an instance keeps (DBG-16, LONG-03)
	searches := options.Searches()
	order := " ORDER BY seq LIMIT ?"
	if searches {
		w.in("kind", []string{string(model.ActivityVariableSet), string(model.ActivityVariableDeleted)})
		order = " ORDER BY seq"
	}
	args := w.args
	if !searches {
		args = append(args, limit)
	}
	rows, err := this.query("SELECT instance_id, seq, run, ts, kind, element_id, element_type, data FROM activities"+w.String()+order, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result = []model.Activity{}
	for len(result) < limit && rows.Next() {
		var a model.Activity
		var ts int64
		var kind, data string
		if err = rows.Scan(&a.InstanceId, &a.Seq, &a.Run, &ts, &kind, &a.ElementId, &a.ElementType, &data); err != nil {
			return nil, err
		}
		a.Time = fromMillis(ts)
		a.Kind = model.ActivityKind(kind)
		a.Data = fromJson[map[string]any](data)
		if searches && !options.Matches(a) {
			continue
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

// CountElementPass increments the pass counter of an element (LONG-04).
func (this *Tx) CountElementPass(instanceId string, run int, elementId string, at time.Time) error {
	_, err := this.exec("INSERT INTO element_stats (instance_id, run, element_id, count, last_at) VALUES (?, ?, ?, 1, ?) "+
		"ON CONFLICT (instance_id, run, element_id) DO UPDATE SET count = element_stats.count + 1, last_at = excluded.last_at",
		instanceId, run, elementId, millis(at))
	return err
}

func (this *Tx) RecordElementError(instanceId string, run int, elementId string, message string, at time.Time) error {
	_, err := this.exec("INSERT INTO element_stats (instance_id, run, element_id, count, last_at, last_error, error_at) VALUES (?, ?, ?, 0, ?, ?, ?) "+
		"ON CONFLICT (instance_id, run, element_id) DO UPDATE SET last_error = excluded.last_error, error_at = excluded.error_at",
		instanceId, run, elementId, millis(at), message, millis(at))
	return err
}

func (this *Tx) ListElementStats(instanceId string, run int) (result []model.ElementStats, err error) {
	w := &where{}
	w.add("instance_id = ?", instanceId)
	if run > 0 {
		w.add("run = ?", run)
	}
	rows, err := this.query("SELECT element_id, run, count, last_at, last_error, error_at FROM element_stats"+w.String()+" ORDER BY run, element_id", w.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result = []model.ElementStats{}
	for rows.Next() {
		var s model.ElementStats
		var last int64
		var errorAt sql.NullInt64
		if err = rows.Scan(&s.ElementId, &s.Run, &s.Count, &last, &s.LastError, &errorAt); err != nil {
			return nil, err
		}
		s.LastAt = fromMillis(last)
		s.ErrorAt = fromNullMillis(errorAt)
		result = append(result, s)
	}
	return result, rows.Err()
}

const incidentColumns = "id, namespace, owner, instance_id, run, deployment_id, deployment_version, element_id, element_name, message, ts, variables"

func (this *Tx) InsertIncident(i model.Incident) error {
	_, err := this.exec("INSERT INTO incidents ("+incidentColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		i.Id, i.Namespace, i.Owner, i.InstanceId, i.Run, i.DeploymentId, i.DeploymentVersion, i.ElementId, i.ElementName, i.Message, millis(i.Time), toJson(i.Variables))
	return err
}

func (this *Tx) ListIncidents(namespace string, options model.IncidentListOptions, access Access) (result model.Page[model.Incident], err error) {
	w := &where{}
	w.add("namespace = ?", namespace)
	if !access.Admin {
		// incidents are visible with the instance they belong to
		allowed := &where{}
		allowed.add("owner = ?", access.Owner)
		if len(access.Ids) > 0 {
			allowed.in("instance_id", access.Ids)
		}
		if len(access.Gateway) > 0 {
			gateway := &where{}
			gateway.in("id", access.Gateway)
			allowed.add("instance_id IN (SELECT id FROM instances WHERE network_id <> '' AND "+gateway.conditions[0]+")", gateway.args...)
		}
		w.add("("+strings.Join(allowed.conditions, " OR ")+")", allowed.args...)
	}
	if options.InstanceId != "" {
		w.add("instance_id = ?", options.InstanceId)
	}
	if options.Run > 0 {
		w.add("run = ?", options.Run)
	}
	if options.DeploymentId != "" {
		w.add("deployment_id = ?", options.DeploymentId)
	}
	if options.ElementId != "" {
		w.add("element_id = ?", options.ElementId)
	}
	order, err := orderBy(options.Sort, map[string]string{"time": "ts", "deployment_id": "deployment_id", "element_id": "element_id"}, "ts DESC, id ASC")
	if err != nil {
		return result, err
	}
	if result.Total, err = this.count("SELECT COUNT(*) FROM incidents"+w.String(), w.args...); err != nil {
		return result, err
	}
	limit, limitArgs := limitOffset(options.PageOptions)
	rows, err := this.query("SELECT "+incidentColumns+" FROM incidents"+w.String()+order+limit, append(w.args, limitArgs...)...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	result.Items = []model.Incident{}
	for rows.Next() {
		var i model.Incident
		var ts int64
		var variables string
		if err = rows.Scan(&i.Id, &i.Namespace, &i.Owner, &i.InstanceId, &i.Run, &i.DeploymentId, &i.DeploymentVersion, &i.ElementId, &i.ElementName, &i.Message, &ts, &variables); err != nil {
			return result, err
		}
		i.Time = fromMillis(ts)
		i.Variables = fromJson[map[string]any](variables)
		result.Items = append(result.Items, i)
	}
	page := options.PageOptions.Normalized()
	result.Limit, result.Offset = page.Limit, page.Offset
	return result, rows.Err()
}
