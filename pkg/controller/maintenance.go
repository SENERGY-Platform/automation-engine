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
	"maps"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// procedures are the message start events of a process, except device and import events
func procedures(proc *bpmn.Process) []model.MaintenanceProcedure {
	result := []model.MaintenanceProcedure{}
	for _, start := range proc.StartEvents() {
		if start.Kind != bpmn.EventMessage || start.Message == nil || start.Message.Name == "" || start.IsDeviceEvent() {
			continue
		}
		result = append(result, model.MaintenanceProcedure{Id: start.Message.Name, ElementId: start.Id, Name: start.Name, Parameters: bpmn.StartParameters(start)})
	}
	return result
}

// ListMaintenanceProcedures lists the procedures an instance offers (SMS-13).
func (this *Controller) ListMaintenanceProcedures(ctx context.Context, token string, namespace string, id string) (result []model.MaintenanceProcedure, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		inst, err := this.readInstance(ctx, tx, token, namespace, id, Read)
		if err != nil {
			return err
		}
		proc, err := this.engine.Process(tx, inst.DeploymentId, inst.DeploymentVersion)
		if err != nil {
			return err
		}
		result = procedures(proc)
		return nil
	})
	return result, err
}

// StartMaintenance starts a procedure of an instance as an instance of its own that refers to
// it; administrate on the instance is needed, as before (SMS-13).
func (this *Controller) StartMaintenance(ctx context.Context, token string, namespace string, id string, procedure string, input model.MaintenanceStartInput) (model.Instance, error) {
	err := this.store.Tx(ctx, func(tx *store.Tx) error {
		inst, err := this.readInstance(ctx, tx, token, namespace, id, Administrate)
		if err != nil {
			return err
		}
		return inCloud(inst.Instance)
	})
	if err != nil {
		return model.Instance{}, err
	}
	return this.startMaintenance(ctx, token, id, procedure, input.Variables)
}

// StartMaintenanceInternal starts a procedure without a caller, e.g. for the watcher (TASK-27);
// the procedure runs as the owner of the instance.
func (this *Controller) StartMaintenanceInternal(ctx context.Context, id string, procedure string, variables map[string]any) (model.Instance, error) {
	return this.startMaintenance(ctx, "", id, procedure, variables)
}

// startMaintenance starts a procedure; auto_select_all fields use the token, or the owner's
// without caller
func (this *Controller) startMaintenance(ctx context.Context, token string, id string, procedure string, variables map[string]any) (model.Instance, error) {
	var request engine.StartRequest
	err := this.store.Tx(ctx, func(tx *store.Tx) error {
		inst, err := tx.GetInstance(id, false)
		if err != nil {
			return err
		}
		if inst.ParentId != "" {
			return model.BadRequest("instance %v is itself a maintenance procedure", id)
		}
		items := []model.Instance{inst.Instance}
		if err = withStatus(tx, items); err != nil {
			return err
		}
		if items[0].Status != model.StatusReady {
			return model.Conflict("instance %v is %v, a maintenance procedure needs it ready without errors", id, items[0].Status)
		}
		proc, err := this.engine.Process(tx, inst.DeploymentId, inst.DeploymentVersion)
		if err != nil {
			return err
		}
		elementId := ""
		for _, p := range procedures(proc) {
			if p.Id == procedure {
				elementId = p.ElementId
			}
		}
		if elementId == "" {
			return model.NotFound("instance %v has no maintenance procedure %v", id, procedure)
		}
		start := maps.Clone(inst.StartVariables)
		if start == nil {
			start = map[string]any{}
		}
		maps.Copy(start, variables)
		request = engine.StartRequest{DeploymentId: inst.DeploymentId, DeploymentVersion: inst.DeploymentVersion, Owner: inst.Owner, Variables: start, StartElementId: elementId, ParentId: inst.Id}
		return nil
	})
	if err != nil {
		return model.Instance{}, err
	}
	if token == "" && this.parameters != nil && this.parametersUserToken != nil {
		if token, err = this.parametersUserToken(request.Owner); err != nil {
			return model.Instance{}, model.Unavailable("token of %v: %v", request.Owner, err)
		}
	}
	if token != "" {
		if request.Variables, err = this.withAutoSelected(ctx, token, request.DeploymentId, request.DeploymentVersion, request.StartElementId, request.Variables); err != nil {
			return model.Instance{}, err
		}
	}
	result, _, err := this.engine.StartInstance(ctx, request)
	return result, err
}

// UpdateParameters restarts an instance with new start parameters; the new run removes the
// modules of the previous one (SMS-03, SMS-09). Needs administrate on the instance and execute
// on its deployment, as before.
func (this *Controller) UpdateParameters(ctx context.Context, token string, namespace string, id string, input model.ParametersInput) (model.Instance, error) {
	err := this.store.Tx(ctx, func(tx *store.Tx) error {
		inst, err := this.readInstance(ctx, tx, token, namespace, id, Administrate)
		if err != nil {
			return err
		}
		if err = inCloud(inst.Instance); err != nil {
			return err
		}
		if inst.ParentId != "" {
			return model.BadRequest("instance %v is a maintenance procedure; change the parameters of %v", id, inst.ParentId)
		}
		_, err = this.readDeployment(ctx, tx, token, namespace, inst.DeploymentId, Execute, false)
		return err
	})
	if err != nil {
		return model.Instance{}, err
	}
	variables := input.Variables
	if variables == nil {
		variables = map[string]any{}
	}
	var deploymentId string
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		inst, err := tx.GetInstance(id, false)
		deploymentId = inst.DeploymentId
		return err
	})
	if err != nil {
		return model.Instance{}, err
	}
	if variables, err = this.withAutoSelected(ctx, token, deploymentId, input.DeploymentVersion, "", variables); err != nil {
		return model.Instance{}, err
	}
	return this.engine.RestartWith(ctx, id, input.DeploymentVersion, variables, model.CauseParameters)
}

// DeleteVariable removes a variable of an instance (SMS-10).
func (this *Controller) DeleteVariable(ctx context.Context, token string, namespace string, id string, name string) error {
	if err := this.checkAll(ctx, token, namespace, []string{id}, Administrate); err != nil {
		return err
	}
	if err := this.localOnly(ctx, id); err != nil {
		return err
	}
	return this.engine.DeleteVariable(ctx, id, name)
}
