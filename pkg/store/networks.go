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
	"errors"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// Network is a gateway the cloud synchronises with (SYNC-12).
type Network struct {
	Id            string
	EngineVersion string
	Protocol      int
	Compatible    bool
	StartedAt     time.Time
	LastContact   time.Time
	// the latest position of the change stream of the gateway and the one the cloud took
	Latest    int64
	Acked     int64
	Storage   json.RawMessage
	CreatedAt time.Time
}

const networkColumns = "id, engine_version, protocol, compatible, started_at, last_contact, latest, acked, storage, created_at"

func scanNetwork(row interface{ Scan(...any) error }) (n Network, err error) {
	var compatible int
	var started, contact, created int64
	var storage string
	err = row.Scan(&n.Id, &n.EngineVersion, &n.Protocol, &compatible, &started, &contact, &n.Latest, &n.Acked, &storage, &created)
	n.Compatible = compatible == 1
	if started > 0 {
		n.StartedAt = fromMillis(started)
	}
	if contact > 0 {
		n.LastContact = fromMillis(contact)
	}
	n.CreatedAt = fromMillis(created)
	if storage != "" {
		n.Storage = json.RawMessage(storage)
	}
	return n, err
}

// LockNetwork reads a network and locks it until the end of the transaction, so that messages
// of one gateway are taken one after the other by any replica; it creates the network on its
// first message (SYNC-10).
func (this *Tx) LockNetwork(id string, now time.Time) (Network, error) {
	_, err := this.exec("INSERT INTO networks (id, created_at) VALUES (?, ?) ON CONFLICT (id) DO NOTHING", id, millis(now))
	if err != nil {
		return Network{}, err
	}
	return scanNetwork(this.queryRow("SELECT "+networkColumns+" FROM networks WHERE id = ?"+this.forUpdate(true), id))
}

func (this *Tx) UpdateNetwork(n Network) error {
	_, err := this.exec("UPDATE networks SET engine_version = ?, protocol = ?, compatible = ?, started_at = ?, last_contact = ?, latest = ?, acked = ?, storage = ? WHERE id = ?",
		n.EngineVersion, n.Protocol, boolInt(n.Compatible), snapshotMillis(n.StartedAt), snapshotMillis(n.LastContact), n.Latest, n.Acked, string(n.Storage), n.Id)
	return err
}

// GetNetwork reads a network; found is false if it never reported.
func (this *Tx) GetNetwork(id string) (Network, bool, error) {
	n, err := scanNetwork(this.queryRow("SELECT "+networkColumns+" FROM networks WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return n, false, nil
	}
	return n, err == nil, err
}

// ListNetworks reads the given networks, all if ids is nil.
func (this *Tx) ListNetworks(ids []string) (result []Network, err error) {
	w := &where{}
	if ids != nil {
		if len(ids) == 0 {
			return []Network{}, nil
		}
		w.in("id", ids)
	}
	rows, err := this.query("SELECT "+networkColumns+" FROM networks"+w.String()+" ORDER BY id", w.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result = []Network{}
	for rows.Next() {
		n, err := scanNetwork(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, n)
	}
	return result, rows.Err()
}

// StaleNetworks lists networks without contact since before (SYNC-07).
func (this *Tx) StaleNetworks(before time.Time) (result []string, err error) {
	rows, err := this.query("SELECT id FROM networks WHERE last_contact < ? ORDER BY id", millis(before))
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

// DeleteNetwork removes a network with its deployments, instances and desired state (SYNC-07).
// Instances and deployments go the way of every deletion, so that their rights are removed
// from permissions-v2 and the change stream reports the deleted instances.
func (this *Tx) DeleteNetwork(id string) error {
	instances, err := this.NetworkInstanceIds(id)
	if err != nil {
		return err
	}
	for _, instanceId := range instances {
		if err = this.DeleteInstance(instanceId); err != nil {
			return err
		}
	}
	deployments, err := this.networkDeploymentIds(id)
	if err != nil {
		return err
	}
	for _, deploymentId := range deployments {
		if err = this.DeleteDeployment(deploymentId); err != nil {
			return err
		}
	}
	for _, statement := range []string{
		"DELETE FROM network_desired WHERE network_id = ?",
		"DELETE FROM networks WHERE id = ?",
	} {
		if _, err := this.exec(statement, id); err != nil {
			return err
		}
	}
	return nil
}

// Desired is the desired state of a resource of a gateway and how far the gateway applied it.
type Desired struct {
	NetworkId      string
	Kind           string
	Id             string
	Version        int64
	Deleted        bool
	Spec           json.RawMessage
	AppliedVersion int64
	Error          string
	AppliedAt      time.Time
	SentAt         time.Time
	UpdatedAt      time.Time
}

const desiredColumns = "network_id, kind, id, version, deleted, spec, applied_version, error, applied_at, sent_at, updated_at"

func scanDesired(row interface{ Scan(...any) error }) (d Desired, err error) {
	var deleted int
	var spec string
	var applied, sent, updated int64
	err = row.Scan(&d.NetworkId, &d.Kind, &d.Id, &d.Version, &deleted, &spec, &d.AppliedVersion, &d.Error, &applied, &sent, &updated)
	d.Deleted = deleted == 1
	if spec != "" {
		d.Spec = json.RawMessage(spec)
	}
	if applied > 0 {
		d.AppliedAt = fromMillis(applied)
	}
	if sent > 0 {
		d.SentAt = fromMillis(sent)
	}
	d.UpdatedAt = fromMillis(updated)
	return d, err
}

// Desire sets the desired state of a resource of a gateway with the next version; it goes out
// with the next pass of the reconciler (SYNC-01). The spec is JSON of the resource content.
func (this *Tx) Desire(networkId string, kind string, id string, deleted bool, spec any, now time.Time) (version int64, err error) {
	raw := ""
	if spec != nil {
		temp, err := json.Marshal(spec)
		if err != nil {
			return 0, err
		}
		raw = string(temp)
	}
	// versions start at the time, not at 1: the cloud forgets applied deletions and ended
	// instances, and a later resource with the same id must be newer than what the gateway
	// applied before
	_, err = this.exec("INSERT INTO network_desired (network_id, kind, id, version, deleted, spec, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?) "+
		"ON CONFLICT (network_id, kind, id) DO UPDATE SET version = CASE WHEN network_desired.version >= excluded.version THEN network_desired.version + 1 ELSE excluded.version END, "+
		"deleted = excluded.deleted, spec = excluded.spec, sent_at = 0, updated_at = excluded.updated_at",
		networkId, kind, id, millis(now), boolInt(deleted), raw, millis(now))
	if err != nil {
		return 0, err
	}
	err = this.queryRow("SELECT version FROM network_desired WHERE network_id = ? AND kind = ? AND id = ?", networkId, kind, id).Scan(&version)
	return version, err
}

// GetDesired reads the desired state of a resource; found is false if there is none.
func (this *Tx) GetDesired(networkId string, kind string, id string) (Desired, bool, error) {
	d, err := scanDesired(this.queryRow("SELECT "+desiredColumns+" FROM network_desired WHERE network_id = ? AND kind = ? AND id = ?", networkId, kind, id))
	if errors.Is(err, sql.ErrNoRows) {
		return d, false, nil
	}
	return d, err == nil, err
}

// ListDesired lists the desired state of a network, of one kind if kind is set.
func (this *Tx) ListDesired(networkId string, kind string) (result []Desired, err error) {
	w := &where{}
	w.add("network_id = ?", networkId)
	if kind != "" {
		w.add("kind = ?", kind)
	}
	return this.desired("SELECT "+desiredColumns+" FROM network_desired"+w.String()+" ORDER BY kind, id", w.args...)
}

// DueDesired lists resources the gateway has not applied in their version and that were not
// sent since before, of networks in contact since contactAfter (SYNC-08).
func (this *Tx) DueDesired(before time.Time, contactAfter time.Time, limit int) ([]Desired, error) {
	return this.desired("SELECT d.network_id, d.kind, d.id, d.version, d.deleted, d.spec, d.applied_version, d.error, d.applied_at, d.sent_at, d.updated_at "+
		"FROM network_desired d JOIN networks n ON n.id = d.network_id "+
		"WHERE d.applied_version < d.version AND d.sent_at < ? AND n.last_contact >= ? AND n.compatible = 1 ORDER BY d.network_id, d.kind, d.id LIMIT ?",
		millis(before), millis(contactAfter), limit)
}

func (this *Tx) desired(query string, args ...any) (result []Desired, err error) {
	rows, err := this.query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result = []Desired{}
	for rows.Next() {
		d, err := scanDesired(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

// MarkDesiredSent remembers that a version went out; a newer desired version resets it.
func (this *Tx) MarkDesiredSent(networkId string, kind string, id string, version int64, at time.Time) error {
	_, err := this.exec("UPDATE network_desired SET sent_at = ? WHERE network_id = ? AND kind = ? AND id = ? AND version = ?", millis(at), networkId, kind, id, version)
	return err
}

// SetDesiredApplied records what the gateway applied; an older report changes nothing. A
// deletion the gateway applied is forgotten.
func (this *Tx) SetDesiredApplied(networkId string, kind string, id string, version int64, message string, at time.Time) error {
	_, err := this.exec("UPDATE network_desired SET applied_version = ?, error = ?, applied_at = ? WHERE network_id = ? AND kind = ? AND id = ? AND applied_version <= ? AND version >= ?",
		version, message, millis(at), networkId, kind, id, version, version)
	if err != nil {
		return err
	}
	_, err = this.exec("DELETE FROM network_desired WHERE network_id = ? AND kind = ? AND id = ? AND deleted = 1 AND applied_version >= version AND error = ''", networkId, kind, id)
	return err
}

// LowerDesiredApplied takes back what a gateway acknowledged but no longer reports, e.g. after
// it lost its database: the applied version falls to the reported one and the resource is due
// again. A gateway that reports as much or more changes nothing.
func (this *Tx) LowerDesiredApplied(networkId string, kind string, id string, version int64, message string) error {
	_, err := this.exec("UPDATE network_desired SET applied_version = ?, error = ?, sent_at = 0 WHERE network_id = ? AND kind = ? AND id = ? AND applied_version > ?",
		version, message, networkId, kind, id, version)
	return err
}

// ForgetEndedInstance removes the start or stop of an instance the gateway reported as ended;
// both are done, and the gateway forgets them once the instance is gone there.
func (this *Tx) ForgetEndedInstance(networkId string, id string) error {
	_, err := this.exec("DELETE FROM network_desired WHERE network_id = ? AND kind = 'instance' AND id = ? AND deleted = 0", networkId, id)
	return err
}

// UpsertReplicatedInstance writes the state of an instance as its gateway reported it; name and
// description belong to the cloud and stay; an
// instance of a deployment the cloud no longer has is skipped. restart_at only shows when the
// gateway restarts it; DueRestarts leaves instances of gateways out (FOG-07).
func (this *Tx) UpsertReplicatedInstance(networkId string, i model.Instance) error {
	var exists int
	err := this.queryRow("SELECT COUNT(*) FROM deployments WHERE id = ? AND network_id = ?", i.DeploymentId, networkId).Scan(&exists)
	if err != nil || exists == 0 {
		return err
	}
	_, err = this.exec("INSERT INTO instances ("+instanceColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 1, ?, 0, 0, ?, ?, ?, ?) "+
		"ON CONFLICT (id) DO UPDATE SET deployment_version = excluded.deployment_version, deployment_name = excluded.deployment_name, run = excluded.run, state = excluded.state, "+
		"end_reason = excluded.end_reason, start_variables = excluded.start_variables, started_at = excluded.started_at, ended_at = excluded.ended_at, "+
		"last_activity_at = excluded.last_activity_at, restart_at = excluded.restart_at, owner = excluded.owner",
		i.Id, i.Namespace, i.Owner, i.DeploymentId, i.DeploymentName, i.DeploymentVersion, i.ModelId, i.ModelVersion, i.Run, string(i.State), i.EndReason, toJson(i.StartVariables),
		millis(i.CreatedAt), millis(i.StartedAt), nullMillis(i.EndedAt), millis(i.LastActivityAt), nullMillis(i.RestartAt), i.ParentId, networkId, i.Name, i.Description)
	if err != nil {
		return err
	}
	return this.changed(i.Id, i.Namespace, 0, false)
}

// ReplaceReplicatedIncidents keeps the incidents of the current run as the gateway reported them.
func (this *Tx) ReplaceReplicatedIncidents(instanceId string, run int, incidents []model.Incident) error {
	if _, err := this.exec("DELETE FROM incidents WHERE instance_id = ? AND run = ?", instanceId, run); err != nil {
		return err
	}
	for _, i := range incidents {
		i.InstanceId, i.Run = instanceId, run
		if err := this.InsertIncident(i); err != nil {
			return err
		}
	}
	return nil
}

// NetworkInstanceIds lists the instances of a gateway, for its snapshot (LONG-10).
func (this *Tx) NetworkInstanceIds(networkId string) (result []string, err error) {
	rows, err := this.query("SELECT id FROM instances WHERE network_id = ? ORDER BY id", networkId)
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

func (this *Tx) networkDeploymentIds(networkId string) (result []string, err error) {
	rows, err := this.query("SELECT id FROM deployments WHERE network_id = ? ORDER BY id", networkId)
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

// DesiredCounts are the resources of a network not applied yet and those that failed.
type DesiredCounts struct {
	Pending int
	Failed  int
}

// DesiredBacklog counts per network what its gateway has not applied (SYNC-12).
func (this *Tx) DesiredBacklog() (map[string]DesiredCounts, error) {
	rows, err := this.query("SELECT network_id, " +
		"SUM(CASE WHEN applied_version < version THEN 1 ELSE 0 END), " +
		"SUM(CASE WHEN applied_version >= version AND error <> '' THEN 1 ELSE 0 END) FROM network_desired GROUP BY network_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]DesiredCounts{}
	for rows.Next() {
		var id string
		var c DesiredCounts
		if err = rows.Scan(&id, &c.Pending, &c.Failed); err != nil {
			return nil, err
		}
		result[id] = c
	}
	return result, rows.Err()
}
