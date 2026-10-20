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

package modules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/devices"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/models/go/models"
)

const (
	TypeProcessDeployment      = "process_deployment"
	TypeProcessDeploymentStart = "process_deployment_start"
)

// Processes is the part of the engine API the process modules use, with the owner's token.
type Processes interface {
	GetModel(ctx context.Context, token string, namespace string, id string) (model.Model, error)
	GetModelVersion(ctx context.Context, token string, namespace string, id string, version int) (model.ModelVersion, error)
	CreateDeployment(ctx context.Context, token string, namespace string, input model.DeploymentInput) (model.Deployment, error)
	PutDeployment(ctx context.Context, token string, namespace string, id string, input model.DeploymentInput) (model.Deployment, error)
	StartInstance(ctx context.Context, token string, namespace string, input model.StartInstanceInput) (model.Instance, error)
	// the gateways the user may read (SYNC-12)
	ListNetworks(ctx context.Context, token string) ([]model.Network, error)
	// removes the deployments and instances these modules create (SMS-06)
	Resources
}

// Namespaces gives the referenced namespaces of the namespace of an instance (NS-08).
type Namespaces interface {
	Get(name string) (model.Namespace, bool)
}

// AddProcessModules registers process_deployment (TASK-20) and process_deployment_start (TASK-21).
func (this *Runtime) AddProcessModules(processes Processes, namespaces Namespaces) {
	p := &processModules{runtime: this, processes: processes, namespaces: namespaces}
	this.resources = processes
	this.add(TypeProcessDeployment, &deployHandler{p})
	this.add(TypeProcessDeploymentStart, &startHandler{p})
	this.AddCheck(TypeProcessDeployment, p.checkDeployment)
	this.AddCheck(TypeProcessDeploymentStart, p.checkInstance)
}

type processModules struct {
	runtime    *Runtime
	processes  Processes
	namespaces Namespaces
}

// input reads a task input; values that are not strings are given as JSON, like the worker did
func input(task Task, name string) (string, bool) {
	v, ok := task.Inputs[name]
	if !ok || v == nil {
		return "", false
	}
	if s, ok := v.(string); ok {
		return s, true
	}
	raw, _ := json.Marshal(v)
	return string(raw), true
}

func boolInput(task Task, name string) (value bool, given bool) {
	v, ok := task.Inputs[name]
	if !ok {
		return false, false
	}
	switch b := v.(type) {
	case bool:
		return b, true
	case string:
		return b == "true", true
	}
	return false, false
}

// context of a task: owner, token, root instance and target namespace
type processTask struct {
	owner     string
	token     string
	root      store.Instance
	namespace string
}

func (this *processModules) begin(ctx context.Context, task Task, requested string) (result processTask, err error) {
	err = this.runtime.store.Tx(ctx, func(tx *store.Tx) error {
		result.root, err = tx.GetInstance(task.RootInstanceId, false)
		return err
	})
	if err != nil {
		return result, err
	}
	result.owner = result.root.Owner
	ns, _ := this.namespaces.Get(result.root.Namespace)
	result.namespace = requested
	if result.namespace == "" && len(ns.ReferencedNamespaces) > 0 {
		result.namespace = ns.ReferencedNamespaces[0]
	}
	if result.namespace == "" || !slices.Contains(ns.ReferencedNamespaces, result.namespace) {
		return result, fmt.Errorf("namespace %v may not create resources in namespace %q (NS-08)", result.root.Namespace, result.namespace)
	}
	if result.token, err = this.runtime.token(result.owner); err != nil {
		return result, fmt.Errorf("token of %v: %w", result.owner, err)
	}
	return result, nil
}

type deployHandler struct {
	*processModules
}

const deployPrefix = TypeProcessDeployment + "."

// Do deploys a process like the process deployment worker, internally instead of over the
// prepared deployment of process-deployment (TASK-20). With a key, an existing deployment of
// the instance is updated instead, e.g. by a maintenance procedure.
func (this *deployHandler) Do(ctx context.Context, task Task) (modules []model.Module, outputs map[string]any, err error) {
	namespace, _ := input(task, deployPrefix+"namespace")
	p, err := this.begin(ctx, task, namespace)
	if err != nil {
		return nil, nil, err
	}
	modelId, _ := input(task, deployPrefix+"process_model_id")
	if modelId == "" {
		return nil, nil, fmt.Errorf("missing process model id")
	}
	m, err := this.processes.GetModel(ctx, p.token, p.namespace, modelId)
	if err != nil {
		return nil, nil, err
	}
	version, err := this.processes.GetModelVersion(ctx, p.token, p.namespace, modelId, m.Version)
	if err != nil {
		return nil, nil, err
	}
	proc, _, err := bpmn.Parse(version.Xml)
	if err != nil {
		return nil, nil, err
	}
	deployment := model.DeploymentInput{ModelId: modelId, Name: m.Name, Source: "smart_service", ManagedBy: p.root.Id,
		Selections: map[string]model.Selection{}, Parameters: map[string]model.ElementParameters{}}
	if name, ok := input(task, deployPrefix+"name"); ok && name != "" {
		deployment.Name = name
	}
	handling := model.IncidentHandling{Restart: false, Notify: true}
	if v, ok := boolInput(task, deployPrefix+"on_incident.restart"); ok {
		handling.Restart = v
	}
	if v, ok := boolInput(task, deployPrefix+"on_incident.notify"); ok {
		handling.Notify = v
	}
	deployment.IncidentHandling = &handling
	if err = this.elementInputs(task, proc, &deployment); err != nil {
		return nil, nil, err
	}
	if prefer, ok := boolInput(task, deployPrefix+"prefer_fog_deployment"); ok && prefer {
		if deployment.NetworkId, err = this.gatewayFor(ctx, p.token, deployment.Selections); err != nil {
			return nil, nil, err
		}
	}

	moduleData := map[string]any{}
	if raw, ok := input(task, deployPrefix+"module_data"); ok {
		// invalid JSON is ignored, as by the worker
		_ = json.Unmarshal([]byte(raw), &moduleData)
	}
	key, hasKey := input(task, deployPrefix+"key")
	moduleId := task.ModuleId()
	var result model.Deployment
	existing := ""
	if hasKey && key != "" {
		existingModules, err := this.runtime.existing(ctx, task, key, TypeProcessDeployment)
		if err != nil {
			return nil, nil, err
		}
		if len(existingModules) > 0 {
			existing, _ = existingModules[0].Data["process_deployment_id"].(string)
			if existing != "" {
				moduleId = existingModules[0].Id
				for k, v := range existingModules[0].Data {
					if _, set := moduleData[k]; !set {
						moduleData[k] = v
					}
				}
			}
		}
	}
	if existing != "" {
		strategy, _ := input(task, deployPrefix+"update_strategy")
		if strategy == "" {
			strategy = string(model.UpdateRestart)
		}
		deployment.UpdateStrategy = model.UpdateStrategy(strategy)
		if result, err = this.processes.PutDeployment(ctx, p.token, p.namespace, existing, deployment); err != nil {
			return nil, nil, err
		}
	} else if result, err = this.processes.CreateDeployment(ctx, p.token, p.namespace, deployment); err != nil {
		return nil, nil, err
	}
	moduleData["process_deployment_id"] = result.Id
	moduleData["process_deployment_name"] = result.Name
	keys := []string{}
	if hasKey && key != "" {
		keys = []string{key}
	}
	// the deployment is complete now; the runtime delivers the done event as soon as the
	// instance waits for it (SMS-04)
	done := "deployment_done_" + result.Id
	modules = []model.Module{{Id: moduleId, ModuleType: TypeProcessDeployment, Data: moduleData, Keys: keys,
		DeleteInfo: &model.ModuleDeleteInfo{UserId: p.owner, Resource: &model.ResourceRef{Kind: model.ResourceDeployment, Namespace: p.namespace, Id: result.Id}}}}
	return modules, map[string]any{"process_deployment_id": result.Id, "done_event": done, "is_fog_deployment": result.NetworkId != "", "fog_hub": result.NetworkId}, nil
}

// gatewayFor chooses the first gateway that carries every selected device, with the devices of
// selected groups, as smart-service-module-worker-process did; without devices, with imports
// or without such gateway the deployment stays in the cloud (TASK-20, DEP-09)
func (this *processModules) gatewayFor(ctx context.Context, token string, selections map[string]model.Selection) (string, error) {
	selected := []string{}
	for _, s := range selections {
		if s.ImportId != "" {
			return "", nil
		}
		if s.DeviceId != "" {
			selected = append(selected, s.DeviceId)
		}
		if s.DeviceGroupId != "" {
			group, err, _ := this.runtime.iot.ReadDeviceGroup(s.DeviceGroupId, token, false)
			if err != nil {
				return "", err
			}
			selected = append(selected, group.DeviceIds...)
		}
	}
	if len(selected) == 0 {
		this.runtime.logger.WarnContext(ctx, "a process deployment without devices stays in the cloud")
		return "", nil
	}
	networks, err := this.processes.ListNetworks(ctx, token)
	if err != nil {
		return "", err
	}
	for _, network := range networks {
		if !network.Compatible {
			continue
		}
		hub, err, code := this.runtime.iot.ReadHub(network.Id, token, models.Read)
		if code == http.StatusNotFound || code == http.StatusForbidden {
			continue
		}
		if err != nil {
			return "", err
		}
		carries := true
		for _, id := range selected {
			pure, _ := devices.SplitModifier(id)
			if !slices.Contains(hub.DeviceIds, pure) && !slices.Contains(hub.DeviceIds, id) {
				carries = false
				break
			}
		}
		if carries {
			return network.Id, nil
		}
	}
	return "", nil
}

// elementInputs reads the selections and overrides per element (TASK-20)
func (this *deployHandler) elementInputs(task Task, proc *bpmn.Process, deployment *model.DeploymentInput) error {
	for _, e := range proc.Ordered() {
		prefix := deployPrefix + e.Id + "."
		if raw, ok := input(task, prefix+"selection"); ok {
			selection, err := selectionOf(raw)
			if err != nil {
				return fmt.Errorf("%vselection: %w", prefix, err)
			}
			deployment.Selections[e.Id] = selection
		}
		params := model.ElementParameters{}
		for _, m := range e.Inputs {
			if value, ok := input(task, prefix+"parameter."+m.Name); ok {
				if params.Inputs == nil {
					params.Inputs = map[string]string{}
				}
				params.Inputs[m.Name] = value
			}
		}
		for name := range task.Inputs {
			if variable, ok := strings.CutPrefix(name, prefix+"variables."); ok {
				value, _ := input(task, name)
				if params.Variables == nil {
					params.Variables = map[string]string{}
				}
				params.Variables[variable] = value
			}
		}
		if value, ok := input(task, prefix+"time"); ok && e.Timer != nil {
			params.Timer = value
		}
		if e.Type == bpmn.StartEvent {
			for _, field := range e.FormFields {
				if value, ok := input(task, deployPrefix+"start_parameter.default."+field.Id); ok {
					if params.Defaults == nil {
						params.Defaults = map[string]string{}
					}
					params.Defaults[field.Id] = value
				}
			}
		}
		if len(params.Inputs) > 0 || len(params.Variables) > 0 || params.Timer != "" || len(params.Defaults) > 0 {
			deployment.Parameters[e.Id] = params
		}
	}
	return nil
}

// selectionOf converts the iot option of smart services into a device selection
func selectionOf(raw string) (result model.Selection, err error) {
	var option struct {
		DeviceSelection *struct {
			DeviceId         string  `json:"device_id"`
			ServiceId        *string `json:"service_id"`
			Path             *string `json:"path"`
			CharacteristicId *string `json:"characteristic_id"`
		} `json:"device_selection"`
		DeviceGroupSelection *struct {
			Id string `json:"id"`
		} `json:"device_group_selection"`
		ImportSelection *struct {
			Id               string  `json:"id"`
			Path             *string `json:"path"`
			CharacteristicId *string `json:"characteristic_id"`
		} `json:"import_selection"`
		GenericEventSource json.RawMessage `json:"generic_event_source"`
	}
	if err = json.Unmarshal([]byte(raw), &option); err != nil {
		return result, err
	}
	path := func(p *string, characteristic *string) *model.SelectedPath {
		if p == nil {
			return nil
		}
		result := &model.SelectedPath{Path: *p}
		if characteristic != nil {
			result.CharacteristicId = *characteristic
		}
		return result
	}
	switch {
	case option.DeviceSelection != nil:
		result.DeviceId = option.DeviceSelection.DeviceId
		if option.DeviceSelection.ServiceId != nil {
			result.ServiceId = *option.DeviceSelection.ServiceId
		}
		result.Path = path(option.DeviceSelection.Path, option.DeviceSelection.CharacteristicId)
	case option.DeviceGroupSelection != nil:
		result.DeviceGroupId = option.DeviceGroupSelection.Id
	case option.ImportSelection != nil:
		result.ImportId = option.ImportSelection.Id
		result.Path = path(option.ImportSelection.Path, option.ImportSelection.CharacteristicId)
	case len(option.GenericEventSource) > 0 && string(option.GenericEventSource) != "null":
		return result, fmt.Errorf("generic event sources are not supported")
	default:
		return result, fmt.Errorf("no device, device group or import selected")
	}
	return result, nil
}

type startHandler struct {
	*processModules
}

const startPrefix = TypeProcessDeploymentStart + "."

// Do starts an instance of a deployment, normally one the instance deployed before (TASK-21).
func (this *startHandler) Do(ctx context.Context, task Task) (modules []model.Module, outputs map[string]any, err error) {
	deploymentId, _ := input(task, startPrefix+"process_deployment_id")
	if deploymentId == "" {
		return nil, nil, fmt.Errorf("missing process_deployment_id")
	}
	namespace := ""
	err = this.runtime.store.Tx(ctx, func(tx *store.Tx) error {
		deployed, err := tx.ModulesOfInstance(task.RootInstanceId, "", TypeProcessDeployment)
		for _, m := range deployed {
			if m.Data["process_deployment_id"] == deploymentId && m.DeleteInfo != nil && m.DeleteInfo.Resource != nil {
				namespace = m.DeleteInfo.Resource.Namespace
			}
		}
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	p, err := this.begin(ctx, task, namespace)
	if err != nil {
		return nil, nil, err
	}
	variables := map[string]any{}
	for name, value := range task.Inputs {
		if variable, ok := strings.CutPrefix(name, startPrefix+"input."); ok {
			if s, ok := value.(string); ok {
				var parsed any
				if json.Unmarshal([]byte(s), &parsed) == nil {
					value = parsed
				}
			}
			variables[variable] = value
		}
	}
	inst, err := this.processes.StartInstance(ctx, p.token, p.namespace, model.StartInstanceInput{DeploymentId: deploymentId, Variables: variables})
	if err != nil {
		return nil, nil, err
	}
	// the instance id serves as business key of the former engine wrapper
	data := map[string]any{"process_instance_id": inst.Id, "business_key": inst.Id}
	modules = []model.Module{{Id: task.ModuleId(), ModuleType: TypeProcessDeploymentStart, Data: data, Keys: []string{},
		DeleteInfo: &model.ModuleDeleteInfo{UserId: p.owner, Resource: &model.ResourceRef{Kind: model.ResourceInstance, Namespace: p.namespace, Id: inst.Id}}}}
	return modules, map[string]any{"process_instance_id": inst.Id, "business_key": inst.Id}, nil
}

// checkDeployment reports a deployment that no longer exists
func (this *processModules) checkDeployment(ctx context.Context, m model.Module) (string, error) {
	return this.checkResource(ctx, m, func(tx *store.Tx, ref model.ResourceRef) (string, error) {
		_, err := tx.GetDeployment(ref.Id, false)
		if errors.Is(err, model.ErrNotFound) {
			return fmt.Sprintf("deployment %v not found", ref.Id), nil
		}
		return "", err
	})
}

// checkInstance reports a started instance that ended other than completed
func (this *processModules) checkInstance(ctx context.Context, m model.Module) (string, error) {
	return this.checkResource(ctx, m, func(tx *store.Tx, ref model.ResourceRef) (string, error) {
		inst, err := tx.GetInstance(ref.Id, false)
		if errors.Is(err, model.ErrNotFound) {
			return fmt.Sprintf("instance %v not found", ref.Id), nil
		}
		if err != nil {
			return "", err
		}
		if inst.State == model.StateIncident || inst.State == model.StateTerminated {
			return fmt.Sprintf("instance %v is %v: %v", ref.Id, inst.State, inst.EndReason), nil
		}
		return "", nil
	})
}

func (this *processModules) checkResource(ctx context.Context, m model.Module, check func(tx *store.Tx, ref model.ResourceRef) (string, error)) (problem string, err error) {
	if m.DeleteInfo == nil || m.DeleteInfo.Resource == nil {
		return "module names no resource", nil
	}
	err = this.runtime.store.Tx(ctx, func(tx *store.Tx) error {
		problem, err = check(tx, *m.DeleteInfo.Resource)
		return err
	})
	return problem, err
}
