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
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/models/go/models"
)

// preparedElement is what the deploy dialog shows of an element, independent of the spelling
// of aliases: the deprecated single aspect is folded into the aspect list.
type preparedElement struct {
	BpmnId         string
	Name           string
	Group          string
	Order          int64
	Kind           string
	Retries        int64
	FunctionId     string
	Characteristic string
	DeviceClassId  string
	AspectIds      []string
	Parameter      map[string]string
	Script         string
	ValueVariable  string
	Qos            int
	Variables      map[string]string
	Time           string
	Title          string
	Message        string
}

func value(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func criteriaOf(e *preparedElement, c models.ProcessFilterCriteria) {
	e.FunctionId, e.Characteristic, e.DeviceClassId = value(c.FunctionId), value(c.CharacteristicId), value(c.DeviceClassId)
	e.AspectIds = slices.Clone(c.AspectIds)
	if a := value(c.AspectId); a != "" && !slices.Contains(e.AspectIds, a) {
		e.AspectIds = append(e.AspectIds, a)
	}
}

func project(d models.Deployment) []preparedElement {
	result := []preparedElement{}
	for _, element := range d.Elements {
		// a group key only has to be the same within a lane; process-deployment prefixed it
		e := preparedElement{BpmnId: element.BpmnId, Name: element.Name, Group: strings.TrimPrefix(value(element.Group), "lane:"), Order: element.Order}
		switch {
		case element.Task != nil:
			e.Kind, e.Retries = "task", element.Task.Retries
			criteriaOf(&e, element.Task.Selection.FilterCriteria)
			// the parser of process-deployment offered the inputs.* parameters of a device task
			for name, v := range element.Task.Parameter {
				if strings.HasPrefix(name, "inputs") {
					if e.Parameter == nil {
						e.Parameter = map[string]string{}
					}
					e.Parameter[name] = v
				}
			}
		case element.ConditionalEvent != nil:
			e.Kind = "conditional_event"
			criteriaOf(&e, element.ConditionalEvent.Selection.FilterCriteria)
			e.Script, e.ValueVariable, e.Qos = element.ConditionalEvent.Script, element.ConditionalEvent.ValueVariable, element.ConditionalEvent.Qos
			if len(element.ConditionalEvent.Variables) > 0 {
				e.Variables = element.ConditionalEvent.Variables
			}
		case element.MessageEvent != nil:
			e.Kind = "message_event"
			criteriaOf(&e, element.MessageEvent.Selection.FilterCriteria)
		case element.TimeEvent != nil:
			e.Kind, e.Time = "time_event:"+element.TimeEvent.Type, element.TimeEvent.Time
		case element.Notification != nil:
			e.Kind, e.Title, e.Message = "notification", element.Notification.Title, element.Notification.Message
		}
		result = append(result, e)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].BpmnId < result[j].BpmnId })
	return result
}

func readFixture[T any](t *testing.T, path string) T {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result T
	if err = json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func prepareFixture(t *testing.T, e *env, xml string) models.Deployment {
	t.Helper()
	var prepared models.Deployment
	if status, _ := e.call(alice, http.MethodPost, "/deployment/v3/prepared-deployments?with_options=false", map[string]string{"xml": xml, "svg": "<svg/>"}, &prepared); status != http.StatusOK {
		t.Fatalf("prepare: %v", status)
	}
	return prepared
}

// checkFixture prepares the model of a parser case of process-deployment
// (lib/tests/resources/<name>/raw.bpmn) and compares the elements with those its parser found
// (parsed.json), except the dropped ones
func checkFixture(t *testing.T, e *env, name string, dropped ...string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata/prepared", name, "raw.bpmn"))
	if err != nil {
		t.Fatal(err)
	}
	expected := slices.DeleteFunc(project(readFixture[models.Deployment](t, filepath.Join("testdata/prepared", name, "parsed.json"))),
		func(e preparedElement) bool { return slices.Contains(dropped, e.BpmnId) })
	actual := slices.DeleteFunc(project(prepareFixture(t, e, string(raw))),
		func(e preparedElement) bool { return slices.Contains(dropped, e.BpmnId) })
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("elements differ:\n%s", differences(actual, expected))
	}
}

// The deploy dialog offers the elements the parser of process-deployment found: device tasks
// and events with their criteria, lanes as groups, timers and notifications. Not compared are
// use_marshaller and generic-source-event, whose message events of the analytics pipeline are
// dropped (EVT-03); prepared_elements_compat_test.go holds the cases that needed a change.
func TestPreparedDeploymentOffersTheElementsOfTheOldParser(t *testing.T) {
	e := newEnv(t)
	cases := map[string][]string{
		"0pools0lanes1group":           nil,
		"0pools0lanes1task_filtered":   nil,
		"0pools0lanes2tasks":           nil,
		"0pools0lanes2tasks_snrgy1807": nil,
		"1pool2lanes4tasks":            nil,
		// only the executable process runs (BPMN-01); this task is in the pool of another one
		"2pools3lanes5tasks":    {"Task_0kdvhvm"},
		"conditional_event_3":   nil,
		"conditional_event_qos": nil,
		"parameter-no-restart":  nil,
		"parameter-restart":     nil,
		"task_aspect_list":      nil,
		"without_options":       nil,
	}
	for name, dropped := range cases {
		t.Run(name, func(t *testing.T) { checkFixture(t, e, name, dropped...) })
	}
}

// The start parameters of the old parser case estimate_start_parameter.
func TestPreparedDeploymentOffersTheStartParametersOfTheOldParser(t *testing.T) {
	e := newEnv(t)
	raw, err := os.ReadFile("testdata/start_parameters/raw.bpmn")
	if err != nil {
		t.Fatal(err)
	}
	expected := readFixture[[]models.ProcessStartParameter](t, "testdata/start_parameters/expected_start_parameters.json")
	actual := prepareFixture(t, e, string(raw)).StartParameter
	byId := func(list []models.ProcessStartParameter) {
		sort.Slice(list, func(i, j int) bool { return list[i].Id < list[j].Id })
	}
	byId(expected)
	byId(actual)
	if !reflect.DeepEqual(actual, expected) {
		t.Errorf("got %+v, expected %+v", actual, expected)
	}
}

// differences names the elements and fields in which actual and expected differ
func differences(actual []preparedElement, expected []preparedElement) string {
	index := func(list []preparedElement) map[string]preparedElement {
		result := map[string]preparedElement{}
		for _, e := range list {
			result[e.BpmnId] = e
		}
		return result
	}
	a, x := index(actual), index(expected)
	ids := []string{}
	for id := range a {
		ids = append(ids, id)
	}
	for id := range x {
		if _, ok := a[id]; !ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	lines := []string{}
	for _, id := range ids {
		ae, inActual := a[id]
		xe, inExpected := x[id]
		switch {
		case !inActual:
			lines = append(lines, id+": missing")
		case !inExpected:
			lines = append(lines, id+": unexpected")
		default:
			av, xv := reflect.ValueOf(ae), reflect.ValueOf(xe)
			for i := 0; i < av.NumField(); i++ {
				if !reflect.DeepEqual(av.Field(i).Interface(), xv.Field(i).Interface()) {
					lines = append(lines, fmt.Sprintf("%v.%v: got %v, expected %v", id, av.Type().Field(i).Name, av.Field(i).Interface(), xv.Field(i).Interface()))
				}
			}
		}
	}
	return strings.Join(lines, "\n")
}
