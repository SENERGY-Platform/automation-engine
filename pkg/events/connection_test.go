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

package events_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

const deviceLogTopic = "device_log"

// connectionProcess waits for a device to come online and keeps the value and the device in
// "last" and "from"
func connectionProcess(script string) string {
	return b.Process(
		b.Start("start"), b.Gateway("exclusive", "loop", ""),
		`<bpmn:intermediateCatchEvent id="presence" senergy:script="`+script+`" senergy:value_variable_name="x" senergy:qos="1">`+
			b.IO(b.OutScript("last", "event"), b.OutScript("from", "device_id"), b.OutScript("count", "typeof count == 'undefined' ? 1 : count + 1"))+`<bpmn:messageEventDefinition/></bpmn:intermediateCatchEvent>`,
		b.Flow("start", "loop"), b.Flow("loop", "presence"), b.Flow("presence", "loop"),
	)
}

func TestDescribeDeviceConnection(t *testing.T) {
	process, _, err := bpmn.Parse(connectionProcess("x == 'online'"))
	if err != nil {
		t.Fatal(err)
	}
	deployment := model.Deployment{Id: "dep", Namespace: "processes", Owner: "alice"}
	describe := func(describer *events.Describer, selection model.Selection) ([]model.EventDescription, error) {
		result, _, err := describer.Describe(ctx, deployment, 1, process, map[string]model.Selection{"presence": selection})
		return result, err
	}
	cloud := events.NewDescriber(&fakeRepo{}, time.Minute, tokens, fakeImports{}).WithConnectionTopic(deviceLogTopic)
	gateway := events.NewDescriber(&localRepo{}, time.Minute, tokens, fakeImports{}).Local()
	selection := model.Selection{ConnectionDeviceId: "d1"}

	result, err := describe(cloud, selection)
	if err != nil || len(result) != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	d := result[0]
	if d.Topic != deviceLogTopic || d.ConnectionDeviceId != "d1" || d.LocalDeviceId != "" || d.MessageName != "dep_presence" || d.Script != "x == 'online'" || d.ValueVariable != "x" || d.Qos != 1 ||
		d.DeviceId != "" || d.ServiceId != "" || d.ImportId != "" || d.CharacteristicId != "" || d.FunctionId != "" || len(d.Service) != 0 {
		t.Errorf("unexpected description %+v", d)
	}

	result, err = describe(gateway, selection)
	if err != nil || len(result) != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	if d = result[0]; d.Topic != events.LocalConnectionTopic || d.ConnectionDeviceId != "d1" || d.LocalDeviceId != "local-d1" {
		t.Errorf("a gateway knows the device by its local id: %+v", d)
	}

	rejected := map[string]func() error{
		"not configured in the cloud": func() error {
			_, err := describe(events.NewDescriber(&fakeRepo{}, time.Minute, tokens, fakeImports{}), selection)
			return err
		},
		"device without local id on a gateway": func() error {
			_, err := describe(events.NewDescriber(&fakeRepo{}, time.Minute, tokens, fakeImports{}).Local(), selection)
			return err
		},
	}
	for name, run := range rejected {
		if err := run(); model.AsError(err).Kind != model.KindValidation {
			t.Errorf("%v: expected a validation error, got %v", name, err)
		}
	}
}

func TestEvaluatorValueOfAConnection(t *testing.T) {
	e := events.NewEvaluator(fakeUnmarshaller{})
	d := model.EventDescription{Script: "x == 'online'", ValueVariable: "x", ConnectionDeviceId: "d1"}
	for connected, expected := range map[bool]string{true: "online", false: "offline"} {
		result, err := e.Evaluate(d, map[string]any{"connected": connected})
		if err != nil || result.Value != expected || result.Triggered != connected {
			t.Errorf("connected %v: %+v %v", connected, result, err)
		}
	}
	if _, err := e.Evaluate(d, map[string]any{}); err == nil {
		t.Error("a message without a state cannot be evaluated")
	}
}

// The worker reads device_log and correlates the messages of the selected device whose state
// satisfies the condition; the event is "online" or "offline" (SNRGY-5151).
func TestWorkerCorrelatesConnectionOfADevice(t *testing.T) {
	e := newEnv(t)
	c := e.srv.Controller
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: connectionProcess("x == 'online'"),
		Selections: map[string]model.Selection{"presence": {ConnectionDeviceId: "d1"}}})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "processes", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	e.sync()
	if len(e.topics) != 1 || e.topics[0] != deviceLogTopic {
		t.Fatalf("the worker reads device_log: %v", e.topics)
	}
	now := time.Now()
	var offset int64
	send := func(message map[string]any, at time.Time) {
		t.Helper()
		offset++
		raw, _ := json.Marshal(message)
		if err := e.worker.Handle(ctx, events.Message{Topic: deviceLogTopic, Offset: offset, Value: raw, Time: at}); err != nil {
			t.Fatal(err)
		}
	}
	state := func(id string, connected bool) map[string]any {
		return map[string]any{"id": id, "connected": connected, "time": now.UTC().Format(time.RFC3339), "monitor_connection_state": "x", "device_owner": "alice"}
	}
	send(state("d2", true), now)
	send(state("d1", false), now)
	send(map[string]any{"id": "d1", "time": now.UTC().Format(time.RFC3339)}, now)
	send(state("d1", true), now.Add(-2*time.Hour))
	// Kafka delivers again what is older than the deployment; the description counts newer ones only
	send(state("d1", true), now.Add(-10*time.Minute))
	if e.count(inst.Id) != nil {
		t.Fatalf("another device, the wrong state, a message without a state, an old one and one from before the deployment do not correlate: %v", e.count(inst.Id))
	}
	send(state("d1", true), now)
	if e.count(inst.Id) != 1.0 {
		t.Fatalf("a message of the device is correlated: %v", e.count(inst.Id))
	}
	vars, _ := c.GetVariables(ctx, alice, "processes", inst.Id)
	for _, v := range vars {
		if v.Name == "last" && v.Value != "online" {
			t.Errorf("the event is online: %v", v.Value)
		}
		if v.Name == "from" && v.Value != "d1" {
			t.Errorf("device_id is the platform id: %v", v.Value)
		}
	}
	send(state("d1", true), now)
	if e.count(inst.Id) != 2.0 {
		t.Errorf("a repeated state is evaluated again, the condition decides: %v", e.count(inst.Id))
	}
}
