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

// Package fogsync_test runs a cloud and a gateway engine connected like by
// mgw-device-cloud-connector and checks the synchronisation from end to end (section 17).
package fogsync_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/fogsync"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
	"github.com/SENERGY-Platform/automation-engine/pkg/permissions"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
	"github.com/SENERGY-Platform/models/go/models"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
)

var ctx = context.Background()
var alice = client.TestToken("alice")
var bob = client.TestToken("bob")

const network = "hub-1"

type env struct {
	t       *testing.T
	cloud   *server.Server
	gateway *server.Server
	// the connector between both brokers; false drops everything, as the connector does offline
	online atomic.Bool
	// the broker of the cloud, for a gateway that replaces the first one
	cloudBroker *mqtt.Memory
}

// bridge forwards like mgw-device-cloud-connector: processes/{network}/cmd/# of the cloud to
// processes/cmd/# of the gateway, processes/state/# of the gateway to processes/{network}/state/#
func (this *env) bridge(cloudBroker *mqtt.Memory, gatewayBroker *mqtt.Memory) {
	_ = cloudBroker.Subscribe("processes/"+network+"/cmd/#", 1, func(topic string, payload []byte) error {
		if this.online.Load() {
			_ = gatewayBroker.Publish(ctx, "processes/cmd/"+strings.TrimPrefix(topic, "processes/"+network+"/cmd/"), 1, payload)
		}
		return nil
	})
	_ = gatewayBroker.Subscribe("processes/state/#", 1, func(topic string, payload []byte) error {
		if this.online.Load() {
			_ = cloudBroker.Publish(ctx, "processes/"+network+"/state/"+strings.TrimPrefix(topic, "processes/state/"), 1, payload)
		}
		return nil
	})
}

func newEnv(t *testing.T, configure ...func(*configuration.Config)) *env {
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	this := &env{t: t}
	this.online.Store(true)
	cloudBroker, gatewayBroker := mqtt.NewMemory(), mqtt.NewMemory()
	this.cloudBroker = cloudBroker
	this.bridge(cloudBroker, gatewayBroker)

	config := configuration.Default()
	config.SqlitePath = storetest.MemoryDsn()
	config.PollInterval = "20ms"
	config.SyncResendAfter = "300ms"
	config.SyncCallTimeout = "500ms"
	for _, c := range configure {
		c(&config)
	}
	deps, err := client.TestDependencies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// alice may deploy to the gateway, bob only read and start (SEC-05)
	admin := auth.UnsignedToken("device-repository", "admin")
	if _, err, _ = deps.Permissions.SetTopic(admin, permmodel.Topic{Id: permissions.HubsTopic}); err != nil {
		t.Fatal(err)
	}
	_, err, _ = deps.Permissions.SetPermission(admin, permissions.HubsTopic, network, permmodel.ResourcePermissions{UserPermissions: map[string]permmodel.PermissionsMap{
		"alice": {Read: true, Write: true, Execute: true, Administrate: true},
		"bob":   {Read: true, Execute: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err, _ = deps.Permissions.SetPermission(admin, "devices", "d1", permmodel.ResourcePermissions{UserPermissions: map[string]permmodel.PermissionsMap{
		"alice": {Read: true, Write: true, Execute: true, Administrate: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	deps.SyncBroker = cloudBroker
	deps.EventSeries, deps.EventUnmarshaller = series{}, unmarshaller{}
	if this.cloud, err = server.New(ctx, config, logger, deps); err != nil {
		t.Fatal(err)
	}
	this.cloud.Start(ctx)

	gatewayConfig := configuration.Default()
	gatewayConfig.SqlitePath = storetest.MemoryDsn()
	gatewayConfig.PollInterval = "20ms"
	gatewayConfig.Mode, gatewayConfig.MqttBroker = "fog", "memory"
	if this.gateway, err = server.New(ctx, gatewayConfig, logger, server.Dependencies{Broker: gatewayBroker}); err != nil {
		t.Fatal(err)
	}
	this.gateway.Start(ctx)
	this.waitFor("the status of the gateway", func() bool {
		var found bool
		_ = this.cloud.Store.Tx(ctx, func(tx *store.Tx) (err error) { _, found, err = tx.GetNetwork(network); return err })
		return found
	})
	return this
}

func (this *env) waitFor(what string, check func() bool) {
	this.t.Helper()
	this.waitUpTo(what, 10*time.Second, check)
}

func (this *env) waitUpTo(what string, timeout time.Duration, check func() bool) {
	this.t.Helper()
	deadline := time.Now().Add(timeout)
	for !check() {
		if time.Now().After(deadline) {
			this.t.Fatalf("timeout waiting for %v", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitProcess() string {
	return b.Process(b.Message("m", "go"), b.Start("start"), b.CatchMessage("wait", "m"), b.End("end"), b.Flow("start", "wait"), b.Flow("wait", "end"))
}

func (this *env) cloudInstance(id string) model.Instance {
	inst, _ := this.cloud.Controller.GetInstance(ctx, alice, "processes", id)
	return inst
}

func (this *env) gatewayDeployment(id string) (model.Deployment, error) {
	var d model.Deployment
	err := this.gateway.Store.Tx(ctx, func(tx *store.Tx) (err error) { d, err = tx.GetDeployment(id, false); return err })
	return d, err
}

func TestDeployStartAndFollowAnInstanceOnAGateway(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: waitProcess(), NetworkId: network})
	if err != nil {
		t.Fatal(err)
	}
	e.waitFor("the deployment on the gateway", func() bool {
		gd, err := e.gatewayDeployment(d.Id)
		return err == nil && gd.Version == 1 && gd.NetworkId == "" && gd.Owner == "alice"
	})
	inst, err := c.StartInstance(ctx, alice, "processes", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	if inst.State != model.StatePending || inst.NetworkId != network {
		t.Errorf("the cloud shows the instance as pending until the gateway reports it: %+v", inst)
	}
	e.waitFor("the running instance in the cloud", func() bool { return e.cloudInstance(inst.Id).State == model.StateRunning })
	if _, err = e.gateway.Engine.Correlate(ctx, engine.CorrelationRequest{MessageInput: model.MessageInput{Name: "go", InstanceId: inst.Id}}); err != nil {
		t.Fatal(err)
	}
	e.waitFor("the completed instance in the cloud", func() bool { return e.cloudInstance(inst.Id).State == model.StateCompleted })
	if got := e.cloudInstance(inst.Id); got.Status != model.StatusReady || got.Owner != "alice" {
		t.Errorf("status and owner as on the gateway: %+v", got)
	}
	cloudOnly, _ := c.ListInstances(ctx, alice, "processes", model.InstanceListOptions{NetworkId: "cloud"})
	gatewayOnly, _ := c.ListInstances(ctx, alice, "processes", model.InstanceListOptions{NetworkId: network})
	if cloudOnly.Total != 0 || gatewayOnly.Total != 1 {
		t.Errorf("instances are listed by network (API-05): %v %v", cloudOnly.Total, gatewayOnly.Total)
	}

	// the cloud does not run the deployment itself
	var subscriptions int
	_ = e.cloud.Store.Tx(ctx, func(tx *store.Tx) (err error) {
		rows, err := tx.StorageRows()
		subscriptions = rows["subscriptions"]
		return err
	})
	if subscriptions != 0 {
		t.Errorf("no waiting point in the cloud: %v", subscriptions)
	}

	networks, err := c.ListNetworks(ctx, bob)
	if err != nil || len(networks) != 1 || networks[0].Id != network || !networks[0].Online || !networks[0].Compatible || networks[0].EngineVersion == "" {
		t.Errorf("the network overview shows the gateway (SYNC-12): %+v %v", networks, err)
	}
	if others, err := c.ListNetworks(ctx, client.TestToken("carol")); err != nil || len(others) != 0 {
		t.Errorf("only gateways the user may read: %+v %v", others, err)
	}
	detail, err := c.GetNetwork(ctx, alice, network)
	// namespace and deployment; the start of the completed instance is done and forgotten
	if err != nil || len(detail.Resources) != 2 {
		t.Fatalf("desired and applied state per resource (SYNC-13): %+v %v", detail, err)
	}
	for _, r := range detail.Resources {
		if r.State != model.ResourceApplied {
			t.Errorf("every resource is applied: %+v", r)
		}
	}
	if _, err = c.GetNetwork(ctx, client.TestToken("carol"), network); err == nil {
		t.Errorf("a gateway the user may not read is not found")
	}

	if err = c.DeleteDeployment(ctx, alice, "processes", d.Id); err != nil {
		t.Fatal(err)
	}
	e.waitFor("the deletion on the gateway", func() bool { _, err := e.gatewayDeployment(d.Id); return err != nil })
}

func TestStopAndDeleteReachTheGateway(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: waitProcess(), NetworkId: network})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.PutInstance(ctx, alice, "processes", "i-1", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	if again, err := c.PutInstance(ctx, alice, "processes", "i-1", model.StartInstanceInput{DeploymentId: d.Id}); err != nil || again.Id != inst.Id {
		t.Fatalf("a start with the id is idempotent (RUN-01): %+v %v", again, err)
	}
	e.waitFor("the running instance", func() bool { return e.cloudInstance("i-1").State == model.StateRunning })
	if err = c.SetVariable(ctx, alice, "processes", "i-1", "x", 1); err == nil || !strings.Contains(err.Error(), "gateway") {
		t.Errorf("variables of an instance on a gateway change only there: %v", err)
	}
	if err = c.StopInstances(ctx, alice, "processes", []string{"i-1"}); err != nil {
		t.Fatal(err)
	}
	e.waitFor("the stopped instance", func() bool { return e.cloudInstance("i-1").State == model.StateTerminated })
	if err = c.DeleteInstances(ctx, alice, "processes", []string{"i-1"}); err != nil {
		t.Fatal(err)
	}
	e.waitFor("the deletion on the gateway", func() bool {
		var err error
		_ = e.gateway.Store.Tx(ctx, func(tx *store.Tx) error { _, err = tx.GetInstance("i-1", false); return nil })
		return err != nil
	})
}

func TestRightsOnTheGatewayAreChecked(t *testing.T) {
	e := newEnv(t)
	if _, err := e.cloud.Controller.CreateDeployment(ctx, bob, "processes", model.DeploymentInput{Xml: waitProcess(), NetworkId: network}); err == nil || !strings.Contains(err.Error(), "network") {
		t.Errorf("deploying to a gateway needs the administrate right on its hub (SEC-05): %v", err)
	}
	if _, err := e.cloud.Controller.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: waitProcess(), NetworkId: "hub-2"}); err == nil {
		t.Errorf("a hub without rights is refused")
	}
}

func TestDesiredStateIsSentAgainAfterLosses(t *testing.T) {
	e := newEnv(t)
	e.online.Store(false)
	d, err := e.cloud.Controller.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: waitProcess(), NetworkId: network})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err = e.gatewayDeployment(d.Id); err == nil {
		t.Fatal("offline, nothing arrives")
	}
	e.online.Store(true)
	e.waitFor("the deployment after the resend time (SYNC-08)", func() bool { _, err := e.gatewayDeployment(d.Id); return err == nil })
	e.waitFor("the acknowledgement in the cloud (SYNC-13)", func() bool {
		var desired store.Desired
		_ = e.cloud.Store.Tx(ctx, func(tx *store.Tx) (err error) {
			desired, _, err = tx.GetDesired(network, "deployment", d.Id)
			return err
		})
		return desired.AppliedVersion == desired.Version && desired.Version > 0
	})
}

// A smart service deploys with prefer_fog_deployment to the first gateway that carries every
// selected device, as smart-service-module-worker-process did (TASK-20, DEP-09).
func TestProcessDeploymentChoosesTheGatewayOfTheDevices(t *testing.T) {
	output := []models.Content{{ContentVariable: models.ContentVariable{Name: "value", FunctionId: measuring, AspectId: "air"}}}
	answers := map[string]any{
		"/hubs/" + network:  models.Hub{Id: network, DeviceIds: []string{"d1"}},
		"/devices/d1":       models.Device{Id: "d1", LocalId: "local-d1", DeviceTypeId: "dt"},
		"/device-types/dt":  models.DeviceType{Id: "dt", Services: []models.Service{{Id: "s1", LocalId: "temperature", Interaction: models.EVENT, Outputs: output}}},
		"/aspect-nodes/air": models.AspectNode{Id: "air", RootId: "air"},
	}
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if answer, ok := answers[r.URL.Path]; ok {
			_ = json.NewEncoder(w).Encode(answer)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer repository.Close()
	e := newEnv(t, func(c *configuration.Config) { c.DeviceRepositoryUrl = repository.URL })
	c := e.cloud.Controller
	m, err := c.CreateModel(ctx, alice, "processes", model.ModelInput{Xml: b.Process(
		b.Start("start"),
		`<bpmn:intermediateCatchEvent id="work" senergy:function="`+measuring+`" senergy:aspect="air" senergy:characteristic="c" senergy:script="x &gt; 20" senergy:value_variable_name="x"><bpmn:messageEventDefinition/></bpmn:intermediateCatchEvent>`,
		b.End("end"), b.Flow("start", "work"), b.Flow("work", "end"),
	)})
	if err != nil {
		t.Fatal(err)
	}
	service, err := c.CreateDeployment(ctx, alice, "smart-services", model.DeploymentInput{Xml: b.Process(
		b.Start("start"),
		b.Service("deploy", "process_deployment",
			b.InText("process_deployment.process_model_id", m.Id),
			b.InText("process_deployment.prefer_fog_deployment", "true"),
			b.InText("process_deployment.work.selection", `{"device_selection":{"device_id":"d1"}}`),
			b.OutScript("fog", "is_fog_deployment"), b.OutScript("hub", "fog_hub"), b.OutScript("deployment", "process_deployment_id")),
		b.End("end"), b.Flow("start", "deploy"), b.Flow("deploy", "end"),
	)})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "smart-services", model.StartInstanceInput{DeploymentId: service.Id})
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	e.waitFor("the smart service", func() bool {
		current, _ := c.GetInstance(ctx, alice, "smart-services", inst.Id)
		vars, _ := c.GetVariables(ctx, alice, "smart-services", inst.Id)
		values = map[string]any{}
		for _, v := range vars {
			values[v.Name] = v.Value
		}
		return current.State != model.StateRunning
	})
	if values["fog"] != true || values["hub"] != network {
		incidents, _ := c.ListIncidents(ctx, alice, "smart-services", model.IncidentListOptions{InstanceId: inst.Id})
		t.Fatalf("the module reports the gateway: %v %+v", values, incidents.Items)
	}
	deploymentId, _ := values["deployment"].(string)
	e.waitFor("the deployment on the gateway", func() bool { _, err := e.gatewayDeployment(deploymentId); return err == nil })
	var descriptions []model.EventDescription
	_ = e.gateway.Store.Tx(ctx, func(tx *store.Tx) (err error) { descriptions, err = tx.ListEventDescriptions(deploymentId); return err })
	if len(descriptions) != 1 || descriptions[0].Topic != "event/local-d1/temperature" {
		t.Errorf("the gateway gets the descriptions of the cloud with its local topics (SYNC-06, FOG-06): %+v", descriptions)
	}
}

const measuring = "urn:infai:ses:measuring-function:temperature"

// series has a reading of every device at the start of each waiting period
type series struct{}

func (series) Range(_ context.Context, _ string, _ string, _ string, columns []string, from time.Time, _ time.Time, _ int) ([]events.Row, error) {
	return []events.Row{{Time: from.Add(time.Millisecond), Values: map[string]any{columns[0]: 25.0}}}, nil
}

func (series) Last(context.Context, string, string, string, []string) (*events.Row, error) {
	return nil, nil
}

type unmarshaller struct{}

func (unmarshaller) Unmarshal(desc events.MessageDesc) (any, error) {
	return 25.0, nil
}

// The history of an instance on a gateway stays there; the cloud reads it on demand, and
// shows its own copy, marked, while the gateway is offline (DBG-15, DBG-12).
func TestDebuggingReadsTheGateway(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: waitProcess(), NetworkId: network})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "processes", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	e.waitFor("the running instance", func() bool { return e.cloudInstance(inst.Id).State == model.StateRunning })

	state, err := c.DebugState(ctx, alice, "processes", inst.Id, model.DebugOptions{})
	if err != nil || state.Offline || len(state.Active) != 1 || state.Active[0].ElementId != "wait" {
		t.Fatalf("the state comes from the gateway: %+v %v", state, err)
	}
	activities, err := c.ListActivities(ctx, alice, "processes", inst.Id, model.ActivityListOptions{})
	if err != nil || len(activities.Items) == 0 {
		t.Fatalf("the history comes from the gateway: %+v %v", activities, err)
	}
	runs, err := c.ListRuns(ctx, alice, "processes", inst.Id)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs come from the gateway: %+v %v", runs, err)
	}
	history, err := c.HistoryState(ctx, alice, "processes", inst.Id, model.HistoryOptions{Seq: activities.Items[0].Seq})
	if err != nil || history.Offline || history.Seq != activities.Items[0].Seq {
		t.Fatalf("a step of the history comes from the gateway: %+v %v", history, err)
	}
	if _, err = c.DebugState(ctx, client.TestToken("carol"), "processes", inst.Id, model.DebugOptions{}); err == nil {
		t.Errorf("reading needs the right on the instance")
	}

	watchCtx, cancel := context.WithCancel(ctx)
	stream, err := c.Watch(watchCtx, alice, "processes", inst.Id, model.WatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-stream:
		if message.State == nil || message.State.Instance.Id != inst.Id {
			t.Errorf("the live stream starts with the state of the gateway: %+v", message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no live message from the gateway")
	}
	if _, err = e.gateway.Engine.Correlate(ctx, engine.CorrelationRequest{MessageInput: model.MessageInput{Name: "go", InstanceId: inst.Id}}); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-stream:
		if message.Seq == 0 {
			t.Errorf("a change on the gateway streams to the cloud: %+v", message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no live change from the gateway")
	}
	cancel()

	e.online.Store(false)
	start := time.Now()
	state, err = c.DebugState(ctx, alice, "processes", inst.Id, model.DebugOptions{})
	if err != nil || !state.Offline || state.Instance.Id != inst.Id {
		t.Errorf("an offline gateway yields the copy of the cloud, marked: %+v %v", state, err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("the wait for the gateway is bounded")
	}
	if _, err = c.ListActivities(ctx, alice, "processes", inst.Id, model.ActivityListOptions{}); !errors.Is(err, model.ErrUnavailable) {
		t.Errorf("the history of an offline gateway is unavailable: %v", err)
	}
}

// Event evaluations of an instance on a gateway are reconstructed in the cloud: the waiting
// periods come from the gateway, the device data from timescale with the ids of the cloud (EVT-13).
func TestEventsOfAGatewayAreReconstructedInTheCloud(t *testing.T) {
	output := []models.Content{{ContentVariable: models.ContentVariable{Name: "value", FunctionId: measuring, AspectId: "air"}}}
	answers := map[string]any{
		"/devices/d1":       models.Device{Id: "d1", LocalId: "local-d1", DeviceTypeId: "dt"},
		"/device-types/dt":  models.DeviceType{Id: "dt", Services: []models.Service{{Id: "s1", LocalId: "temperature", Interaction: models.EVENT, Outputs: output}}},
		"/aspect-nodes/air": models.AspectNode{Id: "air", RootId: "air"},
	}
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if answer, ok := answers[r.URL.Path]; ok {
			_ = json.NewEncoder(w).Encode(answer)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer repository.Close()
	e := newEnv(t, func(c *configuration.Config) { c.DeviceRepositoryUrl = repository.URL })
	c := e.cloud.Controller
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{NetworkId: network, Xml: b.Process(
		b.Start("start"),
		`<bpmn:intermediateCatchEvent id="reading" senergy:function="`+measuring+`" senergy:aspect="air" senergy:characteristic="c" senergy:script="x &gt; 20" senergy:value_variable_name="x"><bpmn:messageEventDefinition/></bpmn:intermediateCatchEvent>`,
		b.End("end"), b.Flow("start", "reading"), b.Flow("reading", "end"),
	), Selections: map[string]model.Selection{"reading": {DeviceId: "d1", ServiceId: "s1"}}})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "processes", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	e.waitFor("the running instance", func() bool { return e.cloudInstance(inst.Id).State == model.StateRunning })
	result, err := c.EventEvaluations(ctx, alice, "processes", inst.Id, model.EventReconstructionOptions{ElementId: "reading"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Waiting) != 1 || result.Waiting[0].To != nil || len(result.Sources) != 1 || len(result.Evaluations) == 0 {
		t.Fatalf("the waiting period of the gateway and the data of the cloud make the reconstruction: %+v", result)
	}
	if !strings.Contains(strings.Join(result.Limitations, " "), "gateway") {
		t.Errorf("gaps of offline periods are named: %v", result.Limitations)
	}
}

// An instance that ends while the gateway is offline stays on the gateway, whatever its budget
// says, until the cloud has taken its end; then the gateway may remove it, and neither side
// keeps its start any longer (LONG-07, LONG-10).
func TestTheCloudLearnsTheEndBeforeTheGatewayTrims(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: waitProcess(), NetworkId: network})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "processes", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	e.waitFor("the running instance", func() bool { return e.cloudInstance(inst.Id).State == model.StateRunning })
	e.online.Store(false)
	if _, err = e.gateway.Engine.Correlate(ctx, engine.CorrelationRequest{MessageInput: model.MessageInput{Name: "go", InstanceId: inst.Id}}); err != nil {
		t.Fatal(err)
	}
	// trimming has to meet the ended instance, or keeping it proves nothing
	e.waitFor("the completed instance on the gateway", func() bool {
		var state model.InstanceState
		_ = e.gateway.Store.Tx(ctx, func(tx *store.Tx) error {
			inst, err := tx.GetInstance(inst.Id, false)
			state = inst.State
			return err
		})
		return state == model.StateCompleted
	})
	trim := func() {
		err := e.gateway.Store.Tx(ctx, func(tx *store.Tx) error {
			if _, err := tx.TrimOldestHistory(1000); err != nil {
				return err
			}
			_, err := tx.DeleteFinishedInstances("deployment_id", d.Id, time.Now().Add(time.Hour), 1000)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	onGateway := func() bool {
		return e.gateway.Store.Tx(ctx, func(tx *store.Tx) error { _, err := tx.GetInstance(inst.Id, true); return err }) == nil
	}
	trim()
	if !onGateway() {
		t.Fatal("the ended instance stays until the cloud has taken its end")
	}
	e.online.Store(true)
	// a batch the gateway sent while offline is lost and sent again after the resend time of
	// the gateway, 30 s by default; whether one was sent depends on its one-second tick
	e.waitUpTo("the completed instance in the cloud", 45*time.Second, func() bool { return e.cloudInstance(inst.Id).State == model.StateCompleted })
	e.waitFor("the acknowledgement on the gateway", func() bool {
		var acked, latest int64
		_ = e.gateway.Store.Tx(ctx, func(tx *store.Tx) (err error) {
			if acked, err = tx.SyncPosition(store.PositionUpAcked); err != nil {
				return err
			}
			latest, err = tx.LatestChangeSeq()
			return err
		})
		return acked == latest
	})
	trim()
	if onGateway() {
		t.Fatal("once the cloud has taken it, the budget may remove it")
	}
	if got := e.cloudInstance(inst.Id); got.State != model.StateCompleted {
		t.Errorf("the cloud keeps its copy: %+v", got)
	}
	if err = e.gateway.Sync.SendStatus(ctx); err != nil {
		t.Fatal(err)
	}
	var appliedOnGateway, desiredInCloud bool
	_ = e.gateway.Store.Tx(ctx, func(tx *store.Tx) (err error) {
		_, appliedOnGateway, err = tx.GetApplied(fogsync.ResourceInstance, inst.Id)
		return err
	})
	_ = e.cloud.Store.Tx(ctx, func(tx *store.Tx) (err error) {
		_, desiredInCloud, err = tx.GetDesired(network, fogsync.ResourceInstance, inst.Id)
		return err
	})
	if appliedOnGateway || desiredInCloud {
		t.Errorf("the start of an ended instance is forgotten on both sides: gateway %v, cloud %v", appliedOnGateway, desiredInCloud)
	}
}
