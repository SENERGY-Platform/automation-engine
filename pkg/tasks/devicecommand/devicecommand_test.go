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
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

func TestPessimisticCommandKeepsTheHandlerContract(t *testing.T) {
	e := newEnv(t, Parallel)
	d := e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_id": "d1", "service_id": "s-get"}), ""), nil)
	inst := e.start(d)
	list := e.producer.messages()
	if len(list) != 1 {
		t.Fatalf("expected one command, got %v", len(list))
	}
	s := list[0]
	if s.topic != "connector" || s.key != "d1" {
		t.Errorf("message goes to the handler topic with the device id as key (TASK-04): %v %v", s.topic, s.key)
	}
	info := s.msg.TaskInfo
	if info.ProcessInstanceId != inst.Id || info.ProcessDefinitionId != d || info.TenantId != "alice" || info.CompletionStrategy != Pessimistic || info.TaskId == "" || info.Time == "" {
		t.Errorf("task_info must carry instance, deployment id as definition id, owner and strategy (TASK-05, DATA-04): %+v", info)
	}
	if s.msg.Metadata.ResponseTo != "response" || s.msg.Metadata.OutputCharacteristic != "c" || s.msg.Metadata.Service.Id != "s-get" {
		t.Errorf("unexpected metadata %+v", s.msg.Metadata)
	}
	e.expectState(inst.Id, model.StateRunning)
	e.respond(s, 21.5)
	e.expectState(inst.Id, model.StateCompleted)
	if out, ok := e.variable(inst.Id, "out").([]any); !ok || len(out) != 1 || out[0] != 21.5 {
		t.Errorf("result is a list from payload version 2 on (TASK-10): %#v", e.variable(inst.Id, "out"))
	}
	e.respond(s, 99)
	if out := e.variable(inst.Id, "out").([]any); out[0] != 21.5 {
		t.Errorf("a late answer must not change anything (TASK-11)")
	}
	if e.metrics.sent["alice/"+d] != 1 {
		t.Errorf("the cost metric counts per owner and deployment (TASK-12): %v", e.metrics.sent)
	}
}

func TestOptimisticCommandCompletesWithoutAnswer(t *testing.T) {
	e := newEnv(t, Parallel)
	d := e.deploy(process(Optimistic, payload(controlling, map[string]any{"device_id": "d1", "service_id": "s-set", "input": true}), ""), nil)
	inst := e.start(d)
	e.expectState(inst.Id, model.StateCompleted)
	list := e.producer.messages()
	if len(list) != 1 || list[0].msg.TaskInfo.CompletionStrategy != Optimistic || list[0].msg.Request.Input["data"] != "true" {
		t.Fatalf("unexpected messages %+v", list)
	}
}

func TestDeviceGroupInParallelCollectsAllResults(t *testing.T) {
	e := newEnv(t, Parallel)
	d := e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_group_id": "g"}), ""), nil)
	inst := e.start(d)
	list := e.producer.messages()
	if len(list) != 2 || list[0].key != "d1" || list[1].key != "d2" {
		t.Fatalf("one command per device and matching service (TASK-06): %+v", list)
	}
	if !strings.HasSuffix(list[0].msg.TaskInfo.TaskId, "_0_0") || !strings.HasSuffix(list[1].msg.TaskInfo.TaskId, "_1_0") {
		t.Errorf("sub-task ids keep their format: %v %v", list[0].msg.TaskInfo.TaskId, list[1].msg.TaskInfo.TaskId)
	}
	e.respond(list[1], 2.0)
	e.expectState(inst.Id, model.StateRunning)
	if len(e.producer.messages()) != 2 {
		t.Fatalf("an answer must not resend the commands still in flight")
	}
	e.respond(list[0], 1.0)
	e.expectState(inst.Id, model.StateCompleted)
	if out := e.variable(inst.Id, "out").([]any); len(out) != 2 || out[0] != 1.0 || out[1] != 2.0 {
		t.Fatalf("results in device order: %v", out)
	}
}

func TestDeviceGroupSequentialSendsOneAfterTheOther(t *testing.T) {
	e := newEnv(t, Sequential)
	d := e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_group_id": "g"}), ""), nil)
	inst := e.start(d)
	if list := e.producer.messages(); len(list) != 1 {
		t.Fatalf("sequential sends one command, got %v", len(list))
	}
	e.respond(e.producer.messages()[0], 1.0)
	list := e.producer.messages()
	if len(list) != 2 || list[1].key != "d2" {
		t.Fatalf("the answer sends the next command: %+v", list)
	}
	e.respond(list[1], 2.0)
	e.expectState(inst.Id, model.StateCompleted)
}

func TestDeviceGroupTimeoutCompletesWithPartialResults(t *testing.T) {
	e := newEnv(t, Parallel)
	d := e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_group_id": "g", "retries": 0}), ""), nil)
	inst := e.start(d)
	e.respond(e.producer.messages()[0], 1.0)
	e.advance(2 * time.Minute)
	e.expectState(inst.Id, model.StateCompleted)
	if out := e.variable(inst.Id, "out").([]any); len(out) != 1 || out[0] != 1.0 {
		t.Fatalf("partial result expected: %v", out)
	}
}

func TestErrorAnswerRetriesTheCommand(t *testing.T) {
	e := newEnv(t, Parallel)
	d := e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_id": "d1", "service_id": "s-get"}), ""), nil)
	inst := e.start(d)
	first := e.producer.messages()[0]
	first.msg.Response.Output = map[string]string{"error": "device offline"}
	raw, _ := json.Marshal(first.msg)
	if err := e.dc.HandleError(ctx, raw); err != nil {
		t.Fatal(err)
	}
	e.advance(time.Second)
	list := e.producer.messages()
	if len(list) != 2 {
		t.Fatalf("payload retries 1 allow a second attempt, got %v messages", len(list))
	}
	if err := e.dc.HandleError(ctx, raw); err != nil {
		t.Fatal(err)
	}
	e.expectState(inst.Id, model.StateIncident)
}

func TestInputsOverwriteAndSelection(t *testing.T) {
	e := newEnv(t, Parallel)
	extra := `<camunda:inputParameter name="inputs.level">7</camunda:inputParameter>`
	path := &model.SelectedPath{Path: "value.level"}
	selection := map[string]model.Selection{"cmd": {DeviceId: "d2", ServiceId: "s-set", Path: path}}
	d := e.deploy(process(Pessimistic, payload(controlling, map[string]any{"device_id": "d1", "service_id": "s-set", "input": map[string]any{"level": 0, "on": true}}), extra), selection)
	inst := e.start(d)
	list := e.producer.messages()
	if len(list) != 1 || list[0].key != "d2" {
		t.Fatalf("the selection of the deployment decides the device (DEP-05): %+v", list)
	}
	var input map[string]any
	_ = json.Unmarshal([]byte(list[0].msg.Request.Input["data"]), &input)
	if input["level"] != 7.0 || input["on"] != true {
		t.Errorf("inputs.<path> sets inside the input with type conversion (TASK-09): %v", input)
	}
	if list[0].msg.Metadata.Version != 3 {
		t.Errorf("a selected path needs payload version 3, got %v", list[0].msg.Metadata.Version)
	}
	e.respond(list[0], nil)
	e.expectState(inst.Id, model.StateCompleted)
}

func TestIncidentInputFailsWithoutRetry(t *testing.T) {
	e := newEnv(t, Parallel)
	extra := `<camunda:inputParameter name="incident">value too high</camunda:inputParameter>`
	d := e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_id": "d1", "service_id": "s-get", "retries": 3}), extra), nil)
	inst := e.start(d)
	got := e.expectState(inst.Id, model.StateIncident)
	if !strings.Contains(got.EndReason, "user triggered incident: value too high") {
		t.Fatalf("unexpected reason %v", got.EndReason)
	}
	if len(e.producer.messages()) != 0 {
		t.Fatalf("nothing is sent")
	}
}

func TestEventServiceReadsLastValue(t *testing.T) {
	e := newEnv(t, Parallel)
	d := e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_id": "e1", "service_id": "s-event"}), ""), nil)
	inst := e.start(d)
	e.expectState(inst.Id, model.StateCompleted)
	if out := e.variable(inst.Id, "out").([]any); len(out) != 1 || out[0] == nil {
		t.Fatalf("the last value from timescale is the result (TASK-08): %v", out)
	}
	if len(e.producer.messages()) != 0 {
		t.Fatalf("no command for an event service")
	}
}
