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
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

// The tests of this file follow those of the worker of event-worker v1.2.0
// (pkg/worker/worker_test.go): several descriptions of one device service, qos 0 and 1.

// recordingCorrelator counts the correlations per message name
type recordingCorrelator struct {
	mux    sync.Mutex
	counts map[string]int
}

func (this *recordingCorrelator) Correlate(_ context.Context, request engine.CorrelationRequest) (model.CorrelationResult, error) {
	this.mux.Lock()
	defer this.mux.Unlock()
	this.counts[request.Name]++
	return model.CorrelationResult{}, nil
}

func (this *recordingCorrelator) QueueNotification(context.Context, string, string, string, string) error {
	return nil
}

func (this *recordingCorrelator) snapshot() map[string]int {
	this.mux.Lock()
	defer this.mux.Unlock()
	result := map[string]int{}
	for k, v := range this.counts {
		result[k] = v
	}
	return result
}

type conditionDescription struct {
	name   string
	script string
	qos    int
}

// workerWith runs the worker on descriptions of the device d1 and its event service
func workerWith(t *testing.T, descriptions ...conditionDescription) (*events.Worker, *recordingCorrelator) {
	t.Helper()
	s := storetest.OpenSQLite(t)
	now := time.Now()
	list := []model.EventDescription{}
	for _, d := range descriptions {
		list = append(list, model.EventDescription{Id: d.name, DeploymentId: "dep", Version: 1, ElementId: d.name, Namespace: "processes", Owner: "alice",
			MessageName: d.name, DeviceId: "d1", ServiceId: "urn:infai:ses:service:event", Topic: "urn_infai_ses_service_event",
			Script: d.script, ValueVariable: "value", Qos: d.qos})
	}
	err := s.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.InsertDeployment(model.Deployment{Id: "dep", Namespace: "processes", Name: "d", Owner: "alice", Version: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		return tx.ReplaceEventDescriptions("dep", list, nil, now)
	})
	if err != nil {
		t.Fatal(err)
	}
	correlator := &recordingCorrelator{counts: map[string]int{}}
	consume := func(context.Context, []string, func(events.Message) error) error { return nil }
	worker := events.NewWorker(events.DefaultWorkerConfig(), s, events.NewEvaluator(fakeUnmarshaller{}), correlator, consume, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err = worker.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	return worker, correlator
}

func handle(t *testing.T, worker *events.Worker, offset int64, value any) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"device_id": "d1", "service_id": "urn:infai:ses:service:event", "value": map[string]any{"value": value}})
	if err := worker.Handle(ctx, events.Message{Topic: "urn_infai_ses_service_event", Offset: offset, Value: raw}); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, correlator *recordingCorrelator, expected map[string]int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !reflect.DeepEqual(correlator.snapshot(), expected) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := correlator.snapshot(); !reflect.DeepEqual(got, expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}
}

// event-worker TestWorkerSimpleScriptQos1
func TestEveryDescriptionOfAServiceIsEvaluatedOnItsOwn(t *testing.T) {
	worker, correlator := workerWith(t,
		conditionDescription{"event_1", `value == "testmessage"`, 1},
		conditionDescription{"event_2", `value == "testmessage"`, 1},
		conditionDescription{"event_3", `value == "nope"`, 1},
	)
	handle(t, worker, 1, "testmessage")
	handle(t, worker, 2, "testmessage")
	if got := correlator.snapshot(); !reflect.DeepEqual(got, map[string]int{"event_1": 2, "event_2": 2}) {
		t.Fatalf("each true condition correlates its event, before the message is taken (EVT-05): %v", got)
	}
}

// event-worker TestWorkerSimpleScriptQos0
func TestQos0DescriptionsAreCorrelatedAfterTheMessageIsTaken(t *testing.T) {
	worker, correlator := workerWith(t,
		conditionDescription{"event_1", `value == "testmessage"`, 0},
		conditionDescription{"event_2", `value == "testmessage"`, 0},
		conditionDescription{"event_3", `value == "nope"`, 0},
	)
	handle(t, worker, 1, "testmessage")
	handle(t, worker, 2, "testmessage")
	waitFor(t, correlator, map[string]int{"event_1": 2, "event_2": 2})
}

// event-worker TestWorkerSimpleScriptQosMixed
func TestQos1DescriptionsAreCorrelatedBeforeTheMessageIsTakenNextToQos0Ones(t *testing.T) {
	worker, correlator := workerWith(t,
		conditionDescription{"event_1", `value == "testmessage"`, 1},
		conditionDescription{"event_2", `value == "testmessage"`, 0},
		conditionDescription{"event_3", `value == "nope"`, 1},
	)
	handle(t, worker, 1, "testmessage")
	if got := correlator.snapshot()["event_1"]; got != 1 {
		t.Fatalf("the qos 1 event is correlated when Handle returns: %v", correlator.snapshot())
	}
	waitFor(t, correlator, map[string]int{"event_1": 1, "event_2": 1})
}

// event-worker TestWorkerComplexScriptQos1: a variable that is no JSON is a string
func TestConditionReadsAVariableThatIsNoJsonAsString(t *testing.T) {
	e := events.NewEvaluator(fakeUnmarshaller{})
	d := model.EventDescription{Script: "value == expected", ValueVariable: "value", Variables: map[string]string{"expected": "marshalled:testmessage"}}
	result, err := e.Evaluate(d, map[string]any{"value": "marshalled:testmessage"})
	if err != nil || !result.Triggered {
		t.Fatalf("%+v %v", result, err)
	}
	d.Variables["expected"] = "marshalled:nope"
	if result, err = e.Evaluate(d, map[string]any{"value": "marshalled:testmessage"}); err != nil || result.Triggered {
		t.Fatalf("another string does not trigger: %+v %v", result, err)
	}
}
