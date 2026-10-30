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

	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/fogsync"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/tracing"
)

func (this *Controller) StartInstance(ctx context.Context, token string, namespace string, input model.StartInstanceInput) (model.Instance, error) {
	return this.startInstance(ctx, token, namespace, "", input)
}

func (this *Controller) PutInstance(ctx context.Context, token string, namespace string, id string, input model.StartInstanceInput) (model.Instance, error) {
	if err := validId(id); err != nil {
		return model.Instance{}, err
	}
	return this.startInstance(ctx, token, namespace, id, input)
}

func (this *Controller) startInstance(ctx context.Context, token string, namespace string, id string, input model.StartInstanceInput) (model.Instance, error) {
	user, _, err := this.begin(token, namespace)
	if err != nil {
		return model.Instance{}, err
	}
	if input.DeploymentId == "" {
		return model.Instance{}, model.BadRequest("deployment_id is required")
	}
	var deployment model.Deployment
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		// starting needs the execute right; on a shared deployment the starting user becomes owner (SEC-12)
		deployment, err = this.readDeployment(ctx, tx, token, namespace, input.DeploymentId, Execute, false)
		if err != nil {
			return err
		}
		if id == "" {
			return nil
		}
		existing, err := tx.GetInstance(id, false)
		if isNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		// an existing id that the caller cannot see conflicts without details
		if existing.Namespace != namespace || this.checkResource(ctx, user, namespace, KindInstance, id, existing.Owner, existing.NetworkId, Read) != nil {
			return model.Conflict("instance id %v is already used", id)
		}
		return nil
	})
	if err != nil {
		return model.Instance{}, err
	}
	var instanceOwner string
	if deployment.NetworkId != "" {
		// an instance of a gateway belongs to the owner of the network, not to the starting user (FOG-11)
		instanceOwner, err = this.networkOwner(ctx, token, deployment.NetworkId, input.Owner)
	} else {
		instanceOwner, err = owner(user, input.Owner)
	}
	if err != nil {
		return model.Instance{}, err
	}
	variables, err := this.withAutoSelected(ctx, token, input.DeploymentId, input.DeploymentVersion, "", input.Variables)
	if err != nil {
		return model.Instance{}, err
	}
	if deployment.NetworkId != "" {
		return this.startOnNetwork(ctx, user, deployment, id, instanceOwner, variables, input.Name, input.Description)
	}
	result, _, err := this.engine.StartInstance(ctx, engine.StartRequest{
		DeploymentId: input.DeploymentId, DeploymentVersion: input.DeploymentVersion, InstanceId: id, Owner: instanceOwner, Variables: variables,
		Name: input.Name, Description: input.Description,
	})
	if err == nil {
		// marks the span of the request with the new instance; its later work carries the id
		// in spans of its own
		tracing.WithBaggage(ctx, tracing.KeyInstanceId, result.Id)
	}
	return result, err
}

// UpdateInstanceInfo changes name and description of an instance; they are no part of the run,
// so instances of a gateway take them in the cloud.
func (this *Controller) UpdateInstanceInfo(ctx context.Context, token string, namespace string, id string, input model.InstanceInfoInput) (result model.Instance, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		inst, err := this.readInstance(ctx, tx, token, namespace, id, Write)
		if err != nil {
			return err
		}
		if err = tx.SetInstanceInfo(id, inst.Namespace, input.Name, input.Description); err != nil {
			return err
		}
		result = inst.Instance
		result.Name, result.Description = input.Name, input.Description
		return nil
	})
	return result, err
}

func (this *Controller) readInstance(ctx context.Context, tx *store.Tx, token string, namespace string, id string, right Right) (store.Instance, error) {
	user, _, err := this.begin(token, namespace)
	if err != nil {
		return store.Instance{}, err
	}
	inst, err := tx.GetInstance(id, false)
	if err != nil {
		return inst, err
	}
	if err = inNamespace(inst.Namespace, namespace, KindInstance, id); err != nil {
		return inst, err
	}
	return inst, this.checkResource(ctx, user, namespace, KindInstance, id, inst.Owner, inst.NetworkId, right)
}

func (this *Controller) GetInstance(ctx context.Context, token string, namespace string, id string) (result model.Instance, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		inst, err := this.readInstance(ctx, tx, token, namespace, id, Read)
		if err != nil {
			return err
		}
		items := []model.Instance{inst.Instance}
		err = withStatus(tx, items)
		result = items[0]
		return err
	})
	return result, err
}

// withStatus derives the status of instances from their run state and module errors (SMS-11)
func withStatus(tx *store.Tx, instances []model.Instance) error {
	ids := make([]string, 0, len(instances))
	for _, inst := range instances {
		ids = append(ids, inst.Id)
	}
	moduleErrors, err := tx.ModuleErrors(ids)
	if err != nil {
		return err
	}
	for i := range instances {
		inst := &instances[i]
		switch inst.State {
		case model.StateRunning:
			inst.Status, inst.StatusMessage = model.StatusRunning, ""
		case model.StateCompleted:
			inst.Status, inst.StatusMessage = model.StatusReady, ""
		default:
			inst.Status, inst.StatusMessage = model.StatusError, inst.EndReason
		}
		if m, ok := moduleErrors[inst.Id]; ok && inst.Status != model.StatusError {
			// the format smart-service-repository used
			inst.Status = model.StatusError
			inst.StatusMessage = fmt.Sprintf("module error: module.id = %s; module.type= %s; error = %s", m.Id, m.ModuleType, m.Error)
		}
	}
	return nil
}

func (this *Controller) ListInstances(ctx context.Context, token string, namespace string, options model.InstanceListOptions) (result model.Page[model.Instance], err error) {
	user, _, err := this.begin(token, namespace)
	if err != nil {
		return result, err
	}
	access, err := this.access(ctx, user, namespace, KindInstance, Read)
	if err != nil {
		return result, err
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		if result, err = tx.ListInstances(namespace, options, access); err != nil {
			return err
		}
		return withStatus(tx, result.Items)
	})
	return result, err
}

// checkAll verifies a right on all instances before anything changes
func (this *Controller) checkAll(ctx context.Context, token string, namespace string, ids []string, right Right) error {
	if len(ids) == 0 {
		return model.BadRequest("ids are required")
	}
	return this.store.Tx(ctx, func(tx *store.Tx) error {
		for _, id := range ids {
			if _, err := this.readInstance(ctx, tx, token, namespace, id, right); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteInstances removes instances with history, variables, incidents and runtime state
// (HIST-03); a running instance is removed as well.
func (this *Controller) DeleteInstances(ctx context.Context, token string, namespace string, ids []string) error {
	if err := this.checkAll(ctx, token, namespace, ids, Administrate); err != nil {
		return err
	}
	err := this.store.Tx(ctx, func(tx *store.Tx) error {
		for _, id := range ids {
			// the lock waits for a running step of the instance
			inst, err := tx.GetInstance(id, true)
			if err != nil {
				if isNotFound(err) {
					continue
				}
				return err
			}
			if inst.NetworkId != "" && this.networks != nil {
				if err = this.desireInstance(tx, inst.Instance, true); err != nil {
					return err
				}
			}
			if err := tx.DeleteInstance(id); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		this.wake()
	}
	return err
}

func (this *Controller) StopInstances(ctx context.Context, token string, namespace string, ids []string) error {
	if err := this.checkAll(ctx, token, namespace, ids, Write); err != nil {
		return err
	}
	var errs []error
	for _, id := range ids {
		var networkId string
		err := this.store.Tx(ctx, func(tx *store.Tx) error {
			inst, err := tx.GetInstance(id, true)
			if err != nil || inst.NetworkId == "" || this.networks == nil {
				return err
			}
			networkId = inst.NetworkId
			// the gateway stops it and reports the result
			return this.desireInstance(tx, inst.Instance, false)
		})
		if err == nil && networkId == "" {
			err = this.engine.StopInstance(ctx, id, "stopped")
		}
		if err != nil && !errors.Is(err, model.ErrConflict) {
			errs = append(errs, err)
		}
	}
	this.wake()
	return errors.Join(errs...)
}

func (this *Controller) RestartInstance(ctx context.Context, token string, namespace string, id string, input model.RestartInput) (model.Instance, error) {
	if err := this.checkAll(ctx, token, namespace, []string{id}, Write); err != nil {
		return model.Instance{}, err
	}
	if err := this.localOnly(ctx, id); err != nil {
		return model.Instance{}, err
	}
	return this.engine.RestartInstance(ctx, id, input.DeploymentVersion, model.CauseRestart)
}

func (this *Controller) ListRuns(ctx context.Context, token string, namespace string, id string) (result []model.Run, err error) {
	if networkId, _, err := this.onGateway(ctx, token, namespace, id); err != nil || networkId != "" {
		if err == nil {
			err = this.networks.Call(ctx, networkId, fogsync.MethodListRuns, namespace, id, fogsync.RequestArgs{}, &result)
		}
		return result, err
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		if _, err := this.readInstance(ctx, tx, token, namespace, id, Read); err != nil {
			return err
		}
		result, err = tx.ListRuns(id)
		return err
	})
	return result, err
}

func (this *Controller) GetVariables(ctx context.Context, token string, namespace string, id string) (result []model.Variable, err error) {
	if networkId, _, err := this.onGateway(ctx, token, namespace, id); err != nil || networkId != "" {
		if err == nil {
			err = this.networks.Call(ctx, networkId, fogsync.MethodGetVariables, namespace, id, fogsync.RequestArgs{}, &result)
		}
		return result, err
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		if _, err := this.readInstance(ctx, tx, token, namespace, id, Read); err != nil {
			return err
		}
		result, err = tx.GetVariables(id)
		return err
	})
	return result, err
}

func (this *Controller) SetVariable(ctx context.Context, token string, namespace string, id string, name string, value any) error {
	if err := this.checkAll(ctx, token, namespace, []string{id}, Write); err != nil {
		return err
	}
	if err := this.localOnly(ctx, id); err != nil {
		return err
	}
	return this.engine.SetVariable(ctx, id, name, value)
}

// TriggerWait needs the write right like setting a variable: it changes the course of the
// instance, which the read right of the debugger does not cover (DBG-17, DBG-08).
func (this *Controller) TriggerWait(ctx context.Context, token string, namespace string, id string, elementId string, input model.TriggerInput) error {
	if err := this.checkAll(ctx, token, namespace, []string{id}, Write); err != nil {
		return err
	}
	if err := this.localOnly(ctx, id); err != nil {
		return err
	}
	return this.engine.TriggerWait(ctx, id, elementId, input.LocalVariables)
}

func (this *Controller) ListActivities(ctx context.Context, token string, namespace string, id string, options model.ActivityListOptions) (result model.ActivityPage, err error) {
	if networkId, _, err := this.onGateway(ctx, token, namespace, id); err != nil || networkId != "" {
		if err == nil {
			result, err = this.gatewayActivities(ctx, networkId, namespace, id, options)
		}
		return result, err
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		inst, err := this.readInstance(ctx, tx, token, namespace, id, Read)
		if err != nil {
			return err
		}
		result.Items, err = tx.ListActivities(id, options)
		// entries before the oldest one kept were evicted (LONG-03)
		result.Truncated = inst.ActivityMinSeq > 1 && options.AfterSeq < inst.ActivityMinSeq-1
		return err
	})
	return result, err
}

// gatewayActivities reads the activity log from the gateway. A gateway with an older engine
// ignores the search and returns every entry, so the cloud filters again and pages on while a
// page comes back full; the entries an instance keeps bound the pages (DBG-16, LONG-03).
func (this *Controller) gatewayActivities(ctx context.Context, networkId string, namespace string, id string, options model.ActivityListOptions) (result model.ActivityPage, err error) {
	if !options.Searches() {
		err = this.networks.Call(ctx, networkId, fogsync.MethodListActivities, namespace, id, requestOptions(options), &result)
		return result, err
	}
	limit := options.Limit
	if limit <= 0 || limit > model.MaxPageLimit {
		limit = model.MaxPageLimit
	}
	request := options
	request.Limit = model.MaxPageLimit
	result.Items = []model.Activity{}
	for {
		var page model.ActivityPage
		if err = this.networks.Call(ctx, networkId, fogsync.MethodListActivities, namespace, id, requestOptions(request), &page); err != nil {
			return result, err
		}
		result.Truncated = result.Truncated || page.Truncated
		for _, a := range page.Items {
			if !options.Matches(a) {
				continue
			}
			result.Items = append(result.Items, a)
			if len(result.Items) == limit {
				return result, nil
			}
		}
		if len(page.Items) < model.MaxPageLimit {
			return result, nil
		}
		request.AfterSeq = page.Items[len(page.Items)-1].Seq
	}
}

func (this *Controller) ListElementStats(ctx context.Context, token string, namespace string, id string, run int) (result []model.ElementStats, err error) {
	if networkId, _, err := this.onGateway(ctx, token, namespace, id); err != nil || networkId != "" {
		if err == nil {
			err = this.networks.Call(ctx, networkId, fogsync.MethodListElementStats, namespace, id, fogsync.RequestArgs{Run: run}, &result)
		}
		return result, err
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		if _, err := this.readInstance(ctx, tx, token, namespace, id, Read); err != nil {
			return err
		}
		result, err = tx.ListElementStats(id, run)
		return err
	})
	return result, err
}

func (this *Controller) ListIncidents(ctx context.Context, token string, namespace string, options model.IncidentListOptions) (result model.Page[model.Incident], err error) {
	user, _, err := this.begin(token, namespace)
	if err != nil {
		return result, err
	}
	access, err := this.access(ctx, user, namespace, KindInstance, Read)
	if err != nil {
		return result, err
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		result, err = tx.ListIncidents(namespace, options, access)
		return err
	})
	return result, err
}

// CorrelateMessage delivers a message to the caller's instances and deployments; admins, e.g.
// the event-worker with client credentials, correlate across owners or for one owner (EVT-01).
func (this *Controller) CorrelateMessage(ctx context.Context, token string, input model.MessageInput) (model.CorrelationResult, error) {
	user, err := this.parseToken(token)
	if err != nil {
		return model.CorrelationResult{}, err
	}
	if input.InstanceId != "" {
		if err = validId(input.InstanceId); err != nil {
			return model.CorrelationResult{}, err
		}
	}
	filter := input.Owner
	if !user.IsAdmin() {
		if input.Owner != "" && input.Owner != user.GetUserId() {
			return model.CorrelationResult{}, model.Forbidden("only admins may correlate for another owner")
		}
		filter = user.GetUserId()
	}
	return this.engine.Correlate(ctx, engine.CorrelationRequest{MessageInput: input, OwnerFilter: filter})
}
