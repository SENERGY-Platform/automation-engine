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
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
	"github.com/SENERGY-Platform/models/go/models"
)

// The tests here are the test cases of smart-service-module-worker-process
// (pkg/tests/resources/test-cases): how the task inputs become the deployment. The worker filled
// a prepared deployment of process-deployment; the engine builds the deployment input itself.

const processTaskId = "Task_03f9hy3"

// the elements of the prepared deployments of the worker test cases
var processModelXml = b.Process(
	b.StartWithForm("StartEvent_1", "foo=1", "bar=2"),
	b.Task(processTaskId, b.InText("inputs.b", "0"), b.InText("inputs.g", "0"), b.InText("inputs.r", "0")),
	b.End("end"),
	b.Flow("StartEvent_1", processTaskId), b.Flow(processTaskId, "end"),
)

// fakeProcesses records the deployments the module creates
type fakeProcesses struct {
	created  []model.DeploymentInput
	networks []model.Network
}

func (this *fakeProcesses) GetModel(_ context.Context, _ string, _ string, id string) (model.Model, error) {
	return model.Model{Id: id, Name: "model name", Version: 1}, nil
}

func (this *fakeProcesses) GetModelVersion(_ context.Context, _ string, _ string, id string, version int) (model.ModelVersion, error) {
	return model.ModelVersion{ModelId: id, Version: version, Xml: processModelXml}, nil
}

func (this *fakeProcesses) CreateDeployment(_ context.Context, _ string, _ string, input model.DeploymentInput) (model.Deployment, error) {
	this.created = append(this.created, input)
	return model.Deployment{Id: "new-deployment-id", Name: input.Name, NetworkId: input.NetworkId}, nil
}

func (this *fakeProcesses) PutDeployment(_ context.Context, _ string, _ string, _ string, _ model.DeploymentInput) (model.Deployment, error) {
	return model.Deployment{}, errors.New("unexpected update")
}

func (this *fakeProcesses) StartInstance(context.Context, string, string, model.StartInstanceInput) (model.Instance, error) {
	return model.Instance{}, errors.New("unexpected start")
}

func (this *fakeProcesses) ListNetworks(context.Context, string) ([]model.Network, error) {
	return this.networks, nil
}

func (this *fakeProcesses) RemoveResource(context.Context, model.ResourceRef) error {
	return errors.New("unexpected removal")
}

type referencingProcesses struct{}

func (referencingProcesses) Get(name string) (model.Namespace, bool) {
	return model.Namespace{Name: name, ReferencedNamespaces: []string{"processes"}}, true
}

// processDeploymentInputs are the inputs every worker test case has, with the given ones
func processDeploymentInputs(inputs map[string]any) map[string]any {
	result := map[string]any{
		"foo":                                 "bar",
		"process_deployment.module_data":      `{"additional-info": 42}`,
		"process_deployment.name":             "test-deployment-name-updated",
		"process_deployment.process_model_id": "test-model-id",
	}
	for name, value := range inputs {
		result[name] = value
	}
	return result
}

type processDeploymentResult struct {
	processes *fakeProcesses
	modules   []model.Module
	outputs   map[string]any
	err       error
}

// deployProcess runs a process_deployment task in an instance of alice; repository answers the
// device-repository paths, networks are the gateways of alice
func deployProcess(t *testing.T, inputs map[string]any, networks []model.Network, repository map[string]any) processDeploymentResult {
	t.Helper()
	s := storetest.OpenSQLite(t)
	instance := instanceWith(t, s, "smart-services")
	deviceRepository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if answer, ok := repository[r.URL.Path]; ok {
			_ = json.NewEncoder(w).Encode(answer)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(deviceRepository.Close)
	rt := NewRuntime(s, Settings{DeviceRepositoryUrl: deviceRepository.URL}, func(user string) (string, error) { return "Bearer token-of-" + user, nil }, logger)
	processes := &fakeProcesses{networks: networks}
	rt.AddProcessModules(processes, referencingProcesses{})
	modules, outputs, err := rt.types[TypeProcessDeployment].handler.Do(context.Background(), Task{Id: "task1", InstanceId: instance, RootInstanceId: instance,
		Namespace: "smart-services", Owner: "alice", Inputs: processDeploymentInputs(inputs)})
	return processDeploymentResult{processes: processes, modules: modules, outputs: outputs, err: err}
}

// deployed is the one deployment input of a successful task
func (this processDeploymentResult) deployed(t *testing.T) model.DeploymentInput {
	t.Helper()
	if this.err != nil {
		t.Fatal(this.err)
	}
	if len(this.processes.created) != 1 {
		t.Fatalf("one deployment: %+v", this.processes.created)
	}
	return this.processes.created[0]
}

func TestProcessDeploymentSelectsDeviceServiceAndPathOfATask(t *testing.T) {
	deployed := deployProcess(t, map[string]any{
		"process_deployment." + processTaskId + ".selection": `{"device_selection":{"device_id":"device_1","service_id":"s1","characteristic_id":"test-characteristic","path":"root.value_s1.v1"}}`,
	}, nil, nil).deployed(t)
	expected := map[string]model.Selection{processTaskId: {DeviceId: "device_1", ServiceId: "s1", Path: &model.SelectedPath{Path: "root.value_s1.v1", CharacteristicId: "test-characteristic"}}}
	if !reflect.DeepEqual(deployed.Selections, expected) {
		t.Errorf("%#v", deployed.Selections)
	}
}

// Inputs of the task override those of the model; values that are no strings are given as JSON.
func TestProcessDeploymentOverridesTheInputsOfATask(t *testing.T) {
	deployed := deployProcess(t, map[string]any{
		"process_deployment." + processTaskId + ".parameter.inputs.b": "100",
		"process_deployment." + processTaskId + ".parameter.inputs.r": 255,
	}, nil, nil).deployed(t)
	expected := map[string]string{"inputs.b": "100", "inputs.r": "255"}
	if !reflect.DeepEqual(deployed.Parameters[processTaskId].Inputs, expected) {
		t.Errorf("%#v", deployed.Parameters)
	}
}

// The selections of the message and conditional event cases of the worker.
func TestProcessDeploymentSelectsTheSourceOfAnEvent(t *testing.T) {
	for name, test := range map[string]struct {
		selection string
		expected  model.Selection
	}{
		"device":                 {`{"device_selection":{"device_id":"device_1","service_id":"s1","characteristic_id":"test-characteristic","path":"root.value_s1.v1"}}`, model.Selection{DeviceId: "device_1", ServiceId: "s1", Path: &model.SelectedPath{Path: "root.value_s1.v1", CharacteristicId: "test-characteristic"}}},
		"device without path":    {`{"device_selection":{"device_id":"device_1","service_id":"s1"}}`, model.Selection{DeviceId: "device_1", ServiceId: "s1"}},
		"device without service": {`{"device_selection":{"device_id":"device_1"}}`, model.Selection{DeviceId: "device_1"}},
		"group":                  {`{"device_group_selection":{"id":"group_1"}}`, model.Selection{DeviceGroupId: "group_1"}},
		"import":                 {`{"import_selection":{"id":"import_2","characteristic_id":"test-characteristic","path":"root.value"}}`, model.Selection{ImportId: "import_2", Path: &model.SelectedPath{Path: "root.value", CharacteristicId: "test-characteristic"}}},
		"import without path":    {`{"import_selection":{"id":"import_2"}}`, model.Selection{ImportId: "import_2"}},
	} {
		t.Run(name, func(t *testing.T) {
			deployed := deployProcess(t, map[string]any{"process_deployment.StartEvent_1.selection": test.selection}, nil, nil).deployed(t)
			if !reflect.DeepEqual(deployed.Selections, map[string]model.Selection{"StartEvent_1": test.expected}) {
				t.Errorf("%#v", deployed.Selections)
			}
		})
	}
}

func TestProcessDeploymentSetsTheVariablesOfAnEvent(t *testing.T) {
	deployed := deployProcess(t, map[string]any{
		"process_deployment.StartEvent_1.selection":     `{"device_selection":{"device_id":"device_1","service_id":"s1","characteristic_id":"test-characteristic","path":"root.value_s1.v1"}}`,
		"process_deployment.StartEvent_1.variables.foo": "13",
	}, nil, nil).deployed(t)
	if !reflect.DeepEqual(deployed.Parameters["StartEvent_1"].Variables, map[string]string{"foo": "13"}) {
		t.Errorf("%#v", deployed.Parameters)
	}
}

func TestProcessDeploymentSetsTheDefaultsOfStartParameters(t *testing.T) {
	deployed := deployProcess(t, map[string]any{
		"process_deployment.start_parameter.default.bar": "43",
		"process_deployment.start_parameter.default.foo": "14",
	}, nil, nil).deployed(t)
	if !reflect.DeepEqual(deployed.Parameters["StartEvent_1"].Defaults, map[string]string{"bar": "43", "foo": "14"}) {
		t.Errorf("%#v", deployed.Parameters)
	}
}

func TestProcessDeploymentTakesTheIncidentHandlingOfTheTask(t *testing.T) {
	deployed := deployProcess(t, map[string]any{
		"process_deployment.on_incident.notify":  "false",
		"process_deployment.on_incident.restart": "true",
	}, nil, nil).deployed(t)
	if deployed.IncidentHandling == nil || *deployed.IncidentHandling != (model.IncidentHandling{Restart: true, Notify: false}) {
		t.Errorf("%#v", deployed.IncidentHandling)
	}
}

func TestProcessDeploymentIsNamedByTheTask(t *testing.T) {
	deployed := deployProcess(t, nil, nil, nil).deployed(t)
	if deployed.Name != "test-deployment-name-updated" {
		t.Errorf("%v", deployed.Name)
	}
}

func TestProcessDeploymentWithoutModelIdFailsWithoutDeploying(t *testing.T) {
	result := deployProcess(t, map[string]any{"process_deployment.process_model_id": ""}, nil, nil)
	if result.err == nil || len(result.processes.created) != 0 {
		t.Errorf("%v %+v", result.err, result.processes.created)
	}
}

// The module keeps the module data of the task next to the id and name of the deployment.
func TestProcessDeploymentModuleKeepsTheModuleDataOfTheTask(t *testing.T) {
	result := deployProcess(t, nil, nil, nil)
	if result.err != nil {
		t.Fatal(result.err)
	}
	expected := map[string]any{"additional-info": float64(42), "process_deployment_id": "new-deployment-id", "process_deployment_name": "test-deployment-name-updated"}
	if len(result.modules) != 1 || result.modules[0].Id != "i-smart-services.task1" || result.modules[0].ModuleType != TypeProcessDeployment ||
		!reflect.DeepEqual(result.modules[0].Data, expected) {
		t.Errorf("%+v", result.modules)
	}
}

func TestProcessDeploymentOutputsTheDeploymentAndItsDoneEvent(t *testing.T) {
	result := deployProcess(t, nil, nil, nil)
	if result.err != nil {
		t.Fatal(result.err)
	}
	expected := map[string]any{"process_deployment_id": "new-deployment-id", "done_event": "deployment_done_new-deployment-id", "is_fog_deployment": false, "fog_hub": ""}
	if !reflect.DeepEqual(result.outputs, expected) {
		t.Errorf("%#v", result.outputs)
	}
}

// With prefer_fog_deployment, the deployment goes to the first gateway that carries every
// selected device, also the devices of a group and a device selected with a service group.
func TestPreferredFogDeploymentGoesToTheGatewayOfTheSelectedDevices(t *testing.T) {
	const device = "urn:infai:ses:device:79fa231c-45d9-4266-b5fb-2c051bfd8c0d"
	networks := []model.Network{{Id: "wrong1", Compatible: true}, {Id: "nid", Compatible: true}, {Id: "wrong2", Compatible: true}}
	repository := map[string]any{
		"/hubs/wrong1":           models.Hub{Id: "wrong1", DeviceIds: []string{"device_unrelated_1"}},
		"/hubs/nid":              models.Hub{Id: "nid", DeviceIds: []string{"device_1", "device_unrelated_2", device}},
		"/hubs/wrong2":           models.Hub{Id: "wrong2", DeviceIds: []string{"device_unrelated_3"}},
		"/device-groups/group_1": models.DeviceGroup{Id: "group_1", DeviceIds: []string{"device_1"}},
	}
	for name, test := range map[string]struct{ element, selection string }{
		"device of a task":            {processTaskId, `{"device_selection":{"device_id":"device_1","service_id":"s1","characteristic_id":"test-characteristic","path":"root.value_s1.v1"}}`},
		"group of a task":             {processTaskId, `{"device_group_selection":{"id":"group_1"}}`},
		"device of an event":          {"StartEvent_1", `{"device_selection":{"device_id":"device_1","service_id":"s1","characteristic_id":"test-characteristic","path":"root.value_s1.v1"}}`},
		"group of an event":           {"StartEvent_1", `{"device_group_selection":{"id":"group_1"}}`},
		"device with a service group": {processTaskId, `{"device_selection":{"device_id":"` + device + `$service_group_selection=sg1"}}`},
	} {
		t.Run(name, func(t *testing.T) {
			result := deployProcess(t, map[string]any{
				"process_deployment.prefer_fog_deployment":          "true",
				"process_deployment." + test.element + ".selection": test.selection,
			}, networks, repository)
			deployed := result.deployed(t)
			if deployed.NetworkId != "nid" || result.outputs["is_fog_deployment"] != true || result.outputs["fog_hub"] != "nid" {
				t.Errorf("%v %#v", deployed.NetworkId, result.outputs)
			}
		})
	}
}

// Two tasks deploy twice, each with a module of its own.
func TestEveryProcessDeploymentTaskCreatesItsOwnDeploymentAndModule(t *testing.T) {
	s := storetest.OpenSQLite(t)
	instance := instanceWith(t, s, "smart-services")
	rt := NewRuntime(s, Settings{}, func(user string) (string, error) { return "Bearer token-of-" + user, nil }, logger)
	processes := &fakeProcesses{}
	rt.AddProcessModules(processes, referencingProcesses{})
	ids := []string{}
	for _, task := range []string{"task1", "task2"} {
		modules, _, err := rt.types[TypeProcessDeployment].handler.Do(context.Background(), Task{Id: task, InstanceId: instance, RootInstanceId: instance,
			Namespace: "smart-services", Owner: "alice", Inputs: processDeploymentInputs(map[string]any{
				"process_deployment." + processTaskId + ".selection": `{"device_group_selection":{"id":"group_1"}}`,
			})})
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range modules {
			ids = append(ids, m.Id)
		}
	}
	if len(processes.created) != 2 || !reflect.DeepEqual(ids, []string{"i-smart-services.task1", "i-smart-services.task2"}) {
		t.Errorf("%v %+v", ids, processes.created)
	}
}

// withModel lets the fake repository answer with xml as the model to deploy
func withModel(t *testing.T, xml string) {
	previous := processModelXml
	processModelXml = xml
	t.Cleanup(func() { processModelXml = previous })
}

// A design passes the JUEL expression of the model, e.g. "${timer}", for the process to evaluate
// at runtime, as Camunda did. The model keeps its expression, which the engine converts at
// deploy; as a parameter it would be a date "${timer}" and fail validation.
func TestProcessDeploymentKeepsTheExpressionOfTheModel(t *testing.T) {
	withModel(t, b.Process(
		b.StartWithForm("StartEvent_1"),
		b.Task(processTaskId, b.InText("inputs", "${available_current}")),
		b.CatchTimer("timer", "timeDate", "${timer}"),
		b.End("end"),
		b.Flow("StartEvent_1", processTaskId), b.Flow(processTaskId, "timer"), b.Flow("timer", "end"),
	))
	deployed := deployProcess(t, map[string]any{
		"process_deployment." + processTaskId + ".parameter.inputs": "${available_current}",
		"process_deployment.timer.time":                             "${timer}",
	}, nil, nil).deployed(t)
	if len(deployed.Parameters) != 0 {
		t.Errorf("the expressions of the model stay: %#v", deployed.Parameters)
	}
}

func TestProcessDeploymentTakesAValueForATimerWithExpression(t *testing.T) {
	withModel(t, b.Process(
		b.StartWithForm("StartEvent_1"), b.CatchTimer("timer", "timeDuration", "${delay}"), b.End("end"),
		b.Flow("StartEvent_1", "timer"), b.Flow("timer", "end"),
	))
	deployed := deployProcess(t, map[string]any{"process_deployment.timer.time": "PT5M"}, nil, nil).deployed(t)
	if deployed.Parameters["timer"].Timer != "PT5M" {
		t.Errorf("%#v", deployed.Parameters)
	}
}

// Another expression than the model's cannot be passed as a value; taken as text, it would fail
// at deploy or, for an input, reach the device as "${...}".
func TestProcessDeploymentRejectsAnExpressionOtherThanTheModels(t *testing.T) {
	withModel(t, b.Process(
		b.StartWithForm("StartEvent_1"), b.CatchTimer("timer", "timeDate", "${timer}"), b.End("end"),
		b.Flow("StartEvent_1", "timer"), b.Flow("timer", "end"),
	))
	result := deployProcess(t, map[string]any{"process_deployment.timer.time": "${other}"}, nil, nil)
	if result.err == nil || len(result.processes.created) != 0 {
		t.Fatalf("expected an error without deployment: %v %+v", result.err, result.processes.created)
	}
}
