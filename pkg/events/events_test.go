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
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/devices"
	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
	"github.com/SENERGY-Platform/models/go/models"
	"github.com/SENERGY-Platform/service-commons/pkg/jwt"
)

var ctx = context.Background()

const measuring = "urn:infai:ses:measuring-function:temperature"

type fakeRepo struct {
	mux    sync.Mutex
	groups map[string]models.DeviceGroup
}

func (this *fakeRepo) GetToken(user string) (devices.Token, error) {
	return devices.Token("Bearer " + user), nil
}
func (this *fakeRepo) GetDevice(_ devices.Token, id string) (models.Device, error) {
	return models.Device{Id: id, DeviceTypeId: "dt"}, nil
}
func (this *fakeRepo) GetService(_ devices.Token, _ models.Device, id string) (models.Service, error) {
	return models.Service{Id: id}, nil
}
func (this *fakeRepo) GetProtocol(devices.Token, string) (models.Protocol, error) {
	return models.Protocol{}, nil
}
func (this *fakeRepo) GetDeviceType(devices.Token, string) (models.DeviceType, error) {
	output := []models.Content{{ContentVariable: models.ContentVariable{Name: "value", FunctionId: measuring, AspectId: "air"}}}
	return models.DeviceType{Id: "dt", Services: []models.Service{
		{Id: "urn:infai:ses:service:event", Interaction: models.EVENT, Outputs: output},
		{Id: "s-request", Interaction: models.REQUEST, Outputs: output},
		{Id: "s-other", Interaction: models.EVENT},
	}}, nil
}
func (this *fakeRepo) GetDeviceGroup(_ devices.Token, id string) (models.DeviceGroup, error) {
	this.mux.Lock()
	defer this.mux.Unlock()
	return this.groups[id], nil
}

func aspects() *devices.AspectNodes {
	return devices.NewAspectNodesWith(func(_ devices.Token, id string) (models.AspectNode, error) {
		return models.AspectNode{Id: id, RootId: id}, nil
	})
}

type fakeImports struct{}

func (fakeImports) Instance(devices.Token, string) (models.Import, error) {
	return models.Import{Id: "imp", ImportTypeId: "type", KafkaTopic: "urn_infai_ses_import_imp"}, nil
}
func (fakeImports) Type(devices.Token, string) (models.ImportType, error) {
	return models.ImportType{Id: "type", Name: "weather", Output: models.ImportContentVariable{SubContentVariables: []models.ImportContentVariable{{Name: "value", Type: "https://schema.org/Float"}}}}, nil
}

func eventProcess() string {
	return b.Process(
		b.Start("start"), b.Gateway("exclusive", "loop", ""),
		`<bpmn:intermediateCatchEvent id="reading" senergy:function="`+measuring+`" senergy:aspect="air" senergy:characteristic="c" senergy:script="x &gt; 20" senergy:value_variable_name="x" senergy:qos="1">`+
			b.IO(b.OutScript("last", "event"), b.OutScript("count", "typeof count == 'undefined' ? 1 : count + 1"))+`<bpmn:messageEventDefinition/></bpmn:intermediateCatchEvent>`,
		b.Flow("start", "loop"), b.Flow("loop", "reading"), b.Flow("reading", "loop"),
	)
}

func TestDescribeExpandsGroupsDevicesAndImports(t *testing.T) {
	repo := &fakeRepo{groups: map[string]models.DeviceGroup{"g": {Id: "g", DeviceIds: []string{"d1", "d2"}}}}
	describer := events.NewDescriber(repo, aspects(), fakeImports{})
	process, _, err := bpmn.Parse(eventProcess())
	if err != nil {
		t.Fatal(err)
	}
	deployment := model.Deployment{Id: "dep", Namespace: "processes", Owner: "alice"}
	cases := map[string]struct {
		selection model.Selection
		expected  []string
	}{
		"group":               {model.Selection{DeviceGroupId: "g"}, []string{"d1/urn:infai:ses:service:event", "d2/urn:infai:ses:service:event"}},
		"device":              {model.Selection{DeviceId: "d1"}, []string{"d1/urn:infai:ses:service:event"}},
		"device with service": {model.Selection{DeviceId: "d1", ServiceId: "s-request"}, []string{"d1/s-request"}},
		"import":              {model.Selection{ImportId: "imp", Path: &model.SelectedPath{Path: "value", CharacteristicId: "ci"}}, []string{"imp"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			result, groups, err := describer.Describe(deployment, 1, process, map[string]model.Selection{"reading": c.selection})
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, d := range result {
				if d.ImportId != "" {
					got = append(got, d.ImportId)
				} else {
					got = append(got, d.DeviceId+"/"+d.ServiceId)
				}
				if d.MessageName != "dep_reading" || d.Script != "x > 20" || d.ValueVariable != "x" || d.Qos != 1 || d.Owner != "alice" {
					t.Errorf("unexpected description %+v", d)
				}
			}
			if strings.Join(got, ",") != strings.Join(c.expected, ",") {
				t.Errorf("expected %v, got %v", c.expected, got)
			}
			if name == "group" && (len(groups) != 1 || groups[0] != "g") {
				t.Errorf("the group is recorded for recomputation: %v", groups)
			}
			if name == "import" {
				var service models.Service
				_ = json.Unmarshal(result[0].Service, &service)
				if result[0].Topic != "urn_infai_ses_import_imp" || service.Id != "type" || len(service.Outputs) != 1 || result[0].CharacteristicId != "ci" || result[0].Path != "value" {
					t.Errorf("import description: %+v %+v", result[0], service)
				}
			}
			if name == "device" && result[0].Topic != "urn_infai_ses_service_event" {
				t.Errorf("topic of the service data: %v", result[0].Topic)
			}
		})
	}
}

// aspectImports serves an import type whose output names two aspects, as import-repository
// answers: the list in aspect_ids, the deprecated aspect_id filled with its first entry
type aspectImports struct{ fakeImports }

func (aspectImports) Type(devices.Token, string) (models.ImportType, error) {
	return models.ImportType{Id: "type", Name: "weather", Output: models.ImportContentVariable{Name: "output", Type: models.Structure,
		SubContentVariables: []models.ImportContentVariable{{Name: "value", Type: models.Structure, SubContentVariables: []models.ImportContentVariable{
			{Name: "temperature", Type: models.Float, FunctionId: measuring, AspectId: "air", AspectIds: []string{"air", "outside"}},
		}}}}}, nil
}

// The unmarshalling finds the value of an import by function and aspects; a variable of an
// import type that serves several aspects keeps all of them in the description (EVT-02).
func TestImportDescriptionKeepsAllAspectsOfAVariable(t *testing.T) {
	describer := events.NewDescriber(&fakeRepo{}, aspects(), aspectImports{})
	process, _, err := bpmn.Parse(eventProcess())
	if err != nil {
		t.Fatal(err)
	}
	result, _, err := describer.Describe(model.Deployment{Id: "dep", Namespace: "processes", Owner: "alice"}, 1, process,
		map[string]model.Selection{"reading": {ImportId: "imp"}})
	if err != nil || len(result) != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	var service models.Service
	if err = json.Unmarshal(result[0].Service, &service); err != nil {
		t.Fatal(err)
	}
	temperature := service.Outputs[0].ContentVariable.SubContentVariables[0]
	if temperature.AspectId != "air" || !reflect.DeepEqual(temperature.AspectIds, []string{"air", "outside"}) {
		t.Fatalf("the variable keeps its aspects: %+v", temperature)
	}
}

// fakeUnmarshaller reads the field value of the message, fails for "broken" and finds
// device-repository unavailable for "unavailable"
type fakeUnmarshaller struct{}

func (fakeUnmarshaller) Unmarshal(desc events.MessageDesc) (any, error) {
	if desc.Message["value"] == "broken" {
		return nil, errors.New("cannot unmarshal")
	}
	if desc.Message["value"] == "unavailable" {
		return nil, fmt.Errorf("%w: connection refused", events.ErrUnavailable)
	}
	return desc.Message["value"], nil
}

func TestEvaluatorTriggerRules(t *testing.T) {
	e := events.NewEvaluator(fakeUnmarshaller{})
	cases := []struct {
		script    string
		value     any
		triggered bool
		fails     bool
	}{
		{"x > 20", 25.0, true, false},
		{"x > 20", 10.0, false, false},
		{"'true'", 1.0, true, false},
		{"x", 1.0, true, false},
		{"x", 0.5, false, false},
		{"x > limit", 25.0, false, false},
		{"({})", 1.0, false, true},
	}
	for _, c := range cases {
		d := model.EventDescription{Script: c.script, ValueVariable: "x", Variables: map[string]string{"limit": "30"}}
		result, err := e.Evaluate(d, map[string]any{"value": c.value})
		if (err != nil) != c.fails || result.Triggered != c.triggered {
			t.Errorf("%v with %v: triggered %v, error %v", c.script, c.value, result.Triggered, err)
		}
	}
}

type env struct {
	t      *testing.T
	srv    *server.Server
	worker *events.Worker
	repo   *fakeRepo
	series *fakeSeries
	topics []string
}

// fakeSeries holds stored messages of every device and service in time order
type fakeSeries struct {
	mux    sync.Mutex
	rows   []events.Row
	tokens []string
}

func (this *fakeSeries) add(at time.Time, value any) {
	this.mux.Lock()
	defer this.mux.Unlock()
	this.rows = append(this.rows, events.Row{Time: at, Values: map[string]any{"value": value}})
}

func (this *fakeSeries) Range(_ context.Context, token string, _ string, _ string, columns []string, from time.Time, to time.Time, limit int) (result []events.Row, err error) {
	this.mux.Lock()
	defer this.mux.Unlock()
	this.tokens = append(this.tokens, token)
	if len(columns) != 1 || columns[0] != "value" {
		return nil, errors.New("unexpected columns")
	}
	for _, row := range this.rows {
		if row.Time.After(from) && row.Time.Before(to) && len(result) < limit {
			result = append(result, row)
		}
	}
	return result, nil
}

func (this *fakeSeries) Last(context.Context, string, string, string, []string) (*events.Row, error) {
	this.mux.Lock()
	defer this.mux.Unlock()
	if len(this.rows) == 0 {
		return nil, nil
	}
	last := this.rows[len(this.rows)-1]
	return &last, nil
}

func newEnv(t *testing.T) *env {
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	config := configuration.Default()
	config.SqlitePath = storetest.MemoryDsn()
	repo := &fakeRepo{groups: map[string]models.DeviceGroup{"g": {Id: "g", DeviceIds: []string{"d1"}}}}
	series := &fakeSeries{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := server.New(ctx, config, logger, server.Dependencies{DeviceRepository: repo, AspectNodes: aspects(),
		EventSeries: series, EventUnmarshaller: fakeUnmarshaller{}, UserToken: auth.UnsignedTokens})
	if err != nil {
		t.Fatal(err)
	}
	this := &env{t: t, srv: srv, repo: repo, series: series}
	consume := func(context.Context, []string, func(events.Message) error) error { return nil }
	this.worker = events.NewWorker(events.DefaultWorkerConfig(), srv.Store, events.NewEvaluator(fakeUnmarshaller{}), srv.Engine, consume, logger)
	return this
}

func (this *env) sync() {
	this.t.Helper()
	topics, err := this.worker.Sync(ctx)
	if err != nil {
		this.t.Fatal(err)
	}
	this.topics = topics
}

func (this *env) send(offset int64, deviceId string, value any, at time.Time) {
	this.t.Helper()
	if err := this.handle(offset, deviceId, value, at); err != nil {
		this.t.Fatal(err)
	}
}

func (this *env) handle(offset int64, deviceId string, value any, at time.Time) error {
	raw, _ := json.Marshal(map[string]any{"device_id": deviceId, "service_id": "urn:infai:ses:service:event", "value": map[string]any{"value": value}})
	return this.worker.Handle(ctx, events.Message{Topic: "urn_infai_ses_service_event", Partition: 0, Offset: offset, Value: raw, Time: at})
}

func (this *env) count(instanceId string) any {
	this.t.Helper()
	vars, err := this.srv.Controller.GetVariables(ctx, alice, "processes", instanceId)
	if err != nil {
		this.t.Fatal(err)
	}
	for _, v := range vars {
		if v.Name == "count" {
			return v.Value
		}
	}
	return nil
}

var alice = client.TestToken("alice")

func TestWorkerEvaluatesCorrelatesAndFollowsChanges(t *testing.T) {
	e := newEnv(t)
	c := e.srv.Controller
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: eventProcess(), Selections: map[string]model.Selection{"reading": {DeviceId: "d1", ServiceId: "urn:infai:ses:service:event"}}})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "processes", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	e.sync()
	if len(e.topics) != 1 || e.topics[0] != "urn_infai_ses_service_event" {
		t.Fatalf("the worker reads only topics with descriptions: %v", e.topics)
	}
	now := time.Now()
	e.send(1, "d1", 25.0, now)
	if e.count(inst.Id) != 1.0 {
		t.Fatalf("a triggered event is correlated: %v", e.count(inst.Id))
	}
	e.send(1, "d1", 25.0, now)
	e.send(2, "d1", 10.0, now)
	e.send(3, "d1", 30.0, now.Add(-2*time.Hour))
	e.send(4, "d2", 30.0, now)
	if e.count(inst.Id) != 1.0 {
		t.Fatalf("redelivery, false condition, old message and other device change nothing: %v", e.count(inst.Id))
	}
	e.send(5, "d1", 26.0, now)
	if e.count(inst.Id) != 2.0 {
		t.Fatalf("expected a second correlation: %v", e.count(inst.Id))
	}
	vars, _ := c.GetVariables(ctx, alice, "processes", inst.Id)
	for _, v := range vars {
		if v.Name == "last" && v.Value != 26.0 {
			t.Errorf("the unmarshalled value is the local variable event: %v", v.Value)
		}
	}

	_, err = c.PutDeployment(ctx, alice, "processes", d.Id, model.DeploymentInput{Xml: eventProcess(), UpdateStrategy: model.UpdateDrain, Selections: map[string]model.Selection{"reading": {DeviceId: "d2", ServiceId: "urn:infai:ses:service:event"}}})
	if err != nil {
		t.Fatal(err)
	}
	e.sync()
	e.send(6, "d1", 30.0, now)
	e.send(7, "d2", 30.0, now)
	if e.count(inst.Id) != 3.0 {
		t.Fatalf("after the update only the new device counts (revision sync, DEP-08): %v", e.count(inst.Id))
	}
	if err = c.DeleteDeployment(ctx, alice, "processes", d.Id); err != nil {
		t.Fatal(err)
	}
	e.sync()
	if len(e.topics) != 0 {
		t.Fatalf("deleted descriptions leave the worker: %v", e.topics)
	}
}

func TestEvaluationErrorsAreNotifiedOncePerWindow(t *testing.T) {
	e := newEnv(t)
	d, err := e.srv.Controller.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: eventProcess(), Selections: map[string]model.Selection{"reading": {DeviceId: "d1", ServiceId: "urn:infai:ses:service:event"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.srv.Controller.StartInstance(ctx, alice, "processes", model.StartInstanceInput{DeploymentId: d.Id}); err != nil {
		t.Fatal(err)
	}
	e.sync()
	e.send(1, "d1", "broken", time.Now())
	e.send(2, "d1", "broken", time.Now())
	var notifications []store.Notification
	_ = e.srv.Store.Tx(ctx, func(tx *store.Tx) (err error) {
		notifications, err = tx.DueNotifications(time.Now().Add(time.Hour), 100)
		return err
	})
	if len(notifications) != 1 || notifications[0].UserId != "alice" {
		t.Fatalf("one notification to the owner per window (EVT-07): %+v", notifications)
	}
}

func TestUnavailableDeviceRepositoryRereadsTheMessageWithoutNotifying(t *testing.T) {
	e := newEnv(t)
	d, err := e.srv.Controller.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: eventProcess(), Selections: map[string]model.Selection{"reading": {DeviceId: "d1", ServiceId: "urn:infai:ses:service:event"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.srv.Controller.StartInstance(ctx, alice, "processes", model.StartInstanceInput{DeploymentId: d.Id}); err != nil {
		t.Fatal(err)
	}
	e.sync()
	if err = e.handle(1, "d1", "unavailable", time.Now()); !errors.Is(err, events.ErrUnavailable) {
		t.Fatalf("a qos 1 message is not acknowledged while device-repository is unavailable: %v", err)
	}
	var notifications []store.Notification
	_ = e.srv.Store.Tx(ctx, func(tx *store.Tx) (err error) {
		notifications, err = tx.DueNotifications(time.Now().Add(time.Hour), 100)
		return err
	})
	if len(notifications) != 0 {
		t.Fatalf("an outage of device-repository is not the owner's problem: %+v", notifications)
	}
}

func TestDeviceGroupChangeRecomputesDescriptions(t *testing.T) {
	e := newEnv(t)
	c := e.srv.Controller
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: eventProcess(), Selections: map[string]model.Selection{"reading": {DeviceGroupId: "g"}}})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := c.ListEventDescriptions(ctx, alice, "processes", d.Id)
	if len(list) != 1 {
		t.Fatalf("one device in the group: %+v", list)
	}
	e.repo.mux.Lock()
	e.repo.groups["g"] = models.DeviceGroup{Id: "g", DeviceIds: []string{"d1", "d2"}}
	e.repo.mux.Unlock()
	if err = c.RecomputeDeviceGroup(ctx, "g"); err != nil {
		t.Fatal(err)
	}
	list, _ = c.ListEventDescriptions(ctx, alice, "processes", d.Id)
	if len(list) != 2 {
		t.Fatalf("the new device gets a description (DEP-08): %+v", list)
	}
}

func TestReconstructionShowsEvaluationsNextToCorrelations(t *testing.T) {
	e := newEnv(t)
	c := e.srv.Controller
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: eventProcess(), Selections: map[string]model.Selection{"reading": {DeviceId: "d1", ServiceId: "urn:infai:ses:service:event"}}})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "processes", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	e.sync()
	time.Sleep(5 * time.Millisecond)
	low := time.Now()
	e.series.add(low, 10.0)
	e.series.add(low.Add(time.Millisecond), 25.0)
	time.Sleep(5 * time.Millisecond)
	e.send(1, "d1", 25.0, time.Now())
	time.Sleep(5 * time.Millisecond)
	// stored, but never delivered to the worker
	missed := time.Now()
	e.series.add(missed, 30.0)
	time.Sleep(5 * time.Millisecond)

	result, err := c.EventEvaluations(ctx, alice, "processes", inst.Id, model.EventReconstructionOptions{ElementId: "reading"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Waiting) != 2 || !result.Waiting[0].Correlated || result.Waiting[1].To != nil || len(result.Correlations) != 1 {
		t.Fatalf("waiting periods and correlations come from the activity log (EVT-09): %+v %+v", result.Waiting, result.Correlations)
	}
	triggered := []bool{}
	for _, evaluation := range result.Evaluations {
		triggered = append(triggered, evaluation.Triggered)
	}
	if !reflect.DeepEqual(triggered, []bool{false, true, true}) || result.Evaluations[1].Value != 25.0 {
		t.Fatalf("values are evaluated like in the event worker (EVT-10): %+v", result.Evaluations)
	}
	if len(result.Hints) != 1 || !strings.Contains(result.Hints[0], "would have triggered") {
		t.Fatalf("the missed value is a deviation (EVT-11): %v", result.Hints)
	}
	// tokens of the same user differ in their expiry, which counts in seconds
	if parsed, err := jwt.Parse(e.series.tokens[0]); err != nil || parsed.GetUserId() != "alice" {
		t.Fatalf("the time series is read with the token of the owner: %v", e.series.tokens[0])
	}
	var paged []any
	options := model.EventReconstructionOptions{ElementId: "reading", Limit: 1}
	for range 5 {
		page, err := c.EventEvaluations(ctx, alice, "processes", inst.Id, options)
		if err != nil {
			t.Fatal(err)
		}
		if page.Next != nil && len(page.Hints) > 0 {
			t.Fatalf("a partial page names no deviations: %v", page.Hints)
		}
		for _, evaluation := range page.Evaluations {
			paged = append(paged, evaluation.Value)
		}
		if page.Next == nil {
			break
		}
		options.After = page.Next
	}
	if !reflect.DeepEqual(paged, []any{10.0, 25.0, 30.0}) {
		t.Fatalf("pages of one evaluation per source continue with next: %v", paged)
	}

	state, err := c.DebugState(ctx, alice, "processes", inst.Id, model.DebugOptions{EvaluateEvents: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Waits) != 1 || state.Waits[0].Message == nil || len(state.Waits[0].Message.Events) != 1 {
		t.Fatalf("the wait names its event source (DBG-02): %+v", state.Waits)
	}
	current := state.Waits[0].Message.Events[0].Current
	if current == nil || !current.Triggered || current.Value != 30.0 {
		t.Fatalf("the current value is evaluated on request (DBG-18): %+v", current)
	}
}
