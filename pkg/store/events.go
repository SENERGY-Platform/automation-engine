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
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

const eventColumns = "id, deployment_id, version, element_id, namespace, owner, message_name, device_id, service_id, device_group_id, import_id, topic, script, value_variable, variables, qos, characteristic_id, function_id, aspect_ids, path, service, revision"

func scanEventDescription(row interface{ Scan(...any) error }, extra ...any) (d model.EventDescription, err error) {
	var variables, aspects, service string
	err = row.Scan(append([]any{&d.Id, &d.DeploymentId, &d.Version, &d.ElementId, &d.Namespace, &d.Owner, &d.MessageName, &d.DeviceId, &d.ServiceId, &d.DeviceGroupId, &d.ImportId,
		&d.Topic, &d.Script, &d.ValueVariable, &variables, &d.Qos, &d.CharacteristicId, &d.FunctionId, &aspects, &d.Path, &service, &d.Revision}, extra...)...)
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

// ReplaceEventDescriptions sets the descriptions and device groups of one version of a
// deployment; those of other versions stay, for the instances still running on them (DEP-04).
func (this *Tx) ReplaceEventDescriptions(deploymentId string, version int, descriptions []model.EventDescription, groups []string, now time.Time) error {
	revision, err := this.nextRevision()
	if err != nil {
		return err
	}
	if err = this.tombstoneEventDescriptions(deploymentId, version, revision, now); err != nil {
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
	if _, err = this.exec("DELETE FROM deployment_device_groups WHERE deployment_id = ? AND version = ?", deploymentId, version); err != nil {
		return err
	}
	for _, g := range groups {
		if _, err = this.exec("INSERT INTO deployment_device_groups (deployment_id, version, group_id) VALUES (?, ?, ?) ON CONFLICT (deployment_id, version, group_id) DO NOTHING", deploymentId, version, g); err != nil {
			return err
		}
	}
	return nil
}

// tombstoneEventDescriptions removes the descriptions of a version of a deployment, of all its
// versions with version 0, and leaves tombstones for the event workers
func (this *Tx) tombstoneEventDescriptions(deploymentId string, version int, revision int64, now time.Time) error {
	w := &where{}
	w.add("deployment_id = ?", deploymentId)
	if version > 0 {
		w.add("version = ?", version)
	}
	args := append([]any{revision, millis(now)}, w.args...)
	if _, err := this.exec("INSERT INTO event_description_tombstones (id, revision, removed_at) SELECT id, ?, ? FROM event_descriptions"+w.String(), args...); err != nil {
		return err
	}
	_, err := this.exec("DELETE FROM event_descriptions"+w.String(), w.args...)
	return err
}

// RemoveUnusedVersions removes what older versions of deployments no longer need, of one
// deployment or, with an empty id, of all (DEP-04): the event descriptions and device groups of a
// version on which no instance runs any more, and a version that no instance and no run refers
// to; the current version stays. Ended instances keep their version for history and debugging
// (DBG-13) until the retention removes them.
// Versions of deployments in the namespaces keep stay (KeepVersions); their descriptions go all
// the same.
func (this *Tx) RemoveUnusedVersions(deploymentId string, keep []string, now time.Time) error {
	filter, args := "", []any{}
	if deploymentId != "" {
		filter, args = " AND e.deployment_id = ?", []any{deploymentId}
	}
	rows, err := this.query(`SELECT DISTINCT e.deployment_id, e.version FROM (
			SELECT deployment_id, version FROM event_descriptions UNION SELECT deployment_id, version FROM deployment_device_groups
		) e JOIN deployments d ON d.id = e.deployment_id
		WHERE e.version < d.version AND NOT EXISTS (
			SELECT 1 FROM instances i WHERE i.deployment_id = e.deployment_id AND i.deployment_version = e.version AND i.state = ?
		)`+filter, append([]any{model.StateRunning}, args...)...)
	if err != nil {
		return err
	}
	type key struct {
		deploymentId string
		version      int
	}
	var unused []key
	for rows.Next() {
		var k key
		if err = rows.Scan(&k.deploymentId, &k.version); err != nil {
			rows.Close()
			return err
		}
		unused = append(unused, k)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if len(unused) > 0 {
		revision, err := this.nextRevision()
		if err != nil {
			return err
		}
		for _, k := range unused {
			if err = this.tombstoneEventDescriptions(k.deploymentId, k.version, revision, now); err != nil {
				return err
			}
			if _, err = this.exec("DELETE FROM deployment_device_groups WHERE deployment_id = ? AND version = ?", k.deploymentId, k.version); err != nil {
				return err
			}
		}
	}
	filter = ""
	if deploymentId != "" {
		filter = " AND v.deployment_id = ?"
	}
	unusedVersion := `v.version < (SELECT d.version FROM deployments d WHERE d.id = v.deployment_id)
		AND NOT EXISTS (SELECT 1 FROM instances i WHERE i.deployment_id = v.deployment_id AND i.deployment_version = v.version)
		AND NOT EXISTS (SELECT 1 FROM runs r JOIN instances i ON i.id = r.instance_id WHERE i.deployment_id = v.deployment_id AND r.deployment_version = v.version)` + filter
	if len(keep) > 0 {
		unusedVersion += " AND (SELECT d.namespace FROM deployments d WHERE d.id = v.deployment_id) NOT IN (?" + strings.Repeat(", ?", len(keep)-1) + ")"
		for _, ns := range keep {
			args = append(args, ns)
		}
	}
	if _, err = this.exec("DELETE FROM resource_checks WHERE EXISTS (SELECT 1 FROM deployment_versions v WHERE v.deployment_id = resource_checks.deployment_id AND v.version = resource_checks.version AND "+unusedVersion+")", args...); err != nil {
		return err
	}
	_, err = this.exec("DELETE FROM deployment_versions WHERE EXISTS (SELECT 1 FROM deployment_versions v WHERE v.deployment_id = deployment_versions.deployment_id AND v.version = deployment_versions.version AND "+unusedVersion+")", args...)
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
	return this.tombstoneEventDescriptions(deploymentId, 0, revision, now)
}

// EventDescriptionsSince returns descriptions and removed ids with a revision above the given
// one, and the highest revision; revision 0 loads everything.
func (this *Tx) EventDescriptionsSince(revision int64) (result []model.EventDescription, removed []string, latest int64, err error) {
	if err = this.queryRow("SELECT value FROM counters WHERE name = 'event_descriptions'").Scan(&latest); err != nil {
		return nil, nil, 0, err
	}
	// descriptions of deployments of gateways read the topics of the gateway; there the
	// deployment has no network id
	// with the creation of their deployment version, before which the worker ignores messages
	rows, err := this.query("SELECT "+eventColumns+", COALESCE((SELECT v.created_at FROM deployment_versions v WHERE v.deployment_id = event_descriptions.deployment_id AND v.version = event_descriptions.version), 0) "+
		"FROM event_descriptions WHERE revision > ? AND revision <= ? "+
		"AND deployment_id NOT IN (SELECT id FROM deployments WHERE network_id <> '') ORDER BY revision, id", revision, latest)
	if err != nil {
		return nil, nil, 0, err
	}
	for rows.Next() {
		var activeSince int64
		d, err := scanEventDescription(rows, &activeSince)
		if err != nil {
			rows.Close()
			return nil, nil, 0, err
		}
		if activeSince > 0 {
			d.ActiveSince = fromMillis(activeSince)
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

// ListEventDescriptions lists the descriptions of one version of a deployment, of all its
// versions with version 0.
func (this *Tx) ListEventDescriptions(deploymentId string, version int) (result []model.EventDescription, err error) {
	w := &where{}
	w.add("deployment_id = ?", deploymentId)
	if version > 0 {
		w.add("version = ?", version)
	}
	rows, err := this.query("SELECT "+eventColumns+" FROM event_descriptions"+w.String()+" ORDER BY element_id, id", w.args...)
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

// DeploymentNamespaces lists the namespaces that have deployments.
func (this *Tx) DeploymentNamespaces() (result []string, err error) {
	rows, err := this.query("SELECT DISTINCT namespace FROM deployments ORDER BY namespace")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, err
		}
		result = append(result, name)
	}
	return result, rows.Err()
}

// DescribedVersions lists the versions of a deployment with event descriptions or device groups.
func (this *Tx) DescribedVersions(deploymentId string) (result []int, err error) {
	rows, err := this.query("SELECT version FROM event_descriptions WHERE deployment_id = ? UNION SELECT version FROM deployment_device_groups WHERE deployment_id = ? ORDER BY version", deploymentId, deploymentId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var version int
		if err = rows.Scan(&version); err != nil {
			return nil, err
		}
		result = append(result, version)
	}
	return result, rows.Err()
}

// DeploymentsOfDeviceGroup lists deployments whose events depend on a device group (DEP-08).
func (this *Tx) DeploymentsOfDeviceGroup(groupId string) (result []string, err error) {
	rows, err := this.query("SELECT DISTINCT deployment_id FROM deployment_device_groups WHERE group_id = ? ORDER BY deployment_id", groupId)
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

// DeviceGroupsOfDeployment lists the device groups the events of one version of a deployment
// depend on, of all its versions with version 0.
func (this *Tx) DeviceGroupsOfDeployment(deploymentId string, version int) (result []string, err error) {
	w := &where{}
	w.add("deployment_id = ?", deploymentId)
	if version > 0 {
		w.add("version = ?", version)
	}
	rows, err := this.query("SELECT DISTINCT group_id FROM deployment_device_groups"+w.String()+" ORDER BY group_id", w.args...)
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
