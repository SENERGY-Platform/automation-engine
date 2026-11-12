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
	"encoding/json"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

func (this *Tx) ListNamespaces() (result []model.Namespace, err error) {
	rows, err := this.query("SELECT config, created_at, updated_at FROM namespaces ORDER BY created_at, name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var config string
		var created, updated int64
		if err = rows.Scan(&config, &created, &updated); err != nil {
			return nil, err
		}
		var ns model.Namespace
		if err = json.Unmarshal([]byte(config), &ns); err != nil {
			return nil, err
		}
		ns.CreatedAt = fromMillis(created)
		ns.UpdatedAt = fromMillis(updated)
		result = append(result, ns)
	}
	return result, rows.Err()
}

// InsertNamespace stores a namespace unless one with the name exists; it reports whether it did.
func (this *Tx) InsertNamespace(ns model.Namespace, at time.Time) (bool, error) {
	result, err := this.exec("INSERT INTO namespaces (name, config, created_at, updated_at) VALUES (?, ?, ?, ?) ON CONFLICT (name) DO NOTHING",
		ns.Name, toJson(ns), millis(at), millis(at))
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (this *Tx) UpdateNamespace(ns model.Namespace, at time.Time) error {
	_, err := this.exec("UPDATE namespaces SET config = ?, updated_at = ? WHERE name = ?", toJson(ns), millis(at), ns.Name)
	return err
}

func (this *Tx) DeleteNamespace(name string) error {
	_, err := this.exec("DELETE FROM namespaces WHERE name = ?", name)
	return err
}

// NamespaceInUse reports whether models, deployments or instances belong to the namespace.
func (this *Tx) NamespaceInUse(name string) (bool, error) {
	for _, table := range []string{"models", "deployments", "instances"} {
		n, err := this.count("SELECT COUNT(*) FROM "+table+" WHERE namespace = ?", name)
		if err != nil || n > 0 {
			return n > 0, err
		}
	}
	return false, nil
}

func (this *Tx) GetUserSettings(userId string) (result model.UserSettings, err error) {
	result.UserId = userId
	err = this.queryRow("SELECT max_retention_days FROM user_settings WHERE user_id = ?", userId).Scan(&result.MaxRetentionDays)
	if err == sql.ErrNoRows {
		return result, nil
	}
	return result, err
}

func (this *Tx) SetUserSettings(settings model.UserSettings) error {
	_, err := this.exec("INSERT INTO user_settings (user_id, max_retention_days) VALUES (?, ?) ON CONFLICT (user_id) DO UPDATE SET max_retention_days = excluded.max_retention_days",
		settings.UserId, settings.MaxRetentionDays)
	return err
}

func (this *Tx) DeleteUserSettings(userId string) error {
	_, err := this.exec("DELETE FROM user_settings WHERE user_id = ?", userId)
	return err
}

// UserRetentionCaps lists users with a retention limit.
func (this *Tx) UserRetentionCaps() (result []model.UserSettings, err error) {
	rows, err := this.query("SELECT user_id, max_retention_days FROM user_settings WHERE max_retention_days > 0 ORDER BY user_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s model.UserSettings
		if err = rows.Scan(&s.UserId, &s.MaxRetentionDays); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

// Registration is a resource whose rights still have to be written to permissions-v2.
type Registration struct {
	Kind      model.Kind
	Id        string
	Namespace string
	Owner     string
}

// PendingRegistrations lists resources not yet registered in permissions-v2.
func (this *Tx) PendingRegistrations(kind model.Kind, limit int) (result []Registration, err error) {
	rows, err := this.query("SELECT id, namespace, owner FROM "+string(kind)+" WHERE perm_registered = 0 ORDER BY id LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		r := Registration{Kind: kind}
		if err = rows.Scan(&r.Id, &r.Namespace, &r.Owner); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// MarkRegistered records a registration done for owner; false if the resource is gone or its
// owner changed meanwhile, then the registration has to be repeated or undone.
func (this *Tx) MarkRegistered(kind model.Kind, id string, owner string) (bool, error) {
	result, err := this.exec("UPDATE "+string(kind)+" SET perm_registered = 1 WHERE id = ? AND owner = ?", id, owner)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// MarkUnregistered schedules a resource for (re)registration, e.g. after an owner change.
func (this *Tx) MarkUnregistered(kind model.Kind, id string) error {
	_, err := this.exec("UPDATE "+string(kind)+" SET perm_registered = 0 WHERE id = ?", id)
	return err
}

// MarkNamespaceUnregistered schedules all resources of a kind in a namespace for registration,
// e.g. when a level becomes registered.
func (this *Tx) MarkNamespaceUnregistered(kind model.Kind, namespace string) error {
	_, err := this.exec("UPDATE "+string(kind)+" SET perm_registered = 0 WHERE namespace = ?", namespace)
	return err
}

// queueRemovals queues the rights of matching resources for removal from permissions-v2; it
// runs before the resources are deleted. Unregistered resources may have a registration in
// flight, so they are queued as well; removing an unknown resource is harmless.
func (this *Tx) queueRemovals(table string, condition string, args ...any) error {
	if this.withoutRemovals {
		return nil
	}
	_, err := this.exec("INSERT INTO permission_removals (topic, resource_id, due_at) SELECT namespace || '_"+table+"', id, ? FROM "+table+" WHERE "+condition+" ON CONFLICT (topic, resource_id) DO NOTHING",
		append([]any{millis(time.Now())}, args...)...)
	return err
}

func (this *Tx) QueueRemoval(topic string, id string, at time.Time) error {
	if this.withoutRemovals {
		return nil
	}
	_, err := this.exec("INSERT INTO permission_removals (topic, resource_id, due_at) VALUES (?, ?, ?) ON CONFLICT (topic, resource_id) DO NOTHING", topic, id, millis(at))
	return err
}

type Removal struct {
	Topic      string
	ResourceId string
	Attempts   int
}

func (this *Tx) DueRemovals(now time.Time, limit int) (result []Removal, err error) {
	rows, err := this.query("SELECT topic, resource_id, attempts FROM permission_removals WHERE due_at <= ? ORDER BY due_at LIMIT ?", millis(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r Removal
		if err = rows.Scan(&r.Topic, &r.ResourceId, &r.Attempts); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (this *Tx) DeleteRemoval(topic string, id string) error {
	_, err := this.exec("DELETE FROM permission_removals WHERE topic = ? AND resource_id = ?", topic, id)
	return err
}

func (this *Tx) RetryRemoval(topic string, id string, due time.Time, message string) error {
	_, err := this.exec("UPDATE permission_removals SET attempts = attempts + 1, due_at = ?, last_error = ? WHERE topic = ? AND resource_id = ?", millis(due), message, topic, id)
	return err
}

// Notification is a message to a user, sent after commit through the notifier (INC-02).
type Notification struct {
	Id      string
	UserId  string
	Title   string
	Message string
	Topic   string
	// of the resource the notification is about; empty for none, e.g. the storage budget
	Namespace string
	// seconds within which the notifier drops it if the user already has one with the same title,
	// message and topic; 0 for none (SNRGY-3911)
	DuplicateWindow int64
	Attempts        int
	// when it is due; ClaimNotification takes it only at this value
	DueAt     time.Time
	CreatedAt time.Time
}

// DefaultDuplicateWindow is the window of the notifications the engine sends on its own, e.g. of
// incidents, as process-incident-api had: a repeated incident is reported once an hour.
const DefaultDuplicateWindow = 3600

// InsertNotification queues a notification; a queued one with the same id stays as it is.
func (this *Tx) InsertNotification(n Notification) error {
	_, err := this.exec("INSERT INTO notifications (id, user_id, title, message, topic, namespace, duplicate_window, due_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (id) DO NOTHING",
		n.Id, n.UserId, n.Title, n.Message, n.Topic, n.Namespace, n.DuplicateWindow, millis(n.CreatedAt), millis(n.CreatedAt))
	return err
}

func (this *Tx) DueNotifications(now time.Time, limit int) (result []Notification, err error) {
	rows, err := this.query("SELECT id, user_id, title, message, topic, namespace, duplicate_window, attempts, due_at, created_at FROM notifications WHERE due_at <= ? ORDER BY due_at LIMIT ?", millis(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var n Notification
		var due, created int64
		if err = rows.Scan(&n.Id, &n.UserId, &n.Title, &n.Message, &n.Topic, &n.Namespace, &n.DuplicateWindow, &n.Attempts, &due, &created); err != nil {
			return nil, err
		}
		n.DueAt, n.CreatedAt = fromMillis(due), fromMillis(created)
		result = append(result, n)
	}
	return result, rows.Err()
}

func (this *Tx) DeleteNotification(id string) error {
	_, err := this.exec("DELETE FROM notifications WHERE id = ?", id)
	return err
}

func (this *Tx) RetryNotification(id string, due time.Time, message string) error {
	_, err := this.exec("UPDATE notifications SET attempts = attempts + 1, due_at = ?, last_error = ? WHERE id = ?", millis(due), message, id)
	return err
}

// ClaimNotification takes a due notification for sending until the given time, if it is still due
// at dueAt as read: another replica that read it as well finds it claimed and leaves it, so that
// it is sent once. Sending ends the claim by deleting the notification or setting its retry; a
// replica that ends while sending leaves it to be sent again once the claim runs out.
func (this *Tx) ClaimNotification(id string, dueAt time.Time, until time.Time) (bool, error) {
	result, err := this.exec("UPDATE notifications SET due_at = ? WHERE id = ? AND due_at = ?", millis(until), id, millis(dueAt))
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

// PostponeNotification moves a notification that was not attempted, without counting an attempt.
// One that another replica claimed is not due at now and keeps its claim.
func (this *Tx) PostponeNotification(id string, due time.Time, now time.Time) error {
	_, err := this.exec("UPDATE notifications SET due_at = ? WHERE id = ? AND due_at <= ?", millis(due), id, millis(now))
	return err
}

// RestartStats count automatic restarts of a deployment within the current window (INC-04).
type RestartStats struct {
	WindowStart time.Time
	Count       int
	FaultySince *time.Time
}

func (this *Tx) GetRestartStats(deploymentId string) (result RestartStats, err error) {
	var window int64
	var faulty sql.NullInt64
	err = this.queryRow("SELECT restart_window_start, restart_count, faulty_since FROM deployments WHERE id = ?", deploymentId).Scan(&window, &result.Count, &faulty)
	result.WindowStart = fromMillis(window)
	result.FaultySince = fromNullMillis(faulty)
	return result, notFound(err, "deployment %v not found", deploymentId)
}

func (this *Tx) SetRestartStats(deploymentId string, stats RestartStats) error {
	_, err := this.exec("UPDATE deployments SET restart_window_start = ?, restart_count = ?, faulty_since = ? WHERE id = ?",
		millis(stats.WindowStart), stats.Count, nullMillis(stats.FaultySince), deploymentId)
	return err
}

// FaultySince lists the deployments marked faulty after a restart loop with the time of the mark;
// with networkId, it lists every deployment of that gateway, a nil time for those not marked.
func (this *Tx) FaultySince(networkId string) (map[string]*time.Time, error) {
	query, args := "SELECT id, faulty_since FROM deployments WHERE faulty_since IS NOT NULL", []any{}
	if networkId != "" {
		query, args = "SELECT id, faulty_since FROM deployments WHERE network_id = ?", []any{networkId}
	}
	rows, err := this.query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]*time.Time{}
	for rows.Next() {
		var id string
		var faulty sql.NullInt64
		if err = rows.Scan(&id, &faulty); err != nil {
			return nil, err
		}
		result[id] = fromNullMillis(faulty)
	}
	return result, rows.Err()
}

// SetFaultySince sets the faulty mark of a deployment alone; the cloud takes it so over from a
// gateway, which counts the restarts (SYNC-02).
func (this *Tx) SetFaultySince(deploymentId string, since *time.Time) error {
	_, err := this.exec("UPDATE deployments SET faulty_since = ? WHERE id = ?", nullMillis(since), deploymentId)
	return err
}

// DueRestarts lists instances whose automatic restart is due. Instances of gateways are left
// out: their gateway restarts them, the cloud only shows the planned time (FOG-07).
func (this *Tx) DueRestarts(now time.Time, limit int) (result []string, err error) {
	rows, err := this.query("SELECT id FROM instances WHERE restart_at IS NOT NULL AND restart_at <= ? AND network_id = '' ORDER BY restart_at LIMIT ?", millis(now), limit)
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

func (this *Tx) NextRestartDue() (*time.Time, error) {
	var due sql.NullInt64
	err := this.queryRow("SELECT MIN(restart_at) FROM instances WHERE network_id = ''").Scan(&due)
	return fromNullMillis(due), err
}

// ConsecutiveIncidentRuns counts the runs that ended with an incident, from the latest backwards.
func (this *Tx) ConsecutiveIncidentRuns(instanceId string) (int, error) {
	runs, err := this.ListRuns(instanceId)
	if err != nil {
		return 0, err
	}
	n := 0
	for i := len(runs) - 1; i >= 0 && runs[i].State == model.StateIncident; i-- {
		n++
	}
	return n, nil
}

// StorageCategories assigns the tables of instances to the parts of the storage report
// (LONG-14): runtime state, history, outboxes and IO values.
var StorageCategories = map[string][]string{
	"runtime": {"instances", "tokens", "subscriptions", "jobs", "variables", "command_subtasks", "instance_messages", "message_receipts", "modules", "module_keys", "watches", "watch_devices"},
	"history": {"activities", "incidents", "runs", "element_stats", "history_snapshots"},
	"outbox":  {"changes", "notifications", "permission_removals", "module_removals"},
	"io":      {"io_values"},
}

// StorageRows counts rows per table of the storage report (LONG-14).
func (this *Tx) StorageRows() (map[string]int, error) {
	result := map[string]int{}
	for _, tables := range StorageCategories {
		for _, table := range tables {
			n, err := this.count("SELECT COUNT(*) FROM " + table)
			if err != nil {
				return nil, err
			}
			result[table] = n
		}
	}
	return result, nil
}

// StorageBytes is the size of the tables per part of the storage report, their indexes
// included (LONG-14). On SQLite it reads every page of the database, so callers ask at most every
// few minutes.
func (this *Tx) StorageBytes() (map[string]int64, error) {
	perTable := map[string]int64{}
	if this.dialect == Postgres {
		for _, tables := range StorageCategories {
			for _, table := range tables {
				var size int64
				if err := this.queryRow("SELECT COALESCE(pg_total_relation_size(to_regclass(?)), 0)", table).Scan(&size); err != nil {
					return nil, err
				}
				perTable[table] = size
			}
		}
	} else {
		// an index counts for its table
		rows, err := this.query("SELECT COALESCE(m.tbl_name, s.name), SUM(s.pgsize) FROM dbstat s LEFT JOIN sqlite_master m ON m.name = s.name GROUP BY 1")
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var table string
			var size int64
			if err = rows.Scan(&table, &size); err != nil {
				return nil, err
			}
			perTable[table] = size
		}
		if err = rows.Err(); err != nil {
			return nil, err
		}
	}
	result := map[string]int64{}
	for category, tables := range StorageCategories {
		result[category] = 0
		for _, table := range tables {
			result[category] += perTable[table]
		}
	}
	return result, nil
}

// DatabaseBytes is the size of the database.
func (this *Tx) DatabaseBytes() (int64, error) {
	var size int64
	if this.dialect == Postgres {
		err := this.queryRow("SELECT pg_database_size(current_database())").Scan(&size)
		return size, err
	}
	var pages, pageSize int64
	if err := this.queryRow("PRAGMA page_count").Scan(&pages); err != nil {
		return 0, err
	}
	err := this.queryRow("PRAGMA page_size").Scan(&pageSize)
	return pages * pageSize, err
}

// StalledInstances counts running instances per namespace without element change since before (LONG-13).
func (this *Tx) StalledInstances(before time.Time) (map[string]int, error) {
	rows, err := this.query("SELECT namespace, COUNT(*) FROM instances WHERE state = 'running' AND last_activity_at < ? GROUP BY namespace", millis(before))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]int{}
	for rows.Next() {
		var ns string
		var n int
		if err = rows.Scan(&ns, &n); err != nil {
			return nil, err
		}
		result[ns] = n
	}
	return result, rows.Err()
}

type ActivityCount struct {
	InstanceId string
	Namespace  string
	Count      int64
}

// TopActivityInstances lists the instances with the most activity entries (LONG-14); the count
// follows from the sequence numbers, because the log is trimmed from its beginning.
func (this *Tx) TopActivityInstances(limit int) (result []ActivityCount, err error) {
	rows, err := this.query("SELECT id, namespace, activity_seq - activity_min_seq + 1 AS n FROM instances ORDER BY n DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c ActivityCount
		if err = rows.Scan(&c.InstanceId, &c.Namespace, &c.Count); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
