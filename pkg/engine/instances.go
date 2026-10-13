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

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/script"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/timer"
	"github.com/google/uuid"
)

// StartRequest starts an instance of a deployment (RUN-01).
type StartRequest struct {
	DeploymentId string
	// 0 selects the current version
	DeploymentVersion int
	// empty: the engine generates a UUID; given: the start is idempotent
	InstanceId string
	Owner      string
	Variables  map[string]any
	// start event; empty selects the start event without definition
	StartElementId string
	// local variables of the start event, e.g. of a message start
	Locals map[string]any
	// the instance a maintenance procedure belongs to (SMS-13)
	ParentId string
	// set by users, e.g. for smart-service instances
	Name, Description string
}

// ErrDisabled refuses a start of a deployment that is switched off (DEP-12).
func ErrDisabled(deploymentId string) error {
	return model.Conflict("deployment %v is disabled", deploymentId)
}

// StartInstance creates an instance and runs it until all tokens wait or end. With a given
// id, a second start with the same content returns the existing instance and created=false;
// different content is a conflict.
func (this *Engine) StartInstance(ctx context.Context, request StartRequest) (result model.Instance, created bool, err error) {
	start := func() error {
		return this.store.Tx(ctx, func(tx *store.Tx) error {
			result, created, err = this.startInstance(tx, request, model.CauseStart)
			return err
		})
	}
	err = start()
	if request.InstanceId != "" && store.IsUniqueViolation(err) {
		// a concurrent start with the same id won; now the idempotency check sees its instance
		err = start()
	}
	return result, created, err
}

func (this *Engine) startInstance(tx *store.Tx, request StartRequest, cause model.RunCause) (model.Instance, bool, error) {
	deployment, err := tx.GetDeployment(request.DeploymentId, false)
	if err != nil {
		return model.Instance{}, false, err
	}
	if deployment.Disabled {
		return model.Instance{}, false, ErrDisabled(deployment.Id)
	}
	version := request.DeploymentVersion
	if version == 0 {
		version = deployment.Version
	}
	if version < 1 || version > deployment.Version {
		return model.Instance{}, false, model.BadRequest("deployment %v has no version %v", deployment.Id, version)
	}
	proc, err := this.process(tx, deployment.Id, version)
	if err != nil {
		return model.Instance{}, false, err
	}
	start, err := startElement(proc, request.StartElementId)
	if err != nil {
		return model.Instance{}, false, err
	}
	ns, _ := this.namespaces.Get(deployment.Namespace)
	variables, err := startVariables(start, request.Variables, this.config.MaxVariableBytes, ns.JsonStringParameters)
	if err != nil {
		return model.Instance{}, false, err
	}
	if request.InstanceId != "" {
		existing, err := tx.GetInstance(request.InstanceId, true)
		if err == nil {
			if existing.DeploymentId != request.DeploymentId ||
				(request.DeploymentVersion != 0 && existing.DeploymentVersion != request.DeploymentVersion) ||
				!sameJson(existing.StartVariables, variables) {
				return model.Instance{}, false, model.Conflict("instance %v already exists with different content", request.InstanceId)
			}
			return existing.Instance, false, nil
		}
		if !errors.Is(err, model.ErrNotFound) {
			return model.Instance{}, false, err
		}
	}
	id := request.InstanceId
	if id == "" {
		id = uuid.NewString()
	}
	now := this.now()
	inst := &store.Instance{
		Instance: model.Instance{
			Id: id, Namespace: deployment.Namespace, Owner: request.Owner, DeploymentId: deployment.Id, DeploymentName: deployment.Name,
			DeploymentVersion: version, ModelId: deployment.ModelId, ModelVersion: deployment.ModelVersion, Run: 0, State: model.StateRunning,
			StartVariables: variables, CreatedAt: now, StartedAt: now, LastActivityAt: now, ParentId: request.ParentId, Name: request.Name, Description: request.Description,
		},
		ActivityMinSeq: 1,
	}
	if err = tx.InsertInstance(*inst); err != nil {
		return model.Instance{}, false, err
	}
	if err = this.beginRun(tx, inst, version, start.Id, request.Locals, cause); err != nil {
		return model.Instance{}, false, err
	}
	return inst.Instance, true, nil
}

func startElement(proc *bpmn.Process, id string) (*bpmn.Element, error) {
	if id == "" {
		start := proc.NoneStartEvent()
		if start == nil {
			return nil, model.BadRequest("process has no start event without definition and cannot be started directly")
		}
		return start, nil
	}
	start := proc.Element(id)
	if start == nil || start.Type != bpmn.StartEvent {
		return nil, model.BadRequest("%v is not a start event", id)
	}
	return start, nil
}

// startVariables normalizes the variables and adds defaults of missing form fields (BPMN-02)
func startVariables(start *bpmn.Element, given map[string]any, maxBytes int, jsonStrings bool) (map[string]any, error) {
	result := map[string]any{}
	for _, field := range start.FormFields {
		if field.DefaultValue == "" {
			continue
		}
		result[field.Id] = typedDefault(field)
	}
	types := map[string]string{}
	for _, field := range start.FormFields {
		types[field.Id] = field.Type
	}
	for name, value := range given {
		if _, isString := value.(string); jsonStrings && types[name] == "string" && value != nil && !isString {
			// a string form field keeps other values as JSON, as Camunda did; models of smart
			// services parse them, e.g. a list of selected devices (SMS-08)
			raw, err := json.Marshal(value)
			if err != nil {
				return nil, model.BadRequest("variable %v: %v", name, err)
			}
			value = string(raw)
		}
		normalized, err := script.Normalize(value, maxBytes)
		if err != nil {
			return nil, model.BadRequest("variable %v: %v", name, err)
		}
		result[name] = normalized
	}
	return result, nil
}

func typedDefault(field bpmn.FormField) any {
	switch field.Type {
	case "long", "double", "number":
		if f, err := strconv.ParseFloat(field.DefaultValue, 64); err == nil {
			return f
		}
	case "boolean":
		if b, err := strconv.ParseBool(field.DefaultValue); err == nil {
			return b
		}
	}
	return field.DefaultValue
}

func sameJson(a, b map[string]any) bool {
	ra, _ := json.Marshal(a)
	rb, _ := json.Marshal(b)
	var na, nb any
	_ = json.Unmarshal(ra, &na)
	_ = json.Unmarshal(rb, &nb)
	return reflect.DeepEqual(na, nb)
}

// beginRun starts the next run of an instance at a start event with its start variables (RUN-11)
func (this *Engine) beginRun(tx *store.Tx, inst *store.Instance, version int, startElementId string, locals map[string]any, cause model.RunCause) error {
	now := this.now()
	inst.Run++
	inst.DeploymentVersion = version
	inst.State = model.StateRunning
	inst.EndReason = ""
	inst.EndedAt = nil
	// a manual restart replaces a pending automatic one
	inst.RestartAt = nil
	this.metrics.InstanceStarted(inst.Namespace, inst.DeploymentId, inst.Owner)
	inst.StartedAt = now
	inst.LastActivityAt = now
	if err := tx.InsertRun(model.Run{InstanceId: inst.Id, Run: inst.Run, DeploymentVersion: version, Cause: cause, State: model.StateRunning, StartedAt: now}); err != nil {
		return err
	}
	// variables of the previous run do not carry over
	if err := tx.DeleteVariables(inst.Id); err != nil {
		return err
	}
	// neither do its modules nor its maintenance procedures: processes that create modules run
	// briefly, every run starts without them (SMS-03, SMS-09)
	removed, err := tx.RemoveModulesOfInstance(inst.Id)
	if err != nil {
		return err
	}
	r, err := this.newRunner(tx, inst)
	if err != nil {
		return err
	}
	if err = r.log(model.ActivityRunStarted, nil, map[string]any{"cause": string(cause), "deployment_version": version, "start_variables": r.preview(inst.StartVariables)}); err != nil {
		return err
	}
	if removed > 0 {
		if err = r.log(model.ActivityModulesRemoved, nil, map[string]any{"count": removed}); err != nil {
			return err
		}
	}
	for _, name := range sortedKeys(inst.StartVariables) {
		if err = r.setProcessVariable(nil, name, inst.StartVariables[name]); err != nil {
			if err = r.handle(err); err != nil {
				return err
			}
			return r.finish()
		}
	}
	if _, err = r.newToken(startElementId, "", maps.Clone(locals)); err != nil {
		if err = r.handle(err); err != nil {
			return err
		}
	}
	return r.finish()
}

// startTimerDue computes the next due time of a timer start event after from; nil if the
// timer has no further occurrence. Expressions see no variables, there is no instance yet.
func (this *Engine) startTimerDue(start *bpmn.Element, from time.Time) (*time.Time, error) {
	value := start.Timer.Value
	if start.Timer.Expression {
		result, err := this.sandbox.Run(start.Timer.Value, nil)
		if err != nil {
			return nil, fmt.Errorf("timer expression: %w", err)
		}
		s, ok := result.Value.(string)
		if !ok {
			return nil, errors.New("timer expression must evaluate to a string")
		}
		value = s
	}
	due, err := timer.Next(start.Timer.Kind, value, from, this.config.Location)
	if errors.Is(err, timer.ErrNoNextOccurrence) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("timer: %w", err)
	}
	if start.Timer.Kind == timer.KindDate && !due.After(from) {
		// a start date in the past does not fire after a restart or an update
		return nil, nil
	}
	return &due, nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// lockRunning locks a running instance and prepares a runner for it
func (this *Engine) lockRunning(tx *store.Tx, instanceId string) (*runner, error) {
	inst, err := tx.GetInstance(instanceId, true)
	if err != nil {
		return nil, err
	}
	if inst.State != model.StateRunning {
		return nil, model.Conflict("instance %v is not running", instanceId)
	}
	return this.newRunner(tx, &inst)
}

// StopInstance ends the current run; history stays (RUN-04).
func (this *Engine) StopInstance(ctx context.Context, instanceId string, reason string) error {
	return this.store.Tx(ctx, func(tx *store.Tx) error {
		return this.stopInstance(tx, instanceId, reason)
	})
}

func (this *Engine) stopInstance(tx *store.Tx, instanceId string, reason string) error {
	inst, err := tx.GetInstance(instanceId, true)
	if err != nil {
		return err
	}
	if inst.State != model.StateRunning && inst.RestartAt != nil {
		// stopping an instance after an incident cancels its automatic restart
		inst.RestartAt = nil
		return tx.UpdateInstance(inst)
	}
	r, err := this.lockRunning(tx, instanceId)
	if err != nil {
		return err
	}
	if err = r.endRun(model.StateTerminated, reason); err != nil {
		return err
	}
	return r.finish()
}

// RestartInstance starts a new run of the instance with its stored start variables; a running
// run is stopped first. version 0 selects the current deployment version (RUN-11).
func (this *Engine) RestartInstance(ctx context.Context, instanceId string, version int, cause model.RunCause) (result model.Instance, err error) {
	return this.RestartWith(ctx, instanceId, version, nil, cause)
}

// RestartWith restarts with new start variables, e.g. after a parameter change of a smart
// service (SMS-09); nil keeps the stored ones.
func (this *Engine) RestartWith(ctx context.Context, instanceId string, version int, variables map[string]any, cause model.RunCause) (result model.Instance, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		inst, err := tx.GetInstance(instanceId, true)
		if err != nil {
			return err
		}
		if variables != nil {
			deployment, err := tx.GetDeployment(inst.DeploymentId, false)
			if err != nil {
				return err
			}
			if version == 0 {
				version = deployment.Version
			}
			proc, err := this.process(tx, deployment.Id, version)
			if err != nil {
				return err
			}
			start := proc.NoneStartEvent()
			if start == nil {
				return model.BadRequest("deployment %v version %v has no start event without definition", deployment.Id, version)
			}
			ns, _ := this.namespaces.Get(deployment.Namespace)
			if inst.StartVariables, err = startVariables(start, variables, this.config.MaxVariableBytes, ns.JsonStringParameters); err != nil {
				return err
			}
			if err = tx.SetStartVariables(inst.Id, inst.StartVariables); err != nil {
				return err
			}
		}
		inst, err = this.restartInstance(tx, instanceId, version, cause)
		result = inst.Instance
		return err
	})
	return result, err
}

func (this *Engine) restartInstance(tx *store.Tx, instanceId string, version int, cause model.RunCause) (store.Instance, error) {
	inst, err := tx.GetInstance(instanceId, true)
	if err != nil {
		return inst, err
	}
	// everything that can refuse the restart is checked before the current run ends, so that a
	// refused restart leaves the instance as it was
	deployment, err := tx.GetDeployment(inst.DeploymentId, false)
	if err != nil {
		return inst, err
	}
	if deployment.Disabled {
		return inst, ErrDisabled(deployment.Id)
	}
	if version == 0 {
		version = deployment.Version
	}
	if version < 1 || version > deployment.Version {
		return inst, model.BadRequest("deployment %v has no version %v", deployment.Id, version)
	}
	proc, err := this.process(tx, deployment.Id, version)
	if err != nil {
		return inst, err
	}
	start := proc.NoneStartEvent()
	if start == nil {
		return inst, model.BadRequest("deployment %v version %v has no start event without definition", deployment.Id, version)
	}
	if inst.State == model.StateRunning {
		r, err := this.newRunner(tx, &inst)
		if err != nil {
			return inst, err
		}
		if err = r.endRun(model.StateTerminated, "restart"); err != nil {
			return inst, err
		}
		if err = r.finish(); err != nil {
			return inst, err
		}
	}
	inst.DeploymentName = deployment.Name
	if err = this.beginRun(tx, &inst, version, start.Id, nil, cause); err != nil {
		return inst, err
	}
	return inst, nil
}

// SetVariable sets a variable of a running instance (RUN-05).
// SetVariable sets a variable of an instance in any state (RUN-05, SMS-10): variables of a
// smart service that is ready are read by its maintenance procedures.
func (this *Engine) SetVariable(ctx context.Context, instanceId string, name string, value any) error {
	if strings.TrimSpace(name) == "" {
		return model.BadRequest("variable name must not be empty")
	}
	return this.store.Tx(ctx, func(tx *store.Tx) error {
		inst, err := tx.GetInstance(instanceId, true)
		if err != nil {
			return err
		}
		r, err := this.newRunner(tx, &inst)
		if err != nil {
			return err
		}
		normalized, err := script.Normalize(value, this.config.MaxVariableBytes)
		if err != nil {
			return model.BadRequest("variable %v: %v", name, err)
		}
		if err = r.setProcessVariable(nil, name, normalized); err != nil {
			var inc *incidentError
			if errors.As(err, &inc) {
				return model.BadRequest("%v", inc.err)
			}
			return err
		}
		return r.finish()
	})
}

// ApplyUpdate handles running instances after a deployment got a new version (DEP-04).
func (this *Engine) ApplyUpdate(tx *store.Tx, deploymentId string, strategy model.UpdateStrategy) error {
	if strategy == model.UpdateDrain {
		return nil
	}
	ids, err := tx.InstanceIdsOfDeployment(deploymentId, model.StateRunning)
	if err != nil {
		return err
	}
	for _, id := range ids {
		switch strategy {
		case model.UpdateStop:
			err = this.stopInstance(tx, id, "deployment updated")
		case model.UpdateRestart:
			_, err = this.restartInstance(tx, id, 0, model.CauseUpdate)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// ActivateVersion registers the message and timer start events of the current version of a
// deployment; older versions no longer receive new starts.
func (this *Engine) ActivateVersion(tx *store.Tx, deploymentId string, version int) error {
	proc, err := this.process(tx, deploymentId, version)
	if err != nil {
		return err
	}
	deployment, err := tx.GetDeployment(deploymentId, false)
	if err != nil {
		return err
	}
	ns, _ := this.namespaces.Get(deployment.Namespace)
	now := this.now()
	var events []store.StartEvent
	for _, start := range proc.StartEvents() {
		switch start.Kind {
		case bpmn.EventMessage:
			if ns.MessageStartsAreProcedures && !start.IsDeviceEvent() {
				// started only through their instance (SMS-13), not by a message to anyone
				continue
			}
			events = append(events, store.StartEvent{Version: version, ElementId: start.Id, Kind: string(bpmn.EventMessage), MessageName: messageName(deploymentId, start)})
		case bpmn.EventTimer:
			due, err := this.startTimerDue(start, now)
			if err != nil {
				return model.ValidationFailed([]model.Issue{{ElementId: start.Id, Message: err.Error()}})
			}
			events = append(events, store.StartEvent{Version: version, ElementId: start.Id, Kind: string(bpmn.EventTimer), DueAt: due})
			this.signal()
		}
	}
	return tx.ReplaceStartEvents(deploymentId, events)
}

// DeleteVariable removes a variable of an instance in any state (SMS-10).
func (this *Engine) DeleteVariable(ctx context.Context, instanceId string, name string) error {
	return this.store.Tx(ctx, func(tx *store.Tx) error {
		inst, err := tx.GetInstance(instanceId, true)
		if err != nil {
			return err
		}
		r, err := this.newRunner(tx, &inst)
		if err != nil {
			return err
		}
		if _, ok := r.vars[name]; !ok {
			return model.NotFound("variable %v not found", name)
		}
		delete(r.vars, name)
		delete(r.varSizes, name)
		if err = tx.DeleteVariable(instanceId, name); err != nil {
			return err
		}
		if err = r.log(model.ActivityVariableDeleted, nil, map[string]any{"name": name}); err != nil {
			return err
		}
		return r.finish()
	})
}
