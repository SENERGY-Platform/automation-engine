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

// InstancesPerVersion counts the instances of a deployment by the version they run on.
func (this *Tx) InstancesPerVersion(deploymentId string) (map[int]int, error) {
	rows, err := this.query("SELECT deployment_version, COUNT(*) FROM instances WHERE deployment_id = ? AND parent_id = '' GROUP BY deployment_version", deploymentId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[int]int{}
	for rows.Next() {
		var version, count int
		if err = rows.Scan(&version, &count); err != nil {
			return nil, err
		}
		result[version] = count
	}
	return result, rows.Err()
}

// ResourceCheck is the stored result of the check of the resources of a deployment version.
type ResourceCheck struct {
	DeploymentId string
	Version      int
	Changed      []model.ChangedResource
	// import type id -> hash of its first seen content
	Hashes    map[string]string
	CheckedAt time.Time
}

func (this *Tx) ResourceChecks(deploymentId string) (map[int]ResourceCheck, error) {
	rows, err := this.query("SELECT version, changed, hashes, checked_at FROM resource_checks WHERE deployment_id = ?", deploymentId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[int]ResourceCheck{}
	for rows.Next() {
		var check ResourceCheck
		var changed, hashes string
		var checked int64
		if err = rows.Scan(&check.Version, &changed, &hashes, &checked); err != nil {
			return nil, err
		}
		check.DeploymentId, check.CheckedAt = deploymentId, fromMillis(checked)
		check.Changed = fromJson[[]model.ChangedResource](changed)
		check.Hashes = fromJson[map[string]string](hashes)
		result[check.Version] = check
	}
	return result, rows.Err()
}

func (this *Tx) SetResourceCheck(check ResourceCheck) error {
	if check.Changed == nil {
		check.Changed = []model.ChangedResource{}
	}
	if check.Hashes == nil {
		check.Hashes = map[string]string{}
	}
	_, err := this.exec("INSERT INTO resource_checks (deployment_id, version, changed, hashes, checked_at) VALUES (?, ?, ?, ?, ?) "+
		"ON CONFLICT (deployment_id, version) DO UPDATE SET changed = excluded.changed, hashes = excluded.hashes, checked_at = excluded.checked_at",
		check.DeploymentId, check.Version, toJson(check.Changed), toJson(check.Hashes), millis(check.CheckedAt))
	return err
}

// AllDeployments lists the deployments of all namespaces by id, those after the id after.
func (this *Tx) AllDeployments(after string, limit int) ([]model.Deployment, error) {
	rows, err := this.query("SELECT "+deploymentColumns+" FROM deployments WHERE id > ? ORDER BY id LIMIT ?", after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []model.Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}
