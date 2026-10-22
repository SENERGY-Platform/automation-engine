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

package deprecated_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/models/go/models"
)

const emptyTimerModel = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="Definitions_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="wait" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:intermediateCatchEvent id="pause"><bpmn:timerEventDefinition/></bpmn:intermediateCatchEvent>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="pause"/>
    <bpmn:sequenceFlow id="f2" sourceRef="pause" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`

// The deploy dialog fills a timer the old designer left empty; the deployment waits that long,
// as process-deployment wrote the time into the model.
func TestDeployFillsAnEmptyTimerWithTheTimeOfTheDialog(t *testing.T) {
	e := newEnv(t)
	var prepared models.Deployment
	if status, _ := e.call(alice, http.MethodPost, "/deployment/v3/prepared-deployments", map[string]string{"xml": emptyTimerModel, "svg": "<svg/>"}, &prepared); status != http.StatusOK {
		t.Fatalf("prepare: %v", status)
	}
	if len(prepared.Elements) != 1 || prepared.Elements[0].TimeEvent == nil || prepared.Elements[0].TimeEvent.Time != "" {
		t.Fatalf("expected the empty timer to fill in, got %+v", prepared.Elements)
	}
	prepared.Elements[0].TimeEvent.Time = "PT2H"
	var deployed models.Deployment
	if status, _ := e.call(alice, http.MethodPost, "/deployment/v3/deployments", prepared, &deployed); status != http.StatusOK {
		t.Fatalf("deploy: %v", status)
	}
	version, err := e.srv.Controller.GetDeploymentVersion(ctx, alice, "processes", deployed.Id, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(version.Xml, "PT2H") {
		t.Errorf("the model carries the time of the dialog: %s", version.Xml)
	}
}

// A timer the dialog left empty as well cannot run and is reported.
func TestDeployWithAnEmptyTimerIsRejected(t *testing.T) {
	e := newEnv(t)
	old := models.Deployment{Version: 3, Name: "wait", Diagram: models.Diagram{XmlRaw: emptyTimerModel, Svg: "<svg/>"},
		Elements: []models.Element{{BpmnId: "pause", TimeEvent: &models.TimeEvent{Type: "timeDuration"}}}}
	if status, _ := e.call(alice, http.MethodPost, "/deployment/v3/deployments", old, nil); status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %v", status)
	}
	if page, err := e.srv.Controller.ListDeployments(ctx, alice, "processes", model.DeploymentListOptions{}); err != nil || page.Total != 0 {
		t.Fatalf("nothing is deployed: %+v %v", page, err)
	}
}

const durationTimerModel = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="Definitions_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="wait" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:intermediateCatchEvent id="pause"><bpmn:timerEventDefinition><bpmn:timeDuration>R/PT120S</bpmn:timeDuration></bpmn:timerEventDefinition></bpmn:intermediateCatchEvent>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="pause"/>
    <bpmn:sequenceFlow id="f2" sourceRef="pause" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`

// The deploy dialog took a repeating interval as duration as well; it becomes its period like
// the one in the model, instead of rejecting the deployment.
func TestDeployTakesARepeatingIntervalOfTheDialogAsDuration(t *testing.T) {
	e := newEnv(t)
	old := models.Deployment{Version: 3, Name: "wait", Diagram: models.Diagram{XmlRaw: durationTimerModel, Svg: "<svg/>"},
		Elements: []models.Element{{BpmnId: "pause", TimeEvent: &models.TimeEvent{Type: "timeDuration", Time: "R/PT1800S"}}}}
	var deployed models.Deployment
	if status, _ := e.call(alice, http.MethodPost, "/deployment/v3/deployments", old, &deployed); status != http.StatusOK {
		t.Fatalf("deploy: %v", status)
	}
	version, err := e.srv.Controller.GetDeploymentVersion(ctx, alice, "processes", deployed.Id, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := version.Parameters["pause"].Timer; got != "PT1800S" {
		t.Errorf("timer parameter %q, want PT1800S", got)
	}
}
