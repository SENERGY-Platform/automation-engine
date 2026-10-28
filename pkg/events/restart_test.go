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
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

type consumerStart struct {
	ctx    context.Context
	topics []string
	handle func(events.Message) error
}

// A message the consumer hands over while the worker replaces it because the set of topics
// changed is still correlated: its handling does not end with the replaced consumer. A message
// in that window was lost, on the platform as the message being handled, on a gateway as the
// message the handler swap had not reached yet (SNRGY-5037).
func TestAMessageDuringAConsumerRestartIsCorrelated(t *testing.T) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	e := newEnv(t)
	c := e.srv.Controller
	starts := make(chan consumerStart, 10)
	consume := func(consumerCtx context.Context, topics []string, handle func(events.Message) error) error {
		starts <- consumerStart{ctx: consumerCtx, topics: topics, handle: handle}
		return nil
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	worker := events.NewWorker(events.DefaultWorkerConfig(), e.srv.Store, events.NewEvaluator(fakeUnmarshaller{}), e.srv.Engine, consume, logger)
	next := func() consumerStart {
		t.Helper()
		select {
		case s := <-starts:
			return s
		case <-time.After(10 * time.Second):
			t.Fatal("timeout waiting for the consumer to start")
			return consumerStart{}
		}
	}

	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: eventProcess(), Selections: map[string]model.Selection{"reading": {DeviceId: "d1", ServiceId: "urn:infai:ses:service:event"}}})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "processes", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	go worker.Run(runCtx)
	first := next()

	// another deployment brings another topic, so the worker replaces its consumer
	if _, err = c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: eventProcess(), Selections: map[string]model.Selection{"reading": {DeviceId: "d1", ServiceId: "s-other"}}}); err != nil {
		t.Fatal(err)
	}
	second := next()
	if first.ctx.Err() == nil || len(second.topics) != 2 {
		t.Fatalf("the first consumer is replaced by one for both topics: %v %v", first.ctx.Err(), second.topics)
	}

	raw, _ := json.Marshal(map[string]any{"device_id": "d1", "service_id": "urn:infai:ses:service:event", "value": map[string]any{"value": 25.0}})
	if err = first.handle(events.Message{Topic: "urn_infai_ses_service_event", Offset: 1, Value: raw, Time: time.Now()}); err != nil {
		t.Errorf("the message of the replaced consumer is handled: %v", err)
	}
	if e.count(inst.Id) != 1.0 {
		t.Errorf("and correlated: %v", e.count(inst.Id))
	}
}

// A description counts only messages from after its deployment version was created. A consumer
// that continues at a committed offset delivers what was produced while its topic was not read,
// e.g. after the only deployment on that topic was deleted and deployed again: those messages
// trigger nothing, younger than MaxMessageAge or not (SNRGY-5037).
func TestADeploymentIgnoresEventsFromBeforeItWasDeployed(t *testing.T) {
	e := newEnv(t)
	c := e.srv.Controller
	selections := map[string]model.Selection{"reading": {DeviceId: "d1", ServiceId: "urn:infai:ses:service:event"}}
	beforeFirst := time.Now().Add(-10 * time.Minute)
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: eventProcess(), Selections: selections})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "processes", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	e.sync()
	e.send(1, "d1", 25.0, beforeFirst)
	if e.count(inst.Id) != nil {
		t.Fatalf("an event of ten minutes before the deployment triggers nothing: %v", e.count(inst.Id))
	}
	e.send(2, "d1", 25.0, time.Now())
	if e.count(inst.Id) != 1.0 {
		t.Fatalf("an event after the deployment is correlated: %v", e.count(inst.Id))
	}

	if err = c.DeleteDeployment(ctx, alice, "processes", d.Id); err != nil {
		t.Fatal(err)
	}
	e.sync()
	whileDeleted := time.Now()
	time.Sleep(20 * time.Millisecond)
	again, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: eventProcess(), Selections: selections})
	if err != nil {
		t.Fatal(err)
	}
	next, err := c.StartInstance(ctx, alice, "processes", model.StartInstanceInput{DeploymentId: again.Id})
	if err != nil {
		t.Fatal(err)
	}
	e.sync()
	e.send(3, "d1", 25.0, whileDeleted)
	if e.count(next.Id) != nil {
		t.Fatalf("an event from while no deployment read the topic triggers nothing: %v", e.count(next.Id))
	}
	e.send(4, "d1", 25.0, time.Now())
	if e.count(next.Id) != 1.0 {
		t.Fatalf("an event after the new deployment is correlated: %v", e.count(next.Id))
	}
}
