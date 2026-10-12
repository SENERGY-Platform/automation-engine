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
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

const deploymentColumns = "id, namespace, name, description, owner, model_id, model_version, model_deleted, version, source, retention_days, faulty_since, created_at, updated_at, managed_by, network_id"

func scanDeployment(row interface{ Scan(...any) error }) (d model.Deployment, err error) {
	var deleted int
	var created, updated int64
	var faulty sql.NullInt64
	err = row.Scan(&d.Id, &d.Namespace, &d.Name, &d.Description, &d.Owner, &d.ModelId, &d.ModelVersion, &deleted, &d.Version, &d.Source, &d.RetentionDays, &faulty, &created, &updated, &d.ManagedBy, &d.NetworkId)
	d.ModelDeleted = deleted == 1
	d.FaultySince = fromNullMillis(faulty)
	d.CreatedAt = fromMillis(created)
	d.UpdatedAt = fromMillis(updated)
	return d, err
}

func (this *Tx) InsertDeployment(d model.Deployment) error {
	_, err := this.exec("INSERT INTO deployments ("+deploymentColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		d.Id, d.Namespace, d.Name, d.Description, d.Owner, d.ModelId, d.ModelVersion, boolInt(d.ModelDeleted), d.Version, d.Source, d.RetentionDays, nullMillis(d.FaultySince), millis(d.CreatedAt), millis(d.UpdatedAt), d.ManagedBy, d.NetworkId)
	return err
}

func (this *Tx) UpdateDeployment(d model.Deployment) error {
	_, err := this.exec("UPDATE deployments SET name = ?, description = ?, owner = ?, model_id = ?, model_version = ?, model_deleted = ?, version = ?, source = ?, retention_days = ?, faulty_since = ?, updated_at = ?, managed_by = ? WHERE id = ?",
		d.Name, d.Description, d.Owner, d.ModelId, d.ModelVersion, boolInt(d.ModelDeleted), d.Version, d.Source, d.RetentionDays, nullMillis(d.FaultySince), millis(d.UpdatedAt), d.ManagedBy, d.Id)
	return err
}

func (this *Tx) GetDeployment(id string, lock bool) (model.Deployment, error) {
	d, err := scanDeployment(this.queryRow("SELECT "+deploymentColumns+" FROM deployments WHERE id = ?"+this.forUpdate(lock), id))
	return d, notFound(err, "deployment %v not found", id)
}

// DeleteDeployment removes the deployment with all versions, instances, history, incidents
// and start events in one statement through ON DELETE CASCADE (DEP-11). Registered rights of
// the deployment and its instances are queued for removal.
func (this *Tx) DeleteDeployment(id string) error {
	if err := this.RemoveEventDescriptions(id, time.Now()); err != nil {
		return err
	}
	if err := this.queueRemovals("instances", "deployment_id = ?", id); err != nil {
		return err
	}
	if err := this.queueModuleRemovals("instance_id IN (SELECT id FROM instances WHERE deployment_id = ?)", id); err != nil {
		return err
	}
	if err := this.queueRemovals("deployments", "id = ?", id); err != nil {
		return err
	}
	_, err := this.exec("DELETE FROM deployments WHERE id = ?", id)
	return err
}

func (this *Tx) ListDeployments(namespace string, options model.DeploymentListOptions, access Access) (result model.Page[model.Deployment], err error) {
	w := &where{}
	w.add("namespace = ?", namespace)
	w.access("", access)
	w.search("name", options.Search)
	if len(options.Ids) > 0 {
		w.in("id", options.Ids)
	}
	if options.ModelId != "" {
		w.add("model_id = ?", options.ModelId)
	}
	if options.Owner != "" {
		w.add("owner = ?", options.Owner)
	}
	if options.Source != "" {
		w.add("source = ?", options.Source)
	}
	w.network(options.NetworkId)
	order, err := orderBy(options.Sort, map[string]string{"name": "LOWER(name)", "created_at": "created_at", "updated_at": "updated_at"}, "LOWER(name) ASC, id ASC")
	if err != nil {
		return result, err
	}
	if result.Total, err = this.count("SELECT COUNT(*) FROM deployments"+w.String(), w.args...); err != nil {
		return result, err
	}
	limit, limitArgs := limitOffset(options.PageOptions)
	rows, err := this.query("SELECT "+deploymentColumns+" FROM deployments"+w.String()+order+limit, append(w.args, limitArgs...)...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	result.Items = []model.Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return result, err
		}
		result.Items = append(result.Items, d)
	}
	page := options.PageOptions.Normalized()
	result.Limit, result.Offset = page.Limit, page.Offset
	return result, rows.Err()
}

func (this *Tx) InsertDeploymentVersion(v model.DeploymentVersion) error {
	selections := ""
	if len(v.Selections) > 0 {
		selections = toJson(v.Selections)
	}
	parameters := ""
	if len(v.Parameters) > 0 {
		parameters = toJson(v.Parameters)
	}
	_, err := this.exec("INSERT INTO deployment_versions (deployment_id, version, model_version, xml, svg, update_strategy, incident_restart, incident_notify, selections, parameters, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		v.DeploymentId, v.Version, v.ModelVersion, v.Xml, v.Svg, string(v.UpdateStrategy), boolInt(v.IncidentHandling.Restart), boolInt(v.IncidentHandling.Notify), selections, parameters, millis(v.CreatedAt))
	return err
}

func (this *Tx) GetDeploymentVersion(deploymentId string, version int) (v model.DeploymentVersion, err error) {
	var strategy string
	var created int64
	var restart, notify int
	var selections, parameters string
	err = this.queryRow("SELECT deployment_id, version, model_version, xml, svg, update_strategy, incident_restart, incident_notify, selections, parameters, created_at FROM deployment_versions WHERE deployment_id = ? AND version = ?", deploymentId, version).
		Scan(&v.DeploymentId, &v.Version, &v.ModelVersion, &v.Xml, &v.Svg, &strategy, &restart, &notify, &selections, &parameters, &created)
	v.Selections = fromJson[map[string]model.Selection](selections)
	v.Parameters = fromJson[map[string]model.ElementParameters](parameters)
	v.UpdateStrategy = model.UpdateStrategy(strategy)
	v.IncidentHandling = model.IncidentHandling{Restart: restart == 1, Notify: notify == 1}
	v.CreatedAt = fromMillis(created)
	return v, notFound(err, "version %v of deployment %v not found", version, deploymentId)
}

func (this *Tx) GetIncidentHandling(deploymentId string, version int) (result model.IncidentHandling, err error) {
	var restart, notify int
	err = this.queryRow("SELECT incident_restart, incident_notify FROM deployment_versions WHERE deployment_id = ? AND version = ?", deploymentId, version).Scan(&restart, &notify)
	return model.IncidentHandling{Restart: restart == 1, Notify: notify == 1}, notFound(err, "version %v of deployment %v not found", version, deploymentId)
}

func (this *Tx) DeploymentVersionCreated(deploymentId string, version int) (time.Time, error) {
	var created int64
	err := this.queryRow("SELECT created_at FROM deployment_versions WHERE deployment_id = ? AND version = ?", deploymentId, version).Scan(&created)
	return fromMillis(created), notFound(err, "version %v of deployment %v not found", version, deploymentId)
}

// ListDeploymentVersions returns versions without XML and SVG, newest first.
func (this *Tx) ListDeploymentVersions(deploymentId string, page model.PageOptions) (result model.Page[model.DeploymentVersion], err error) {
	if result.Total, err = this.count("SELECT COUNT(*) FROM deployment_versions WHERE deployment_id = ?", deploymentId); err != nil {
		return result, err
	}
	limit, limitArgs := limitOffset(page)
	rows, err := this.query("SELECT deployment_id, version, model_version, update_strategy, incident_restart, incident_notify, created_at FROM deployment_versions WHERE deployment_id = ? ORDER BY version DESC"+limit, append([]any{deploymentId}, limitArgs...)...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	result.Items = []model.DeploymentVersion{}
	for rows.Next() {
		var v model.DeploymentVersion
		var strategy string
		var created int64
		var restart, notify int
		if err = rows.Scan(&v.DeploymentId, &v.Version, &v.ModelVersion, &strategy, &restart, &notify, &created); err != nil {
			return result, err
		}
		v.UpdateStrategy = model.UpdateStrategy(strategy)
		v.IncidentHandling = model.IncidentHandling{Restart: restart == 1, Notify: notify == 1}
		v.CreatedAt = fromMillis(created)
		result.Items = append(result.Items, v)
	}
	page = page.Normalized()
	result.Limit, result.Offset = page.Limit, page.Offset
	return result, rows.Err()
}

// StartEvent is a message or timer start event of the current version of a deployment.
type StartEvent struct {
	DeploymentId string
	Version      int
	ElementId    string
	Kind         string
	MessageName  string
	DueAt        *time.Time
	// from the deployment
	Namespace string
	Owner     string
}

func (this *Tx) ReplaceStartEvents(deploymentId string, events []StartEvent) error {
	if _, err := this.exec("DELETE FROM start_events WHERE deployment_id = ?", deploymentId); err != nil {
		return err
	}
	for _, e := range events {
		_, err := this.exec("INSERT INTO start_events (deployment_id, version, element_id, kind, message_name, due_at) VALUES (?, ?, ?, ?, ?, ?)",
			deploymentId, e.Version, e.ElementId, e.Kind, e.MessageName, nullMillis(e.DueAt))
		if err != nil {
			return err
		}
	}
	return nil
}

const startEventSelect = "SELECT s.deployment_id, s.version, s.element_id, s.kind, s.message_name, s.due_at, d.namespace, d.owner FROM start_events s JOIN deployments d ON d.id = s.deployment_id"

func scanStartEvents(rows *sql.Rows, err error) (result []StartEvent, _ error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e StartEvent
		var due sql.NullInt64
		if err = rows.Scan(&e.DeploymentId, &e.Version, &e.ElementId, &e.Kind, &e.MessageName, &due, &e.Namespace, &e.Owner); err != nil {
			return nil, err
		}
		e.DueAt = fromNullMillis(due)
		result = append(result, e)
	}
	return result, rows.Err()
}

// MessageStartEvents finds message start events by message name, optionally of one owner.
func (this *Tx) MessageStartEvents(messageName string, owner string) ([]StartEvent, error) {
	w := &where{}
	w.add("s.kind = 'message'")
	w.add("s.message_name = ?", messageName)
	if owner != "" {
		w.add("d.owner = ?", owner)
	}
	return scanStartEvents(this.query(startEventSelect+w.String()+" ORDER BY s.deployment_id, s.element_id", w.args...))
}

func (this *Tx) DueStartTimers(now time.Time, limit int) ([]StartEvent, error) {
	return scanStartEvents(this.query(startEventSelect+" WHERE s.kind = 'timer' AND s.due_at IS NOT NULL AND s.due_at <= ? ORDER BY s.due_at LIMIT ?", millis(now), limit))
}

func (this *Tx) GetStartEvent(deploymentId string, elementId string) (StartEvent, error) {
	events, err := scanStartEvents(this.query(startEventSelect+" WHERE s.deployment_id = ? AND s.element_id = ?", deploymentId, elementId))
	if err != nil {
		return StartEvent{}, err
	}
	if len(events) == 0 {
		return StartEvent{}, model.NotFound("start event %v of deployment %v not found", elementId, deploymentId)
	}
	return events[0], nil
}

func (this *Tx) SetStartEventDue(deploymentId string, elementId string, due *time.Time) error {
	_, err := this.exec("UPDATE start_events SET due_at = ? WHERE deployment_id = ? AND element_id = ?", nullMillis(due), deploymentId, elementId)
	return err
}

func (this *Tx) NextStartTimerDue() (*time.Time, error) {
	var due sql.NullInt64
	err := this.queryRow("SELECT MIN(due_at) FROM start_events WHERE kind = 'timer'").Scan(&due)
	return fromNullMillis(due), err
}
