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
	"fmt"
	"slices"
	"strings"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/modules"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/service-commons/pkg/jwt"
)

// addResources adds the resources a version uses and those that changed since (SMS-15)
func (this *Controller) addResources(tx *store.Tx, v *model.DeploymentVersion) error {
	process, err := this.engine.Process(tx, v.DeploymentId, v.Version)
	if err != nil {
		return err
	}
	used := modules.UsedResources(process)
	if len(used.ProcessModels)+len(used.Flows)+len(used.ImportTypes) == 0 {
		return nil
	}
	v.UsedResources = &used
	checks, err := tx.ResourceChecks(v.DeploymentId)
	if err != nil {
		return err
	}
	v.ChangedResources = checks[v.Version].Changed
	return nil
}

// usage is a version that uses a resource, with its instances
type usage struct {
	deployment model.Deployment
	version    int
	instances  int
}

// usagesOf finds, over all users, the deployment versions of a namespace in use whose module
// tasks name a resource
func (this *Controller) usagesOf(tx *store.Tx, namespace string, kind string, id string) ([]usage, error) {
	var result []usage
	for offset := 0; ; offset += 1000 {
		page, err := tx.ListDeployments(namespace, model.DeploymentListOptions{PageOptions: model.PageOptions{Limit: 1000, Offset: offset}}, store.Access{Admin: true})
		if err != nil {
			return nil, err
		}
		for _, d := range page.Items {
			versions, err := this.engine.VersionsInUse(tx, d)
			if err != nil {
				return nil, err
			}
			for _, v := range versions {
				if slices.Contains(modules.UsedResources(v.Process).Of(kind), id) {
					result = append(result, usage{deployment: d, version: v.Version, instances: v.Instances})
				}
			}
		}
		if len(page.Items) < 1000 {
			return result, nil
		}
	}
}

// ResourceUsage counts who uses a process model, flow or import type, so that its repository
// can refuse to delete it (SMS-15)
func (this *Controller) ResourceUsage(ctx context.Context, token string, namespace string, kind string, id string) (result model.ResourceUsage, err error) {
	user, ns, err := this.begin(token, namespace)
	if err != nil {
		return result, err
	}
	if !slices.Contains([]string{model.ResourceKindProcessModels, model.ResourceKindFlows, model.ResourceKindImportTypes}, kind) {
		return result, model.BadRequest("unknown resource kind %v", kind)
	}
	if strings.TrimSpace(id) == "" {
		return result, model.BadRequest("missing resource id")
	}
	result.Readable = []model.ResourceUsageDeployment{}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		usages, err := this.usagesOf(tx, ns.Name, kind, strings.TrimSpace(id))
		if err != nil {
			return err
		}
		for _, u := range usages {
			result.Versions++
			result.Instances += u.instances
			if this.checkResource(ctx, user, ns.Name, KindDeployment, u.deployment.Id, u.deployment.Owner, u.deployment.NetworkId, Read) == nil {
				result.Readable = append(result.Readable, model.ResourceUsageDeployment{Id: u.deployment.Id, Version: u.version, Current: u.version == u.deployment.Version,
					ModelId: u.deployment.ModelId, Name: u.deployment.Name})
			}
		}
		return nil
	})
	return result, err
}

// DeploymentInstanceCount counts the instances of all users on a version of a deployment, for
// those who may administrate it, e.g. to see how many have not moved to a new version yet
func (this *Controller) DeploymentInstanceCount(ctx context.Context, token string, namespace string, id string, version int) (result model.Count, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		d, err := this.readDeployment(ctx, tx, token, namespace, id, Administrate, false)
		if err != nil {
			return err
		}
		if version == 0 {
			version = d.Version
		}
		counts, err := tx.InstancesPerVersion(id)
		result.Count = counts[version]
		return err
	})
	return result, err
}

// modelInUse refuses to delete a model that module tasks of any namespace deploy: every new or
// changed instance of such a deployment would fail (SMS-15). Admins may force the delete.
func (this *Controller) modelInUse(ctx context.Context, tx *store.Tx, user jwt.Token, id string) error {
	total := model.ResourceUsage{Readable: []model.ResourceUsageDeployment{}}
	for _, ns := range this.namespaces.List() {
		usages, err := this.usagesOf(tx, ns.Name, model.ResourceKindProcessModels, id)
		if err != nil {
			return err
		}
		for _, u := range usages {
			total.Versions++
			total.Instances += u.instances
			if this.checkResource(ctx, user, ns.Name, KindDeployment, u.deployment.Id, u.deployment.Owner, u.deployment.NetworkId, Read) == nil {
				total.Readable = append(total.Readable, model.ResourceUsageDeployment{Id: u.deployment.Id, Version: u.version, Name: u.deployment.Name})
			}
		}
	}
	if total.Versions == 0 {
		return nil
	}
	names := []string{}
	for _, d := range total.Readable {
		names = append(names, fmt.Sprintf("%v (%v)", d.Name, d.Id))
	}
	return model.Conflict("the model is used by %v deployment versions with %v instances: %v; an admin may delete it with force", total.Versions, total.Instances, strings.Join(names, ", "))
}
