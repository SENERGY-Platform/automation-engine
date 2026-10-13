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

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// SetIoValue stores a value; deployment and instance ids bind it to their lifetime (IO-03).
func (this *Tx) SetIoValue(userId string, v model.IoValue) error {
	_, err := this.exec("INSERT INTO io_values (user_id, io_key, value, deployment_id, instance_id, updated_at) VALUES (?, ?, ?, ?, ?, ?) "+
		"ON CONFLICT (user_id, io_key) DO UPDATE SET value = excluded.value, deployment_id = excluded.deployment_id, instance_id = excluded.instance_id, updated_at = excluded.updated_at",
		userId, v.Key, toJson(v.Value), nullString(v.DeploymentId), nullString(v.InstanceId), millis(v.UpdatedAt))
	return err
}

// GetIoValue returns the value of a key; ok is false if the key is not set.
func (this *Tx) GetIoValue(userId string, key string) (result model.IoValue, ok bool, err error) {
	var raw string
	var deployment, instance sql.NullString
	var updated int64
	err = this.queryRow("SELECT io_key, value, deployment_id, instance_id, updated_at FROM io_values WHERE user_id = ? AND io_key = ?", userId, key).
		Scan(&result.Key, &raw, &deployment, &instance, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	result.Value = fromJson[any](raw)
	result.DeploymentId, result.InstanceId = deployment.String, instance.String
	result.UpdatedAt = fromMillis(updated)
	return result, true, nil
}

func (this *Tx) DeleteIoValue(userId string, key string) error {
	_, err := this.exec("DELETE FROM io_values WHERE user_id = ? AND io_key = ?", userId, key)
	return err
}

// ListIoValues lists the values of a user filtered by deployment and instance; the key filter
// is applied by the caller, SQLite has no regular expressions.
func (this *Tx) ListIoValues(userId string, deploymentId string, instanceId string, sort string) (result []model.IoValue, err error) {
	w := &where{}
	w.add("user_id = ?", userId)
	if deploymentId != "" {
		w.add("deployment_id = ?", deploymentId)
	}
	if instanceId != "" {
		w.add("instance_id = ?", instanceId)
	}
	order := " ORDER BY io_key ASC"
	switch sort {
	case "", "key", "key.asc":
	case "key.desc":
		order = " ORDER BY io_key DESC"
	case "updated_at", "updated_at.asc":
		order = " ORDER BY updated_at ASC, io_key ASC"
	case "updated_at.desc":
		order = " ORDER BY updated_at DESC, io_key ASC"
	default:
		return nil, model.BadRequest("unknown sort %q", sort)
	}
	rows, err := this.query("SELECT io_key, value, deployment_id, instance_id, updated_at FROM io_values"+w.String()+order, w.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result = []model.IoValue{}
	for rows.Next() {
		var v model.IoValue
		var raw string
		var deployment, instance sql.NullString
		var updated int64
		if err = rows.Scan(&v.Key, &raw, &deployment, &instance, &updated); err != nil {
			return nil, err
		}
		v.Value = fromJson[any](raw)
		v.DeploymentId, v.InstanceId = deployment.String, instance.String
		v.UpdatedAt = fromMillis(updated)
		result = append(result, v)
	}
	return result, rows.Err()
}

// CountIoValues counts the values of a user that no instance holds, for the limit (LONG-11).
func (this *Tx) CountIoValues(userId string) (int, error) {
	return this.count("SELECT COUNT(*) FROM io_values WHERE user_id = ? AND instance_id IS NULL", userId)
}

const scheduleColumns = "id, namespace, deployment_id, owner, cron, alias, disabled, next_due, created_at, updated_at, created_by"

func scanSchedule(row interface{ Scan(...any) error }) (s model.Schedule, err error) {
	var disabled int
	var due sql.NullInt64
	var created, updated int64
	err = row.Scan(&s.Id, &s.Namespace, &s.DeploymentId, &s.Owner, &s.Cron, &s.Alias, &disabled, &due, &created, &updated, &s.CreatedBy)
	s.Disabled = disabled == 1
	s.NextDue = fromNullMillis(due)
	s.CreatedAt, s.UpdatedAt = fromMillis(created), fromMillis(updated)
	return s, err
}

func (this *Tx) SaveSchedule(s model.Schedule) error {
	_, err := this.exec("INSERT INTO schedules ("+scheduleColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) "+
		"ON CONFLICT (id) DO UPDATE SET deployment_id = excluded.deployment_id, cron = excluded.cron, alias = excluded.alias, disabled = excluded.disabled, next_due = excluded.next_due, updated_at = excluded.updated_at, created_by = excluded.created_by",
		s.Id, s.Namespace, s.DeploymentId, s.Owner, s.Cron, s.Alias, boolInt(s.Disabled), nullMillis(s.NextDue), millis(s.CreatedAt), millis(s.UpdatedAt), s.CreatedBy)
	return err
}

func (this *Tx) GetSchedule(id string) (model.Schedule, error) {
	s, err := scanSchedule(this.queryRow("SELECT "+scheduleColumns+" FROM schedules WHERE id = ?", id))
	return s, notFound(err, "schedule %v not found", id)
}

func (this *Tx) DeleteSchedule(id string) error {
	_, err := this.exec("DELETE FROM schedules WHERE id = ?", id)
	return err
}

func (this *Tx) ListSchedules(namespace string, options model.ScheduleListOptions, access Access) (result model.Page[model.Schedule], err error) {
	w := &where{}
	w.add("namespace = ?", namespace)
	w.access("", access)
	if options.DeploymentId != "" {
		w.add("deployment_id = ?", options.DeploymentId)
	}
	if options.Owner != "" {
		w.add("owner = ?", options.Owner)
	}
	if options.CreatedBy != "" {
		w.add("created_by = ?", options.CreatedBy)
	}
	if result.Total, err = this.count("SELECT COUNT(*) FROM schedules"+w.String(), w.args...); err != nil {
		return result, err
	}
	limit, limitArgs := limitOffset(options.PageOptions)
	rows, err := this.query("SELECT "+scheduleColumns+" FROM schedules"+w.String()+" ORDER BY created_at, id"+limit, append(w.args, limitArgs...)...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	result.Items = []model.Schedule{}
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return result, err
		}
		result.Items = append(result.Items, s)
	}
	page := options.PageOptions.Normalized()
	result.Limit, result.Offset = page.Limit, page.Offset
	return result, rows.Err()
}

func (this *Tx) DueSchedules(now time.Time, limit int) (result []model.Schedule, err error) {
	rows, err := this.query("SELECT "+scheduleColumns+" FROM schedules WHERE disabled = 0 AND next_due IS NOT NULL AND next_due <= ? ORDER BY next_due LIMIT ?", millis(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, rows.Err()
}
