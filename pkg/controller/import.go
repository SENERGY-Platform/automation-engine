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

package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// errDryRun rolls back an import that only reports
var errDryRun = errors.New("dry run")

// ImportNetwork takes over the deployments of a gateway from its export into a cloud that does
// not know them, with their ids and versions, so that the gateway applies them without an
// update; its instances follow with the synchronisation (SNRGY-5066). Admins only.
//
// The deployments are written directly, not through createDeployment: that starts at version
// 1, and the gateway ignores a version below the one it runs (fogsync applyDeployment), now
// and for every later change in the cloud. Deployments that exist stay as they are, so that a
// repeated import creates nothing.
func (this *Controller) ImportNetwork(ctx context.Context, token string, networkId string, doc model.GatewayExport, options model.NetworkImportOptions) (result model.NetworkImport, err error) {
	if _, err = this.admin(token); err != nil {
		return result, err
	}
	if this.networks == nil {
		return result, model.BadRequest("an import needs the synchronisation with gateways (sync_mqtt_broker)")
	}
	if err = validId(networkId); err != nil {
		return result, err
	}
	if doc.Format != model.GatewayExportFormat || doc.FormatVersion != model.GatewayExportVersion {
		return result, model.BadRequest("unknown format %q version %v, expected %q version %v", doc.Format, doc.FormatVersion, model.GatewayExportFormat, model.GatewayExportVersion)
	}
	if this.hubOwners == nil {
		return result, model.BadRequest("an import needs device_repository_url, which names the owner of the network")
	}
	owner, err := this.hubOwners.HubOwner(ctx, token, networkId)
	if errors.Is(err, model.ErrNotFound) {
		return result, model.Unprocessable("hub %v is not known to device-repository", networkId)
	}
	if err != nil {
		return result, model.Unavailable("device-repository: owner of network %v: %v", networkId, err)
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		result = model.NetworkImport{NetworkId: networkId, Owner: owner, DryRun: options.DryRun, Deployments: []model.ImportedDeployment{}, Models: []model.ImportedModel{}}
		now := time.Now().UTC()
		var created []model.ExportedDeployment
		for _, entry := range doc.Deployments {
			report, err := this.importDeployment(tx, networkId, owner, entry, options, now)
			if err != nil {
				return fmt.Errorf("deployment %v: %w", entry.Deployment.Id, err)
			}
			result.Deployments = append(result.Deployments, report)
			if report.Status == model.ImportCreated {
				created = append(created, entry)
			}
		}
		if result.Models, err = importModels(tx, owner, created, now); err != nil {
			return err
		}
		if len(created) > 0 {
			result.SnapshotRequested = true
		}
		if err = requestSnapshot(tx, networkId, len(created) > 0, options.CreateNetwork, now); err != nil {
			return err
		}
		if options.DryRun {
			return errDryRun
		}
		return nil
	})
	if errors.Is(err, errDryRun) {
		return result, nil
	}
	if err == nil && result.SnapshotRequested {
		this.networks.Wake()
		// otherwise the gateway learns the position with its next status; the import stands
		_ = this.networks.RequestChanges(ctx, networkId)
	}
	return result, err
}

// importDeployment writes a deployment of the export with all its versions, event
// descriptions and device groups and desires it on the gateway in the version it runs there
func (this *Controller) importDeployment(tx *store.Tx, networkId string, owner string, entry model.ExportedDeployment, options model.NetworkImportOptions, now time.Time) (report model.ImportedDeployment, err error) {
	d := entry.Deployment
	report = model.ImportedDeployment{Id: d.Id, Name: d.Name, Namespace: d.Namespace, Status: model.ImportSkipped}
	if err := validId(d.Id); err != nil {
		report.Reason = err.Error()
		return report, nil
	}
	// namespaces come from the configuration of the cloud, not from the export
	ns, ok := this.namespaces.Get(d.Namespace)
	if !ok {
		report.Reason = fmt.Sprintf("namespace %v is not known to the cloud", d.Namespace)
		return report, nil
	}
	if err := ns.CheckGateway(networkId); err != nil {
		report.Reason = err.Error()
		return report, nil
	}
	existing, err := tx.GetDeployment(d.Id, false)
	if err == nil {
		report.Status = model.ImportExists
		if existing.NetworkId != networkId {
			report.Reason = fmt.Sprintf("the id is used by a deployment of network %q", existing.NetworkId)
		}
		return report, nil
	}
	if !isNotFound(err) {
		return report, err
	}
	versions := slices.Clone(entry.Versions)
	slices.SortFunc(versions, func(a, b model.DeploymentVersion) int { return a.Version - b.Version })
	if !slices.ContainsFunc(versions, func(v model.DeploymentVersion) bool { return v.Version == d.Version }) || d.Version < 1 {
		report.Reason = fmt.Sprintf("version %v of the deployment is not in the export", d.Version)
		return report, nil
	}
	if d.Owner != owner {
		report.Reason = fmt.Sprintf("owner %v replaced by %v, the owner of the hub", d.Owner, owner)
	}
	d.Owner, d.NetworkId, d.FaultySince, d.Conversion = owner, networkId, nil, nil
	if err = tx.InsertDeployment(d); err != nil {
		return report, err
	}
	for _, v := range versions {
		v.DeploymentId = d.Id
		if err = tx.InsertDeploymentVersion(v); err != nil {
			return report, err
		}
	}
	descriptions := slices.Clone(entry.EventDescriptions)
	for i := range descriptions {
		descriptions[i].DeploymentId, descriptions[i].Owner = d.Id, owner
	}
	if len(descriptions) > 0 || len(entry.DeviceGroups) > 0 {
		if err = tx.ReplaceEventDescriptions(d.Id, descriptions, entry.DeviceGroups, now); err != nil {
			return report, err
		}
	}
	// the same version the gateway runs: it only replaces the event descriptions
	if err = this.desireDeployment(tx, d); err != nil {
		return report, err
	}
	report.Status = model.ImportCreated
	return report, nil
}

// importModels creates the models of the created deployments that the cloud lacks, with a
// model version per model version of their deployment versions; of a model that exists, only
// missing versions are added. A model deleted in the former cloud is created as well: a
// gateway never learns of the deletion (model_deleted changes only in the cloud), and the
// former cloud, which knew it, is gone.
func importModels(tx *store.Tx, owner string, created []model.ExportedDeployment, now time.Time) ([]model.ImportedModel, error) {
	type source struct {
		deployment model.Deployment
		// per model version the first deployment version that carries it
		versions map[int]model.DeploymentVersion
	}
	var order []string
	sources := map[string]*source{}
	for _, entry := range created {
		d := entry.Deployment
		if d.ModelId == "" {
			continue
		}
		s, ok := sources[d.ModelId]
		if !ok {
			s = &source{deployment: d, versions: map[int]model.DeploymentVersion{}}
			sources[d.ModelId] = s
			order = append(order, d.ModelId)
		}
		versions := slices.Clone(entry.Versions)
		slices.SortFunc(versions, func(a, b model.DeploymentVersion) int { return a.Version - b.Version })
		for _, v := range versions {
			if _, ok := s.versions[v.ModelVersion]; v.ModelVersion > 0 && !ok {
				s.versions[v.ModelVersion] = v
			}
		}
	}
	result := []model.ImportedModel{}
	for _, id := range order {
		s := sources[id]
		report := model.ImportedModel{Id: id, VersionsAdded: []int{}}
		m, err := tx.GetModel(id, true)
		exists := err == nil
		if err != nil && !isNotFound(err) {
			return nil, err
		}
		numbers := make([]int, 0, len(s.versions))
		for n := range s.versions {
			numbers = append(numbers, n)
		}
		slices.Sort(numbers)
		if !exists && len(numbers) == 0 {
			report.Status, report.Reason = model.ImportSkipped, "no deployment version names a version of the model"
			result = append(result, report)
			continue
		}
		var add []int
		for _, n := range numbers {
			if exists {
				if _, err = tx.GetModelVersion(id, n); err == nil {
					continue
				} else if !isNotFound(err) {
					return nil, err
				}
			}
			add = append(add, n)
		}
		if !exists {
			m = model.Model{Id: id, Namespace: s.deployment.Namespace, Name: s.deployment.Name, Owner: owner, Version: numbers[len(numbers)-1],
				CreatedAt: s.versions[numbers[0]].CreatedAt, UpdatedAt: now}
			if err = tx.InsertModel(m); err != nil {
				return nil, err
			}
		}
		for _, n := range add {
			v := s.versions[n]
			if err = tx.InsertModelVersion(model.ModelVersion{ModelId: id, Version: n, Xml: v.Xml, Svg: v.Svg, CreatedAt: v.CreatedAt}); err != nil {
				return nil, err
			}
		}
		switch {
		case !exists:
			report.Status = model.ImportCreated
		case len(add) > 0:
			report.Status = model.ImportExtended
			if latest := add[len(add)-1]; latest > m.Version {
				m.Version = latest
			}
			m.UpdatedAt = now
			if err = tx.UpdateModel(m); err != nil {
				return nil, err
			}
		default:
			report.Status = model.ImportExists
		}
		if add != nil {
			report.VersionsAdded = add
		}
		result = append(result, report)
	}
	return result, nil
}

// requestSnapshot makes the gateway send all its instances again: the cloud dropped those of
// deployments it did not know (UpsertReplicatedInstance). A network the cloud has never heard
// of starts at position 0 with its first contact anyway; it is created only on request, with
// the import as its last contact, since the cleanup removes networks without contact
// (network_cleanup_max_age) together with their deployments.
func requestSnapshot(tx *store.Tx, networkId string, created bool, create bool, now time.Time) error {
	n, found, err := tx.GetNetwork(networkId)
	if err != nil {
		return err
	}
	if !found {
		if !create {
			return nil
		}
		if n, err = tx.LockNetwork(networkId, now); err != nil {
			return err
		}
		n.LastContact = now
		return tx.UpdateNetwork(n)
	}
	if !created {
		return nil
	}
	// locked and read again, so that a concurrent message of the gateway is not overwritten
	if n, err = tx.LockNetwork(networkId, now); err != nil {
		return err
	}
	n.Acked = 0
	return tx.UpdateNetwork(n)
}
