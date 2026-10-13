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

package integration_test

import (
	"errors"
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// roomService sets up one info module of the given type for the room of its start form
func roomService(moduleType string) string {
	return b.Process(
		b.StartWithForm("start", "room="),
		b.Service("describe", "info", b.InText("info.module_type", moduleType), b.InText("info.module_data", `{"room":"{{.room}}"}`)),
		b.End("end"),
		b.Flow("start", "describe"), b.Flow("describe", "end"),
	)
}

func modules(t *testing.T, e *env, instanceId string) []model.Module {
	t.Helper()
	return must[model.Page[model.Module]](t)(e.c.ListModules(ctx, alice, smartServices, model.ModuleListOptions{InstanceId: instanceId})).Items
}

// A smart service from its design to an upgrade: the instance sets up its module, a parameter
// change replaces it, a new version of the design reaches the instance once the user moves it,
// and a delete removes the instance with its modules (SMS-03, SMS-09).
func TestSmartServiceFromDesignToUpgrade(t *testing.T) {
	e := newEnv(t)
	design := must[model.Model](t)(e.c.CreateModel(ctx, alice, smartServices, model.ModelInput{Name: "Room", Xml: roomService("widget")}))
	d := must[model.Deployment](t)(e.c.CreateDeployment(ctx, alice, smartServices, model.DeploymentInput{ModelId: design.Id}))
	parameters := must[[]model.ParameterDescription](t)(e.c.DeploymentParameters(ctx, alice, smartServices, d.Id))
	if len(parameters) != 1 || parameters[0].Id != "room" {
		t.Fatalf("the start parameters of the release: %+v", parameters)
	}

	inst := must[model.Instance](t)(e.c.StartInstance(ctx, alice, smartServices, model.StartInstanceInput{DeploymentId: d.Id, Name: "Kitchen", Variables: map[string]any{"room": "kitchen"}}))
	e.ready(inst.Id)
	if found := modules(t, e, inst.Id); len(found) != 1 || found[0].ModuleType != "widget" || found[0].Data["room"] != "kitchen" {
		t.Fatalf("the module of the instance: %+v", found)
	}

	if _, err := e.c.UpdateParameters(ctx, alice, smartServices, inst.Id, model.ParametersInput{Variables: map[string]any{"room": "bath"}}); err != nil {
		t.Fatal(err)
	}
	e.ready(inst.Id)
	if found := modules(t, e, inst.Id); len(found) != 1 || found[0].Data["room"] != "bath" {
		t.Fatalf("a parameter change replaces the module: %+v", found)
	}

	design = must[model.Model](t)(e.c.UpdateModel(ctx, alice, smartServices, design.Id, model.ModelInput{Name: "Room", Xml: roomService("tile")}))
	d = must[model.Deployment](t)(e.c.PutDeployment(ctx, alice, smartServices, d.Id, model.DeploymentInput{ModelId: design.Id, UpdateStrategy: model.UpdateDrain}))
	if d.Version != 2 || d.ModelVersion != 2 {
		t.Fatalf("the new design is version 2 of the release: %+v", d)
	}
	if count := must[model.Count](t)(e.c.DeploymentInstanceCount(ctx, alice, smartServices, d.Id, 1)); count.Count != 1 {
		t.Fatalf("the instance stays on version 1 until it is moved: %+v", count)
	}
	moved := must[model.Instance](t)(e.c.UpdateParameters(ctx, alice, smartServices, inst.Id, model.ParametersInput{Variables: map[string]any{"room": "bath"}, DeploymentVersion: 2}))
	if moved.DeploymentVersion != 2 {
		t.Fatalf("the instance moves to version 2: %+v", moved)
	}
	if ready := e.ready(inst.Id); ready.Name != "Kitchen" {
		t.Errorf("the instance keeps its name: %+v", ready)
	}
	if found := modules(t, e, inst.Id); len(found) != 1 || found[0].ModuleType != "tile" {
		t.Fatalf("the new version set up its module: %+v", found)
	}

	if err := e.c.DeleteInstances(ctx, alice, smartServices, []string{inst.Id}); err != nil {
		t.Fatal(err)
	}
	if found := must[model.Page[model.Module]](t)(e.c.ListModules(ctx, alice, smartServices, model.ModuleListOptions{})); found.Total != 0 {
		t.Fatalf("the modules go with the instance: %+v", found)
	}
}

// A smart service deploys and starts a process for its user. The model it deploys is
// protected while in use; deleting the smart service removes the process it manages (NS-08,
// SMS-15).
func TestSmartServiceManagesAProcess(t *testing.T) {
	e := newEnv(t)
	m := must[model.Model](t)(e.c.CreateModel(ctx, alice, processes, model.ModelInput{Name: "Wait", Xml: b.Process(
		b.Message("m", "go"),
		b.Start("start"), b.Task("configure", b.InText("target", "1"), b.OutScript("target", "target")), b.CatchMessage("wait", "m"), b.End("end"),
		b.Flow("start", "configure"), b.Flow("configure", "wait"), b.Flow("wait", "end"),
	)}))
	d := must[model.Deployment](t)(e.c.CreateDeployment(ctx, alice, smartServices, model.DeploymentInput{Name: "Manager", Xml: b.Process(
		b.StartWithForm("start", "target="),
		b.Service("deploy", "process_deployment",
			b.InText("process_deployment.process_model_id", m.Id),
			b.InText("process_deployment.name", "for {{.target}}"),
			b.InText("process_deployment.configure.parameter.target", "{{.target}}"),
			b.OutScript("deployment", "process_deployment_id"), b.OutScript("done", "done_event")),
		`<bpmn:intermediateCatchEvent id="deployed" senergy:messageName="done"><bpmn:messageEventDefinition/></bpmn:intermediateCatchEvent>`,
		b.Service("run", "process_deployment_start", b.InText("process_deployment_start.process_deployment_id", "{{.deployment}}")),
		b.End("end"),
		b.Flow("start", "deploy"), b.Flow("deploy", "deployed"), b.Flow("deployed", "run"), b.Flow("run", "end"),
	)}))
	inst := must[model.Instance](t)(e.c.StartInstance(ctx, alice, smartServices, model.StartInstanceInput{DeploymentId: d.Id, Variables: map[string]any{"target": "42"}}))
	e.ready(inst.Id)

	managed := must[model.Page[model.Deployment]](t)(e.c.ListDeployments(ctx, alice, processes, model.DeploymentListOptions{}))
	if managed.Total != 1 || managed.Items[0].Name != "for 42" || managed.Items[0].ManagedBy == "" {
		t.Fatalf("the smart service deployed the process for the user: %+v", managed)
	}
	running := must[model.Page[model.Instance]](t)(e.c.ListInstances(ctx, alice, processes, model.InstanceListOptions{DeploymentId: managed.Items[0].Id}))
	if running.Total != 1 {
		t.Fatalf("and started it: %+v", running)
	}
	if vars := must[[]model.Variable](t)(e.c.GetVariables(ctx, alice, processes, running.Items[0].Id)); variable(vars, "target") != "42" {
		t.Errorf("with the parameter of the smart service: %+v", vars)
	}

	usage := must[model.ResourceUsage](t)(e.c.ResourceUsage(ctx, alice, smartServices, model.ResourceKindProcessModels, m.Id))
	if usage.Versions != 1 || usage.Instances != 1 || len(usage.Readable) != 1 || usage.Readable[0].Id != d.Id {
		t.Fatalf("the model is in use: %+v", usage)
	}
	if err := e.c.DeleteModel(ctx, alice, processes, m.Id, false); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("a model in use is not deleted: %v", err)
	}

	if err := e.c.DeleteInstances(ctx, alice, smartServices, []string{inst.Id}); err != nil {
		t.Fatal(err)
	}
	// the outbox of module removals, at once instead of in its next pass
	if err := e.srv.ModuleRemover.Process(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.c.GetDeployment(ctx, alice, processes, managed.Items[0].Id); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("the managed process goes with the smart service: %v", err)
	}
	if err := e.c.DeleteDeployment(ctx, alice, smartServices, d.Id); err != nil {
		t.Fatal(err)
	}
	if err := e.c.DeleteModel(ctx, alice, processes, m.Id, false); err != nil {
		t.Fatalf("an unused model is deleted: %v", err)
	}
}
