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

package migrate

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/SENERGY-Platform/automation-engine/pkg/deprecated"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/google/uuid"
)

// the topic of hubs in permissions-v2
const hubsTopic = "hubs"

// networkOwner is the owner of what process-sync kept for a gateway: process-sync stored no
// owner, so it is the one user who may administrate the hub, or the one named with
// -owner network:<id>=<user>
func (this *migration) networkOwner(ctx context.Context, networkId string) (string, string) {
	if owner, ok := this.options.Owners["network:"+networkId]; ok {
		return owner, ""
	}
	if owner, known := this.networkOwners[networkId]; known {
		return owner, ""
	}
	rights, found, err := this.sources.Rights(ctx, hubsTopic, networkId)
	if err != nil {
		return "", err.Error()
	}
	var admins []string
	for user, r := range rights.UserPermissions {
		if r.Administrate && user != "" {
			admins = append(admins, user)
		}
	}
	sort.Strings(admins)
	if !found || len(admins) != 1 {
		return "", "the hub has " + strings.Join(append([]string{}, admins...), ", ") + " as administrators; name the owner with -owner network:" + networkId + "=<user>"
	}
	this.networkOwners[networkId] = admins[0]
	return admins[0], ""
}

// fogDeployments takes the deployments process-sync keeps for gateways as deployments of their
// network; a gateway applies them once its process module runs the engine (MIG-06)
func (this *migration) fogDeployments(ctx context.Context) error {
	const step = "fog-deployments"
	deployments, err := this.sources.FogDeployments(ctx)
	if err != nil {
		return err
	}
	this.fogIds = map[string]string{}
	for _, fd := range deployments {
		id := firstNonEmpty(fd.Deployment.Id, fd.CamundaDeploymentId)
		this.fogIds[id] = id
		if fd.CamundaDeploymentId != "" {
			this.fogIds[fd.CamundaDeploymentId] = id
		}
		owner, problem := this.networkOwner(ctx, fd.NetworkId)
		if owner == "" {
			this.result(step, "deployment", id, "", Manual, "%v", problem)
			continue
		}
		exists, err := this.target.deploymentExists(ctx, id)
		switch {
		case err != nil:
			this.result(step, "deployment", id, owner, Failed, "%v", err)
			continue
		case exists:
			this.result(step, "deployment", id, owner, Existing, "")
			continue
		}
		fd.Deployment.Version = 3
		input, conversion, err := deprecated.DeploymentInput(ctx, this.target.Controller, this.target.token(), processes, fd.Deployment, "")
		input.Owner, input.NetworkId = owner, fd.NetworkId
		switch {
		case err != nil:
			this.result(step, "deployment", id, owner, Manual, "%v", describe(err))
		case !this.options.Apply:
			this.planned[id] = input.Xml
		default:
			if _, err = this.target.Controller.PutDeployment(ctx, this.target.token(), processes, id, input); err != nil {
				this.result(step, "deployment", id, owner, Failed, "%v", describe(err))
			}
		}
		if err == nil {
			this.result(step, "deployment", id, owner, Migrated, "network %v", fd.NetworkId)
		}
		this.findings(step, id, owner, conversion.Rewritten, conversion.Check, conversion.Unsupported)
	}
	return nil
}

// fogStarts takes the starts process-sync watched over as instances of the network; they run
// once the gateway runs the engine. The business key is the instance id where it is
// unambiguous, also against the instances of the cloud (MIG-02, MIG-06, see assign).
func (this *migration) fogStarts(ctx context.Context) error {
	const step = "fog-instances"
	starts, err := this.sources.FogStarts(ctx)
	if err != nil {
		return err
	}
	var accepted []FogStart
	var candidates []candidate
	seen := map[string]bool{}
	for _, s := range starts {
		key := strings.TrimPrefix(s.BusinessKey, "wardened:")
		deploymentId, ok := this.fogIds[s.DeploymentId]
		if !ok || !this.known(ctx, deploymentId) {
			this.result(step, "instance", key, "", Skipped, "deployment %v of network %v is not migrated", s.DeploymentId, s.NetworkId)
			continue
		}
		owner, problem := this.networkOwner(ctx, s.NetworkId)
		if owner == "" {
			this.result(step, "instance", key, "", Manual, "%v", problem)
			continue
		}
		origin := s.NetworkId + "/" + deploymentId + "/" + key
		if seen[origin] {
			this.result(step, "instance", key, owner, Skipped, "the same start as another one of process-sync in network %v (RUN-09)", s.NetworkId)
			continue
		}
		seen[origin] = true
		accepted = append(accepted, s)
		candidates = append(candidates, candidate{oldId: key, origin: "the start of " + key + " in network " + s.NetworkId, key: key,
			generated: uuid.NewSHA1(instanceNamespace, []byte("fog/"+origin)).String(), owner: owner, deploymentId: deploymentId, networkId: s.NetworkId})
	}
	ids := this.assign(ctx, candidates)
	for n, s := range accepted {
		c, a := candidates[n], ids[n]
		switch {
		case a.err != nil:
			this.result(step, "instance", c.key, c.owner, Failed, "%v", a.err)
			continue
		case a.manual != "":
			this.result(step, "instance", c.key, c.owner, Manual, "%v", a.manual)
			continue
		}
		exists, err := this.target.instanceExists(ctx, a.id)
		switch {
		case err != nil:
			this.result(step, "instance", a.id, c.owner, Failed, "%v", err)
			continue
		case exists:
			this.result(step, "instance", a.id, c.owner, Existing, "")
			continue
		}
		if this.options.Apply {
			_, err = this.target.Controller.PutInstance(ctx, this.target.token(), processes, a.id, model.StartInstanceInput{DeploymentId: c.deploymentId, Variables: s.Parameters, Owner: c.owner})
			if err != nil {
				this.result(step, "instance", a.id, c.owner, Failed, "%v", describe(err))
				continue
			}
		}
		message := fmt.Sprintf("network %v; it starts once the gateway runs the engine", s.NetworkId)
		this.result(step, "instance", a.id, c.owner, Migrated, "%v", strings.Join(append([]string{message}, a.notes...), "; "))
	}
	return nil
}
