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
	"encoding/json"
	"errors"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/fogsync/protocol"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/service-commons/pkg/jwt"
	"github.com/google/uuid"
)

// Networks keeps the desired state of gateways; set in the cloud when the synchronisation with
// gateways is configured (DEP-09, SYNC-01). Deployments and instances of a gateway are stored
// in the cloud with their network id, but run on the gateway: the cloud only desires them.
type Networks interface {
	Desire(tx *store.Tx, networkId string, resource protocol.Resource) error
	// sends the desired state now instead of with the next pass
	Wake()
	// the networks with the ids, all if ids is nil, with their resources if detailed
	Overview(ctx context.Context, ids []string, detailed bool) ([]model.Network, error)
	// reads from a gateway (DBG-15) and watches an instance there (DBG-12)
	Call(ctx context.Context, networkId string, method string, namespace string, instance string, args protocol.RequestArgs, result any) error
	Watch(ctx context.Context, networkId string, namespace string, instance string, options model.WatchOptions) (<-chan model.DebugMessage, error)
}

// HubChecker checks rights on gateways in permissions-v2, topic hubs (SEC-05).
type HubChecker interface {
	CheckHub(ctx context.Context, token jwt.Token, networkId string, right Right) (bool, error)
	// the ids of the gateways the user may read
	ReadableHubs(ctx context.Context, token jwt.Token) ([]string, error)
}

// ListNetworks shows the gateways the user may read with their synchronisation (SYNC-12).
func (this *Controller) ListNetworks(ctx context.Context, token string) ([]model.Network, error) {
	user, err := this.parseToken(token)
	if err != nil {
		return nil, err
	}
	if this.networks == nil {
		return []model.Network{}, nil
	}
	var ids []string
	if checker, ok := this.auth.(HubChecker); ok && !user.IsAdmin() {
		if ids, err = checker.ReadableHubs(ctx, user); err != nil {
			return nil, err
		}
		if ids == nil {
			// no readable gateway; nil would select all of them
			ids = []string{}
		}
	}
	return this.networks.Overview(ctx, ids, false)
}

// GetNetwork shows a gateway with the desired and the applied state of its resources (SYNC-13).
func (this *Controller) GetNetwork(ctx context.Context, token string, networkId string) (model.Network, error) {
	user, err := this.parseToken(token)
	if err != nil {
		return model.Network{}, err
	}
	if err = this.checkNetwork(ctx, user, networkId, Read); err != nil {
		if errors.Is(err, model.ErrForbidden) {
			return model.Network{}, model.NotFound("network %v not found", networkId)
		}
		return model.Network{}, err
	}
	list, err := this.networks.Overview(ctx, []string{networkId}, true)
	if err != nil {
		return model.Network{}, err
	}
	if len(list) == 0 {
		return model.Network{}, model.NotFound("network %v not found", networkId)
	}
	return list[0], nil
}

// SetNetworks enables deployments to gateways.
func (this *Controller) SetNetworks(networks Networks) {
	this.networks = networks
}

// checkNetwork verifies that gateways are reachable and the user has the right on the gateway:
// rx to read and start, a to deploy and delete (SEC-05). Without permissions-v2, rights on
// gateways are not checked.
func (this *Controller) checkNetwork(ctx context.Context, user jwt.Token, networkId string, right Right) error {
	if this.networks == nil {
		return model.BadRequest("deployments to gateways need the synchronisation with gateways (sync_mqtt_broker)")
	}
	if err := validId(networkId); err != nil {
		return err
	}
	checker, ok := this.auth.(HubChecker)
	if !ok || user.IsAdmin() {
		return nil
	}
	allowed, err := checker.CheckHub(ctx, user, networkId, right)
	if err != nil {
		return err
	}
	if !allowed {
		return model.Forbidden("missing right %c on network %v", right, networkId)
	}
	return nil
}

// desireDeployment hands the current version of a deployment of a gateway to the
// synchronisation, with its namespace and event descriptions (NS-10, SYNC-06)
func (this *Controller) desireDeployment(tx *store.Tx, d model.Deployment) error {
	ns, ok := this.namespaces.Get(d.Namespace)
	if !ok {
		return model.NotFound("namespace %v not found", d.Namespace)
	}
	if err := this.networks.Desire(tx, d.NetworkId, protocol.Resource{Kind: protocol.ResourceNamespace, Id: ns.Name, Namespace: &ns}); err != nil {
		return err
	}
	version, err := tx.GetDeploymentVersion(d.Id, d.Version)
	if err != nil {
		return err
	}
	descriptions, err := tx.ListEventDescriptions(d.Id)
	if err != nil {
		return err
	}
	groups, err := tx.DeviceGroupsOfDeployment(d.Id)
	if err != nil {
		return err
	}
	return this.networks.Desire(tx, d.NetworkId, protocol.Resource{Kind: protocol.ResourceDeployment, Id: d.Id,
		Deployment: &protocol.DeploymentSpec{Deployment: d, Version: version, Descriptions: descriptions, Groups: groups}})
}

func (this *Controller) desireDeletedDeployment(tx *store.Tx, d model.Deployment) error {
	return this.networks.Desire(tx, d.NetworkId, protocol.Resource{Kind: protocol.ResourceDeployment, Id: d.Id, Deleted: true})
}

// startOnNetwork records an instance the gateway shall start; it is pending until the gateway
// reports it. A repeated start with the id returns the instance (RUN-01).
func (this *Controller) startOnNetwork(ctx context.Context, user jwt.Token, d model.Deployment, id string, owner string, variables map[string]any, name string, description string) (result model.Instance, err error) {
	if err = this.checkNetwork(ctx, user, d.NetworkId, Execute); err != nil {
		return result, err
	}
	if id == "" {
		id = uuid.NewString()
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		existing, err := tx.GetInstance(id, false)
		if err == nil {
			if existing.DeploymentId != d.Id {
				return model.Conflict("instance %v already exists with different content", id)
			}
			result = existing.Instance
			return nil
		}
		if !isNotFound(err) {
			return err
		}
		now := time.Now().UTC()
		result = model.Instance{Id: id, Namespace: d.Namespace, Owner: owner, DeploymentId: d.Id, DeploymentName: d.Name, DeploymentVersion: d.Version,
			ModelId: d.ModelId, ModelVersion: d.ModelVersion, State: model.StatePending, StartVariables: variables, CreatedAt: now, StartedAt: now, LastActivityAt: now, NetworkId: d.NetworkId,
			Name: name, Description: description}
		if err = tx.InsertInstance(store.Instance{Instance: result, ActivityMinSeq: 1}); err != nil {
			return err
		}
		return this.networks.Desire(tx, d.NetworkId, protocol.Resource{Kind: protocol.ResourceInstance, Id: id,
			Instance: &protocol.InstanceSpec{DeploymentId: d.Id, Owner: owner, Variables: variables}})
	})
	if err == nil {
		this.networks.Wake()
	}
	return result, err
}

// desireInstance stops or deletes an instance of a gateway; the gateway reports the result
func (this *Controller) desireInstance(tx *store.Tx, inst model.Instance, deleted bool) error {
	resource := protocol.Resource{Kind: protocol.ResourceInstance, Id: inst.Id, Deleted: deleted}
	if !deleted {
		resource.Instance = &protocol.InstanceSpec{DeploymentId: inst.DeploymentId, Owner: inst.Owner, Variables: inst.StartVariables, Stopped: true}
	}
	return this.networks.Desire(tx, inst.NetworkId, resource)
}

// inCloud rejects changes that only the gateway of an instance can make
func inCloud(inst model.Instance) error {
	if inst.NetworkId != "" {
		return model.BadRequest("instance %v runs on gateway %v; it can only be started, stopped and deleted from here", inst.Id, inst.NetworkId)
	}
	return nil
}

func (this *Controller) wake() {
	if this.networks != nil {
		this.networks.Wake()
	}
}

// localOnly rejects a change of an instance that runs on a gateway
func (this *Controller) localOnly(ctx context.Context, id string) error {
	return this.store.Tx(ctx, func(tx *store.Tx) error {
		inst, err := tx.GetInstance(id, false)
		if err != nil {
			return err
		}
		return inCloud(inst.Instance)
	})
}

// onGateway reads an instance with the right to read it and, for an instance of a gateway, the
// right to read the gateway; networkId is empty for an instance of the cloud
func (this *Controller) onGateway(ctx context.Context, token string, namespace string, id string) (networkId string, inst model.Instance, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		read, err := this.readInstance(ctx, tx, token, namespace, id, Read)
		inst = read.Instance
		return err
	})
	if err != nil || inst.NetworkId == "" || this.networks == nil {
		return "", inst, err
	}
	user, err := this.parseToken(token)
	if err != nil {
		return "", inst, err
	}
	return inst.NetworkId, inst, this.checkNetwork(ctx, user, inst.NetworkId, Read)
}

// requestOptions is the JSON of request options
func requestOptions(value any) protocol.RequestArgs {
	raw, _ := json.Marshal(value)
	return protocol.RequestArgs{Options: raw}
}

// offlineState is the state of an instance of an unreachable gateway: what the cloud has (DBG-15)
func offlineState(inst model.Instance) model.DebugState {
	return model.DebugState{Instance: inst, Offline: true, Active: []model.ActiveElement{}, Waits: []model.Wait{}, Variables: map[string]model.DebugVariable{}, Incidents: []model.Incident{}}
}
