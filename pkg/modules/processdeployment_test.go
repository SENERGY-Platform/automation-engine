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
	"strings"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
	devicerepository "github.com/SENERGY-Platform/device-repository/v3/lib/client"
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

// fakeProcesses records the deployments the module creates, puts and removes
type fakeProcesses struct {
	created  []model.DeploymentInput
	put      map[string]model.DeploymentInput
	removed  []model.ResourceRef
	networks []model.Network
	// "put <id>" and "remove <id>" in the order of the calls, failed ones included
	calls []string
	// the answers to PutDeployment and RemoveResource
	putErr    error
	removeErr error
	// set, a removal removes the rows of the engine, as storeResources does
	store *store.Store
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

func (this *fakeProcesses) PutDeployment(_ context.Context, _ string, _ string, id string, input model.DeploymentInput) (model.Deployment, error) {
	this.calls = append(this.calls, "put "+id)
	if this.putErr != nil {
		return model.Deployment{}, this.putErr
	}
	if this.put == nil {
		this.put = map[string]model.DeploymentInput{}
	}
	this.put[id] = input
	return model.Deployment{Id: id, Name: input.Name, NetworkId: input.NetworkId}, nil
}

func (this *fakeProcesses) StartInstance(context.Context, string, string, model.StartInstanceInput) (model.Instance, error) {
	return model.Instance{}, errors.New("unexpected start")
}

func (this *fakeProcesses) ListNetworks(context.Context, string) ([]model.Network, error) {
	return this.networks, nil
}

func (this *fakeProcesses) RemoveResource(ctx context.Context, ref model.ResourceRef) error {
	this.calls = append(this.calls, "remove "+ref.Id)
	if this.removeErr != nil {
		return this.removeErr
	}
	this.removed = append(this.removed, ref)
	if this.store != nil {
		return storeResources{this.store}.RemoveResource(ctx, ref)
	}
	return nil
}

type referencingProcesses struct{}

func (referencingProcesses) Get(name string) (model.Namespace, bool) {
	return model.Namespace{Name: name, ReferencedNamespaces: []string{"processes"}}, true
}

// cloudOnlyProcesses runs the namespace processes only in the cloud (NS-10)
type cloudOnlyProcesses struct{}

func (cloudOnlyProcesses) Get(name string) (model.Namespace, bool) {
	ns, ok := referencingProcesses{}.Get(name)
	ns.CloudOnly = name == "processes"
	return ns, ok
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
	return runProcessDeployment(t, &fakeProcesses{networks: networks}, nil, nil, inputs, repository)
}

// runProcessDeployment runs the task in an instance of alice with the given modules, after storing
// the given deployments
func runProcessDeployment(t *testing.T, processes *fakeProcesses, modules []model.Module, deployments []model.Deployment, inputs map[string]any, repository map[string]any) processDeploymentResult {
	t.Helper()
	run, _ := processDeploymentRunner(t, processes, referencingProcesses{}, modules, deployments, repository)
	return run(inputs)
}

// processDeploymentRunner prepares an instance of alice with the given modules and deployments
// and gives a function that runs a process_deployment task in it, as often as a test needs
func processDeploymentRunner(t *testing.T, processes *fakeProcesses, namespaces Namespaces, modules []model.Module, deployments []model.Deployment, repository map[string]any) (func(inputs map[string]any) processDeploymentResult, *store.Store) {
	t.Helper()
	s := storetest.OpenSQLite(t)
	instance := instanceWith(t, s, "smart-services", modules...)
	err := s.Tx(context.Background(), func(tx *store.Tx) error {
		for _, d := range deployments {
			if err := tx.InsertDeployment(d); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	deviceRepository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if answer, ok := repository[r.URL.Path]; ok {
			_ = json.NewEncoder(w).Encode(answer)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(deviceRepository.Close)
	rt := NewRuntime(s, Settings{DeviceRepository: devicerepository.NewClient(deviceRepository.URL, devicerepository.Options{})}, func(user string) (string, error) { return "Bearer token-of-" + user, nil }, logger)
	processes.store = s
	rt.AddProcessModules(processes, namespaces)
	return func(inputs map[string]any) processDeploymentResult {
		modules, outputs, err := rt.types[TypeProcessDeployment].handler.Do(context.Background(), Task{Id: "task1", InstanceId: instance, RootInstanceId: instance,
			Namespace: "smart-services", Owner: "alice", Inputs: processDeploymentInputs(inputs)})
		return processDeploymentResult{processes: processes, modules: modules, outputs: outputs, err: err}
	}, s
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
		// the output of an analytics operator, as the designer writes it and as the iot option of
		// smart-service-module-worker-process named it (SNRGY-5113)
		"analytics operator":   {`{"pipeline_id":"p1","operator_id":"o1"}`, model.Selection{PipelineId: "p1", OperatorId: "o1"}},
		"generic event source": {`{"generic_event_source":{"filter_type":"operatorId","filter_ids":"p1:o1","topic":"analytics-stopping"}}`, model.Selection{PipelineId: "p1", OperatorId: "o1"}},
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

// Devices a process finds at runtime count for the gateway like selected ones (SNRGY-5060): the
// first gateway carries the selected device only, so without them the process would go there.
func TestPreferredFogDeploymentConsidersTheDevicesOfTheRuntime(t *testing.T) {
	const target = "urn:infai:ses:device:1f2a9a4e-0b0c-4f43-9d4a-8c4e5b1f3c11"
	networks := []model.Network{{Id: "trigger-only", Compatible: true}, {Id: "nid", Compatible: true}}
	repository := map[string]any{
		"/hubs/trigger-only": models.Hub{Id: "trigger-only", DeviceIds: []string{"device_1"}},
		"/hubs/nid":          models.Hub{Id: "nid", DeviceIds: []string{"device_1", "device_unrelated", target}},
	}
	selection := `{"device_selection":{"device_id":"device_1","service_id":"s1"}}`
	for name, test := range map[string]struct {
		devices any
		network string
	}{
		"without devices":             {nil, "trigger-only"},
		"empty devices":               {"", "trigger-only"},
		"device of the same hub":      {`["` + target + `"]`, "nid"},
		"device with a service group": {`["` + target + `$service_group_selection=sg1"]`, "nid"},
		"devices as list value":       {[]any{target, "device_1"}, "nid"},
		"device on no common hub":     {`["` + target + `","device_elsewhere"]`, ""},
	} {
		t.Run(name, func(t *testing.T) {
			inputs := map[string]any{
				"process_deployment.prefer_fog_deployment":           "true",
				"process_deployment." + processTaskId + ".selection": selection,
			}
			if test.devices != nil {
				inputs["process_deployment.devices"] = test.devices
			}
			result := deployProcess(t, inputs, networks, repository)
			deployed := result.deployed(t)
			if deployed.NetworkId != test.network || result.outputs["fog_hub"] != test.network || result.outputs["is_fog_deployment"] != (test.network != "") {
				t.Errorf("%v %#v", deployed.NetworkId, result.outputs)
			}
		})
	}
}

// Ignored, invalid devices would put the process on the gateway of the selected devices.
func TestProcessDeploymentRejectsInvalidDevices(t *testing.T) {
	for _, devices := range []any{`device_1`, `{"id":"device_1"}`, `[1]`, map[string]any{"id": "device_1"}} {
		result := deployProcess(t, map[string]any{
			"process_deployment.prefer_fog_deployment": "true",
			"process_deployment.devices":               devices,
		}, nil, nil)
		if result.err == nil || !strings.Contains(result.err.Error(), "process_deployment.devices") || len(result.processes.created) != 0 {
			t.Errorf("%#v: %v %+v", devices, result.err, result.processes.created)
		}
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

const (
	deploymentKey       = "heating"
	earlierDeploymentId = "earlier-deployment-id"
	earlierModuleId     = "i-smart-services.earlier-task"
)

// earlierDeploymentRunner prepares an instance whose module of deploymentKey names
// earlierDeploymentId, stored on networkId unless the engine no longer has it, and gives a
// function that runs a process_deployment task with that key
func earlierDeploymentRunner(t *testing.T, processes *fakeProcesses, namespaces Namespaces, networkId string, stored bool, repository map[string]any) (func(inputs map[string]any) processDeploymentResult, *store.Store) {
	t.Helper()
	return earlierDeploymentRunnerWith(t, processes, namespaces, model.Deployment{NetworkId: networkId}, stored, repository)
}

// earlierDeploymentRunnerWith is earlierDeploymentRunner with the fields of earlier, e.g. Disabled
func earlierDeploymentRunnerWith(t *testing.T, processes *fakeProcesses, namespaces Namespaces, earlierDeployment model.Deployment, stored bool, repository map[string]any) (func(inputs map[string]any) processDeploymentResult, *store.Store) {
	t.Helper()
	earlier := model.Module{Id: earlierModuleId, ModuleType: TypeProcessDeployment, Keys: []string{deploymentKey},
		Data:       map[string]any{"process_deployment_id": earlierDeploymentId, "process_deployment_name": "earlier"},
		DeleteInfo: &model.ModuleDeleteInfo{UserId: "alice", Resource: &model.ResourceRef{Kind: model.ResourceDeployment, Namespace: "processes", Id: earlierDeploymentId}}}
	var deployments []model.Deployment
	if stored {
		now := time.Now()
		d := earlierDeployment
		d.Id, d.Name, d.Owner, d.Version, d.ManagedBy, d.CreatedAt, d.UpdatedAt = earlierDeploymentId, "earlier", "alice", 1, "i-smart-services", now, now
		if d.Namespace == "" {
			d.Namespace = "processes"
		}
		deployments = append(deployments, d)
	}
	run, s := processDeploymentRunner(t, processes, namespaces, []model.Module{earlier}, deployments, repository)
	return func(inputs map[string]any) processDeploymentResult {
		return run(withInputs(inputs, map[string]any{"process_deployment.key": deploymentKey}))
	}, s
}

func storedDeployment(t *testing.T, s *store.Store, id string) bool {
	t.Helper()
	err := s.Tx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.GetDeployment(id, false)
		return err
	})
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		t.Fatal(err)
	}
	return err == nil
}

// the gateway nid carries device_1
var (
	gatewayOfDevice1    = []model.Network{{Id: "nid", Compatible: true}}
	repositoryOfDevice1 = map[string]any{"/hubs/nid": models.Hub{Id: "nid", DeviceIds: []string{"device_1"}}}
	fogOnDevice1        = map[string]any{
		"process_deployment.prefer_fog_deployment":           "true",
		"process_deployment." + processTaskId + ".selection": `{"device_selection":{"device_id":"device_1","service_id":"s1"}}`,
	}
	cloudOnDevice1 = withInputs(fogOnDevice1, map[string]any{"process_deployment.prefer_fog_deployment": "false"})
)

func withInputs(inputs map[string]any, more map[string]any) map[string]any {
	result := map[string]any{}
	for name, value := range inputs {
		result[name] = value
	}
	for name, value := range more {
		result[name] = value
	}
	return result
}

// The id of a replacement is the same on a retry and new for every network and every earlier id,
// so that a retry does not leave a second deployment and a deleted id is never put again.
func TestReplacementIdIsStableAndNeverAnEarlierOne(t *testing.T) {
	toGateway := replacementId(earlierDeploymentId, "nid")
	if toGateway != replacementId(earlierDeploymentId, "nid") {
		t.Error("not stable")
	}
	ids := []string{earlierDeploymentId, toGateway, replacementId(earlierDeploymentId, ""), replacementId(earlierDeploymentId, "other"),
		replacementId(toGateway, ""), replacementId(replacementId(toGateway, ""), "nid")}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Errorf("%v repeats: %v", id, ids)
		}
		seen[id] = true
	}
}

// A deployment cannot change its network (SNRGY-5137): moved between the cloud and a gateway, it
// is replaced by one under a derived id, put before the earlier one is removed. One the engine no
// longer has is put under the derived id as well, never under its old id. Either way the module of
// the key names the replacement.
func TestProcessDeploymentReplacesWhatItCannotUpdate(t *testing.T) {
	for name, test := range map[string]struct {
		earlierNetwork string
		stored         bool
		inputs         map[string]any
		target         string
	}{
		"from the cloud to a gateway":           {"", true, fogOnDevice1, "nid"},
		"from another gateway to a gateway":     {"other", true, fogOnDevice1, "nid"},
		"to the cloud, a device elsewhere":      {"nid", true, withInputs(fogOnDevice1, map[string]any{"process_deployment.devices": `["device_elsewhere"]`}), ""},
		"to the cloud, preference switched off": {"nid", true, cloudOnDevice1, ""},
		"missing, now for a gateway":            {"", false, fogOnDevice1, "nid"},
		"missing, now for the cloud":            {"", false, cloudOnDevice1, ""},
	} {
		t.Run(name, func(t *testing.T) {
			processes := &fakeProcesses{networks: gatewayOfDevice1}
			run, s := earlierDeploymentRunner(t, processes, referencingProcesses{}, test.earlierNetwork, test.stored, repositoryOfDevice1)
			result := run(withInputs(test.inputs, map[string]any{"process_deployment.update_strategy": "drain"}))
			if result.err != nil {
				t.Fatal(result.err)
			}
			replacement := replacementId(earlierDeploymentId, test.target)
			expectedCalls := []string{"put " + replacement}
			if test.stored {
				expectedCalls = append(expectedCalls, "remove "+earlierDeploymentId)
			}
			if !reflect.DeepEqual(processes.calls, expectedCalls) || len(processes.created) != 0 {
				t.Errorf("calls %v, created %+v", processes.calls, processes.created)
			}
			if test.stored && !reflect.DeepEqual(processes.removed, []model.ResourceRef{{Kind: model.ResourceDeployment, Namespace: "processes", Id: earlierDeploymentId}}) {
				t.Errorf("removed %+v", processes.removed)
			}
			if put := processes.put[replacement]; put.NetworkId != test.target || put.UpdateStrategy != model.UpdateDrain {
				t.Errorf("put %+v", processes.put)
			}
			if storedDeployment(t, s, earlierDeploymentId) {
				t.Error("the earlier deployment stays")
			}
			if len(result.modules) != 1 {
				t.Fatalf("%+v", result.modules)
			}
			m := result.modules[0]
			if m.Id != earlierModuleId || m.Data["process_deployment_id"] != replacement || !reflect.DeepEqual(m.Keys, []string{deploymentKey}) ||
				m.DeleteInfo == nil || m.DeleteInfo.Resource == nil || *m.DeleteInfo.Resource != (model.ResourceRef{Kind: model.ResourceDeployment, Namespace: "processes", Id: replacement}) {
				t.Errorf("%+v %+v", m, m.DeleteInfo)
			}
			expected := map[string]any{"process_deployment_id": replacement, "done_event": "deployment_done_" + replacement, "is_fog_deployment": test.target != "", "fog_hub": test.target}
			if !reflect.DeepEqual(result.outputs, expected) {
				t.Errorf("%#v", result.outputs)
			}
		})
	}
}

// On the same network the deployment of the key is put with the update strategy as before; so it
// is in a namespace that runs only in the cloud, where the controller refuses to update a gateway
// deployment instead of the module moving it (NS-10).
func TestProcessDeploymentUpdatesTheDeploymentOfTheKeyInPlace(t *testing.T) {
	for name, test := range map[string]struct {
		namespaces     Namespaces
		earlierNetwork string
		inputs         map[string]any
		target         string
	}{
		"on the same gateway":                    {referencingProcesses{}, "nid", fogOnDevice1, "nid"},
		"in the cloud":                           {referencingProcesses{}, "", cloudOnDevice1, ""},
		"on a gateway of a cloud-only namespace": {cloudOnlyProcesses{}, "nid", fogOnDevice1, ""},
	} {
		t.Run(name, func(t *testing.T) {
			processes := &fakeProcesses{networks: gatewayOfDevice1}
			run, _ := earlierDeploymentRunner(t, processes, test.namespaces, test.earlierNetwork, true, repositoryOfDevice1)
			result := run(test.inputs)
			if result.err != nil {
				t.Fatal(result.err)
			}
			put := processes.put[earlierDeploymentId]
			if !reflect.DeepEqual(processes.calls, []string{"put " + earlierDeploymentId}) || put.NetworkId != test.target || put.UpdateStrategy != model.UpdateRestart {
				t.Errorf("calls %v, put %+v", processes.calls, processes.put)
			}
			if len(processes.created) != 0 || len(result.modules) != 1 || result.modules[0].Id != earlierModuleId || result.outputs["process_deployment_id"] != earlierDeploymentId {
				t.Errorf("created %+v, %+v %#v", processes.created, result.modules, result.outputs)
			}
		})
	}
}

// The replacement is put before the earlier deployment is removed: if it fails, e.g. on the rights
// of the gateway, the earlier deployment stays.
func TestProcessDeploymentKeepsTheEarlierDeploymentWhenItsReplacementFails(t *testing.T) {
	failure := errors.New("no rights on the gateway")
	processes := &fakeProcesses{networks: gatewayOfDevice1, putErr: failure}
	run, s := earlierDeploymentRunner(t, processes, referencingProcesses{}, "", true, repositoryOfDevice1)
	result := run(fogOnDevice1)
	if !errors.Is(result.err, failure) || !reflect.DeepEqual(processes.calls, []string{"put " + replacementId(earlierDeploymentId, "nid")}) || result.modules != nil {
		t.Errorf("%v calls %v modules %+v", result.err, processes.calls, result.modules)
	}
	if !storedDeployment(t, s, earlierDeploymentId) {
		t.Error("the earlier deployment is gone")
	}
}

// A removal that fails after the replacement fails the task; the retry puts the same replacement
// and removes the earlier deployment, without a second replacement.
func TestProcessDeploymentRetriesTheRemovalWithTheSameReplacement(t *testing.T) {
	failure := errors.New("gateway unreachable")
	processes := &fakeProcesses{networks: gatewayOfDevice1, removeErr: failure}
	run, s := earlierDeploymentRunner(t, processes, referencingProcesses{}, "", true, repositoryOfDevice1)
	if result := run(fogOnDevice1); !errors.Is(result.err, failure) || result.modules != nil {
		t.Fatalf("%v %+v", result.err, result.modules)
	}
	processes.removeErr = nil
	result := run(fogOnDevice1)
	if result.err != nil {
		t.Fatal(result.err)
	}
	replacement := replacementId(earlierDeploymentId, "nid")
	expected := []string{"put " + replacement, "remove " + earlierDeploymentId, "put " + replacement, "remove " + earlierDeploymentId}
	if !reflect.DeepEqual(processes.calls, expected) || len(processes.created) != 0 || result.outputs["process_deployment_id"] != replacement {
		t.Errorf("calls %v, created %+v, %#v", processes.calls, processes.created, result.outputs)
	}
	if storedDeployment(t, s, earlierDeploymentId) {
		t.Error("the earlier deployment stays")
	}
}

// A result the engine does not apply leaves the module naming the removed deployment; the retry
// finds it missing and puts the same replacement again instead of creating another one.
func TestProcessDeploymentRetryAfterAnUnappliedResultPutsTheSameReplacement(t *testing.T) {
	processes := &fakeProcesses{networks: gatewayOfDevice1}
	run, _ := earlierDeploymentRunner(t, processes, referencingProcesses{}, "", true, repositoryOfDevice1)
	first := run(fogOnDevice1)
	second := run(fogOnDevice1)
	if first.err != nil || second.err != nil {
		t.Fatal(first.err, second.err)
	}
	replacement := replacementId(earlierDeploymentId, "nid")
	expected := []string{"put " + replacement, "remove " + earlierDeploymentId, "put " + replacement}
	if !reflect.DeepEqual(processes.calls, expected) || second.outputs["process_deployment_id"] != replacement {
		t.Errorf("calls %v, %#v", processes.calls, second.outputs)
	}
}

// A switched-off deployment is not moved (DEP-12): a replacement would run. The old id is put, and
// the controller refuses the change of network, so the task fails rather than switching it on.
func TestProcessDeploymentDoesNotMoveASwitchedOffDeployment(t *testing.T) {
	processes := &fakeProcesses{networks: gatewayOfDevice1}
	run, s := earlierDeploymentRunnerWith(t, processes, referencingProcesses{}, model.Deployment{Disabled: true}, true, repositoryOfDevice1)
	result := run(fogOnDevice1)
	if result.err != nil {
		t.Fatal(result.err)
	}
	if !reflect.DeepEqual(processes.calls, []string{"put " + earlierDeploymentId}) || processes.put[earlierDeploymentId].NetworkId != "nid" || len(processes.created) != 0 {
		t.Errorf("calls %v, put %+v, created %+v", processes.calls, processes.put, processes.created)
	}
	if !storedDeployment(t, s, earlierDeploymentId) {
		t.Error("the earlier deployment is gone")
	}
}

// A deployment stays on its gateway while that still carries every device, also when another
// gateway that carries them is listed first, e.g. one added later (SNRGY-5137).
func TestProcessDeploymentStaysOnItsGatewayWhileItFits(t *testing.T) {
	networks := []model.Network{{Id: "added-later", Compatible: true}, {Id: "nid", Compatible: true}}
	repository := map[string]any{
		"/hubs/added-later": models.Hub{Id: "added-later", DeviceIds: []string{"device_1"}},
		"/hubs/nid":         models.Hub{Id: "nid", DeviceIds: []string{"device_1"}},
	}
	processes := &fakeProcesses{networks: networks}
	run, _ := earlierDeploymentRunner(t, processes, referencingProcesses{}, "nid", true, repository)
	result := run(fogOnDevice1)
	if result.err != nil {
		t.Fatal(result.err)
	}
	if !reflect.DeepEqual(processes.calls, []string{"put " + earlierDeploymentId}) || processes.put[earlierDeploymentId].NetworkId != "nid" || result.outputs["fog_hub"] != "nid" {
		t.Errorf("calls %v, put %+v, %#v", processes.calls, processes.put, result.outputs)
	}
}

// A deployment of the key in another namespace is not moved: put under its id, the controller
// answers with a conflict and nothing is created or removed.
func TestProcessDeploymentDoesNotMoveADeploymentOfAnotherNamespace(t *testing.T) {
	processes := &fakeProcesses{networks: gatewayOfDevice1}
	run, s := earlierDeploymentRunnerWith(t, processes, referencingProcesses{}, model.Deployment{Namespace: "elsewhere"}, true, repositoryOfDevice1)
	result := run(fogOnDevice1)
	if result.err != nil {
		t.Fatal(result.err)
	}
	if !reflect.DeepEqual(processes.calls, []string{"put " + earlierDeploymentId}) || len(processes.created) != 0 {
		t.Errorf("calls %v, created %+v", processes.calls, processes.created)
	}
	if !storedDeployment(t, s, earlierDeploymentId) {
		t.Error("the earlier deployment is gone")
	}
}
