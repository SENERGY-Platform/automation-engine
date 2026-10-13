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
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
	"github.com/SENERGY-Platform/models/go/models"
)

// newLocalEnv runs device commands like on a gateway: over the broker of the gateway
func newLocalEnv(t *testing.T) (*env, *mqtt.Memory) {
	e := newEnv(t, Parallel)
	broker := mqtt.NewMemory()
	e.dc = New(Config{GroupScheduler: Parallel, LocalCommandPrefix: "engine-"}, e.s, newRepo(), e.aspects, fakeMarshaller{}, fakeTimescale{}, NewLocalTransport(broker, "engine-"), e.metrics, e.logger)
	e.dc.SetCompleter(e.e)
	e.e.RegisterTaskType(TaskType, e.dc)
	_ = broker.Subscribe(LocalResponseTopic, 2, func(topic string, payload []byte) error { return e.dc.HandleLocalResponse(ctx, topic, payload) })
	_ = broker.Subscribe(LocalErrorTopic, 2, func(topic string, payload []byte) error { return e.dc.HandleLocalError(ctx, topic, payload) })
	return e, broker
}

func commands(t *testing.T, broker *mqtt.Memory) (result []LocalEnvelope, topics []string) {
	t.Helper()
	for _, p := range broker.Published("command/#") {
		var envelope LocalEnvelope
		if err := json.Unmarshal(p.Payload, &envelope); err != nil {
			t.Fatal(err)
		}
		result = append(result, envelope)
		topics = append(topics, p.Topic)
	}
	return result, topics
}

func TestLocalCommandGoesToTheConnectorAndCompletesWithItsResponse(t *testing.T) {
	e, broker := newLocalEnv(t)
	d := e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_id": "d1", "service_id": "s-get"}), ""), nil)
	inst := e.start(d)
	sent, topics := commands(t, broker)
	if len(sent) != 1 || topics[0] != "command/local-d1/local-s-get" || !strings.HasPrefix(sent[0].CommandId, "engine-") {
		t.Fatalf("the command goes to command/{local device}/{local service} with the own prefix: %v %+v", topics, sent)
	}
	if p := broker.Published("command/#")[0]; p.Qos != 2 {
		t.Errorf("commands are sent with qos 2 like mgw-external-task-worker: %v", p.Qos)
	}
	// an answer to device-command on the same broker is not this engine's
	_ = broker.Publish(ctx, "response/local-d1/local-s-get", 2, []byte(`{"command_id":"device-command-1","data":"99"}`))
	e.expectState(inst.Id, model.StateRunning)
	answer, _ := json.Marshal(LocalEnvelope{CommandId: sent[0].CommandId, Data: "21.5"})
	_ = broker.Publish(ctx, "response/local-d1/local-s-get", 2, answer)
	e.expectState(inst.Id, model.StateCompleted)
	if out, ok := e.variable(inst.Id, "out").([]any); !ok || len(out) != 1 || out[0] != 21.5 {
		t.Errorf("the answer of the connector is the result: %#v", e.variable(inst.Id, "out"))
	}
}

func TestLocalCommandErrorRetriesAndBecomesAnIncident(t *testing.T) {
	e, broker := newLocalEnv(t)
	d := e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_id": "d1", "service_id": "s-get"}), ""), nil)
	inst := e.start(d)
	sent, _ := commands(t, broker)
	_ = broker.Publish(ctx, "error/command/"+sent[0].CommandId, 2, []byte("device unreachable"))
	e.advance(time.Second)
	if sent, _ = commands(t, broker); len(sent) != 2 {
		t.Fatalf("payload retries 1 allow a second attempt, got %v commands", len(sent))
	}
	_ = broker.Publish(ctx, "error/command/"+sent[1].CommandId, 2, []byte("device unreachable"))
	if got := e.expectState(inst.Id, model.StateIncident); !strings.Contains(got.EndReason, "device unreachable") {
		t.Errorf("the error of the connector is the incident: %v", got.EndReason)
	}
}

func TestLocalCommandWithoutLocalIdFails(t *testing.T) {
	e, broker := newLocalEnv(t)
	repo := newRepo()
	device := repo.devices["d1"]
	device.LocalId = ""
	repo.devices["d1"] = device
	e.dc.repo = repo
	d := e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_id": "d1", "service_id": "s-get", "retries": 0}), ""), nil)
	inst := e.start(d)
	if sent, _ := commands(t, broker); len(sent) != 0 {
		t.Fatalf("nothing can be sent without local id: %+v", sent)
	}
	if got := e.expectState(inst.Id, model.StateIncident); !strings.Contains(got.EndReason, "local id") {
		t.Errorf("%v", got.EndReason)
	}
}

func TestLastValuesAskMgwLastValueWithLocalIds(t *testing.T) {
	var body []map[string]string
	answer := `[{"time":"2026-10-12T10:00:00Z","value":21.5},{"time":null,"value":null}]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		if r.URL.Path != "/last-values" || r.Method != http.MethodPost {
			http.Error(w, "unknown", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(answer))
	}))
	defer server.Close()
	device, service := models.Device{Id: "d1", LocalId: "local-d1"}, models.Service{Id: "s1", LocalId: "local-s1"}
	request := []TimescaleRequest{{Device: device, Service: service, ColumnName: "value.temperature"}, {Device: device, Service: service, ColumnName: "value"}}
	_, err := NewLastValues(server.URL).Query("", request, time.Second)
	expected := []map[string]string{{"DeviceId": "local-d1", "ServiceId": "local-s1", "ColumnName": "temperature"}, {"DeviceId": "local-d1", "ServiceId": "local-s1", "ColumnName": ""}}
	if !reflect.DeepEqual(body, expected) {
		t.Errorf("local ids and the path below the root variable: %v", body)
	}
	if err == nil || !strings.Contains(err.Error(), "no value") {
		t.Errorf("a value never received is an error: %v", err)
	}
	answer = `[{"time":"2026-10-12T10:00:00Z","value":21.5},{"time":"2026-10-12T10:00:00Z","value":{"temperature":21.5}}]`
	result, err := NewLastValues(server.URL).Query("", request, time.Second)
	if err != nil || len(result) != 2 || result[0].Value != 21.5 {
		t.Errorf("%+v %v", result, err)
	}
}
