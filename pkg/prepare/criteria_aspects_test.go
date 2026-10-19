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
	"html"
	"reflect"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
)

// element parses a process with the given element behind the start event
func element(t *testing.T, id string, xml string) *bpmn.Element {
	t.Helper()
	p, _, err := bpmn.Parse(b.Process(b.Start("start"), xml, b.Flow("start", id)))
	if err != nil {
		t.Fatal(err)
	}
	return p.Element(id)
}

func deviceEvent(t *testing.T, attributes string) *bpmn.Element {
	return element(t, "event", `<bpmn:intermediateCatchEvent id="event" senergy:function="f" `+attributes+`><bpmn:messageEventDefinition/></bpmn:intermediateCatchEvent>`)
}

func deviceTask(t *testing.T, payload string) *bpmn.Element {
	return element(t, "task", deviceTaskXml("task", payload))
}

func TestCriteriaOfADeviceEventReadTheAspectAttributes(t *testing.T) {
	cases := []struct {
		name       string
		attributes string
		expected   []string
	}{
		{"reads the ids of a comma separated aspect list", `senergy:aspects="a,b"`, []string{"a", "b"}},
		{"keeps the ids in the order they were modeled", `senergy:aspects="b,a"`, []string{"b", "a"}},
		{"trims whitespace and drops empty entries", `senergy:aspects=" a , ,b, "`, []string{"a", "b"}},
		{"reads the deprecated single aspect as a list with one element", `senergy:aspect="a"`, []string{"a"}},
		{"adds a deprecated aspect the list does not name", `senergy:aspects="a" senergy:aspect="b"`, []string{"a", "b"}},
		{"does not repeat a deprecated aspect the list already names", `senergy:aspects="a,b" senergy:aspect="a"`, []string{"a", "b"}},
		{"returns no aspects for empty attributes", `senergy:aspect="" senergy:aspects=""`, nil},
		{"returns no aspects for missing attributes", ``, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			criteria, ok := CriteriaOf(deviceEvent(t, c.attributes))
			if !ok {
				t.Fatal("a device event has criteria")
			}
			if !reflect.DeepEqual(criteria.AspectIds, c.expected) {
				t.Errorf("expected %v, got %v", c.expected, criteria.AspectIds)
			}
		})
	}
}

func TestCriteriaOfADeviceTaskReadTheAspectsOfThePayload(t *testing.T) {
	cases := []struct {
		name     string
		payload  string
		expected []string
	}{
		{"reads the deprecated single aspect as a list with one element", `{"function":{"id":"f"},"aspect":{"id":"a"}}`, []string{"a"}},
		{"reads the aspect list", `{"function":{"id":"f"},"aspects":[{"id":"a"},{"id":"b"}]}`, []string{"a", "b"}},
		{"does not repeat a deprecated aspect the list already names", `{"function":{"id":"f"},"aspect":{"id":"a"},"aspects":[{"id":"a"},{"id":"b"}]}`, []string{"a", "b"}},
		{"treats an empty single aspect as no aspect", `{"function":{"id":"f"},"aspect":{"id":""}}`, nil},
		{"returns no aspects for a payload without one", `{"function":{"id":"f"},"aspect":null}`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			criteria, ok := CriteriaOf(deviceTask(t, c.payload))
			if !ok {
				t.Fatal("a device task with a function has criteria")
			}
			if !reflect.DeepEqual(criteria.AspectIds, c.expected) {
				t.Errorf("expected %v, got %v", c.expected, criteria.AspectIds)
			}
		})
	}
}

func TestCriteriaOfADeviceTaskWithoutFunctionAreMissing(t *testing.T) {
	if _, ok := CriteriaOf(deviceTask(t, `{"aspect":{"id":"a"}}`)); ok {
		t.Error("a payload without function names no criteria")
	}
}

// The bulk request is where the criteria are copied field by field into the shape
// device-selection is asked with; a field left out there is lost without error.
func TestBulkRequestCarriesTheAspectListOfTasksAndEvents(t *testing.T) {
	xml := b.Process(
		b.Start("start"),
		deviceTaskXml("task", `{"function":{"id":"f"},"aspects":[{"id":"parent"},{"id":"child"}]}`),
		`<bpmn:intermediateCatchEvent id="event" senergy:function="f" senergy:aspects="parent,child"><bpmn:messageEventDefinition/></bpmn:intermediateCatchEvent>`,
		b.Flow("start", "task"), b.Flow("task", "event"),
	)
	p, _, err := bpmn.Parse(xml)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeSelection{}
	if _, err = New(fake, Config{}).Prepare("token", p); err != nil {
		t.Fatal(err)
	}
	asked := map[string][]string{}
	for _, r := range fake.request {
		if len(r.Criteria) != 1 {
			t.Fatalf("expected one criteria per element, got %+v", r)
		}
		asked[r.Id] = r.Criteria[0].AspectIds
	}
	expected := map[string][]string{"task": {"parent", "child"}, "event": {"parent", "child"}}
	if !reflect.DeepEqual(asked, expected) {
		t.Errorf("expected %v, got %v", expected, asked)
	}
}

func deviceTaskXml(id string, payload string) string {
	return `<bpmn:serviceTask id="` + id + `" senergy:taskType="device_command"><bpmn:extensionElements><camunda:inputOutput><camunda:inputParameter name="payload">` +
		html.EscapeString(payload) + `</camunda:inputParameter></camunda:inputOutput></bpmn:extensionElements></bpmn:serviceTask>`
}
