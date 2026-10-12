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

const eventColumns = "id, deployment_id, version, element_id, namespace, owner, message_name, device_id, service_id, device_group_id, import_id, topic, script, value_variable, variables, qos, characteristic_id, function_id, aspect_ids, path, service, revision"

func scanEventDescription(row interface{ Scan(...any) error }) (d model.EventDescription, err error) {
	var variables, aspects, service string
	err = row.Scan(&d.Id, &d.DeploymentId, &d.Version, &d.ElementId, &d.Namespace, &d.Owner, &d.MessageName, &d.DeviceId, &d.ServiceId, &d.DeviceGroupId, &d.ImportId,
		&d.Topic, &d.Script, &d.ValueVariable, &variables, &d.Qos, &d.CharacteristicId, &d.FunctionId, &aspects, &d.Path, &service, &d.Revision)
	d.Variables = fromJson[map[string]string](variables)
	d.AspectIds = fromJson[[]string](aspects)
	if service != "" {
		d.Service = []byte(service)
	}
	return d, err
}

// nextRevision increments the revision counter; its row lock lasts until commit, so revisions
// become visible in their order and a reader never skips one
func (this *Tx) nextRevision() (int64, error) {
	var revision int64
	err := this.queryRow("UPDATE counters SET value = value + 1 WHERE name = 'event_descriptions' RETURNING value").Scan(&revision)
	return revision, err
}

// ReplaceEventDescriptions sets the descriptions and device groups of a deployment.
func (this *Tx) ReplaceEventDescriptions(deploymentId string, descriptions []model.EventDescription, groups []string, now time.Time) error {
	revision, err := this.nextRevision()
	if err != nil {
		return err
	}
	if err = this.tombstoneEventDescriptions(deploymentId, revision, now); err != nil {
		return err
	}
	for _, d := range descriptions {
		var variables, aspects string
		if len(d.Variables) > 0 {
			variables = toJson(d.Variables)
		}
		if len(d.AspectIds) > 0 {
			aspects = toJson(d.AspectIds)
		}
		_, err = this.exec("INSERT INTO event_descriptions ("+eventColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			d.Id, deploymentId, d.Version, d.ElementId, d.Namespace, d.Owner, d.MessageName, d.DeviceId, d.ServiceId, d.DeviceGroupId, d.ImportId,
			d.Topic, d.Script, d.ValueVariable, variables, d.Qos, d.CharacteristicId, d.FunctionId, aspects, d.Path, string(d.Service), revision)
		if err != nil {
			return err
		}
	}
	if _, err = this.exec("DELETE FROM deployment_device_groups WHERE deployment_id = ?", deploymentId); err != nil {
		return err
	}
	for _, g := range groups {
		if _, err = this.exec("INSERT INTO deployment_device_groups (deployment_id, group_id) VALUES (?, ?) ON CONFLICT (deployment_id, group_id) DO NOTHING", deploymentId, g); err != nil {
			return err
		}
	}
	return nil
}

func (this *Tx) tombstoneEventDescriptions(deploymentId string, revision int64, now time.Time) error {
	if _, err := this.exec("INSERT INTO event_description_tombstones (id, revision, removed_at) SELECT id, ?, ? FROM event_descriptions WHERE deployment_id = ?", revision, millis(now), deploymentId); err != nil {
		return err
	}
	_, err := this.exec("DELETE FROM event_descriptions WHERE deployment_id = ?", deploymentId)
	return err
}

// RemoveEventDescriptions runs before a deployment is deleted.
func (this *Tx) RemoveEventDescriptions(deploymentId string, now time.Time) error {
	n, err := this.count("SELECT COUNT(*) FROM event_descriptions WHERE deployment_id = ?", deploymentId)
	if err != nil || n == 0 {
		return err
	}
	revision, err := this.nextRevision()
	if err != nil {
		return err
	}
	return this.tombstoneEventDescriptions(deploymentId, revision, now)
}

// EventDescriptionsSince returns descriptions and removed ids with a revision above the given
// one, and the highest revision; revision 0 loads everything.
func (this *Tx) EventDescriptionsSince(revision int64) (result []model.EventDescription, removed []string, latest int64, err error) {
	if err = this.queryRow("SELECT value FROM counters WHERE name = 'event_descriptions'").Scan(&latest); err != nil {
		return nil, nil, 0, err
	}
	rows, err := this.query("SELECT "+eventColumns+" FROM event_descriptions WHERE revision > ? AND revision <= ? ORDER BY revision, id", revision, latest)
	if err != nil {
		return nil, nil, 0, err
	}
	for rows.Next() {
		d, err := scanEventDescription(rows)
		if err != nil {
			rows.Close()
			return nil, nil, 0, err
		}
		result = append(result, d)
	}
	rows.Close()
	if revision > 0 {
		rows, err = this.query("SELECT id FROM event_description_tombstones WHERE revision > ? AND revision <= ? ORDER BY revision", revision, latest)
		if err != nil {
			return nil, nil, 0, err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				return nil, nil, 0, err
			}
			removed = append(removed, id)
		}
	}
	return result, removed, latest, nil
}

func (this *Tx) ListEventDescriptions(deploymentId string) (result []model.EventDescription, err error) {
	rows, err := this.query("SELECT "+eventColumns+" FROM event_descriptions WHERE deployment_id = ? ORDER BY element_id, id", deploymentId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result = []model.EventDescription{}
	for rows.Next() {
		d, err := scanEventDescription(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

// DeploymentsOfDeviceGroup lists deployments whose events depend on a device group (DEP-08).
func (this *Tx) DeploymentsOfDeviceGroup(groupId string) (result []string, err error) {
	rows, err := this.query("SELECT deployment_id FROM deployment_device_groups WHERE group_id = ? ORDER BY deployment_id", groupId)
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

func (this *Tx) PurgeEventTombstones(before time.Time) error {
	_, err := this.exec("DELETE FROM event_description_tombstones WHERE removed_at < ?", millis(before))
	return err
}
