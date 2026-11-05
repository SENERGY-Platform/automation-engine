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
	"slices"
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

// Deployments created and deleted on the same topic, as the canaries of the platform do every
// few seconds, do not replace the consumer: a topic no description names any more stays
// subscribed for TopicGrace, and is dropped with one replacement afterwards. Every replacement
// is a rebalance of the whole consumer group, which stopped the evaluation of all events for up
// to 30 s in the cluster (PLAN.md, "Performance in the cluster").
func TestATopicStaysSubscribedForTheGraceAfterItsLastDeployment(t *testing.T) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	e := newEnv(t)
	c := e.srv.Controller
	starts := make(chan consumerStart, 10)
	consume := func(consumerCtx context.Context, topics []string, handle func(events.Message) error) error {
		starts <- consumerStart{ctx: consumerCtx, topics: topics, handle: handle}
		return nil
	}
	config := events.DefaultWorkerConfig()
	config.SyncInterval = 10 * time.Millisecond
	config.TopicGrace = time.Second
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	worker := events.NewWorker(config, e.srv.Store, events.NewEvaluator(fakeUnmarshaller{}), e.srv.Engine, consume, logger)
	next := func(within time.Duration) (consumerStart, bool) {
		t.Helper()
		select {
		case s := <-starts:
			return s, true
		case <-time.After(within):
			return consumerStart{}, false
		}
	}
	deploy := func(serviceId string) string {
		t.Helper()
		d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: eventProcess(), Selections: map[string]model.Selection{"reading": {DeviceId: "d1", ServiceId: serviceId}}})
		if err != nil {
			t.Fatal(err)
		}
		return d.Id
	}
	remove := func(id string) {
		t.Helper()
		if err := c.DeleteDeployment(ctx, alice, "processes", id); err != nil {
			t.Fatal(err)
		}
	}

	deploy("urn:infai:ses:service:event")
	go worker.Run(runCtx)
	if s, ok := next(10 * time.Second); !ok || !slices.Equal(s.topics, []string{"urn_infai_ses_service_event"}) {
		t.Fatalf("the consumer starts with the topic of the deployment: %v %v", ok, s.topics)
	}
	churn := deploy("s-other")
	second, ok := next(10 * time.Second)
	if !ok || !slices.Equal(second.topics, []string{"s-other", "urn_infai_ses_service_event"}) {
		t.Fatalf("a new topic replaces the consumer at once: %v %v", ok, second.topics)
	}

	remove(churn)
	if s, ok := next(300 * time.Millisecond); ok {
		t.Fatalf("deleting the last deployment of a topic does not replace the consumer: %v", s.topics)
	}
	churn = deploy("s-other")
	if s, ok := next(300 * time.Millisecond); ok {
		t.Fatalf("nor does deploying on it again within the grace: %v", s.topics)
	}
	if second.ctx.Err() != nil {
		t.Fatal("the consumer for both topics still runs")
	}

	remove(churn)
	third, ok := next(10 * time.Second)
	if !ok || !slices.Equal(third.topics, []string{"urn_infai_ses_service_event"}) {
		t.Fatalf("after the grace the consumer is replaced by one without the topic: %v %v", ok, third.topics)
	}
	if second.ctx.Err() == nil {
		t.Error("and the previous consumer is stopped")
	}
}
