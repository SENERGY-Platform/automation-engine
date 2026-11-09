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
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

// slowCorrelator takes a while per correlation and records how many ran at once, in all and per
// deployment, what each deployment got, and fails for the message names in fail
type slowCorrelator struct {
	mux           sync.Mutex
	running       int
	most          int
	perDeployment map[string]int
	mostOfOne     int
	order         map[string][]string
	fail          map[string]bool
	delay         time.Duration
}

func (this *slowCorrelator) Correlate(_ context.Context, request engine.CorrelationRequest) (model.CorrelationResult, error) {
	deployment := request.Name[:len("dep0")]
	this.mux.Lock()
	if this.perDeployment == nil {
		this.perDeployment = map[string]int{}
	}
	this.running++
	this.perDeployment[deployment]++
	this.most = max(this.most, this.running)
	this.mostOfOne = max(this.mostOfOne, this.perDeployment[deployment])
	this.mux.Unlock()
	time.Sleep(this.delay)
	this.mux.Lock()
	defer this.mux.Unlock()
	this.running--
	this.perDeployment[deployment]--
	this.order[deployment] = append(this.order[deployment], request.Name)
	if this.fail[request.Name] {
		return model.CorrelationResult{}, model.Unavailable("database gone")
	}
	return model.CorrelationResult{}, nil
}

func (this *slowCorrelator) QueueNotification(context.Context, string, string, string, string) error {
	return nil
}

// parallelWorker runs a worker with workers correlation slots on qos 1 descriptions of the device
// d1: perDeployment descriptions in each of deployments deployments, message names dep<i>_e<j>
func parallelWorker(t *testing.T, workers int, deployments int, perDeployment int, correlator *slowCorrelator) *events.Worker {
	t.Helper()
	s := storetest.OpenSQLite(t)
	now := time.Now()
	err := s.Tx(ctx, func(tx *store.Tx) error {
		for i := 0; i < deployments; i++ {
			deploymentId := fmt.Sprintf("dep%v", i)
			if err := tx.InsertDeployment(model.Deployment{Id: deploymentId, Namespace: "processes", Name: deploymentId, Owner: "alice", Version: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
				return err
			}
			var list []model.EventDescription
			for j := 0; j < perDeployment; j++ {
				name := fmt.Sprintf("%v_e%v", deploymentId, j)
				list = append(list, model.EventDescription{Id: name, DeploymentId: deploymentId, Version: 1, ElementId: fmt.Sprintf("e%v", j), Namespace: "processes", Owner: "alice",
					MessageName: name, DeviceId: "d1", ServiceId: "urn:infai:ses:service:event", Topic: "urn_infai_ses_service_event",
					Script: "true", ValueVariable: "value", Qos: 1})
			}
			if err := tx.ReplaceEventDescriptions(deploymentId, 1, list, nil, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	config := events.DefaultWorkerConfig()
	config.CorrelationWorkers = workers
	consume := func(context.Context, []string, func(events.Message) error) error { return nil }
	worker := events.NewWorker(config, s, events.NewEvaluator(fakeUnmarshaller{}), correlator, consume, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err = worker.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	return worker
}

func deviceMessage(offset int64) events.Message {
	raw, _ := json.Marshal(map[string]any{"device_id": "d1", "service_id": "urn:infai:ses:service:event", "value": map[string]any{"value": 1}})
	return events.Message{Topic: "urn_infai_ses_service_event", Offset: offset, Value: raw}
}

// The qos 1 descriptions of different deployments are correlated at the same time, at most on
// the configured workers, the descriptions of one deployment one after the other; the message is
// handled when all are.
func TestDeploymentsOfAMessageAreCorrelatedInParallel(t *testing.T) {
	correlator := &slowCorrelator{order: map[string][]string{}, delay: 20 * time.Millisecond}
	worker := parallelWorker(t, 3, 6, 3, correlator)
	start := time.Now()
	if err := worker.Handle(ctx, deviceMessage(1)); err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	if correlator.most != 3 {
		t.Errorf("three deployments at once: %v", correlator.most)
	}
	if len(correlator.order) != 6 {
		t.Fatalf("every deployment is handled before Handle returns: %v", correlator.order)
	}
	if correlator.mostOfOne != 1 {
		t.Errorf("the descriptions of a deployment one after the other: %v at once", correlator.mostOfOne)
	}
	for deployment, names := range correlator.order {
		slices.Sort(names)
		if expected := []string{deployment + "_e0", deployment + "_e1", deployment + "_e2"}; !slices.Equal(names, expected) {
			t.Errorf("every description of %v once: %v", deployment, names)
		}
	}
	// 18 correlations of 20 ms: 6 rounds of 3 deployments with 3 each, instead of 18 one after the other
	if took > 300*time.Millisecond {
		t.Errorf("took %v", took)
	}
}

// One worker correlates as before, one after the other.
func TestOneCorrelationWorkerCorrelatesOneAfterTheOther(t *testing.T) {
	correlator := &slowCorrelator{order: map[string][]string{}, delay: time.Millisecond}
	worker := parallelWorker(t, 1, 4, 2, correlator)
	if err := worker.Handle(ctx, deviceMessage(1)); err != nil {
		t.Fatal(err)
	}
	if correlator.most != 1 || len(correlator.order) != 4 {
		t.Errorf("one at a time, all of them: %v %v", correlator.most, correlator.order)
	}
}

// An error of one deployment leaves the message to be read again; the other deployments and the
// later descriptions of the failing one are still handled, as one after the other before.
func TestAnErrorOfOneDeploymentFailsTheMessage(t *testing.T) {
	correlator := &slowCorrelator{order: map[string][]string{}, delay: time.Millisecond, fail: map[string]bool{"dep1_e0": true}}
	worker := parallelWorker(t, 4, 3, 2, correlator)
	err := worker.Handle(ctx, deviceMessage(1))
	if !errors.Is(err, model.ErrUnavailable) {
		t.Fatalf("the message is read again: %v", err)
	}
	for _, deployment := range []string{"dep0", "dep1", "dep2"} {
		if len(correlator.order[deployment]) != 2 {
			t.Errorf("both descriptions of %v are handled: %v", deployment, correlator.order)
		}
	}
}

// Every description evaluates its own copy of the message: a condition that changes its value
// changes it neither for the other descriptions nor under their feet, also when deployments are
// evaluated at the same time (the output of an analytics operator is handed over as it is).
func TestEveryDescriptionEvaluatesItsOwnCopyOfTheMessage(t *testing.T) {
	s := storetest.OpenSQLite(t)
	now := time.Now()
	err := s.Tx(ctx, func(tx *store.Tx) error {
		for i := 0; i < 6; i++ {
			deploymentId := fmt.Sprintf("dep%v", i)
			if err := tx.InsertDeployment(model.Deployment{Id: deploymentId, Namespace: "processes", Name: deploymentId, Owner: "alice", Version: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
				return err
			}
			var list []model.EventDescription
			for j := 0; j < 2; j++ {
				name := fmt.Sprintf("%v_e%v", deploymentId, j)
				list = append(list, model.EventDescription{Id: name, DeploymentId: deploymentId, Version: 1, ElementId: fmt.Sprintf("e%v", j), Namespace: "processes", Owner: "alice",
					MessageName: name, PipelineId: "p1", OperatorId: "o1", Topic: "analytics-stopping", ValueVariable: "x", Qos: 1,
					Script: "var first = x.seen === undefined; x.seen = 1; for (var k = 0; k < 200; k++) { x['k' + k] = k; } first && x.values.length === 2"})
			}
			if err := tx.ReplaceEventDescriptions(deploymentId, 1, list, nil, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	correlator := &slowCorrelator{order: map[string][]string{}}
	config := events.DefaultWorkerConfig()
	config.CorrelationWorkers = 4
	consume := func(context.Context, []string, func(events.Message) error) error { return nil }
	worker := events.NewWorker(config, s, events.NewEvaluator(fakeUnmarshaller{}), correlator, consume, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err = worker.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	for offset := int64(1); offset <= 5; offset++ {
		raw, _ := json.Marshal(map[string]any{"pipeline_id": "p1", "operator_id": "o1", "analytics": map[string]any{"values": []any{1, 2}}})
		if err := worker.Handle(ctx, events.Message{Topic: "analytics-stopping", Offset: offset, Value: raw, Time: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 6; i++ {
		if got := correlator.order[fmt.Sprintf("dep%v", i)]; len(got) != 10 {
			t.Errorf("both descriptions of dep%v correlate every message: %v", i, got)
		}
	}
}
