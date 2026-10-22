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

package devicecommand

import (
	"errors"
	"html"
	"reflect"
	"strings"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/marshaller/lib/configurables"
	"github.com/SENERGY-Platform/models/go/models"
)

// processWithRetries is process with senergy:retries on the task
func processWithRetries(retries string, payloadJson string) string {
	return b.Process(
		b.Start("start"),
		`<bpmn:serviceTask id="cmd" senergy:taskType="device_command" senergy:completionStrategy="pessimistic" senergy:retries="`+retries+`">`+
			`<bpmn:extensionElements><camunda:inputOutput><camunda:inputParameter name="payload">`+html.EscapeString(payloadJson)+`</camunda:inputParameter>`+
			`</camunda:inputOutput></bpmn:extensionElements></bpmn:serviceTask>`,
		b.End("end"), b.Flow("start", "cmd"), b.Flow("cmd", "end"),
	)
}

func aspectIds(nodes []models.AspectNode) []string {
	result := []string{}
	for _, n := range nodes {
		result = append(result, n.Id)
	}
	return result
}

func TestOverwriteSendsTheCommandToTheGivenDevice(t *testing.T) {
	e := newEnv(t, Parallel)
	extra := `<camunda:inputParameter name="overwrite">{"device_id":"d2","service_id":"s-set","protocol_id":"p"}</camunda:inputParameter>`
	d := e.deploy(process(Optimistic, payload(controlling, map[string]any{"input": true}), extra), nil)
	e.start(d)
	list := e.producer.messages()
	if len(list) != 1 || list[0].key != "d2" || list[0].msg.Metadata.Service.Id != "s-set" {
		t.Fatalf("the overwrite names device and service of a payload without them (TASK-09): %+v", list)
	}
}

func TestOverwrittenCommandCompletesWithTheAnswerOfThatDevice(t *testing.T) {
	e := newEnv(t, Parallel)
	extra := `<camunda:inputParameter name="overwrite">{"device_id":"d2","service_id":"s-get","protocol_id":"p"}</camunda:inputParameter>`
	d := e.deploy(process(Pessimistic, payload(measuring, nil), extra), nil)
	inst := e.start(d)
	e.respond(e.producer.messages()[0], 21.5)
	e.expectState(inst.Id, model.StateCompleted)
	if out, ok := e.variable(inst.Id, "out").([]any); !ok || len(out) != 1 || out[0] != 21.5 {
		t.Fatalf("the answer of the overwritten device is the result: %#v", e.variable(inst.Id, "out"))
	}
}

func TestInputsReplacesTheWholeInput(t *testing.T) {
	e := newEnv(t, Parallel)
	m := &recordingMarshaller{}
	e.use(newRepo(), e.aspects, m, Parallel)
	extra := `<camunda:inputParameter name="inputs">"#ff00ff"</camunda:inputParameter>`
	d := e.deploy(process(Optimistic, payload(controlling, map[string]any{"device_id": "d1", "service_id": "s-set", "input": "#ff0064"}), extra), nil)
	e.start(d)
	calls := m.v1Calls()
	if len(calls) != 1 || calls[0].data != "#ff00ff" {
		t.Fatalf("the input \"inputs\" replaces the input of the payload (TASK-09): %+v", calls)
	}
}

// Before payload version 3 the marshaller converts the input from the characteristic of the
// task and applies the configurables (external-task-worker TestWorker, TestConfigurablesCommand).
func TestCommandBeforeVersion3IsMarshalledWithItsCharacteristicAndConfigurables(t *testing.T) {
	e := newEnv(t, Parallel)
	m := &recordingMarshaller{}
	e.use(newRepo(), e.aspects, m, Parallel)
	duration := []configurables.Configurable{{CharacteristicId: "seconds", Values: []configurables.ConfigurableCharacteristicValue{{Path: "duration", Value: "3"}}}}
	d := e.deploy(process(Optimistic, payload(controlling, map[string]any{"device_id": "d1", "service_id": "s-set", "characteristic_id": "rgb",
		"input": map[string]any{"r": 200, "g": 50, "b": 0}, "configurables": duration}), ""), nil)
	e.start(d)
	calls := m.v1Calls()
	if len(calls) != 1 || len(m.v2Calls()) != 0 {
		t.Fatalf("version 2 uses the marshalling of version 1: %+v %+v", calls, m.v2Calls())
	}
	expected := marshalCall{characteristicId: "rgb", data: map[string]any{"r": 200.0, "g": 50.0, "b": 0.0}, configurables: duration}
	if !reflect.DeepEqual(calls[0], expected) {
		t.Errorf("characteristic, input and configurables reach the marshaller unchanged: %+v", calls[0])
	}
}

func TestCommandNamesTheInputCharacteristicOfAControllingFunction(t *testing.T) {
	cases := map[string]map[string]any{
		"function id without rdf type":              {"id": controlling},
		"controlling id with measuring rdf type":    {"id": controlling, "rdf_type": models.SES_ONTOLOGY_MEASURING_FUNCTION},
		"rdf type with an id of another vocabulary": {"id": "set-color", "rdf_type": models.SES_ONTOLOGY_CONTROLLING_FUNCTION},
	}
	for name, function := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, Parallel)
			d := e.deploy(process(Optimistic, payload(controlling, map[string]any{"device_id": "d1", "service_id": "s-set", "function": function, "input": 1}), ""), nil)
			e.start(d)
			list := e.producer.messages()
			if len(list) != 1 || list[0].msg.Metadata.InputCharacteristic != "c" || list[0].msg.Metadata.OutputCharacteristic != "" {
				t.Fatalf("a controlling function sets the input characteristic: %+v", list)
			}
		})
	}
}

// A command without input leaves the values to the defaults of the service (external-task-worker
// TestDefaultValue, TestOnPowerCommand).
func TestCommandWithoutInputAsksTheMarshallerForNoValue(t *testing.T) {
	t.Run("version 3", func(t *testing.T) {
		e := newEnv(t, Parallel)
		m := &recordingMarshaller{}
		e.use(newRepo(), e.aspects, m, Parallel)
		d := e.deploy(process(Optimistic, payload(controlling, map[string]any{"version": 3, "device_id": "d1", "service_id": "s-set", "characteristic_id": ""}), ""), nil)
		e.start(d)
		calls := m.v2Calls()
		if len(calls) != 1 || len(calls[0]) != 0 {
			t.Fatalf("no value to set: %+v", calls)
		}
		if list := e.producer.messages(); len(list) != 1 || list[0].msg.Metadata.Version != 3 {
			t.Fatalf("the command is sent nonetheless: %+v", list)
		}
	})
	t.Run("before version 3", func(t *testing.T) {
		e := newEnv(t, Parallel)
		m := &recordingMarshaller{}
		e.use(newRepo(), e.aspects, m, Parallel)
		d := e.deploy(process(Optimistic, payload(controlling, map[string]any{"version": 0, "device_id": "d1", "service_id": "s-set", "characteristic_id": ""}), ""), nil)
		e.start(d)
		calls := m.v1Calls()
		if len(calls) != 1 || calls[0].data != nil || calls[0].characteristicId != "" {
			t.Fatalf("no value to set: %+v", calls)
		}
	})
}

// external-task-worker TestCommand and TestCommandWithConfigurables (SNRGY-1807)
func TestVersion3CommandHandsPathsAndConfigurablesOfThePayloadToTheMarshaller(t *testing.T) {
	e := newEnv(t, Parallel)
	m := &recordingMarshaller{}
	e.use(newRepo(), aspectTree(), m, Parallel)
	d := e.deploy(process(Optimistic, payload(controlling, map[string]any{
		"version": 3, "device_id": "d1", "service_id": "s-set", "characteristic_id": "rgb", "input_paths": []string{"value.level"},
		"input": map[string]any{"r": 200, "g": 50, "b": 0},
		"configurables_v2": []map[string]any{
			{"path": "value.duration", "characteristic_id": "minutes", "value": 2},
			{"path": "", "characteristic_id": "minutes", "value": 5, "function_id": "f-duration", "aspect_node": map[string]any{"id": "inside_air", "root_id": "air"}},
		},
	}), ""), nil)
	e.start(d)
	calls := m.v2Calls()
	if len(calls) != 1 || len(calls[0]) != 3 {
		t.Fatalf("the input and each configurable are one marshalling request: %+v", calls)
	}
	input, path, byAspect := calls[0][0], calls[0][1], calls[0][2]
	if !reflect.DeepEqual(input.Paths, []string{"value.level"}) || input.CharacteristicId != "rgb" || input.FunctionId != controlling ||
		!reflect.DeepEqual(input.Value, map[string]any{"r": 200.0, "g": 50.0, "b": 0.0}) {
		t.Errorf("the input goes to the input path: %+v", input)
	}
	if !reflect.DeepEqual(path.Paths, []string{"value.duration"}) || path.CharacteristicId != "minutes" || path.Value != 2.0 {
		t.Errorf("a configurable with path is set there: %+v", path)
	}
	if len(byAspect.Paths) != 0 || byAspect.FunctionId != "f-duration" || !reflect.DeepEqual(aspectIds(byAspect.AspectNodes), []string{"inside_air"}) || byAspect.Value != 5.0 {
		t.Errorf("a configurable without path is found by function and aspect: %+v", byAspect)
	}
}

// external-task-worker TestMultiAspectDeviceCommandMarshalsOnlyTheVariableServingEveryAspect
func TestCommandWithSeveralAspectsHandsAllOfThemToTheMarshaller(t *testing.T) {
	e := newEnv(t, Parallel)
	m := &recordingMarshaller{}
	e.use(newRepo(), aspectTree(), m, Parallel)
	d := e.deploy(process(Optimistic, payload(controlling, map[string]any{"version": 3, "device_id": "d1", "service_id": "s-set",
		"aspects": []map[string]any{{"id": "inside_air"}, {"id": "light"}}, "input": 1}), ""), nil)
	e.start(d)
	calls := m.v2Calls()
	if len(calls) != 1 || len(calls[0]) != 1 {
		t.Fatalf("%+v", calls)
	}
	nodes := calls[0][0].AspectNodes
	if !reflect.DeepEqual(aspectIds(nodes), []string{"inside_air", "light"}) || !reflect.DeepEqual(nodes[0].DescendentIds, []string{"kitchen_air"}) {
		t.Errorf("every named aspect reaches the marshaller with its subtree: %+v", nodes)
	}
}

// mgw-external-task-worker TestRetryReportsNotRetryableIncidentsImmediately
func TestUnreadablePayloadBecomesAnIncidentWithoutRetry(t *testing.T) {
	e := newEnv(t, Parallel)
	d := e.deploy(processWithRetries("3", "{not json"), nil)
	inst := e.start(d)
	got := e.expectState(inst.Id, model.StateIncident)
	if !strings.Contains(got.EndReason, "invalid task format") {
		t.Fatalf("unexpected reason %v", got.EndReason)
	}
}

// mgw-external-task-worker TestIncident, TestIncidentAfterExhaustedRetries
func TestCommandTheMarshallerRejectsIsNotSentAndBecomesAnIncident(t *testing.T) {
	e := newEnv(t, Parallel)
	m := &recordingMarshaller{fail: errors.New("Parsing of color failed, Bad Color")}
	e.use(newRepo(), e.aspects, m, Parallel)
	d := e.deploy(process(Pessimistic, payload(controlling, map[string]any{"version": 3, "device_id": "d1", "service_id": "s-set", "input": "foobar"}), ""), nil)
	inst := e.start(d)
	e.expectState(inst.Id, model.StateRunning)
	e.advance(time.Second)
	if len(m.v2Calls()) != 2 {
		t.Fatalf("payload retries 1 allow a second attempt, got %v", len(m.v2Calls()))
	}
	got := e.expectState(inst.Id, model.StateIncident)
	if !strings.Contains(got.EndReason, "Bad Color") {
		t.Errorf("the error of the marshaller is the incident: %v", got.EndReason)
	}
	if len(e.producer.messages()) != 0 {
		t.Errorf("nothing is sent: %+v", e.producer.messages())
	}
}

// process-deployment deployed a version 2 payload of the old designer with a selected device as
// version 3 even without a path; the marshaller then finds the content variable by function and
// aspect, where the marshalling by characteristic of version 2 finds none whose characteristic
// is not set at the variable itself (snowflake-canary).
func TestVersion2CommandWithASelectedDeviceIsMarshalledAsVersion3(t *testing.T) {
	e := newEnv(t, Parallel)
	m := &recordingMarshaller{}
	e.use(newRepo(), e.aspects, m, Parallel)
	d := e.deploy(process(Optimistic, payload(controlling, map[string]any{"input": 42}), ""), map[string]model.Selection{"cmd": {DeviceId: "d1", ServiceId: "s-set"}})
	e.start(d)
	if len(m.v1Calls()) != 0 || len(m.v2Calls()) != 1 || len(m.v2Calls()[0]) != 1 {
		t.Fatalf("one marshalling by function and aspect: v1 %+v v2 %+v", m.v1Calls(), m.v2Calls())
	}
	input := m.v2Calls()[0][0]
	if input.FunctionId != controlling || input.CharacteristicId != "c" || len(input.Paths) != 0 || input.Value != 42.0 {
		t.Errorf("the input goes by function and characteristic, without a path: %+v", input)
	}
	if list := e.producer.messages(); len(list) != 1 || list[0].msg.Metadata.Version != 3 {
		t.Fatalf("the command goes out as version 3: %+v", list)
	}
}
