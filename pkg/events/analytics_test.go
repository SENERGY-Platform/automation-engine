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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

const analyticsTopic = "analytics-stopping"

// pipelineServer answers GET /pipeline/{id} as analytics-pipeline does: p1 of alice with a cloud
// and a local operator, p2 of bob with an operator of the same name, others are not found
func pipelineServer(t *testing.T) *httptest.Server {
	pipelines := map[string]events.Pipeline{
		"p1": {Id: "p1", UserId: "alice", Operators: []events.PipelineOperator{
			{Id: "o1", Name: "stopping", DeploymentType: "cloud", OutputTopic: analyticsTopic},
			{Id: "o2", Name: "local", DeploymentType: "local", OutputTopic: "analytics/local/o2/p1"},
		}},
		"p2": {Id: "p2", UserId: "bob", Operators: []events.PipelineOperator{{Id: "o1", Name: "stopping", DeploymentType: "cloud", OutputTopic: analyticsTopic}}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := strings.CutPrefix(r.URL.Path, "/pipeline/")
		pipeline, found := pipelines[id]
		if r.Method != http.MethodGet || !ok || r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if !found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(pipeline)
	}))
	t.Cleanup(server.Close)
	return server
}

// analyticsProcess waits for an output with enough confidence and keeps it in "last"
func analyticsProcess() string {
	return b.Process(
		b.Start("start"), b.Gateway("exclusive", "loop", ""),
		`<bpmn:intermediateCatchEvent id="prediction" senergy:script="x.overall_confidence &gt;= 0.5" senergy:value_variable_name="x" senergy:qos="1">`+
			b.IO(b.OutScript("last", "event"), b.OutScript("count", "typeof count == 'undefined' ? 1 : count + 1"))+`<bpmn:messageEventDefinition/></bpmn:intermediateCatchEvent>`,
		b.Flow("start", "loop"), b.Flow("loop", "prediction"), b.Flow("prediction", "loop"),
	)
}

func TestDescribeAnalyticsOperator(t *testing.T) {
	process, _, err := bpmn.Parse(analyticsProcess())
	if err != nil {
		t.Fatal(err)
	}
	deployment := model.Deployment{Id: "dep", Namespace: "processes", Owner: "alice"}
	describer := events.NewDescriber(&fakeRepo{}, time.Minute, tokens, fakeImports{}).WithPipelines(events.NewPipelines(pipelineServer(t).URL))
	describe := func(describer *events.Describer, pipeline string, operator string) ([]model.EventDescription, error) {
		result, _, err := describer.Describe(ctx, deployment, 1, process, map[string]model.Selection{"prediction": {PipelineId: pipeline, OperatorId: operator}})
		return result, err
	}
	result, err := describe(describer, "p1", "o1")
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("%+v", result)
	}
	d := result[0]
	if d.Topic != analyticsTopic || d.PipelineId != "p1" || d.OperatorId != "o1" || d.MessageName != "dep_prediction" || d.ValueVariable != "x" || d.Qos != 1 ||
		d.DeviceId != "" || d.ImportId != "" || d.CharacteristicId != "" || len(d.Service) != 0 {
		t.Errorf("unexpected description %+v", d)
	}
	rejected := map[string]func() error{
		"pipeline of another user": func() error { _, err := describe(describer, "p2", "o1"); return err },
		"unknown pipeline":         func() error { _, err := describe(describer, "p3", "o1"); return err },
		"unknown operator":         func() error { _, err := describe(describer, "p1", "o3"); return err },
		"local operator":           func() error { _, err := describe(describer, "p1", "o2"); return err },
		"on a gateway":             func() error { _, err := describe(describer.Local(), "p1", "o1"); return err },
		"not configured": func() error {
			_, err := describe(events.NewDescriber(&fakeRepo{}, time.Minute, tokens, fakeImports{}), "p1", "o1")
			return err
		},
	}
	for name, run := range rejected {
		if err := run(); model.AsError(err).Kind != model.KindValidation {
			t.Errorf("%v: expected a validation error, got %v", name, err)
		}
	}
}

// The worker reads the topic of the operator and correlates only the output of the selected
// pipeline and operator; the event is the object "analytics" of the message (SNRGY-5113).
func TestWorkerCorrelatesAnalyticsOutput(t *testing.T) {
	pipelines := pipelineServer(t)
	e := newEnvWith(t, func(config *configuration.Config) { config.AnalyticsPipelineUrl = pipelines.URL })
	c := e.srv.Controller
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: analyticsProcess(),
		Selections: map[string]model.Selection{"prediction": {PipelineId: "p1", OperatorId: "o1"}}})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "processes", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	e.sync()
	if len(e.topics) != 1 || e.topics[0] != analyticsTopic {
		t.Fatalf("the worker reads the output topic of the operator: %v", e.topics)
	}
	send := func(offset int64, pipeline string, operator string, analytics map[string]any, at time.Time) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"pipeline_id": pipeline, "operator_id": operator, "analytics": analytics, "time": at.UTC().Format(time.RFC3339)})
		if err := e.worker.Handle(ctx, events.Message{Topic: analyticsTopic, Offset: offset, Value: raw, Time: at}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	confident := map[string]any{"stopping_time": "2026-11-09T08:00:00Z", "overall_confidence": 0.8, "timestamp": "2026-11-09T07:00:00Z"}
	send(1, "p2", "o1", confident, now)
	send(2, "p1", "o9", confident, now)
	send(3, "p1", "o1", map[string]any{"overall_confidence": 0.2}, now)
	send(4, "p1", "o1", confident, now.Add(-2*time.Hour))
	if e.count(inst.Id) != nil {
		t.Fatalf("another pipeline, another operator, a false condition and an old message do not correlate: %v", e.count(inst.Id))
	}
	send(5, "p1", "o1", confident, now)
	if e.count(inst.Id) != 1.0 {
		t.Fatalf("the output of the operator is correlated: %v", e.count(inst.Id))
	}
	vars, _ := c.GetVariables(ctx, alice, "processes", inst.Id)
	for _, v := range vars {
		if v.Name == "last" {
			last, _ := v.Value.(map[string]any)
			if last["stopping_time"] != "2026-11-09T08:00:00Z" || last["overall_confidence"] != 0.8 {
				t.Errorf("event is the object analytics of the message: %v", v.Value)
			}
		}
	}

	_, err = c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: analyticsProcess(),
		Selections: map[string]model.Selection{"prediction": {PipelineId: "p2", OperatorId: "o1"}}})
	if model.AsError(err).Kind != model.KindValidation {
		t.Errorf("a deployment on the pipeline of another user is rejected: %v", err)
	}
}
