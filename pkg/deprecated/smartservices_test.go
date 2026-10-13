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

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
)

// smartServiceDesign is a design as the old smart-service designer wrote it: a start form and
// an info module through an external task topic
func smartServiceDesign(moduleType string) string {
	return b.Process(
		b.StartWithForm("start", "room=kitchen"),
		`<bpmn:serviceTask id="info" camunda:type="external" camunda:topic="info"><bpmn:extensionElements><camunda:inputOutput>`+
			`<camunda:inputParameter name="info.module_type">`+moduleType+`</camunda:inputParameter>`+
			`<camunda:inputParameter name="info.module_data">{"room":"{{.room}}"}</camunda:inputParameter>`+
			`</camunda:inputOutput></bpmn:extensionElements></bpmn:serviceTask>`,
		b.End("end"),
		b.Flow("start", "info"), b.Flow("info", "end"),
	)
}

type ssInstance struct {
	Id           string
	Name         string
	ReleaseId    string `json:"release_id"`
	DesignId     string `json:"design_id"`
	NewReleaseId string `json:"new_release_id"`
	Ready        bool
	Error        string
	Parameters   []struct {
		Id    string
		Value any
	}
}

// The smart-service pages of the web-ui: design, release, instance with parameters and modules,
// a new release and the move of the instance onto it (SMS-03, SMS-09).
func TestSmartServicesOfTheWebUi(t *testing.T) {
	e := newEnv(t)
	var design struct {
		Id      string
		UserId  string `json:"user_id"`
		BpmnXml string `json:"bpmn_xml"`
	}
	status, _ := e.call(alice, http.MethodPost, "/smart-services/designs", map[string]any{"name": "Room info", "bpmn_xml": smartServiceDesign("widget"), "svg_xml": "<svg/>"}, &design)
	if status != http.StatusOK || design.Id == "" || design.UserId != "alice" || !strings.Contains(design.BpmnXml, `camunda:topic="info"`) {
		t.Fatalf("the design keeps the format of the old designer: %v %+v", status, design)
	}
	var release struct {
		Id           string
		DesignId     string `json:"design_id"`
		Creator      string
		NewReleaseId string `json:"new_release_id"`
	}
	if status, _ = e.call(alice, http.MethodPost, "/smart-services/releases", map[string]any{"design_id": design.Id}, &release); status != http.StatusOK || release.DesignId != design.Id || release.Creator != "alice" {
		t.Fatalf("release: %v %+v", status, release)
	}
	var extended []struct {
		Id         string
		ParsedInfo struct {
			ParameterDescriptions []struct{ Id, Label string } `json:"parameter_descriptions"`
		} `json:"parsed_info"`
		PermissionsInfo struct {
			Shared      bool
			Permissions map[string]bool
		} `json:"permissions_info"`
	}
	status, header := e.call(alice, http.MethodGet, "/smart-services/extended-releases?latest=true&limit=10&offset=0", nil, &extended)
	if status != http.StatusOK || len(extended) != 1 || header.Get("X-Total-Count") != "1" || len(extended[0].ParsedInfo.ParameterDescriptions) != 1 || !extended[0].PermissionsInfo.Permissions["a"] {
		t.Fatalf("extended releases: %v %+v", status, extended)
	}
	var parameters []struct {
		Id           string
		DefaultValue any `json:"default_value"`
	}
	if e.call(alice, http.MethodGet, "/smart-services/releases/"+release.Id+"/parameters", nil, &parameters); len(parameters) != 1 || parameters[0].Id != "room" {
		t.Errorf("start parameters of the release: %+v", parameters)
	}
	var instance ssInstance
	status, _ = e.call(alice, http.MethodPost, "/smart-services/releases/"+release.Id+"/instances", map[string]any{"name": "Kitchen", "parameters": []map[string]any{{"id": "room", "value": "kitchen"}}}, &instance)
	if status != http.StatusOK || instance.Name != "Kitchen" || instance.ReleaseId != release.Id || instance.DesignId != design.Id {
		t.Fatalf("instance: %v %+v", status, instance)
	}
	e.waitFor("the ready instance", func() bool {
		e.call(alice, http.MethodGet, "/smart-services/instances/"+instance.Id, nil, &instance)
		return instance.Ready
	})
	var modules []struct {
		ModuleType string         `json:"module_type"`
		ModuleData map[string]any `json:"module_data"`
		ReleaseId  string         `json:"release_id"`
	}
	if e.call(alice, http.MethodGet, "/smart-services/modules?instance_id="+instance.Id, nil, &modules); len(modules) != 1 || modules[0].ModuleType != "widget" || modules[0].ModuleData["room"] != "kitchen" || modules[0].ReleaseId != release.Id {
		t.Errorf("modules of the instance: %+v", modules)
	}

	e.call(alice, http.MethodPut, "/smart-services/designs/"+design.Id, map[string]any{"name": "Room info", "bpmn_xml": smartServiceDesign("tile"), "svg_xml": "<svg/>"}, nil)
	var second struct{ Id string }
	if e.call(alice, http.MethodPost, "/smart-services/releases", map[string]any{"design_id": design.Id}, &second); second.Id != release.Id {
		t.Fatalf("a further release of the design is a new version of its deployment: %+v", second)
	}
	e.call(alice, http.MethodGet, "/smart-services/instances/"+instance.Id, nil, &instance)
	if instance.ReleaseId != release.Id+":v1" || instance.NewReleaseId != release.Id {
		t.Errorf("the instance keeps the older version and sees the new release: %+v", instance)
	}
	var all []struct {
		Id           string
		NewReleaseId string `json:"new_release_id"`
	}
	if e.call(alice, http.MethodGet, "/smart-services/releases", nil, &all); len(all) != 2 || all[1].Id != release.Id+":v1" || all[1].NewReleaseId != release.Id {
		t.Errorf("without latest, older versions are releases too: %+v", all)
	}
	var moved ssInstance
	if status, _ = e.call(alice, http.MethodPut, "/smart-services/instances/"+instance.Id+"/parameters?release_id="+release.Id, []map[string]any{{"id": "room", "value": "hall"}}, &moved); status != http.StatusOK {
		t.Fatalf("parameters: %v", status)
	}
	if moved.ReleaseId != release.Id || moved.NewReleaseId != "" {
		t.Errorf("the instance moved to the current release: %+v", moved)
	}
	e.waitFor("the new module", func() bool {
		e.call(alice, http.MethodGet, "/smart-services/modules?instance_id="+instance.Id, nil, &modules)
		return len(modules) == 1 && modules[0].ModuleType == "tile" && modules[0].ModuleData["room"] == "hall"
	})
	if e.call(alice, http.MethodPut, "/smart-services/instances/"+instance.Id+"/info", map[string]any{"name": "Hall", "description": "moved"}, &instance); instance.Name != "Hall" {
		t.Errorf("info: %+v", instance)
	}
	var list []ssInstance
	if status, header = e.call(alice, http.MethodGet, "/smart-services/instances?release-id="+release.Id, nil, &list); len(list) != 1 || list[0].Name != "Hall" || header.Get("X-Total-Count") != "1" {
		t.Errorf("instances of a release: %+v", list)
	}
	if status, _ = e.call(alice, http.MethodDelete, "/smart-services/releases/"+release.Id, nil, nil); status != http.StatusBadRequest {
		t.Errorf("a release with instances stays: %v", status)
	}
	if status, _ = e.call(alice, http.MethodDelete, "/smart-services/instances/"+instance.Id, nil, nil); status != http.StatusOK {
		t.Fatal(status)
	}
	if status, _ = e.call(alice, http.MethodDelete, "/smart-services/releases/"+release.Id, nil, nil); status != http.StatusOK {
		t.Errorf("delete release: %v", status)
	}
	if _, err := e.srv.Controller.GetDeployment(ctx, alice, "smart-services", release.Id); err == nil {
		t.Error("the deployment of the release is gone")
	}
}

// processDeploymentDesign is a design of the old designer that deploys a process model
func processDeploymentDesign(modelId string) string {
	return b.Process(
		b.StartWithForm("start", "room=kitchen"),
		`<bpmn:serviceTask id="deploy" camunda:type="external" camunda:topic="process_deployment"><bpmn:extensionElements><camunda:inputOutput>`+
			`<camunda:inputParameter name="process_deployment.process_model_id">`+modelId+`</camunda:inputParameter>`+
			`</camunda:inputOutput></bpmn:extensionElements></bpmn:serviceTask>`,
		b.End("end"),
		b.Flow("start", "deploy"), b.Flow("deploy", "end"),
	)
}

// process-model-repository, analytics-flow-repo-v2 and import-repository ask who uses a resource
// before they delete it; a release id a client chose is not taken over from another release
// (SMS-15).
func TestResourceUsageAndInstanceCountOfReleases(t *testing.T) {
	e := newEnv(t)
	var design struct{ Id string }
	e.call(alice, http.MethodPost, "/smart-services/designs", map[string]any{"name": "Deployer", "bpmn_xml": processDeploymentDesign("model-1"), "svg_xml": "<svg/>"}, &design)
	var release struct{ Id string }
	status, _ := e.call(alice, http.MethodPost, "/smart-services/releases", map[string]any{"id": "chosen", "design_id": design.Id}, &release)
	if status != http.StatusOK || release.Id != "chosen" {
		t.Fatalf("a release with a chosen id: %v %+v", status, release)
	}
	var other, foreign struct{ Id string }
	e.call(alice, http.MethodPost, "/smart-services/designs", map[string]any{"name": "Other", "bpmn_xml": smartServiceDesign("widget"), "svg_xml": "<svg/>"}, &other)
	if status, _ = e.call(alice, http.MethodPost, "/smart-services/releases", map[string]any{"id": "chosen", "design_id": other.Id}, nil); status != http.StatusConflict {
		t.Fatalf("the id of another release is not reused: %v", status)
	}
	e.call(bob, http.MethodPost, "/smart-services/designs", map[string]any{"name": "Foreign", "bpmn_xml": smartServiceDesign("widget"), "svg_xml": "<svg/>"}, &foreign)
	if status, _ = e.call(bob, http.MethodPost, "/smart-services/releases", map[string]any{"id": "chosen", "design_id": foreign.Id}, nil); status == http.StatusOK {
		t.Fatal("nor taken over by another user")
	}
	var kept struct {
		DesignId string `json:"design_id"`
	}
	if e.call(alice, http.MethodGet, "/smart-services/releases/chosen", nil, &kept); kept.DesignId != design.Id {
		t.Fatalf("the release keeps its design: %+v", kept)
	}

	var instance ssInstance
	if status, _ = e.call(alice, http.MethodPost, "/smart-services/releases/chosen/instances", map[string]any{"name": "Kitchen"}, &instance); status != http.StatusOK {
		t.Fatalf("instance: %v", status)
	}
	var count struct{ Count int }
	if status, _ = e.call(alice, http.MethodGet, "/smart-services/releases/chosen/instance-count", nil, &count); status != http.StatusOK || count.Count != 1 {
		t.Fatalf("instance count: %v %+v", status, count)
	}
	if status, _ = e.call(bob, http.MethodGet, "/smart-services/releases/chosen/instance-count", nil, nil); status == http.StatusOK {
		t.Fatal("only administrators of the release count its instances")
	}

	var usage struct {
		Releases  int
		Instances int
		Readable  []struct {
			Id       string
			DesignId string `json:"design_id"`
		}
	}
	status, _ = e.call(alice, http.MethodGet, "/smart-services/resource-usage/process-models/model-1", nil, &usage)
	if status != http.StatusOK || usage.Releases != 1 || usage.Instances != 1 || len(usage.Readable) != 1 || usage.Readable[0].Id != "chosen" || usage.Readable[0].DesignId != design.Id {
		t.Fatalf("usage of the model: %v %+v", status, usage)
	}
	if status, _ = e.call(bob, http.MethodGet, "/smart-services/resource-usage/process-models/model-1", nil, &usage); status != http.StatusOK || usage.Releases != 1 || len(usage.Readable) != 0 {
		t.Fatalf("others learn the count only: %v %+v", status, usage)
	}
	if status, _ = e.call(alice, http.MethodGet, "/smart-services/resource-usage/pipelines/p1", nil, nil); status != http.StatusBadRequest {
		t.Fatalf("an unknown kind: %v", status)
	}
}
