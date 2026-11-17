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
	"context"
	"html"
	"reflect"
	"slices"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	dsclient "github.com/SENERGY-Platform/device-selection/v2/pkg/client"
	dsmodel "github.com/SENERGY-Platform/device-selection/v2/pkg/model"
	"github.com/SENERGY-Platform/models/go/models"
)

const (
	setOn     = models.CONTROLLING_FUNCTION_PREFIX + "setOn"
	setBright = models.CONTROLLING_FUNCTION_PREFIX + "setBrightness"
	readTemp  = "urn:infai:ses:measuring-function:temperature"
	twoInOne  = `{"function":{"id":"` + setOn + `"},"additional_functions":[{"name":"brightness","function":{"id":"` + setBright + `"},"characteristic_id":"c","aspects":[{"id":"light"}]}]}`
)

// commandWithInputs is a device command with its payload and further task inputs
func commandWithInputs(id string, payload string, inputs ...string) string {
	xml := `<bpmn:serviceTask id="` + id + `" senergy:taskType="device_command"><bpmn:extensionElements><camunda:inputOutput><camunda:inputParameter name="payload">` + html.EscapeString(payload) + `</camunda:inputParameter>`
	for _, name := range inputs {
		xml += `<camunda:inputParameter name="` + name + `">1</camunda:inputParameter>`
	}
	return xml + `</camunda:inputOutput></bpmn:extensionElements></bpmn:serviceTask>`
}

func processOf(t *testing.T, tasks ...string) *bpmn.Process {
	parts := []string{b.Start("start")}
	parts = append(parts, tasks...)
	p, _, err := bpmn.Parse(b.Process(parts...))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func messagesOf(issues []model.Issue) []string {
	result := []string{}
	for _, i := range issues {
		result = append(result, i.ElementId+": "+i.Message)
	}
	return result
}

func TestValidatePayloadsReportsEachStaticProblemOfAdditionalFunctions(t *testing.T) {
	cases := []struct {
		name     string
		payload  string
		inputs   []string
		expected []string
	}{
		{"an entry without a name", `{"function":{"id":"` + setOn + `"},"additional_functions":[{"function":{"id":"` + setBright + `"}}]}`, nil,
			[]string{`task: additional function 1 needs a name`}},
		{"a name with a dot", `{"function":{"id":"` + setOn + `"},"additional_functions":[{"name":"a.b","function":{"id":"` + setBright + `"}}]}`, nil,
			[]string{`task: additional function "a.b": a name may not contain "." or ":"`}},
		{"a name with a colon", `{"function":{"id":"` + setOn + `"},"additional_functions":[{"name":"a:b","function":{"id":"` + setBright + `"}}]}`, nil,
			[]string{`task: additional function "a:b": a name may not contain "." or ":"`}},
		{"a name used twice", `{"function":{"id":"` + setOn + `"},"additional_functions":[{"name":"x","function":{"id":"` + setBright + `"}},{"name":"x","function":{"id":"` + setBright + `"}}]}`, nil,
			[]string{`task: additional function "x" is named twice`}},
		{"an entry without a function", `{"function":{"id":"` + setOn + `"},"additional_functions":[{"name":"x"}]}`, nil,
			[]string{`task: additional function "x" needs a function`}},
		{"a measuring function as additional function", `{"function":{"id":"` + setOn + `"},"additional_functions":[{"name":"x","function":{"id":"` + readTemp + `"}}]}`, nil,
			[]string{`task: a device command with additional functions takes only controlling functions, ` + readTemp + ` is not one`}},
		{"a measuring function as primary function", `{"function":{"id":"` + readTemp + `"},"additional_functions":[{"name":"x","function":{"id":"` + setBright + `"}}]}`, nil,
			[]string{`task: a device command with additional functions takes only controlling functions, ` + readTemp + ` is not one`}},
		{"an input without an entry", twoInOne, []string{"inputs:other"},
			[]string{`task: input "inputs:other" names no additional function`}},
		{"an input with a path without an entry", twoInOne, []string{"inputs:other.value"},
			[]string{`task: input "inputs:other.value" names no additional function`}},
		{"an input without additional functions is left to the runtime, as before", `{"function":{"id":"` + setOn + `"}}`, []string{"inputs:x"}, []string{}},
		{"configurables before version 3", `{"function":{"id":"` + setOn + `"},"configurables":[{"path":"duration"}],"additional_functions":[{"name":"x","function":{"id":"` + setBright + `"}}]}`, nil,
			[]string{`task: a device command with additional functions takes no configurables before version 3`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := messagesOf(ValidatePayloads(processOf(t, commandWithInputs("task", c.payload, c.inputs...))))
			if !reflect.DeepEqual(got, c.expected) {
				t.Errorf("expected %q, got %q", c.expected, got)
			}
		})
	}
}

func TestValidatePayloadsAcceptsAValidPayloadWithInputsOfItsEntries(t *testing.T) {
	issues := ValidatePayloads(processOf(t, commandWithInputs("task", twoInOne, "inputs:brightness", "inputs:brightness.value")))
	if len(issues) > 0 {
		t.Errorf("unexpected %v", messagesOf(issues))
	}
}

func TestValidatePayloadsAcceptsAPayloadWithoutAdditionalFunctions(t *testing.T) {
	issues := ValidatePayloads(processOf(t, commandWithInputs("task", `{"function":{"id":"`+readTemp+`"}}`)))
	if len(issues) > 0 {
		t.Errorf("unexpected %v", messagesOf(issues))
	}
}

// Device-selection answers the set of both criteria with every service of the device, each
// serving one of them; the command goes to one service, so only a service that serves both is
// offered.
func TestPrepareAsksForAllFunctionsAndKeepsOnlyServicesThatServeAll(t *testing.T) {
	light := models.AspectNode{Id: "light"}
	fake := &laneSelection{services: map[string][]dsmodel.PathOption{
		"s-both":   {{Path: "on", FunctionId: setOn, AspectNodes: []models.AspectNode{light}}, {Path: "bright", FunctionId: setBright, AspectNodes: []models.AspectNode{light}}},
		"s-on":     {{Path: "on", FunctionId: setOn, AspectNodes: []models.AspectNode{light}}},
		"s-bright": {{Path: "bright", FunctionId: setBright, AspectNodes: []models.AspectNode{light}}},
	}}
	recorder := &recordingSelection{laneSelection: fake}
	payload := `{"function":{"id":"` + setOn + `"},"aspects":[{"id":"light"}],"additional_functions":[{"name":"brightness","function":{"id":"` + setBright + `"},"characteristic_id":"c","aspects":[{"id":"light"}]}]}`
	elements, err := New(recorder, Config{}).Prepare(context.Background(), "token", processOf(t, commandWithInputs("task", payload)))
	if err != nil {
		t.Fatal(err)
	}
	if len(recorder.request) != 1 || len(recorder.request[0].Criteria) != 2 {
		t.Fatalf("expected one element asked with two criteria, got %+v", recorder.request)
	}
	asked := []string{recorder.request[0].Criteria[0].FunctionId, recorder.request[0].Criteria[1].FunctionId}
	if !reflect.DeepEqual(asked, []string{setOn, setBright}) {
		t.Errorf("asked for %v", asked)
	}
	if len(elements) != 1 || len(elements[0].Options) != 1 {
		t.Fatalf("unexpected %+v", elements)
	}
	ids := []string{}
	for _, s := range elements[0].Options[0].Services {
		ids = append(ids, s.Id)
	}
	if !slices.Equal(ids, []string{"s-both"}) {
		t.Errorf("expected only s-both, got %v", ids)
	}
	if elements[0].Criteria.FunctionId != setOn || len(elements[0].Criteria.AspectIds) != 1 {
		t.Errorf("the answer keeps the primary criteria: %+v", elements[0].Criteria)
	}
}

// recordingSelection is a laneSelection that remembers the request
type recordingSelection struct {
	*laneSelection
	request dsmodel.BulkRequestV2
}

func (this *recordingSelection) GetBulkSelectablesV2(ctx context.Context, token string, bulk dsmodel.BulkRequestV2, options *dsclient.GetBulkSelectablesOptions) (dsmodel.BulkResult, int, error) {
	this.request = bulk
	return this.laneSelection.GetBulkSelectablesV2(ctx, token, bulk, options)
}
