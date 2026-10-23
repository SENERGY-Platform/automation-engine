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
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

const moduleColumns = "m.id, m.namespace, m.instance_id, m.created_by, m.element_id, m.owner, m.module_type, m.data, m.delete_info, m.error, m.created_at, m.updated_at"

func (this *Tx) scanModules(rows *sql.Rows) (result []model.Module, err error) {
	defer rows.Close()
	result = []model.Module{}
	for rows.Next() {
		var m model.Module
		var data, deleteInfo string
		var created, updated int64
		if err = rows.Scan(&m.Id, &m.Namespace, &m.InstanceId, &m.CreatedBy, &m.ElementId, &m.Owner, &m.ModuleType, &data, &deleteInfo, &m.Error, &created, &updated); err != nil {
			return nil, err
		}
		m.Data = fromJson[map[string]any](data)
		if m.Data == nil {
			m.Data = map[string]any{}
		}
		if deleteInfo != "" {
			info := fromJson[model.ModuleDeleteInfo](deleteInfo)
			m.DeleteInfo = &info
		}
		m.CreatedAt, m.UpdatedAt = fromMillis(created), fromMillis(updated)
		result = append(result, m)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return result, this.withKeys(result)
}

func (this *Tx) withKeys(modules []model.Module) error {
	if len(modules) == 0 {
		return nil
	}
	index := map[string]int{}
	args := []any{}
	for i := range modules {
		modules[i].Keys = []string{}
		index[modules[i].Id] = i
		args = append(args, modules[i].Id)
	}
	rows, err := this.query("SELECT module_id, module_key FROM module_keys WHERE module_id IN ("+strings.TrimSuffix(strings.Repeat("?, ", len(args)), ", ")+") ORDER BY module_key", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, key string
		if err = rows.Scan(&id, &key); err != nil {
			return err
		}
		modules[index[id]].Keys = append(modules[index[id]].Keys, key)
	}
	return rows.Err()
}

func deleteInfoJson(info *model.ModuleDeleteInfo) string {
	if info == nil {
		return ""
	}
	return toJson(info)
}

// UpsertModule writes a module; an existing one with the same id is replaced, created_at stays.
// A written module counts as checked when it was written: its resource may still be starting, a
// pipeline for instance, and the workers checked a new module only at their next pass (SMS-11).
// The error of a replaced resource goes with it.
func (this *Tx) UpsertModule(m model.Module) error {
	_, err := this.exec("INSERT INTO modules (id, namespace, instance_id, created_by, element_id, owner, module_type, data, delete_info, error, checked_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?) "+
		"ON CONFLICT (id) DO UPDATE SET instance_id = excluded.instance_id, created_by = excluded.created_by, element_id = excluded.element_id, owner = excluded.owner, module_type = excluded.module_type, data = excluded.data, delete_info = excluded.delete_info, error = '', checked_at = excluded.checked_at, updated_at = excluded.updated_at",
		m.Id, m.Namespace, m.InstanceId, m.CreatedBy, m.ElementId, m.Owner, m.ModuleType, toJson(m.Data), deleteInfoJson(m.DeleteInfo), millis(m.UpdatedAt), millis(m.CreatedAt), millis(m.UpdatedAt))
	if err != nil {
		return err
	}
	if _, err = this.exec("DELETE FROM module_keys WHERE module_id = ?", m.Id); err != nil {
		return err
	}
	for _, key := range m.Keys {
		if _, err = this.exec("INSERT INTO module_keys (module_id, module_key) VALUES (?, ?) ON CONFLICT (module_id, module_key) DO NOTHING", m.Id, key); err != nil {
			return err
		}
	}
	return nil
}

func (this *Tx) GetModule(id string) (model.Module, error) {
	rows, err := this.query("SELECT "+moduleColumns+" FROM modules m WHERE m.id = ?", id)
	if err != nil {
		return model.Module{}, err
	}
	modules, err := this.scanModules(rows)
	if err != nil {
		return model.Module{}, err
	}
	if len(modules) == 0 {
		return model.Module{}, model.NotFound("module %v not found", id)
	}
	return modules[0], nil
}

// ModulesOfInstance lists the modules of an instance, optionally only those with a key and type.
func (this *Tx) ModulesOfInstance(instanceId string, key string, moduleType string) ([]model.Module, error) {
	w := &where{}
	w.add("m.instance_id = ?", instanceId)
	if key != "" {
		w.add("m.id IN (SELECT module_id FROM module_keys WHERE module_key = ?)", key)
	}
	if moduleType != "" {
		w.add("m.module_type = ?", moduleType)
	}
	rows, err := this.query("SELECT "+moduleColumns+" FROM modules m"+w.String()+" ORDER BY m.created_at, m.id", w.args...)
	if err != nil {
		return nil, err
	}
	return this.scanModules(rows)
}

// ListModules lists the modules of the instances of a namespace the access allows (SMS-12).
func (this *Tx) ListModules(namespace string, options model.ModuleListOptions, access Access) (result model.Page[model.Module], err error) {
	w := &where{}
	w.add("m.namespace = ?", namespace)
	w.access("i.", access)
	if options.InstanceId != "" {
		w.add("m.instance_id = ?", options.InstanceId)
	}
	if options.ModuleType != "" {
		w.add("m.module_type = ?", options.ModuleType)
	}
	if options.Key != "" {
		w.add("m.id IN (SELECT module_id FROM module_keys WHERE module_key = ?)", options.Key)
	}
	from := " FROM modules m JOIN instances i ON i.id = m.instance_id"
	if result.Total, err = this.count("SELECT COUNT(*)"+from+w.String(), w.args...); err != nil {
		return result, err
	}
	limit, limitArgs := limitOffset(options.PageOptions)
	rows, err := this.query("SELECT "+moduleColumns+from+w.String()+" ORDER BY m.created_at, m.id"+limit, append(w.args, limitArgs...)...)
	if err != nil {
		return result, err
	}
	if result.Items, err = this.scanModules(rows); err != nil {
		return result, err
	}
	page := options.PageOptions.Normalized()
	result.Limit, result.Offset = page.Limit, page.Offset
	return result, nil
}

// DeleteModule removes a module and queues the removal of its resource (SMS-06).
func (this *Tx) DeleteModule(id string) error {
	if err := this.queueModuleRemovals("id = ?", id); err != nil {
		return err
	}
	_, err := this.exec("DELETE FROM modules WHERE id = ?", id)
	return err
}

// queueModuleRemovals queues the resources of the matching modules for removal; the rows go
// with their instance through ON DELETE CASCADE
func (this *Tx) queueModuleRemovals(condition string, args ...any) error {
	_, err := this.exec("INSERT INTO module_removals (module_id, namespace, owner, module_type, delete_info, next_at, created_at) "+
		"SELECT id, namespace, owner, module_type, delete_info, ?, ? FROM modules WHERE delete_info <> '' AND "+condition+" ON CONFLICT (module_id) DO NOTHING",
		append([]any{millis(time.Now()), millis(time.Now())}, args...)...)
	return err
}

type ModuleRemoval struct {
	ModuleId   string
	Namespace  string
	Owner      string
	ModuleType string
	DeleteInfo model.ModuleDeleteInfo
	Attempts   int
}

func (this *Tx) DueModuleRemovals(now time.Time, limit int) (result []ModuleRemoval, err error) {
	rows, err := this.query("SELECT module_id, namespace, owner, module_type, delete_info, attempts FROM module_removals WHERE next_at <= ? ORDER BY next_at LIMIT ?", millis(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r ModuleRemoval
		var info string
		if err = rows.Scan(&r.ModuleId, &r.Namespace, &r.Owner, &r.ModuleType, &info, &r.Attempts); err != nil {
			return nil, err
		}
		r.DeleteInfo = fromJson[model.ModuleDeleteInfo](info)
		result = append(result, r)
	}
	return result, rows.Err()
}

func (this *Tx) FinishModuleRemoval(moduleId string) error {
	_, err := this.exec("DELETE FROM module_removals WHERE module_id = ?", moduleId)
	return err
}

func (this *Tx) RetryModuleRemoval(moduleId string, next time.Time, message string) error {
	_, err := this.exec("UPDATE module_removals SET attempts = attempts + 1, next_at = ?, last_error = ? WHERE module_id = ?", millis(next), message, moduleId)
	return err
}

// ModulesToCheck lists modules of a type whose last health check is older than before (SMS-11).
func (this *Tx) ModulesToCheck(moduleType string, before time.Time, limit int) ([]model.Module, error) {
	rows, err := this.query("SELECT "+moduleColumns+" FROM modules m WHERE m.module_type = ? AND m.checked_at <= ? ORDER BY m.checked_at LIMIT ?", moduleType, millis(before), limit)
	if err != nil {
		return nil, err
	}
	return this.scanModules(rows)
}

// SetModuleCheck stores the result of a health check; an empty message clears the error.
func (this *Tx) SetModuleCheck(id string, message string, at time.Time) error {
	_, err := this.exec("UPDATE modules SET error = ?, checked_at = ? WHERE id = ?", message, millis(at), id)
	return err
}

// ModuleErrors returns the first module error per instance, for the status (SMS-11).
func (this *Tx) ModuleErrors(instanceIds []string) (map[string]model.Module, error) {
	result := map[string]model.Module{}
	if len(instanceIds) == 0 {
		return result, nil
	}
	w := &where{}
	w.in("m.instance_id", instanceIds)
	w.add("m.error <> ''")
	rows, err := this.query("SELECT "+moduleColumns+" FROM modules m"+w.String()+" ORDER BY m.id", w.args...)
	if err != nil {
		return nil, err
	}
	modules, err := this.scanModules(rows)
	for _, m := range modules {
		if _, ok := result[m.InstanceId]; !ok {
			result[m.InstanceId] = m
		}
	}
	return result, err
}

// BufferMessage keeps a message for an instance until it waits for it (SMS-04); a newer one
// with the same name replaces it.
func (this *Tx) BufferMessage(instanceId string, name string, variables map[string]any, at time.Time) error {
	_, err := this.exec("INSERT INTO instance_messages (instance_id, message_name, variables, created_at) VALUES (?, ?, ?, ?) "+
		"ON CONFLICT (instance_id, message_name) DO UPDATE SET variables = excluded.variables, created_at = excluded.created_at",
		instanceId, name, toJson(variables), millis(at))
	return err
}

// TakeMessage removes and returns a buffered message; false if there is none.
func (this *Tx) TakeMessage(instanceId string, name string) (variables map[string]any, found bool, err error) {
	var raw string
	err = this.queryRow("SELECT variables FROM instance_messages WHERE instance_id = ? AND message_name = ?", instanceId, name).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	_, err = this.exec("DELETE FROM instance_messages WHERE instance_id = ? AND message_name = ?", instanceId, name)
	return fromJson[map[string]any](raw), true, err
}

// ChildInstances lists the maintenance procedures of an instance (SMS-13).
func (this *Tx) ChildInstances(parentId string) (result []string, err error) {
	rows, err := this.query("SELECT id FROM instances WHERE parent_id = ? ORDER BY id", parentId)
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

// RemoveModulesOfInstance removes the modules and maintenance procedures of an instance, e.g.
// before a new run; the resources of the modules are queued for removal. It returns the number
// of removed modules.
func (this *Tx) RemoveModulesOfInstance(instanceId string) (int, error) {
	children, err := this.ChildInstances(instanceId)
	if err != nil {
		return 0, err
	}
	for _, child := range children {
		if err = this.DeleteInstance(child); err != nil {
			return 0, err
		}
	}
	if err = this.queueModuleRemovals("instance_id = ?", instanceId); err != nil {
		return 0, err
	}
	result, err := this.exec("DELETE FROM modules WHERE instance_id = ?", instanceId)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	return int(n), err
}

// RemoveResource removes a resource of the engine a module refers to; a missing one counts as
// removed. The namespace must match, so that a module cannot reach into other namespaces. For a
// deployment or instance of a gateway, it returns the network id: only the cloud rows are
// removed, the caller desires the deletion on the gateway.
func (this *Tx) RemoveResource(ref model.ResourceRef) (networkId string, err error) {
	var namespace string
	var query string
	switch ref.Kind {
	case model.ResourceDeployment:
		query = "SELECT namespace, network_id FROM deployments WHERE id = ?"
	case model.ResourceInstance:
		query = "SELECT namespace, network_id FROM instances WHERE id = ?"
	case model.ResourceWatch:
		query = "SELECT namespace, '' FROM watches WHERE id = ?"
	default:
		return "", model.BadRequest("unknown resource kind %q", ref.Kind)
	}
	err = this.queryRow(query, ref.Id).Scan(&namespace, &networkId)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if ref.Namespace != "" && namespace != ref.Namespace {
		return "", model.Forbidden("%v %v is not in namespace %v", ref.Kind, ref.Id, ref.Namespace)
	}
	switch ref.Kind {
	case model.ResourceDeployment:
		return networkId, this.DeleteDeployment(ref.Id)
	case model.ResourceInstance:
		return networkId, this.DeleteInstance(ref.Id)
	default:
		_, err = this.exec("DELETE FROM watches WHERE id = ?", ref.Id)
		return "", err
	}
}

// Watch is a periodic check of the watcher task type (TASK-27).
type Watch struct {
	Id         string
	InstanceId string
	Namespace  string
	Owner      string
	Procedure  string
	Parameters map[string]any
	Request    model.WatchRequest
	HashType   string
	Interval   time.Duration
	LastHash   string
	NextAt     time.Time
	LastError  string
	CreatedAt  time.Time
}

const watchColumns = "id, instance_id, namespace, owner, procedure_name, parameters, request, hash_type, interval_ms, last_hash, next_at, last_error, created_at"

// SaveWatch creates or replaces a watch; a replaced one starts over with a new baseline.
func (this *Tx) SaveWatch(w Watch) error {
	_, err := this.exec("INSERT INTO watches ("+watchColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, '', ?) "+
		"ON CONFLICT (id) DO UPDATE SET procedure_name = excluded.procedure_name, parameters = excluded.parameters, request = excluded.request, "+
		"hash_type = excluded.hash_type, interval_ms = excluded.interval_ms, last_hash = '', next_at = excluded.next_at, last_error = ''",
		w.Id, w.InstanceId, w.Namespace, w.Owner, w.Procedure, toJson(w.Parameters), toJson(w.Request), w.HashType, w.Interval.Milliseconds(), millis(w.NextAt), millis(w.CreatedAt))
	return err
}

// ClaimDueWatches returns the watches due at now and moves their next check one interval past
// now, so that each is checked once per interval by one replica.
func (this *Tx) ClaimDueWatches(now time.Time, limit int) (result []Watch, err error) {
	rows, err := this.query("SELECT "+watchColumns+" FROM watches WHERE next_at <= ? ORDER BY next_at LIMIT ?"+this.forUpdate(true), millis(now), limit)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var w Watch
		var parameters, request string
		var interval, next, created int64
		if err = rows.Scan(&w.Id, &w.InstanceId, &w.Namespace, &w.Owner, &w.Procedure, &parameters, &request, &w.HashType, &interval, &w.LastHash, &next, &w.LastError, &created); err != nil {
			rows.Close()
			return nil, err
		}
		w.Parameters, w.Request = fromJson[map[string]any](parameters), fromJson[model.WatchRequest](request)
		w.Interval, w.NextAt, w.CreatedAt = time.Duration(interval)*time.Millisecond, fromMillis(next), fromMillis(created)
		result = append(result, w)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for _, w := range result {
		if _, err = this.exec("UPDATE watches SET next_at = ? WHERE id = ?", millis(now.Add(w.Interval)), w.Id); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// UpdateWatch stores the result of a check.
func (this *Tx) UpdateWatch(id string, hash string, message string) error {
	_, err := this.exec("UPDATE watches SET last_hash = ?, last_error = ? WHERE id = ?", hash, message, id)
	return err
}

func (this *Tx) DeleteWatch(id string) error {
	_, err := this.exec("DELETE FROM watches WHERE id = ?", id)
	return err
}
