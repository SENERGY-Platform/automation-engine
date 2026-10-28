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
	"html"
	"reflect"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// membersProcess keeps result as out and results as members
func membersProcess(strategy string, payloadJson string) string {
	return b.Process(
		b.Start("start"),
		`<bpmn:serviceTask id="cmd" senergy:taskType="device_command" senergy:completionStrategy="`+strategy+`">`+
			`<bpmn:extensionElements><camunda:inputOutput><camunda:inputParameter name="payload">`+html.EscapeString(payloadJson)+`</camunda:inputParameter>`+
			`<camunda:outputParameter name="out"><camunda:script scriptFormat="javascript">result</camunda:script></camunda:outputParameter>`+
			`<camunda:outputParameter name="members"><camunda:script scriptFormat="javascript">results</camunda:script></camunda:outputParameter></camunda:inputOutput></bpmn:extensionElements></bpmn:serviceTask>`,
		b.End("end"), b.Flow("start", "cmd"), b.Flow("cmd", "end"),
	)
}

// A read on a device group tells which device each value came from: results lists device,
// service and value of every answering sub-task, in the order of result, which stays as it was
// (SNRGY-5054).
func TestAGroupReadNamesTheDeviceOfEachValue(t *testing.T) {
	e := newEnv(t, Parallel)
	fiveDevices(e, Parallel)
	inst := e.start(e.deploy(membersProcess(Pessimistic, payload(measuring, map[string]any{"device_group_id": "g", "retries": 1})), nil))
	answered := map[int]bool{}
	answerAs(e, answered)
	e.advance(2 * time.Minute)
	e.advance(time.Second)
	answerAs(e, answered)
	e.advance(2 * time.Minute)
	e.expectState(inst.Id, model.StateCompleted)
	if out := e.variable(inst.Id, "out"); !reflect.DeepEqual(out, []any{0.0, 2.0, 3.0, 4.0}) {
		t.Fatalf("result stays the list of values, d1 never answered: %#v", out)
	}
	expected := []any{
		map[string]any{"device_id": "d0", "service_id": "s", "value": 0.0},
		map[string]any{"device_id": "d2", "service_id": "s", "value": 2.0},
		map[string]any{"device_id": "d3", "service_id": "s", "value": 3.0},
		map[string]any{"device_id": "d4", "service_id": "s", "value": 4.0},
	}
	if members := e.variable(inst.Id, "members"); !reflect.DeepEqual(members, expected) {
		t.Errorf("results names the device and service of each value: %#v", members)
	}
}
