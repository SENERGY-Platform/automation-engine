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
// it; administrate on the instance is needed, as before (SMS-13). It starts also while a run of
// the procedure is running, since a user asked for it.
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
	return this.startMaintenance(ctx, token, id, procedure, input.Variables, startAlways, "")
}

// StartMaintenanceInternal starts a procedure without a caller, e.g. for the watcher (TASK-27);
// the procedure runs as the owner of the instance. While a run of the procedure is running, it
// queues a rerun with the variables instead and returns no instance (SNRGY-5143).
func (this *Controller) StartMaintenanceInternal(ctx context.Context, id string, procedure string, variables map[string]any) (model.Instance, error) {
	return this.startMaintenance(ctx, "", id, procedure, variables, startOrQueue, store.RerunHash(variables))
}

// StartMaintenanceRerun starts a rerun queued by StartMaintenanceInternal and removes it. It
// starts nothing and returns no instance while a run of the procedure is running or once
// another replica started the rerun.
func (this *Controller) StartMaintenanceRerun(ctx context.Context, rerun store.MaintenanceRerun) (model.Instance, error) {
	return this.startMaintenance(ctx, "", rerun.InstanceId, rerun.Procedure, rerun.Variables, startQueued, rerun.VariablesHash)
}

// startMode is how a start of a procedure treats a running run of it (SNRGY-5143)
type startMode int

const (
	// start anyway
	startAlways startMode = iota
	// queue a rerun instead
	startOrQueue
	// start a queued rerun once nothing runs
	startQueued
)

// startMaintenance starts a procedure; auto_select_all fields use the token, or the owner's
// without caller. Except for startAlways, it locks the instance while it decides whether to
// start and while it starts, so that replicas start one run of a procedure at a time.
func (this *Controller) startMaintenance(ctx context.Context, token string, id string, procedure string, variables map[string]any, mode startMode, hash string) (model.Instance, error) {
	var request engine.StartRequest
	var run int
	held := false
	err := this.store.Tx(ctx, func(tx *store.Tx) error {
		inst, err := tx.GetInstance(id, mode != startAlways)
		if err != nil {
			return err
		}
		elementId, err := this.maintenanceStart(tx, inst, procedure)
		if err != nil {
			return err
		}
		run = inst.Run
		// decided here already, so that a held start reads no options
		if held, err = this.holdBack(tx, mode, id, procedure, variables, hash); err != nil || held {
			return err
		}
		start := maps.Clone(inst.StartVariables)
		if start == nil {
			start = map[string]any{}
		}
		maps.Copy(start, variables)
		request = engine.StartRequest{DeploymentId: inst.DeploymentId, DeploymentVersion: inst.DeploymentVersion, Owner: inst.Owner, Variables: start, StartElementId: elementId, ParentId: inst.Id, Procedure: procedure}
		return nil
	})
	if err != nil || held {
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
	if mode == startAlways {
		result, _, err := this.engine.StartInstance(ctx, request)
		return result, err
	}
	var result model.Instance
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		// checked again, since the options were read without the lock
		inst, err := tx.GetInstance(id, true)
		if err != nil {
			return err
		}
		if inst.Run != run {
			return model.Conflict("instance %v started a new run meanwhile", id)
		}
		if _, err = this.maintenanceStart(tx, inst, procedure); err != nil {
			return err
		}
		if held, err := this.holdBack(tx, mode, id, procedure, variables, hash); err != nil || held {
			return err
		}
		if result, err = this.engine.StartInstanceIn(tx, request); err != nil {
			return err
		}
		// the run reads the current state, which serves a rerun queued with these variables
		return tx.DeleteMaintenanceRerun(id, procedure, hash)
	})
	return result, err
}

// maintenanceStart checks that the procedure can start on the instance and returns its start
// event
func (this *Controller) maintenanceStart(tx *store.Tx, inst store.Instance, procedure string) (string, error) {
	if inst.ParentId != "" {
		return "", model.BadRequest("instance %v is itself a maintenance procedure", inst.Id)
	}
	items := []model.Instance{inst.Instance}
	if err := withStatus(tx, items); err != nil {
		return "", err
	}
	if items[0].Status != model.StatusReady {
		return "", model.Conflict("instance %v is %v, a maintenance procedure needs it ready without errors", inst.Id, items[0].Status)
	}
	proc, err := this.engine.Process(tx, inst.DeploymentId, inst.DeploymentVersion)
	if err != nil {
		return "", err
	}
	for _, p := range procedures(proc) {
		if p.Id == procedure {
			return p.ElementId, nil
		}
	}
	return "", model.NotFound("instance %v has no maintenance procedure %v", inst.Id, procedure)
}

// holdBack decides, with the instance locked, whether a start waits: a watch queues a rerun while
// a run of the procedure is running, a rerun waits for that run and is dropped once started
func (this *Controller) holdBack(tx *store.Tx, mode startMode, id string, procedure string, variables map[string]any, hash string) (bool, error) {
	if mode == startAlways {
		return false, nil
	}
	running, err := tx.MaintenanceRunning(id, procedure)
	if err != nil {
		return false, err
	}
	if mode == startOrQueue {
		if !running {
			return false, nil
		}
		return true, tx.QueueMaintenanceRerun(store.MaintenanceRerun{InstanceId: id, Procedure: procedure, Variables: variables, VariablesHash: hash, RequestedAt: this.engine.Now()})
	}
	if running {
		return true, nil
	}
	queued, err := tx.MaintenanceRerunQueued(id, procedure, hash)
	return !queued, err
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
	return this.engine.RestartWith(ctx, id, input.DeploymentVersion, variables, input.ValueLabels, model.CauseParameters)
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
