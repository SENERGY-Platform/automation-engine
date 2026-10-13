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
	"strings"
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// A model of the Camunda engine is rewritten on request and then deploys; nothing is stored
// by the conversion itself (MIG-04).
func TestCamundaModelIsConvertedAndDeploys(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		old := b.Process(
			b.Start("start"),
			`<bpmn:scriptTask id="log" scriptFormat="groovy"><bpmn:script>println "started"</bpmn:script></bpmn:scriptTask>`,
			`<bpmn:intermediateCatchEvent id="wait"><bpmn:timerEventDefinition><bpmn:timeDuration xsi:type="bpmn:tFormalExpression">PT${seconds}S</bpmn:timeDuration></bpmn:timerEventDefinition></bpmn:intermediateCatchEvent>`,
			`<bpmn:serviceTask id="mail"><bpmn:extensionElements><camunda:connector><camunda:connectorId>mail-send</camunda:connectorId></camunda:connector></bpmn:extensionElements></bpmn:serviceTask>`,
			b.End("end"),
			b.Flow("start", "log"), b.Flow("log", "wait"), b.Flow("wait", "mail"), b.Flow("mail", "end"),
		)
		conversion, err := c.Convert(ctx, alice, ns, old)
		if err != nil {
			t.Fatal(err)
		}
		if len(conversion.Rewritten) != 2 || len(conversion.Unsupported) != 1 || !strings.Contains(conversion.Unsupported[0].Message, "mail-send") {
			t.Fatalf("groovy and timer are rewritten, mail is not: %+v", conversion)
		}
		if len(conversion.Issues) == 0 || conversion.Issues[0].ElementId != "mail" {
			t.Fatalf("the result is validated against the namespace: %+v", conversion.Issues)
		}
		models, err := c.ListModels(ctx, alice, ns, model.ModelListOptions{})
		if err != nil || models.Total != 0 {
			t.Fatalf("nothing is stored: %+v %v", models, err)
		}
		without := strings.Replace(conversion.Xml, `<bpmn:serviceTask id="mail">`, `<bpmn:task id="mail">`, 1)
		without = strings.Replace(without, `</camunda:connector></bpmn:extensionElements></bpmn:serviceTask>`, `</camunda:connector></bpmn:extensionElements></bpmn:task>`, 1)
		again, err := c.Convert(ctx, alice, ns, without)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: strings.Replace(again.Xml, `<camunda:connector><camunda:connectorId>mail-send</camunda:connectorId></camunda:connector>`, "", 1)}); err != nil {
			t.Fatalf("the rewritten model deploys once the mail task is replaced: %v", err)
		}
	})
}

// Instances carry a name and a description users set, as smart-service instances did.
func TestInstancesHaveNameAndDescription(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		d, err := c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: waitingModel()})
		if err != nil {
			t.Fatal(err)
		}
		inst, err := c.StartInstance(ctx, alice, ns, model.StartInstanceInput{DeploymentId: d.Id, Name: "Kitchen", Description: "lights"})
		if err != nil || inst.Name != "Kitchen" || inst.Description != "lights" {
			t.Fatalf("%+v %v", inst, err)
		}
		if inst, err = c.UpdateInstanceInfo(ctx, alice, ns, inst.Id, model.InstanceInfoInput{Name: "Hall"}); err != nil || inst.Name != "Hall" || inst.Description != "" {
			t.Fatalf("%+v %v", inst, err)
		}
		if inst, err = c.GetInstance(ctx, alice, ns, inst.Id); err != nil || inst.Name != "Hall" {
			t.Fatalf("%+v %v", inst, err)
		}
		if _, err = c.UpdateInstanceInfo(ctx, bob, ns, inst.Id, model.InstanceInfoInput{Name: "x"}); err == nil {
			t.Error("only who may write the instance changes its name")
		}
	})
}

// A deployment may take the content of a model in another form, e.g. rewritten for this
// engine, and keeps its reference to the model (MIG-04).
func TestDeploymentOfAModelWithRewrittenContent(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		m, err := c.CreateModel(ctx, alice, ns, model.ModelInput{Xml: waitingModel(), Svg: "<svg>model</svg>"})
		if err != nil {
			t.Fatal(err)
		}
		other := strings.Replace(waitingModel(), `id="end"`, `id="done"`, 1)
		other = strings.Replace(other, `targetRef="end"`, `targetRef="done"`, 1)
		d, err := c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{ModelId: m.Id, Xml: other})
		if err != nil || d.ModelId != m.Id || d.ModelVersion != 1 {
			t.Fatalf("%+v %v", d, err)
		}
		v, err := c.GetDeploymentVersion(ctx, alice, ns, d.Id, 1)
		if err != nil || !strings.Contains(v.Xml, `id="done"`) || v.Svg != "<svg>model</svg>" {
			t.Fatalf("the given BPMN replaces the content, the SVG of the model stays: %v", err)
		}
	})
}

func TestSchedulesKeepTheirCreatorReference(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		d, err := c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: waitingModel()})
		if err != nil {
			t.Fatal(err)
		}
		s, err := c.CreateSchedule(ctx, alice, ns, model.ScheduleInput{DeploymentId: d.Id, Cron: "@daily", CreatedBy: "widget-1", Disabled: true})
		if err != nil || s.CreatedBy != "widget-1" {
			t.Fatalf("%+v %v", s, err)
		}
		if _, err = c.CreateSchedule(ctx, alice, ns, model.ScheduleInput{DeploymentId: d.Id, Cron: "@daily", Disabled: true}); err != nil {
			t.Fatal(err)
		}
		page, err := c.ListSchedules(ctx, alice, ns, model.ScheduleListOptions{CreatedBy: "widget-1"})
		if err != nil || page.Total != 1 || page.Items[0].Id != s.Id {
			t.Fatalf("%+v %v", page, err)
		}
	})
}

func TestModelsOfOtherOwners(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		if _, err := c.CreateModel(ctx, alice, ns, model.ModelInput{Xml: waitingModel()}); err != nil {
			t.Fatal(err)
		}
		mine, err := c.ListModels(ctx, alice, ns, model.ModelListOptions{NotOwner: "bob"})
		if err != nil || mine.Total != 1 {
			t.Fatalf("%+v %v", mine, err)
		}
		others, err := c.ListModels(ctx, alice, ns, model.ModelListOptions{NotOwner: "alice"})
		if err != nil || others.Total != 0 {
			t.Fatalf("%+v %v", others, err)
		}
	})
}

// Switching a deployment off stops its instances and refuses starts; an update keeps the
// state, switching on allows starts again (DEP-12).
func TestDeploymentSwitchedOffAndOn(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		d, err := c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: waitingModel()})
		if err != nil {
			t.Fatal(err)
		}
		running, err := c.StartInstance(ctx, alice, ns, model.StartInstanceInput{DeploymentId: d.Id})
		if err != nil {
			t.Fatal(err)
		}
		if d, err = c.SetDeploymentDisabled(ctx, alice, ns, d.Id, model.DisabledInput{Disabled: true}); err != nil || !d.Disabled {
			t.Fatalf("%+v %v", d, err)
		}
		if inst, _ := c.GetInstance(ctx, alice, ns, running.Id); inst.State != model.StateTerminated {
			t.Errorf("switching off stops running instances: %v", inst.State)
		}
		if _, err = c.StartInstance(ctx, alice, ns, model.StartInstanceInput{DeploymentId: d.Id}); !errors.Is(err, model.ErrConflict) {
			t.Errorf("a disabled deployment refuses starts: %v", err)
		}
		if d, err = c.PutDeployment(ctx, alice, ns, d.Id, model.DeploymentInput{Xml: waitingModel(), UpdateStrategy: model.UpdateStop}); err != nil || !d.Disabled {
			t.Errorf("an update keeps the state: %+v %v", d, err)
		}
		if _, err = c.SetDeploymentDisabled(ctx, bob, ns, d.Id, model.DisabledInput{Disabled: false}); err == nil {
			t.Error("only who may write the deployment switches it")
		}
		if _, err = c.SetDeploymentDisabled(ctx, alice, ns, d.Id, model.DisabledInput{Disabled: false}); err != nil {
			t.Fatal(err)
		}
		if _, err = c.StartInstance(ctx, alice, ns, model.StartInstanceInput{DeploymentId: d.Id}); err != nil {
			t.Errorf("switched on, it starts again: %v", err)
		}
	})
}
