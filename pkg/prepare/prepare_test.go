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

package prepare

import (
	"encoding/json"
	"html"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	dsclient "github.com/SENERGY-Platform/device-selection/v2/pkg/client"
	dsmodel "github.com/SENERGY-Platform/device-selection/v2/pkg/model"
	"github.com/SENERGY-Platform/device-selection/v2/pkg/model/devicemodel"
	"github.com/SENERGY-Platform/models/go/models"
)

func command(id string, function string, aspect string) string {
	payload := `{"version":2,"function":{"id":"` + function + `"},"characteristic_id":"c","aspect":{"id":"` + aspect + `"}}`
	return `<bpmn:serviceTask id="` + id + `" senergy:taskType="device_command"><bpmn:extensionElements><camunda:inputOutput><camunda:inputParameter name="payload">` + html.EscapeString(payload) + `</camunda:inputParameter></camunda:inputOutput></bpmn:extensionElements></bpmn:serviceTask>`
}

func testProcess(t *testing.T) *bpmn.Process {
	xml := b.Process(
		`<bpmn:laneSet id="ls"><bpmn:lane id="lane1"><bpmn:flowNodeRef>on</bpmn:flowNodeRef><bpmn:flowNodeRef>off</bpmn:flowNodeRef></bpmn:lane></bpmn:laneSet>`,
		b.Start("start"), command("on", "f-on", "a1"), command("off", "f-off", "a1"), command("alone", "f-get", "a2"),
		`<bpmn:intermediateCatchEvent id="ev" senergy:function="f-event" senergy:aspects="a1, a3" senergy:characteristic="ch" senergy:script="x &gt; 1"><bpmn:messageEventDefinition/></bpmn:intermediateCatchEvent>`,
		b.Script("script", "x", "1"),
		b.Flow("start", "on"), b.Flow("on", "off"), b.Flow("off", "alone"), b.Flow("alone", "ev"), b.Flow("ev", "script"),
	)
	p, issues, err := bpmn.ParseAndValidate(xml, bpmn.Rules{})
	if err != nil || len(issues) > 0 {
		t.Fatal(err, issues)
	}
	return p
}

type fakeSelection struct {
	request dsmodel.BulkRequestV2
}

func (this *fakeSelection) GetSelectables(string, []models.DeviceGroupFilterCriteria, *dsclient.GetSelectablesOptions) ([]dsmodel.Selectable, int, error) {
	return nil, 200, nil
}
func (this *fakeSelection) DeviceGroupHelper(string, []string, *dsclient.DeviceGroupHelperOptions) (dsmodel.DeviceGroupHelperResult, int, error) {
	return dsmodel.DeviceGroupHelperResult{}, 200, nil
}
func (this *fakeSelection) GetBulkSelectablesV2(_ string, bulk dsmodel.BulkRequestV2, _ *dsclient.GetBulkSelectablesOptions) (dsmodel.BulkResult, int, error) {
	this.request = bulk
	result := dsmodel.BulkResult{}
	for _, e := range bulk {
		device := &dsmodel.PermSearchDevice{DisplayName: "Lamp " + e.Id}
		device.Id = "device-" + e.Id
		// like device-selection, the service has a path option for each criteria it serves
		paths := []dsmodel.PathOption{}
		for _, c := range e.Criteria {
			path := dsmodel.PathOption{Path: "value.on", CharacteristicId: "c", FunctionId: c.FunctionId}
			for _, a := range c.AspectIds {
				path.AspectNodes = append(path.AspectNodes, models.AspectNode{Id: a})
			}
			paths = append(paths, path)
		}
		result = append(result, dsmodel.BulkResultElement{Id: e.Id, Selectables: []dsmodel.Selectable{{
			Device:             device,
			Services:           []devicemodel.Service{{Id: "s1", Name: "set"}},
			ServicePathOptions: map[string][]dsmodel.PathOption{"s1": paths},
		}}})
	}
	return result, 200, nil
}

func TestPrepareFindsCriteriaAndSharesThemPerLane(t *testing.T) {
	fake := &fakeSelection{}
	elements, err := New(fake, Config{ImportsForEvents: true}).Prepare("token", testProcess(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(elements) != 4 {
		t.Fatalf("three tasks and one device event, got %+v", elements)
	}
	byId := map[string]model.PreparedElement{}
	for _, e := range elements {
		byId[e.ElementId] = e
	}
	if byId["ev"].Kind != model.PreparedEvent || byId["ev"].Criteria.FunctionId != "f-event" || len(byId["ev"].Criteria.AspectIds) != 2 {
		t.Errorf("event criteria come from the senergy attributes: %+v", byId["ev"])
	}
	if byId["on"].Lane != "lane1" || byId["alone"].Lane != "" {
		t.Errorf("lanes: %+v", elements)
	}
	requests := map[string]dsmodel.BulkRequestElementV2{}
	for _, r := range fake.request {
		requests[r.Id] = r
	}
	if len(requests["on"].Criteria) != 2 || len(requests["alone"].Criteria) != 1 {
		t.Errorf("tasks of a lane share all their criteria: %+v", requests)
	}
	if requests["ev"].Criteria[0].Interaction != "event" || !requests["ev"].IncludeImports || requests["on"].IncludeImports {
		t.Errorf("events ask for event services and imports: %+v", requests["ev"])
	}
	option := byId["on"].Options[0]
	if option.Device.Name != "Lamp on" || option.Services[0].Id != "s1" {
		t.Errorf("unexpected option %+v", option)
	}
	var paths []map[string]any
	if err = json.Unmarshal(option.PathOptions["s1"], &paths); err != nil || paths[0]["path"] != "value.on" || paths[0]["characteristicId"] != "c" {
		t.Errorf("path options are passed on unchanged: %s %v", option.PathOptions["s1"], err)
	}
}

func TestValidateSelections(t *testing.T) {
	p := testProcess(t)
	issues := ValidateSelections(p, map[string]model.Selection{
		"on":     {DeviceId: "d", ServiceId: "s"},
		"ev":     {ImportId: "i"},
		"script": {DeviceId: "d"},
		"off":    {DeviceId: "d", DeviceGroupId: "g"},
		"alone":  {ImportId: "i"},
	})
	expected := map[string]bool{"script": true, "off": true, "alone": true}
	if len(issues) != 3 {
		t.Fatalf("unexpected %v", issues)
	}
	for _, issue := range issues {
		if !expected[issue.ElementId] {
			t.Errorf("unexpected issue %v", issue)
		}
	}
}
