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

package client_test

import (
	"errors"
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// smartService describes a room and offers a maintenance procedure that adjusts a level
func smartService() string {
	return b.Process(
		b.Message("m", "adjust"),
		b.StartWithForm("start", "room="),
		b.Service("describe", "info", b.InText("info.module_type", "room"), b.InText("info.module_data", `{"room":"{{.room}}"}`)),
		b.End("end"),
		`<bpmn:startEvent id="adjustStart"><bpmn:extensionElements><camunda:formData><camunda:formField id="level" label="level" type="long"/></camunda:formData></bpmn:extensionElements><bpmn:messageEventDefinition messageRef="m"/></bpmn:startEvent>`,
		b.Service("adjust", "info",
			b.InText("prescript", `variables.write("level", inputs.get("level"))`),
			b.InScript("level", "level"),
			b.InText("info.module_type", "level"),
			b.InText("info.key", "level"),
			b.InScript("info.module_data", "JSON.stringify({level: level, room: room})")),
		b.End("adjusted"),
		b.Flow("start", "describe"), b.Flow("describe", "end"), b.Flow("adjustStart", "adjust"), b.Flow("adjust", "adjusted"),
	)
}

func TestMaintenanceProceduresShareTheModulesAndVariablesOfTheirInstance(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		const smart = "smart-services"
		d, err := c.CreateDeployment(ctx, alice, smart, model.DeploymentInput{Xml: smartService()})
		if err != nil {
			t.Fatal(err)
		}
		inst, err := c.StartInstance(ctx, alice, smart, model.StartInstanceInput{DeploymentId: d.Id, Variables: map[string]any{"room": "kitchen"}})
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, "ready", func() bool {
			current, _ := c.GetInstance(ctx, alice, smart, inst.Id)
			return current.Status == model.StatusReady
		})
		procedures, err := c.ListMaintenanceProcedures(ctx, alice, smart, inst.Id)
		if err != nil || len(procedures) != 1 || procedures[0].Id != "adjust" || len(procedures[0].Parameters) != 1 || procedures[0].Parameters[0].Id != "level" {
			t.Fatalf("message start events are the procedures (SMS-13): %+v %v", procedures, err)
		}
		if correlated, err := c.CorrelateMessage(ctx, alice, model.MessageInput{Name: "adjust", All: true}); err != nil || len(correlated.Correlations) != 0 {
			t.Fatalf("a procedure starts only through its instance, not by message: %+v %v", correlated, err)
		}
		if _, err = c.StartMaintenance(ctx, bob, smart, inst.Id, "adjust", model.MaintenanceStartInput{}); !errors.Is(err, model.ErrNotFound) && !errors.Is(err, model.ErrForbidden) {
			t.Fatalf("starting needs administrate on the instance, got %v", err)
		}
		procedure, err := c.StartMaintenance(ctx, alice, smart, inst.Id, "adjust", model.MaintenanceStartInput{Variables: map[string]any{"level": 5}})
		if err != nil || procedure.ParentId != inst.Id || procedure.Owner != "alice" {
			t.Fatalf("the procedure refers to its instance: %+v %v", procedure, err)
		}
		finished(t, c, smart, procedure.Id)
		modules, err := c.ListModules(ctx, alice, smart, model.ModuleListOptions{InstanceId: inst.Id, Key: "level"})
		if err != nil || modules.Total != 1 || modules.Items[0].Data["level"] != 5.0 || modules.Items[0].Data["room"] != "kitchen" || modules.Items[0].CreatedBy != procedure.Id {
			t.Fatalf("the module of the procedure belongs to the instance: %+v %v", modules, err)
		}
		vars, _ := c.GetVariables(ctx, alice, smart, inst.Id)
		if variable(vars, "level") != 5.0 {
			t.Fatalf("script variables of the procedure are those of the instance: %v", vars)
		}
	})
}

func TestDeploymentParametersOverrideInputsTimersAndDefaults(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		xml := b.Process(
			b.StartWithForm("start", "level=1"),
			b.Task("configure", b.InText("target", "10"), b.OutScript("target", "target")),
			b.Script("copy", "result", "target + '/' + level"),
			b.CatchTimer("pause", "timeDuration", "PT1H"),
			b.End("end"),
			b.Flow("start", "configure"), b.Flow("configure", "copy"), b.Flow("copy", "pause"), b.Flow("pause", "end"),
		)
		if _, err := c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: xml, Parameters: map[string]model.ElementParameters{"configure": {Inputs: map[string]string{"missing": "1"}}}}); !errors.Is(err, model.ErrValidation) {
			t.Fatalf("an input the element does not have is a validation error, got %v", err)
		}
		d, err := c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: xml, Parameters: map[string]model.ElementParameters{
			"start":     {Defaults: map[string]string{"level": "7"}},
			"configure": {Inputs: map[string]string{"target": "20"}},
			"pause":     {Timer: "PT1S"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		params, err := c.GetStartParameters(ctx, alice, ns, d.Id)
		if err != nil || len(params) != 1 || params[0].Default != "7" {
			t.Fatalf("the start form shows the overridden default: %+v %v", params, err)
		}
		inst, err := c.StartInstance(ctx, alice, ns, model.StartInstanceInput{DeploymentId: d.Id})
		if err != nil {
			t.Fatal(err)
		}
		finished(t, c, ns, inst.Id)
		vars, _ := c.GetVariables(ctx, alice, ns, inst.Id)
		if variable(vars, "result") != "20/7" {
			t.Fatalf("inputs and defaults of the deployment apply, the shorter timer fired: %v", vars)
		}
	})
}

func TestParameterChangeStartsOverWithoutTheModulesOfTheLastRun(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		const smart = "smart-services"
		d, err := c.CreateDeployment(ctx, alice, smart, model.DeploymentInput{Xml: smartService()})
		if err != nil {
			t.Fatal(err)
		}
		inst, err := c.StartInstance(ctx, alice, smart, model.StartInstanceInput{DeploymentId: d.Id, Variables: map[string]any{"room": "kitchen"}})
		if err != nil {
			t.Fatal(err)
		}
		ready := func() {
			t.Helper()
			waitFor(t, "ready", func() bool {
				current, _ := c.GetInstance(ctx, alice, smart, inst.Id)
				return current.Status == model.StatusReady
			})
		}
		ready()
		procedure, err := c.StartMaintenance(ctx, alice, smart, inst.Id, "adjust", model.MaintenanceStartInput{Variables: map[string]any{"level": 2}})
		if err != nil {
			t.Fatal(err)
		}
		finished(t, c, smart, procedure.Id)
		if err = c.SetVariable(ctx, alice, smart, inst.Id, "note", "keep"); err != nil {
			t.Fatalf("variables of a ready smart service can be set (SMS-10): %v", err)
		}
		if err = c.DeleteVariable(ctx, alice, smart, inst.Id, "note"); err != nil {
			t.Fatal(err)
		}
		if _, err = c.UpdateParameters(ctx, bob, smart, inst.Id, model.ParametersInput{Variables: map[string]any{"room": "bath"}}); !errors.Is(err, model.ErrNotFound) && !errors.Is(err, model.ErrForbidden) {
			t.Fatalf("a parameter change needs administrate, got %v", err)
		}
		changed, err := c.UpdateParameters(ctx, alice, smart, inst.Id, model.ParametersInput{Variables: map[string]any{"room": "bath"}})
		if err != nil || changed.Run != 2 || changed.StartVariables["room"] != "bath" {
			t.Fatalf("a new run with the new parameters: %+v %v", changed, err)
		}
		ready()
		modules, err := c.ListModules(ctx, alice, smart, model.ModuleListOptions{InstanceId: inst.Id})
		if err != nil || modules.Total != 1 || modules.Items[0].Data["room"] != "bath" {
			t.Fatalf("the modules of the last run and its procedures are gone, the new run created its own (SMS-03): %+v %v", modules, err)
		}
		if _, err = c.GetInstance(ctx, alice, smart, procedure.Id); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("the procedure belonged to the last run: %v", err)
		}
	})
}
