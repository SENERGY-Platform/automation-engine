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
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/models/go/models"
)

// designerModel is BPMN as the old designer wrote it: a start form, a timer, a notification
// through the http-connector and Groovy logging
func designerModel() string {
	return b.Process(
		b.StartWithForm("start", "level=5"),
		`<bpmn:intermediateCatchEvent id="wait" name="Wait"><bpmn:timerEventDefinition><bpmn:timeDuration xsi:type="bpmn:tFormalExpression">PT5M</bpmn:timeDuration></bpmn:timerEventDefinition></bpmn:intermediateCatchEvent>`,
		`<bpmn:serviceTask id="notify" name="Notify"><bpmn:extensionElements><camunda:connector><camunda:inputOutput>`+
			`<camunda:inputParameter name="payload">{"message":"Level ${level}","title":"Info"}</camunda:inputParameter>`+
			`<camunda:inputParameter name="deploymentIdentifier">notification</camunda:inputParameter></camunda:inputOutput>`+
			`<camunda:connectorId>http-connector</camunda:connectorId></camunda:connector></bpmn:extensionElements></bpmn:serviceTask>`,
		`<bpmn:scriptTask id="log" scriptFormat="groovy"><bpmn:script>println "done"</bpmn:script></bpmn:scriptTask>`,
		b.End("end"),
		b.Flow("start", "wait"), b.Flow("wait", "notify"), b.Flow("notify", "log"), b.Flow("log", "end"),
	)
}

func elementById(d models.Deployment, id string) models.Element {
	for _, e := range d.Elements {
		if e.BpmnId == id {
			return e
		}
	}
	return models.Element{}
}

// The deploy dialog of the web-ui prepares the model of the old designer, changes values and
// deploys; the changes become parameters of the deployment (DEP-01, MIG-04).
func TestDeployDialogOfTheWebUi(t *testing.T) {
	e := newEnv(t)
	var prepared models.Deployment
	status, _ := e.call(alice, http.MethodPost, "/deployment/v3/prepared-deployments?with_options=false", map[string]string{"xml": designerModel(), "svg": "<svg/>"}, &prepared)
	if status != http.StatusOK || !prepared.Executable || len(prepared.StartParameter) != 1 || prepared.StartParameter[0].Default != "5" {
		t.Fatalf("%v %+v", status, prepared)
	}
	if timer := elementById(prepared, "wait").TimeEvent; timer == nil || timer.Time != "PT5M" || timer.Type != "timeDuration" {
		t.Fatalf("the timer is configurable: %+v", timer)
	}
	if n := elementById(prepared, "notify").Notification; n == nil || n.Title != "Info" {
		t.Fatalf("the notification is configurable: %+v", n)
	}
	prepared.Name = "Levels"
	elementById(prepared, "wait").TimeEvent.Time = "PT10M"
	elementById(prepared, "notify").Notification.Title = "Changed"
	prepared.StartParameter[0].Default = "7"
	var deployed models.Deployment
	if status, _ = e.call(alice, http.MethodPost, "/deployment/v3/deployments?source=sepl", prepared, &deployed); status != http.StatusOK || deployed.Id == "" {
		t.Fatalf("deploy: %v %+v", status, deployed)
	}
	version, err := e.srv.Controller.GetDeploymentVersion(ctx, alice, "processes", deployed.Id, 1)
	if err != nil {
		t.Fatal(err)
	}
	if p := version.Parameters; p["wait"].Timer != "PT10M" || p["notify"].Inputs["title"] != "Changed" || p["start"].Defaults["level"] != "7" || len(p["notify"].Inputs) != 1 {
		t.Errorf("changed values are parameters, unchanged ones and expressions are not: %+v", p)
	}
	var read models.Deployment
	e.call(alice, http.MethodGet, "/deployment/v3/deployments/"+deployed.Id+"?with_options=false", nil, &read)
	if elementById(read, "wait").TimeEvent.Time != "PT10M" || elementById(read, "notify").Notification.Title != "Changed" || read.StartParameter[0].Default != "7" || read.Diagram.Svg != "<svg/>" {
		t.Errorf("reading shows the deployed values: %+v", read)
	}
	var list []models.Deployment
	if e.call(alice, http.MethodGet, "/deployment/v3/deployments", nil, &list); len(list) != 1 || list[0].Id != deployed.Id {
		t.Errorf("%+v", list)
	}

	inst, err := e.srv.Controller.StartInstance(ctx, alice, "processes", model.StartInstanceInput{DeploymentId: deployed.Id})
	if err != nil {
		t.Fatal(err)
	}
	read.Name = "Levels 2"
	if status, _ = e.call(alice, http.MethodPut, "/deployment/v3/deployments/"+deployed.Id, read, &read); status != http.StatusOK || read.Name != "Levels 2" {
		t.Fatalf("update: %v %+v", status, read)
	}
	e.waitFor("the stopped instance", func() bool { return e.instance(inst.Id).State == model.StateTerminated })

	var dependencies []struct {
		DeploymentId string `json:"deployment_id"`
		Devices      []any
	}
	if e.call(alice, http.MethodGet, "/deployment/dependencies?ids="+deployed.Id+",unknown", nil, &dependencies); len(dependencies) != 1 || dependencies[0].DeploymentId != deployed.Id || len(dependencies[0].Devices) != 0 {
		t.Errorf("dependencies of the requested deployments: %+v", dependencies)
	}
	var ok bool
	if status, _ = e.call(alice, http.MethodDelete, "/deployment/v3/deployments/"+deployed.Id, nil, &ok); status != http.StatusOK || !ok {
		t.Errorf("delete: %v", status)
	}
}

func TestPreparedDeploymentOfAStoredModel(t *testing.T) {
	e := newEnv(t)
	var created struct {
		Id string `json:"_id"`
	}
	e.call(alice, http.MethodPost, "/repository/processes", map[string]any{"name": "Levels", "bpmn_xml": designerModel(), "svgXML": "<svg/>"}, &created)
	var parameters []models.ProcessStartParameter
	if status, _ := e.call(alice, http.MethodGet, "/deployment/v3/start-parameters/"+created.Id, nil, &parameters); status != http.StatusOK || len(parameters) != 1 || parameters[0].Id != "level" {
		t.Errorf("%v %+v", status, parameters)
	}
	var prepared models.Deployment
	if status, _ := e.call(alice, http.MethodGet, "/deployment/v3/prepared-deployments/"+created.Id+"?with_options=false", nil, &prepared); status != http.StatusOK || prepared.Name != "Levels" || len(prepared.Elements) != 2 {
		t.Errorf("%v %+v", status, prepared)
	}
}

func TestModelThatCannotRunIsRejected(t *testing.T) {
	e := newEnv(t)
	xml := b.Process(b.Start("start"), `<bpmn:serviceTask id="mail"><bpmn:extensionElements><camunda:connector><camunda:connectorId>mail-send</camunda:connectorId></camunda:connector></bpmn:extensionElements></bpmn:serviceTask>`,
		b.End("end"), b.Flow("start", "mail"), b.Flow("mail", "end"))
	deployment := models.Deployment{Version: 3, Name: "Mail", Diagram: models.Diagram{XmlRaw: xml, Svg: "<svg/>"}}
	if status, _ := e.call(alice, http.MethodPost, "/deployment/v3/deployments", deployment, nil); status != http.StatusBadRequest {
		t.Errorf("mail-send is named as unsupported: %v", status)
	}
}
