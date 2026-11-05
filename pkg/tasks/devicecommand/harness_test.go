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
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"sync"
	"testing"
	"time"

	devicerepository "github.com/SENERGY-Platform/device-repository/v3/lib/client"
	devicerepomodel "github.com/SENERGY-Platform/device-repository/v3/lib/model"
	"github.com/SENERGY-Platform/models/go/models"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
	"github.com/google/uuid"
)

var ctx = context.Background()

const measuring = "urn:infai:ses:measuring-function:temperature"
const controlling = "urn:infai:ses:controlling-function:set-on"

// --- fakes ---

// tokens of the owners as the token exchange would give them
func tokens(user string) (string, error) {
	return "Bearer " + user, nil
}

// aspectNodes resolves aspect node ids for the fake device-repository
type aspectNodes func(id string) (models.AspectNode, error)

// fakeRepo answers the reads of device commands; any other method of the client is not expected
type fakeRepo struct {
	devicerepository.Interface
	devices     map[string]models.Device
	deviceTypes map[string]models.DeviceType
	groups      map[string]models.DeviceGroup
	aspects     aspectNodes
}

func (this *fakeRepo) ReadDevice(_ context.Context, id string, _ string, _ devicerepomodel.AuthAction) (models.Device, error, int) {
	d, ok := this.devices[id]
	if !ok {
		return d, errors.New("device not found"), http.StatusNotFound
	}
	return d, nil, http.StatusOK
}
func (this *fakeRepo) ReadProtocol(_ context.Context, id string, _ string) (models.Protocol, error, int) {
	return models.Protocol{Id: id, Handler: "connector", ProtocolSegments: []models.ProtocolSegment{{Id: "seg", Name: "data"}}}, nil, http.StatusOK
}
func (this *fakeRepo) ReadDeviceType(_ context.Context, id string, _ string) (models.DeviceType, error, int) {
	return this.deviceTypes[id], nil, http.StatusOK
}
func (this *fakeRepo) ReadDeviceGroup(_ context.Context, id string, _ string, _ bool) (models.DeviceGroup, error, int) {
	return this.groups[id], nil, http.StatusOK
}

// ListAspectNodes leaves unknown ids out, as device-repository does
func (this *fakeRepo) ListAspectNodes(_ context.Context, options devicerepomodel.AspectListOptions) ([]models.AspectNode, int64, error, int) {
	result := []models.AspectNode{}
	for _, id := range options.Ids {
		if node, err := this.aspects(id); err == nil {
			result = append(result, node)
		}
	}
	return result, int64(len(result)), nil, http.StatusOK
}

func service(id string, interaction models.Interaction, functionId string) models.Service {
	variable := models.Content{ProtocolSegmentId: "seg", Serialization: "json", ContentVariable: models.ContentVariable{Name: "value", Type: models.Float, FunctionId: functionId}}
	s := models.Service{Id: id, LocalId: "local-" + id, ProtocolId: "p", Interaction: interaction}
	if functionId == measuring {
		s.Outputs = []models.Content{variable}
	} else {
		s.Inputs = []models.Content{variable}
	}
	return s
}

func newRepo() *fakeRepo {
	return &fakeRepo{
		devices: map[string]models.Device{
			"d1": {Id: "d1", LocalId: "local-d1", DeviceTypeId: "dt"}, "d2": {Id: "d2", LocalId: "local-d2", DeviceTypeId: "dt"}, "e1": {Id: "e1", LocalId: "local-e1", DeviceTypeId: "et"},
		},
		deviceTypes: map[string]models.DeviceType{
			"dt": {Id: "dt", Services: []models.Service{service("s-get", models.REQUEST, measuring), service("s-set", models.REQUEST, controlling)}},
			"et": {Id: "et", Services: []models.Service{service("s-event", models.EVENT, measuring)}},
		},
		groups: map[string]models.DeviceGroup{"g": {Id: "g", DeviceIds: []string{"d1", "d2"}}},
	}
}

// fakeMarshaller writes the input value to the segment "data" and reads it back from there
type fakeMarshaller struct{}

func (fakeMarshaller) MarshalV2(_ context.Context, request MarshallingV2Request) (map[string]string, error, int) {
	result := map[string]string{}
	for _, d := range request.Data {
		raw, _ := json.Marshal(d.Value)
		result["data"] = string(raw)
	}
	return result, nil, http.StatusOK
}
func (fakeMarshaller) UnmarshalV2(_ context.Context, request UnmarshallingV2Request) (any, error, int) {
	var v any
	err := json.Unmarshal([]byte(request.Message["data"]), &v)
	return v, err, http.StatusOK
}
func (this fakeMarshaller) Marshal(ctx context.Context, request MarshallingRequest) (map[string]string, error, int) {
	return this.MarshalV2(ctx, MarshallingV2Request{Data: []MarshallingV2RequestData{{Value: request.Data}}})
}
func (this fakeMarshaller) Unmarshal(ctx context.Context, request UnmarshallingRequest) (any, error, int) {
	return this.UnmarshalV2(ctx, UnmarshallingV2Request{Message: request.Message})
}

type fakeTimescale struct{}

func (fakeTimescale) Query(_ string, request []TimescaleRequest, _ time.Duration) ([]TimescaleResponse, error) {
	result := []TimescaleResponse{}
	for range request {
		result = append(result, TimescaleResponse{Value: 21.5})
	}
	return result, nil
}

type sent struct {
	topic string
	key   string
	msg   ProtocolMsg
}

type fakeProducer struct {
	mux  sync.Mutex
	list []sent
}

func (this *fakeProducer) Produce(_ context.Context, topic string, key string, payload []byte) error {
	var msg ProtocolMsg
	if err := json.Unmarshal(payload, &msg); err != nil {
		return err
	}
	this.mux.Lock()
	defer this.mux.Unlock()
	this.list = append(this.list, sent{topic: topic, key: key, msg: msg})
	return nil
}

func (this *fakeProducer) messages() []sent {
	this.mux.Lock()
	defer this.mux.Unlock()
	return append([]sent{}, this.list...)
}

type fakeMetrics struct {
	mux  sync.Mutex
	sent map[string]int
}

func (this *fakeMetrics) CommandSent(userId string, deploymentId string) {
	this.mux.Lock()
	defer this.mux.Unlock()
	this.sent[userId+"/"+deploymentId]++
}
func (this *fakeMetrics) MarshallingLatency(string, string, string, string, time.Duration) {}

type namespaces struct{}

func (namespaces) Get(name string) (model.Namespace, bool) {
	return model.Namespace{Name: name}, true
}

// --- harness ---

type env struct {
	t        *testing.T
	s        *store.Store
	e        *engine.Engine
	dc       *Type
	producer *fakeProducer
	metrics  *fakeMetrics
	clock    time.Time
	mux      sync.Mutex
	logger   *slog.Logger
	aspects  aspectNodes
}

func newEnv(t *testing.T, scheduler string) *env {
	return newEnvOn(t, scheduler, storetest.OpenSQLite(t))
}

// newEnvOn is newEnv on the given store, e.g. one of storetest.Backends
func newEnvOn(t *testing.T, scheduler string, s *store.Store) *env {
	this := &env{t: t, s: s, producer: &fakeProducer{}, metrics: &fakeMetrics{sent: map[string]int{}}, clock: time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)}
	config := engine.DefaultConfig()
	config.TaskRetryBackoff = time.Second
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	this.e = engine.New(s, config, namespaces{}, logger)
	this.e.SetClock(this.now)
	aspects := aspectNodes(func(id string) (models.AspectNode, error) {
		return models.AspectNode{Id: id, RootId: id}, nil
	})
	repo := newRepo()
	repo.aspects = aspects
	this.dc = New(Config{GroupScheduler: scheduler, ResponseTo: "response", ErrorTo: "errors"}, s, repo, tokens, fakeMarshaller{}, fakeTimescale{}, this.producer, this.metrics, logger)
	this.logger, this.aspects = logger, aspects
	this.dc.SetCompleter(this.e)
	this.e.RegisterTaskType(TaskType, this.dc)
	return this
}

func (this *env) now() time.Time {
	this.mux.Lock()
	defer this.mux.Unlock()
	return this.clock
}

func (this *env) advance(d time.Duration) {
	this.t.Helper()
	this.mux.Lock()
	this.clock = this.clock.Add(d)
	this.mux.Unlock()
	if err := this.e.ProcessDue(ctx); err != nil {
		this.t.Fatal(err)
	}
}

func payload(functionId string, extra map[string]any) string {
	p := map[string]any{"version": 2, "function": map[string]any{"id": functionId}, "characteristic_id": "c", "retries": 1}
	maps.Copy(p, extra)
	raw, _ := json.Marshal(p)
	return string(raw)
}

func process(strategy string, payloadJson string, extraInputs string) string {
	return b.Process(
		b.Start("start"),
		`<bpmn:serviceTask id="cmd" senergy:taskType="device_command" senergy:completionStrategy="`+strategy+`">`+
			`<bpmn:extensionElements><camunda:inputOutput><camunda:inputParameter name="payload">`+html.EscapeString(payloadJson)+`</camunda:inputParameter>`+extraInputs+
			`<camunda:outputParameter name="out"><camunda:script scriptFormat="javascript">result</camunda:script></camunda:outputParameter></camunda:inputOutput></bpmn:extensionElements></bpmn:serviceTask>`,
		b.End("end"), b.Flow("start", "cmd"), b.Flow("cmd", "end"),
	)
}

func (this *env) deploy(xml string, selections map[string]model.Selection) string {
	this.t.Helper()
	id := uuid.NewString()
	err := this.s.Tx(ctx, func(tx *store.Tx) error {
		now := this.now()
		if err := tx.InsertDeployment(model.Deployment{Id: id, Namespace: "processes", Name: "d", Owner: "owner", Version: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		if err := tx.InsertDeploymentVersion(model.DeploymentVersion{DeploymentId: id, Version: 1, Xml: xml, Selections: selections, CreatedAt: now}); err != nil {
			return err
		}
		return this.e.ActivateVersion(tx, id, 1)
	})
	if err != nil {
		this.t.Fatal(err)
	}
	return id
}

func (this *env) start(deploymentId string) model.Instance {
	this.t.Helper()
	inst, _, err := this.e.StartInstance(ctx, engine.StartRequest{DeploymentId: deploymentId, Owner: "alice"})
	if err != nil {
		this.t.Fatal(err)
	}
	this.advance(0)
	return inst
}

func (this *env) instance(id string) store.Instance {
	this.t.Helper()
	var result store.Instance
	if err := this.s.Tx(ctx, func(tx *store.Tx) (err error) { result, err = tx.GetInstance(id, false); return err }); err != nil {
		this.t.Fatal(err)
	}
	return result
}

func (this *env) variable(id string, name string) any {
	this.t.Helper()
	var vars []model.Variable
	_ = this.s.Tx(ctx, func(tx *store.Tx) (err error) { vars, err = tx.GetVariables(id); return err })
	for _, v := range vars {
		if v.Name == name {
			return v.Value
		}
	}
	return nil
}

// respond answers a sent message like a protocol handler
func (this *env) respond(s sent, value any) {
	this.t.Helper()
	raw, _ := json.Marshal(value)
	s.msg.Response.Output = map[string]string{"data": string(raw)}
	msg, _ := json.Marshal(s.msg)
	if err := this.dc.HandleResponse(ctx, msg); err != nil {
		this.t.Fatal(err)
	}
}

func (this *env) expectState(id string, state model.InstanceState) store.Instance {
	this.t.Helper()
	inst := this.instance(id)
	if inst.State != state {
		this.t.Fatalf("expected %v, got %v (%v)", state, inst.State, inst.EndReason)
	}
	return inst
}
