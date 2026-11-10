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

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// caughtProcess is process with an error boundary event on the command that maps what failed
// to instance variables (BPMN-19)
func caughtProcess(strategy string, payloadJson string) string {
	xml := process(strategy, payloadJson, "")
	boundary := b.ErrorBoundary("caught", "cmd", "failure", b.OutScript("failed_device", "device_id"), b.OutScript("failed_service", "service_id")) +
		b.End("failed") + b.Flow("caught", "failed")
	return strings.Replace(xml, b.End("end"), b.End("end")+boundary, 1)
}

func (this *env) failWith(s sent, message string) {
	this.t.Helper()
	s.msg.Response.Output = map[string]string{"error": message}
	raw, _ := json.Marshal(s.msg)
	if err := this.dc.HandleError(ctx, raw); err != nil {
		this.t.Fatal(err)
	}
}

func (this *env) expectCaught(id string, message string, deviceId any, serviceId any) {
	this.t.Helper()
	this.expectState(id, model.StateCompleted)
	if failure, _ := this.variable(id, "failure").(string); !strings.Contains(failure, message) {
		this.t.Errorf("failure %q does not contain %q", failure, message)
	}
	if got := this.variable(id, "failed_device"); got != deviceId {
		this.t.Errorf("device_id %#v, expected %#v", got, deviceId)
	}
	if got := this.variable(id, "failed_service"); got != serviceId {
		this.t.Errorf("service_id %#v, expected %#v", got, serviceId)
	}
}

func TestErrorAnswerOfLastAttemptIsCaughtWithDevice(t *testing.T) {
	e := newEnv(t, Parallel)
	d := e.deploy(caughtProcess(Pessimistic, payload(measuring, map[string]any{"device_id": "d1", "service_id": "s-get", "retries": 0})), nil)
	inst := e.start(d)
	e.failWith(e.producer.messages()[0], "no matching state of device zigbee:0x54ef441000dd7200 within 30s after set command")
	e.expectCaught(inst.Id, "no matching state of device zigbee:0x54ef441000dd7200", "d1", "s-get")
}

func TestTimeoutOfSingleDeviceIsCaughtWithDevice(t *testing.T) {
	e := newEnv(t, Parallel)
	d := e.deploy(caughtProcess(Pessimistic, payload(measuring, map[string]any{"device_id": "d1", "service_id": "s-get", "retries": 0})), nil)
	inst := e.start(d)
	e.advance(2 * time.Minute)
	e.expectCaught(inst.Id, "timed out", "d1", "s-get")
}

func TestErrorAnswerOfGroupMemberIsCaughtWithThatDevice(t *testing.T) {
	e := newEnv(t, Parallel)
	d := e.deploy(caughtProcess(Pessimistic, payload(measuring, map[string]any{"device_group_id": "g", "retries": 0})), nil)
	inst := e.start(d)
	var second sent
	for _, s := range e.producer.messages() {
		if s.key == "d2" {
			second = s
		}
	}
	e.failWith(second, "device offline")
	e.expectCaught(inst.Id, "device offline", "d2", "s-get")
}

func TestGroupTimeoutWithoutAnswerIsCaughtWithoutDevice(t *testing.T) {
	e := newEnv(t, Parallel)
	d := e.deploy(caughtProcess(Pessimistic, payload(measuring, map[string]any{"device_group_id": "g", "retries": 0})), nil)
	inst := e.start(d)
	e.advance(2 * time.Minute)
	e.expectCaught(inst.Id, "timed out", nil, nil)
}

func TestLocalErrorIsCaughtWithDevice(t *testing.T) {
	e, broker := newLocalEnv(t)
	d := e.deploy(caughtProcess(Pessimistic, payload(measuring, map[string]any{"device_id": "d1", "service_id": "s-get", "retries": 0})), nil)
	inst := e.start(d)
	sent, _ := commands(t, broker)
	_ = broker.Publish(ctx, "error/command/"+sent[0].CommandId, 2, []byte("device unreachable"))
	e.expectCaught(inst.Id, "device unreachable", "d1", "s-get")
}

func TestUnreadableAnswerIsCaughtWithDevice(t *testing.T) {
	e := newEnv(t, Parallel)
	d := e.deploy(caughtProcess(Pessimistic, payload(measuring, map[string]any{"device_id": "d1", "service_id": "s-get", "retries": 0})), nil)
	inst := e.start(d)
	s := e.producer.messages()[0]
	// the fake marshaller reads the segment "data", which this answer lacks
	s.msg.Response.Output = map[string]string{"other": "1"}
	raw, _ := json.Marshal(s.msg)
	if err := e.dc.HandleResponse(ctx, raw); err != nil {
		t.Fatal(err)
	}
	e.expectCaught(inst.Id, "unable to unmarshal", "d1", "s-get")
}
