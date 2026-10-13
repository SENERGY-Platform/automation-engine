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

package modules_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
)

var ctx = context.Background()
var alice = client.TestToken("alice")

// keycloak answers token exchanges with an unsigned token of the requested user
func keycloak(t *testing.T) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		token := strings.TrimPrefix(auth.UnsignedToken(r.Form.Get("requested_subject")), "Bearer ")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": token, "expires_in": 300})
	}))
	t.Cleanup(server.Close)
	return server
}

// recorder is a fake service that remembers its requests
type recorder struct {
	mux      sync.Mutex
	requests []string
	bodies   []map[string]any
	answer   func(r *http.Request) any
}

func (this *recorder) server(t *testing.T) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		this.mux.Lock()
		this.requests = append(this.requests, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		this.bodies = append(this.bodies, body)
		this.mux.Unlock()
		if this.answer != nil {
			_ = json.NewEncoder(w).Encode(this.answer(r))
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func (this *recorder) all() []string {
	this.mux.Lock()
	defer this.mux.Unlock()
	return append([]string{}, this.requests...)
}

func newServer(t *testing.T, configure func(*configuration.Config)) *server.Server {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	keycloakUrl := keycloak(t).URL
	srv, err := client.NewTestServer(ctx, func(config *configuration.Config, _ *server.Dependencies) {
		config.AuthEndpoint = keycloakUrl
		config.AuthClientId = "engine"
		configure(config)
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func waitFor(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %v", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestModuleHandlersRunWithTemplatesScriptsAndOwnerTokens(t *testing.T) {
	importDeploy := &recorder{answer: func(r *http.Request) any { return map[string]any{"id": "imp-1", "name": "kitchen import"} }}
	importUrl := importDeploy.server(t).URL
	srv := newServer(t, func(c *configuration.Config) { c.ImportDeployUrl = importUrl })
	c := srv.Controller
	d, err := c.CreateDeployment(ctx, alice, "smart-services", model.DeploymentInput{Xml: b.Process(
		b.StartWithForm("start", "room="),
		b.Service("describe", "info",
			b.InText("prescript", `variables.write("greeting", "hello " + variables.read("room"))`),
			b.InText("info.module_type", "widget"),
			b.InText("info.module_data", `{"room":"{{.room}}"}`)),
		b.Service("load", "import",
			b.InText("import.request", `{"name":"{{.room}} import","import_type_id":"urn:type","configs":[]}`),
			b.OutScript("import_id", "import_id")),
		b.End("end"),
		b.Flow("start", "describe"), b.Flow("describe", "load"), b.Flow("load", "end"),
	)})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "smart-services", model.StartInstanceInput{DeploymentId: d.Id, Variables: map[string]any{"room": "kitchen"}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the smart service", func() bool {
		current, _ := c.GetInstance(ctx, alice, "smart-services", inst.Id)
		return current.Status == model.StatusReady
	})
	vars, _ := c.GetVariables(ctx, alice, "smart-services", inst.Id)
	values := map[string]any{}
	for _, v := range vars {
		values[v.Name] = v.Value
	}
	if values["greeting"] != "hello kitchen" || values["import_id"] != "imp-1" {
		t.Fatalf("prescripts write instance variables, outputs map back: %v", values)
	}
	if requests := importDeploy.all(); len(requests) != 1 || requests[0] != "POST /instances "+auth.UnsignedToken("alice") || importDeploy.bodies[0]["name"] != "kitchen import" {
		t.Fatalf("the import is created from the rendered template with the owner's token (TASK-28): %v %v", requests, importDeploy.bodies)
	}
	modules, err := c.ListModules(ctx, alice, "smart-services", model.ModuleListOptions{InstanceId: inst.Id})
	if err != nil || modules.Total != 2 {
		t.Fatalf("modules: %+v %v", modules, err)
	}
	byType := map[string]model.Module{}
	for _, m := range modules.Items {
		byType[m.ModuleType] = m
	}
	if byType["widget"].Data["room"] != "kitchen" || byType["import"].DeleteInfo == nil || byType["import"].DeleteInfo.Url != importUrl+"/instances/imp-1" {
		t.Fatalf("module data and delete info as the workers wrote them (SMS-12): %+v", modules.Items)
	}
	if other, err := c.ListModules(ctx, client.TestToken("bob"), "smart-services", model.ModuleListOptions{InstanceId: inst.Id}); err != nil || other.Total != 0 {
		t.Fatalf("modules of instances others cannot read stay hidden: %+v %v", other, err)
	}

	if err = c.DeleteInstances(ctx, alice, "smart-services", []string{inst.Id}); err != nil {
		t.Fatal(err)
	}
	if err = srv.ModuleRemover.Process(ctx); err != nil {
		t.Fatal(err)
	}
	if requests := importDeploy.all(); len(requests) != 2 || requests[1] != "DELETE /instances/imp-1 "+auth.UnsignedToken("alice") {
		t.Fatalf("deleting the instance removes the import with the owner's token (SMS-06): %v", requests)
	}
}

func TestHealthChecksMarkStoppedPipelinesInTheStatus(t *testing.T) {
	var running atomic.Bool
	flowEngine := &recorder{answer: func(r *http.Request) any { return map[string]any{"id": "p1", "running": running.Load()} }}
	flowUrl := flowEngine.server(t).URL
	srv := newServer(t, func(c *configuration.Config) {
		c.FlowEngineUrl, c.FlowParserUrl, c.DeviceRepositoryUrl = flowUrl, flowUrl, flowUrl
	})
	c := srv.Controller
	d, err := c.CreateDeployment(ctx, alice, "smart-services", model.DeploymentInput{Xml: b.Process(
		b.Start("start"),
		b.Service("describe", "info", b.InText("info.module_type", "analytics"), b.InText("info.module_data", `{"pipeline_id":"p1"}`)),
		b.End("end"), b.Flow("start", "describe"), b.Flow("describe", "end"),
	)})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "smart-services", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	status := func() model.Instance {
		current, _ := c.GetInstance(ctx, alice, "smart-services", inst.Id)
		return current
	}
	waitFor(t, "the smart service", func() bool { return status().Status == model.StatusReady })
	if err = srv.Modules.CheckModules(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if current := status(); current.Status != model.StatusError || !strings.Contains(current.StatusMessage, "analytics: pipeline p1 is not running") {
		t.Fatalf("a stopped pipeline shows as module error in the status (SMS-11): %+v", current)
	}
	if requests := flowEngine.all(); len(requests) != 1 || requests[0] != "GET /pipeline/p1 "+auth.UnsignedToken("alice") {
		t.Fatalf("the check asks with the owner's token: %v", requests)
	}
	running.Store(true)
	if err = srv.Modules.CheckModules(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if current := status(); current.Status != model.StatusReady {
		t.Fatalf("a recovered module clears the error: %+v", current)
	}
}

func TestSmartServicesDeployAndStartManagedProcesses(t *testing.T) {
	srv := newServer(t, func(*configuration.Config) {})
	c := srv.Controller
	m, err := c.CreateModel(ctx, alice, "processes", model.ModelInput{Xml: b.Process(
		b.Message("m", "go"),
		b.Start("start"), b.Task("configure", b.InText("target", "1"), b.OutScript("target", "target")), b.CatchMessage("wait", "m"), b.End("end"),
		b.Flow("start", "configure"), b.Flow("configure", "wait"), b.Flow("wait", "end"),
	)})
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.CreateDeployment(ctx, alice, "smart-services", model.DeploymentInput{Xml: b.Process(
		b.StartWithForm("start", "target="),
		b.Service("deploy", "process_deployment",
			b.InText("process_deployment.process_model_id", m.Id),
			b.InText("process_deployment.name", "for {{.target}}"),
			b.InText("process_deployment.configure.parameter.target", "{{.target}}"),
			b.OutScript("deployment", "process_deployment_id"), b.OutScript("done", "done_event")),
		`<bpmn:intermediateCatchEvent id="deployed" senergy:messageName="done"><bpmn:messageEventDefinition/></bpmn:intermediateCatchEvent>`,
		b.Service("run", "process_deployment_start", b.InText("process_deployment_start.process_deployment_id", "{{.deployment}}"), b.OutScript("started", "process_instance_id")),
		b.End("end"),
		b.Flow("start", "deploy"), b.Flow("deploy", "deployed"), b.Flow("deployed", "run"), b.Flow("run", "end"),
	)})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "smart-services", model.StartInstanceInput{DeploymentId: d.Id, Variables: map[string]any{"target": "42"}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the smart service", func() bool {
		current, _ := c.GetInstance(ctx, alice, "smart-services", inst.Id)
		return current.State != model.StateRunning
	})
	current, _ := c.GetInstance(ctx, alice, "smart-services", inst.Id)
	if current.Status != model.StatusReady {
		incidents, _ := c.ListIncidents(ctx, alice, "smart-services", model.IncidentListOptions{InstanceId: inst.Id})
		t.Fatalf("the smart service deployed, waited for the done event and started the process: %+v %+v", current, incidents.Items)
	}
	vars, _ := c.GetVariables(ctx, alice, "smart-services", inst.Id)
	values := map[string]any{}
	for _, v := range vars {
		values[v.Name] = v.Value
	}
	deploymentId, _ := values["deployment"].(string)
	managed, err := c.GetDeployment(ctx, alice, "processes", deploymentId)
	if err != nil || managed.ManagedBy != inst.Id || managed.Name != "for 42" {
		t.Fatalf("the deployment refers to the instance that manages it (NS-08): %+v %v", managed, err)
	}
	started, _ := values["started"].(string)
	processVars, _ := c.GetVariables(ctx, alice, "processes", started)
	if len(processVars) != 1 || processVars[0].Value != "42" {
		t.Fatalf("the parameter of the module overrides the input of the deployed process: %+v", processVars)
	}
	modules, _ := c.ListModules(ctx, alice, "smart-services", model.ModuleListOptions{InstanceId: inst.Id})
	if modules.Total != 2 {
		t.Fatalf("modules: %+v", modules)
	}
	for _, m := range modules.Items {
		if m.DeleteInfo == nil || m.DeleteInfo.Resource == nil || m.DeleteInfo.Resource.Namespace != "processes" {
			t.Fatalf("process modules refer to resources of the engine: %+v", m)
		}
	}
	if err = srv.Modules.CheckModules(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if current, _ = c.GetInstance(ctx, alice, "smart-services", inst.Id); current.Status != model.StatusReady {
		t.Fatalf("existing deployment and running instance are healthy: %+v", current)
	}

	if err = c.DeleteInstances(ctx, alice, "smart-services", []string{inst.Id}); err != nil {
		t.Fatal(err)
	}
	if err = srv.ModuleRemover.Process(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = c.GetDeployment(ctx, alice, "processes", deploymentId); model.AsError(err).Kind != model.KindNotFound {
		t.Fatalf("the managed deployment goes with the smart service (SMS-12): %v", err)
	}
	if _, err = c.GetInstance(ctx, alice, "processes", started); model.AsError(err).Kind != model.KindNotFound {
		t.Fatalf("the started process goes with it: %v", err)
	}
}

func TestProcessModulesStayInTheReferencedNamespaces(t *testing.T) {
	srv := newServer(t, func(*configuration.Config) {})
	c := srv.Controller
	d, err := c.CreateDeployment(ctx, alice, "smart-services", model.DeploymentInput{Xml: b.Process(
		b.Start("start"),
		b.Service("deploy", "process_deployment", b.InText("process_deployment.process_model_id", "m"), b.InText("process_deployment.namespace", "smart-services")),
		b.End("end"), b.Flow("start", "deploy"), b.Flow("deploy", "end"),
	)})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "smart-services", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the incident", func() bool {
		current, _ := c.GetInstance(ctx, alice, "smart-services", inst.Id)
		return current.State == model.StateIncident && strings.Contains(current.EndReason, "NS-08")
	})
}

func TestWatcherStartsTheMaintenanceProcedureOnChange(t *testing.T) {
	var devices atomic.Value
	devices.Store(`[{"device":{"id":"urn:infai:ses:device:11111111-1111-1111-1111-111111111111"}}]`)
	deviceSelection := &recorder{}
	selection := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deviceSelection.mux.Lock()
		deviceSelection.requests = append(deviceSelection.requests, r.Method+" "+r.URL.String()+" "+r.Header.Get("Authorization"))
		deviceSelection.mux.Unlock()
		_, _ = w.Write([]byte(devices.Load().(string)))
	}))
	t.Cleanup(selection.Close)
	srv := newServer(t, func(c *configuration.Config) { c.DeviceSelectionUrl, c.WatcherMinInterval = selection.URL, "1ms" })
	c := srv.Controller
	d, err := c.CreateDeployment(ctx, alice, "smart-services", model.DeploymentInput{Xml: b.Process(
		b.Message("m", "devices_changed"),
		b.Start("start"),
		b.Service("watch", "watcher",
			b.InText("watcher.maintenance_procedure", "devices_changed"),
			b.InText("watcher.maintenance_procedure_inputs.level", "3"),
			b.InText("watcher.watch_interval", "1ms"),
			b.InText("watcher.hash_type", "deviceids"),
			b.InText("watcher.watch_devices_by_criteria", `[{"function_id":"f","interaction":"event"}]`)),
		b.End("end"),
		`<bpmn:startEvent id="changed"><bpmn:messageEventDefinition messageRef="m"/></bpmn:startEvent>`,
		b.Service("note", "info", b.InText("info.module_type", "note"), b.InScript("info.module_data", "JSON.stringify({level: level})")),
		b.End("noted"),
		b.Flow("start", "watch"), b.Flow("watch", "end"), b.Flow("changed", "note"), b.Flow("note", "noted"),
	)})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "smart-services", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the smart service", func() bool {
		current, _ := c.GetInstance(ctx, alice, "smart-services", inst.Id)
		return current.Status == model.StatusReady
	})
	notes := func() int {
		page, _ := c.ListModules(ctx, alice, "smart-services", model.ModuleListOptions{InstanceId: inst.Id, ModuleType: "note"})
		return page.Total
	}
	check := func() {
		t.Helper()
		time.Sleep(5 * time.Millisecond)
		if err := srv.Modules.CheckWatches(ctx); err != nil {
			t.Fatal(err)
		}
	}
	check()
	if requests := deviceSelection.all(); len(requests) != 1 || !strings.HasPrefix(requests[0], "POST /v2/query/selectables?include_devices=true ") || !strings.HasSuffix(requests[0], auth.UnsignedToken("alice")) {
		t.Fatalf("the watch asks device-selection with the owner's token: %v", requests)
	}
	check()
	if notes() != 0 {
		t.Fatal("the first check sets the baseline, an unchanged answer starts nothing")
	}
	devices.Store(`[{"device":{"id":"urn:infai:ses:device:22222222-2222-2222-2222-222222222222"}}]`)
	check()
	waitFor(t, "the maintenance procedure", func() bool { return notes() == 1 })
	page, _ := c.ListModules(ctx, alice, "smart-services", model.ModuleListOptions{InstanceId: inst.Id, ModuleType: "note"})
	if page.Items[0].Data["level"] != 3.0 {
		t.Fatalf("the procedure gets the inputs of the watch: %+v", page.Items[0])
	}
	check()
	time.Sleep(50 * time.Millisecond)
	if notes() != 1 {
		t.Fatal("the same answer does not start the procedure again")
	}
}

func TestStartParametersOfSmartServicesWithOptionsAndAutoSelection(t *testing.T) {
	selection := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"device_group":{"id":"g1","name":"lamps"}},{"device_group":{"id":"g2","name":"heaters"}}]`))
	}))
	t.Cleanup(selection.Close)
	srv := newServer(t, func(c *configuration.Config) { c.DeviceSelectionUrl = selection.URL })
	c := srv.Controller
	d, err := c.CreateDeployment(ctx, alice, "smart-services", model.DeploymentInput{Xml: b.Process(
		`<bpmn:startEvent id="start"><bpmn:extensionElements><camunda:formData>`+
			`<camunda:formField id="groups" label="Groups" type="string"><camunda:properties><camunda:property id="iot" value="group"/><camunda:property id="multiple" value="true"/></camunda:properties></camunda:formField>`+
			`<camunda:formField id="everything" type="string"><camunda:properties><camunda:property id="iot" value="group"/><camunda:property id="multiple" value="true"/><camunda:property id="auto_select_all" value="true"/></camunda:properties></camunda:formField>`+
			`</camunda:formData></bpmn:extensionElements></bpmn:startEvent>`,
		b.Script("count", "selected", "JSON.parse(groups).length + '/' + JSON.parse(everything).length"),
		b.End("end"), b.Flow("start", "count"), b.Flow("count", "end"),
	)})
	if err != nil {
		t.Fatal(err)
	}
	described, err := c.DeploymentParameters(ctx, alice, "smart-services", d.Id)
	if err != nil || len(described) != 1 || len(described[0].Options) != 2 || described[0].Options[0].Label != "heaters" {
		t.Fatalf("the start dialog gets the options, auto selected fields stay hidden (SMS-08): %+v %v", described, err)
	}
	inst, err := c.StartInstance(ctx, alice, "smart-services", model.StartInstanceInput{DeploymentId: d.Id, Variables: map[string]any{"groups": []any{described[0].Options[0].Value}}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the smart service", func() bool {
		current, _ := c.GetInstance(ctx, alice, "smart-services", inst.Id)
		return current.State != model.StateRunning
	})
	vars, _ := c.GetVariables(ctx, alice, "smart-services", inst.Id)
	values := map[string]any{}
	for _, v := range vars {
		values[v.Name] = v.Value
	}
	if values["selected"] != "1/2" {
		t.Fatalf("the selection arrives as JSON string and auto_select_all takes every option: %v", values)
	}
	if groups, _ := values["groups"].(string); !strings.HasPrefix(groups, `["{\"device_group_selection\":{\"id\":\"g2\"}`) {
		t.Fatalf("a list in a string field is stored as JSON, as models of smart services expect: %v", values["groups"])
	}
}
