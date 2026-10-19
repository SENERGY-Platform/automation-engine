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
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	dsclient "github.com/SENERGY-Platform/device-selection/v2/pkg/client"
	dsmodel "github.com/SENERGY-Platform/device-selection/v2/pkg/model"
	"github.com/SENERGY-Platform/device-selection/v2/pkg/model/devicemodel"
	"github.com/SENERGY-Platform/models/go/models"
)

// laneSelection answers every element with one device that offers the given services, as
// device-selection does for the criteria set of a lane: the services of all criteria together.
type laneSelection struct {
	fakeSelection
	services map[string][]dsmodel.PathOption
}

func (this *laneSelection) GetBulkSelectablesV2(_ string, bulk dsmodel.BulkRequestV2, _ *dsclient.GetBulkSelectablesOptions) (dsmodel.BulkResult, int, error) {
	result := dsmodel.BulkResult{}
	for _, e := range bulk {
		device := &dsmodel.PermSearchDevice{DisplayName: "Device"}
		device.Id = "device"
		selectable := dsmodel.Selectable{Device: device, ServicePathOptions: this.services}
		for id := range this.services {
			selectable.Services = append(selectable.Services, devicemodel.Service{Id: id, Name: id})
		}
		result = append(result, dsmodel.BulkResultElement{Id: e.Id, Selectables: []dsmodel.Selectable{selectable}})
	}
	return result, 200, nil
}

func laneProcess(tasks map[string]string) string {
	refs := ""
	parts := []string{b.Start("start")}
	ids := make([]string, 0, len(tasks))
	for id := range tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		refs += `<bpmn:flowNodeRef>` + id + `</bpmn:flowNodeRef>`
		parts = append(parts, deviceTaskXml(id, tasks[id]), b.Flow("start", id))
	}
	parts = append([]string{`<bpmn:laneSet id="ls"><bpmn:lane id="lane">` + refs + `</bpmn:lane></bpmn:laneSet>`}, parts...)
	return b.Process(parts...)
}

// process-deployment offered each element of a lane only the services that serve its own
// criteria (ctrl/prepare_deployment.go serviceMatchesCriteria): the function, and every aspect
// by the aspect itself or a descendant on one path option.
func TestTasksOfALaneAreOfferedOnlyTheServicesOfTheirOwnCriteria(t *testing.T) {
	child := models.AspectNode{Id: "child", AncestorIds: []string{"parent"}}
	sibling := models.AspectNode{Id: "sibling", AncestorIds: []string{"parent"}}
	other := models.AspectNode{Id: "other"}
	cases := []struct {
		name     string
		tasks    map[string]string
		services map[string][]dsmodel.PathOption
		expected map[string][]string
	}{
		{
			name: "the service of the function of the task",
			tasks: map[string]string{
				"on":  `{"function":{"id":"f-on"}}`,
				"off": `{"function":{"id":"f-off"}}`,
			},
			services: map[string][]dsmodel.PathOption{
				"s-on":  {{Path: "on", FunctionId: "f-on"}},
				"s-off": {{Path: "off", FunctionId: "f-off"}},
			},
			expected: map[string][]string{"on": {"s-on"}, "off": {"s-off"}},
		},
		{
			name: "a service whose aspect is a descendant of the asked one",
			tasks: map[string]string{
				"inside":  `{"function":{"id":"f"},"aspects":[{"id":"parent"}]}`,
				"outside": `{"function":{"id":"f"},"aspects":[{"id":"other"}]}`,
			},
			services: map[string][]dsmodel.PathOption{
				"s-child": {{Path: "child", FunctionId: "f", AspectNode: child, AspectNodes: []models.AspectNode{child}}},
				"s-other": {{Path: "other", FunctionId: "f", AspectNode: other, AspectNodes: []models.AspectNode{other}}},
			},
			expected: map[string][]string{"inside": {"s-child"}, "outside": {"s-other"}},
		},
		{
			name: "a service with one path option carrying every asked aspect",
			tasks: map[string]string{
				"both":  `{"function":{"id":"f"},"aspects":[{"id":"child"},{"id":"sibling"}]}`,
				"child": `{"function":{"id":"f"},"aspects":[{"id":"child"}]}`,
			},
			services: map[string][]dsmodel.PathOption{
				"s-both":  {{Path: "both", FunctionId: "f", AspectNode: child, AspectNodes: []models.AspectNode{child, sibling}}},
				"s-child": {{Path: "child", FunctionId: "f", AspectNode: child, AspectNodes: []models.AspectNode{child}}},
			},
			expected: map[string][]string{"both": {"s-both"}, "child": {"s-both", "s-child"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, _, err := bpmn.Parse(laneProcess(c.tasks))
			if err != nil {
				t.Fatal(err)
			}
			elements, err := New(&laneSelection{services: c.services}, Config{}).Prepare("token", p)
			if err != nil {
				t.Fatal(err)
			}
			offered := map[string][]string{}
			for _, e := range elements {
				if len(e.Options) != 1 {
					t.Fatalf("%v: expected the device as only option, got %+v", e.ElementId, e.Options)
				}
				ids := []string{}
				for _, s := range e.Options[0].Services {
					ids = append(ids, s.Id)
				}
				sort.Strings(ids)
				offered[e.ElementId] = ids
			}
			if !reflect.DeepEqual(offered, c.expected) {
				t.Errorf("expected %v, got %v", format(c.expected), format(offered))
			}
		})
	}
}

// A task outside a lane is asked with its own criteria alone, so the answer of device-selection
// is offered as it is.
func TestATaskOutsideALaneKeepsTheServicesOfTheAnswer(t *testing.T) {
	p, _, err := bpmn.Parse(b.Process(b.Start("start"), deviceTaskXml("off", `{"function":{"id":"f-off"}}`), b.Flow("start", "off")))
	if err != nil {
		t.Fatal(err)
	}
	services := map[string][]dsmodel.PathOption{
		"s-on":  {{Path: "on", FunctionId: "f-on"}},
		"s-off": {{Path: "off", FunctionId: "f-off"}},
	}
	elements, err := New(&laneSelection{services: services}, Config{}).Prepare("token", p)
	if err != nil {
		t.Fatal(err)
	}
	if len(elements) != 1 || elements[0].Lane != "" || len(elements[0].Options) != 1 {
		t.Fatalf("expected one task outside a lane with one option, got %+v", elements)
	}
	if len(elements[0].Options[0].Services) != 2 {
		t.Errorf("expected both services of the answer, got %+v", elements[0].Options[0].Services)
	}
}

func format(services map[string][]string) string {
	keys := make([]string, 0, len(services))
	for k := range services {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := []string{}
	for _, k := range keys {
		parts = append(parts, k+": "+strings.Join(services[k], ","))
	}
	return strings.Join(parts, "; ")
}
