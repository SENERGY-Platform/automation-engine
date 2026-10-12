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

// UserDependents lists resources of other owners that would be deleted with the user's
// resources (SEC-13): instances of others on deployments the user owns. Deployments of others
// based on the user's models are not dependents, they keep their copy of the BPMN (MOD-08).
func (this *Tx) UserDependents(userId string) (result []model.Dependent, err error) {
	rows, err := this.query("SELECT i.namespace, i.id, i.owner, d.id FROM instances i JOIN deployments d ON d.id = i.deployment_id WHERE d.owner = ? AND i.owner <> ? ORDER BY i.namespace, i.id", userId, userId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result = []model.Dependent{}
	for rows.Next() {
		d := model.Dependent{Kind: model.KindInstance}
		var deploymentId string
		if err = rows.Scan(&d.Namespace, &d.Id, &d.Owner, &deploymentId); err != nil {
			return nil, err
		}
		d.Reason = "instance of deployment " + deploymentId
		result = append(result, d)
	}
	return result, rows.Err()
}

// OwnedCounts counts the resources of a user per permissions-v2 topic name.
func (this *Tx) OwnedCounts(userId string) (map[string]int, error) {
	result := map[string]int{}
	for _, kind := range model.Kinds {
		rows, err := this.query("SELECT namespace, COUNT(*) FROM "+string(kind)+" WHERE owner = ? GROUP BY namespace", userId)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var namespace string
			var n int
			if err = rows.Scan(&namespace, &n); err != nil {
				rows.Close()
				return nil, err
			}
			result[model.Topic(namespace, kind)] = n
		}
		rows.Close()
	}
	return result, nil
}

// DeleteOwned deletes everything a user owns, in all namespaces (SEC-08): instances,
// deployments with all their instances, models and the user's settings. Rights are queued for
// removal from permissions-v2.
//
// Modules are not queued for removal: user-management removes the user's resources in the other
// services itself, and a token of the deleted user would fail every attempt.
func (this *Tx) DeleteOwned(userId string) error {
	if err := this.queueRemovals("instances", "owner = ?", userId); err != nil {
		return err
	}
	if _, err := this.exec("DELETE FROM instances WHERE owner = ?", userId); err != nil {
		return err
	}
	if err := this.queueRemovals("instances", "deployment_id IN (SELECT id FROM deployments WHERE owner = ?)", userId); err != nil {
		return err
	}
	if err := this.queueRemovals("deployments", "owner = ?", userId); err != nil {
		return err
	}
	owned, err := this.query("SELECT id FROM deployments WHERE owner = ?", userId)
	if err != nil {
		return err
	}
	var ids []string
	for owned.Next() {
		var id string
		if err = owned.Scan(&id); err != nil {
			owned.Close()
			return err
		}
		ids = append(ids, id)
	}
	owned.Close()
	for _, id := range ids {
		if err = this.RemoveEventDescriptions(id, time.Now()); err != nil {
			return err
		}
	}
	if _, err := this.exec("DELETE FROM deployments WHERE owner = ?", userId); err != nil {
		return err
	}
	if _, err := this.exec("UPDATE deployments SET model_deleted = 1 WHERE model_id IN (SELECT id FROM models WHERE owner = ?)", userId); err != nil {
		return err
	}
	if err := this.queueRemovals("models", "owner = ?", userId); err != nil {
		return err
	}
	if _, err := this.exec("DELETE FROM models WHERE owner = ?", userId); err != nil {
		return err
	}
	for _, statement := range []string{"DELETE FROM schedules WHERE owner = ?", "DELETE FROM io_values WHERE user_id = ?"} {
		if _, err := this.exec(statement, userId); err != nil {
			return err
		}
	}
	return this.DeleteUserSettings(userId)
}

// Owner returns the owner of a resource; ok is false if it does not exist.
func (this *Tx) Owner(kind model.Kind, id string) (owner string, namespace string, ok bool, err error) {
	if err = kind.Valid(); err != nil {
		return "", "", false, err
	}
	err = this.queryRow("SELECT owner, namespace FROM "+string(kind)+" WHERE id = ?", id).Scan(&owner, &namespace)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	return owner, namespace, err == nil, err
}

// SetOwner changes the owner and schedules the registration of the new owner's rights (SEC-11).
func (this *Tx) SetOwner(kind model.Kind, id string, owner string) error {
	if err := kind.Valid(); err != nil {
		return err
	}
	_, err := this.exec("UPDATE "+string(kind)+" SET owner = ?, perm_registered = 0 WHERE id = ?", owner, id)
	return err
}
