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

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/devices"
	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/models/go/models"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
)

// The event scenarios run the cloud setup: the engine role behind its HTTP API and a separate
// event role on the same database, as two deployments of the binary would. Kafka is replaced
// by an in-memory broker behind the consumer of the event role; device-repository,
// import-deploy, import-repository and Keycloak are one fake service. Descriptions, the
// unmarshalling with its conversion and the correlation are the real ones.

const (
	temperatureFunction = "urn:infai:ses:measuring-function:temperature"
	temperatureService  = "urn:infai:ses:service:temperature"
	thermometers        = "urn:infai:ses:device-group:thermometers"
	importId            = "urn:infai:ses:import:weather"
	importTopic         = "import_weather"
)

var serviceTopic = devices.ServiceIdToTopic(temperatureService)

// reading is the output of the thermometers and of the weather import: a temperature in
// kelvin of the aspect air
func reading() models.ContentVariable {
	return models.ContentVariable{Id: "reading", Name: "reading", Type: models.Structure, SubContentVariables: []models.ContentVariable{
		{Id: "temperature", Name: "temperature", Type: models.Float, CharacteristicId: "kelvin", FunctionId: temperatureFunction, AspectId: "air"},
		{Id: "time", Name: "time", Type: models.String},
	}}
}

// platformFake answers as device-repository, import-deploy, import-repository and Keycloak
func platformFake(t *testing.T) string {
	service := models.Service{Id: temperatureService, LocalId: "temperature", Name: "temperature", Interaction: models.EVENT, ProtocolId: "p",
		Outputs: []models.Content{{Id: "out", ContentVariable: reading(), Serialization: models.JSON, ProtocolSegmentId: "data"}}}
	air := models.AspectNode{Id: "air", Name: "air", RootId: "air", AncestorIds: []string{}, DescendentIds: []string{}, ChildIds: []string{}}
	resources := map[string]any{
		"/devices/d1":                       models.Device{Id: "d1", LocalId: "l1", Name: "Thermometer 1", DeviceTypeId: "thermometer", OwnerId: "alice"},
		"/devices/d2":                       models.Device{Id: "d2", LocalId: "l2", Name: "Thermometer 2", DeviceTypeId: "thermometer", OwnerId: "alice"},
		"/device-types/thermometer":         models.DeviceType{Id: "thermometer", Name: "Thermometer", Services: []models.Service{service}},
		"/device-groups/" + thermometers:    models.DeviceGroup{Id: thermometers, Name: "Thermometers", DeviceIds: []string{"d1", "d2"}},
		"/aspect-nodes/air":                 air,
		"/characteristics/kelvin":           models.Characteristic{Id: "kelvin", Name: "kelvin", Type: models.Float},
		"/characteristics/celsius":          models.Characteristic{Id: "celsius", Name: "celsius", Type: models.Float},
		"/functions/" + temperatureFunction: models.Function{Id: temperatureFunction, Name: "temperature", ConceptId: "temperature"},
		"/concepts/temperature": models.Concept{Id: "temperature", Name: "temperature", CharacteristicIds: []string{"celsius", "kelvin"}, BaseCharacteristicId: "celsius",
			Conversions: []models.ConverterExtension{
				{From: "kelvin", To: "celsius", Formula: "x - 273.15", PlaceholderName: "x"},
				{From: "celsius", To: "kelvin", Formula: "x + 273.15", PlaceholderName: "x"},
			}},
		"/instances":             []models.Import{{Id: importId, Name: "Weather", ImportTypeId: "weather", KafkaTopic: importTopic}},
		"/instances/" + importId: models.Import{Id: importId, Name: "Weather", ImportTypeId: "weather", KafkaTopic: importTopic},
		"/import-types/weather": models.ImportType{Id: "weather", Name: "Weather", Output: models.ImportContentVariable{Name: "output", Type: models.Structure,
			SubContentVariables: []models.ImportContentVariable{
				{Name: "import_id", Type: models.String},
				{Name: "value", Type: models.Structure, SubContentVariables: []models.ImportContentVariable{
					{Name: "temperature", Type: models.Float, CharacteristicId: "kelvin", FunctionId: temperatureFunction, AspectId: "air"},
				}},
			}}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/token"):
			// Keycloak: client credentials of the engine and token exchange for owners
			_ = r.ParseForm()
			user := r.Form.Get("requested_subject")
			if user == "" {
				user = "automation-engine"
			}
			token := strings.TrimPrefix(auth.UnsignedToken(user), "Bearer ")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": token, "expires_in": 300, "token_type": "bearer"})
		case r.URL.Path == "/v2/aspect-nodes":
			w.Header().Set("X-Total-Count", "1")
			_ = json.NewEncoder(w).Encode([]models.AspectNode{air})
		default:
			resource, ok := resources[r.URL.Path]
			if !ok {
				http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(resource)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// kafkaFake is the broker behind the consumer of the event role: the worker subscribes the topics
// of its descriptions, and a test sends to them
type kafkaFake struct {
	mux    sync.Mutex
	topics map[string]*subscription
	offset int64
}

type subscription struct {
	handle func(events.Message) error
}

func (this *kafkaFake) consume(ctx context.Context, topics []string, handle func(events.Message) error) error {
	s := &subscription{handle: handle}
	this.mux.Lock()
	for _, topic := range topics {
		this.topics[topic] = s
	}
	this.mux.Unlock()
	go func() {
		<-ctx.Done()
		this.mux.Lock()
		defer this.mux.Unlock()
		for _, topic := range topics {
			if this.topics[topic] == s {
				delete(this.topics, topic)
			}
		}
	}()
	return nil
}

func (this *kafkaFake) subscribed(topic string) bool {
	this.mux.Lock()
	defer this.mux.Unlock()
	return this.topics[topic] != nil
}

// send delivers a message like Kafka to the event role; the error is what makes Kafka deliver
// it again
func (this *kafkaFake) send(topic string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	this.mux.Lock()
	s := this.topics[topic]
	this.offset++
	offset := this.offset
	this.mux.Unlock()
	if s == nil {
		return nil
	}
	return s.handle(events.Message{Topic: topic, Offset: offset, Value: raw, Time: time.Now()})
}

type eventEnv struct {
	*env
	kafka *kafkaFake
}

func newEventEnv(t *testing.T) *eventEnv {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	platform := platformFake(t)
	var config configuration.Config
	srv, err := client.NewTestServer(ctx, func(c *configuration.Config, deps *server.Dependencies) {
		// a file both roles open, as two processes would; an in-memory database with a shared
		// cache locks whole tables against a second connection pool
		c.SqlitePath = filepath.Join(t.TempDir(), "engine.db")
		c.DeviceRepositoryUrl, c.ImportDeployUrl, c.ImportRepositoryUrl = platform, platform, platform
		c.AuthEndpoint, c.AuthClientId = platform, "automation-engine"
		admin := auth.UnsignedToken("device-repository", "admin")
		for topic, id := range map[string]string{"devices": "d1", "device-groups": thermometers} {
			if _, err, _ := deps.Permissions.SetPermission(admin, topic, id, permmodel.ResourcePermissions{UserPermissions: map[string]permmodel.PermissionsMap{
				"alice": {Read: true, Write: true, Execute: true, Administrate: true},
			}}); err != nil {
				t.Fatal(err)
			}
		}
		config = *c
	})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(srv.Handler)
	t.Cleanup(httpServer.Close)

	broker := &kafkaFake{topics: map[string]*subscription{}}
	config.InternalPort = 0
	eventRole, err := server.NewEventServer(ctx, config, slog.New(slog.NewTextHandler(io.Discard, nil)), server.EventDependencies{Consumer: broker.consume})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = eventRole.Run(ctx) }()
	t.Cleanup(func() { _ = eventRole.Close() })
	return &eventEnv{env: &env{t: t, c: client.New(httpServer.URL), url: httpServer.URL, srv: srv}, kafka: broker}
}

// waitForTopic waits until the event role read the descriptions and subscribed the topic
func (this *eventEnv) waitForTopic(topic string) {
	this.t.Helper()
	this.waitFor("the event role to subscribe "+topic, func() bool { return this.kafka.subscribed(topic) })
}

// measure sends the temperature in kelvin of a thermometer as platform-connector does
func (this *eventEnv) measure(deviceId string, kelvin float64) {
	this.t.Helper()
	err := this.kafka.send(serviceTopic, events.DeviceMessage{DeviceId: deviceId, ServiceId: temperatureService,
		Value: map[string]any{"reading": map[string]any{"temperature": kelvin, "time": time.Now().Format(time.RFC3339)}}})
	if err != nil {
		this.t.Fatalf("the event role takes the message: %v", err)
	}
}

// warmEvent is a device event that triggers above 20 °C and passes the value in celsius
func warmEvent(id string, kind string) string {
	return `<bpmn:` + kind + ` id="` + id + `" senergy:function="` + temperatureFunction + `" senergy:aspect="air" senergy:characteristic="celsius" ` +
		`senergy:script="x &gt; 20" senergy:value_variable_name="x" senergy:qos="1">` +
		b.IO(b.OutScript("temperature", "event")) + `<bpmn:messageEventDefinition/></bpmn:` + kind + `>`
}

func near(value any, want float64) bool {
	f, ok := value.(float64)
	return ok && math.Abs(f-want) < 1e-6
}

// A process waits until a thermometer in the room reports more than 20 °C. The device reports
// kelvin; the event converts and checks the value; other devices and colder values pass by.
// Moving the deployment to another thermometer moves the subscription (EVT-01, DEP-08).
func TestDeviceEventContinuesAWaitingInstance(t *testing.T) {
	e := newEventEnv(t)
	xml := b.Process(b.Start("start"), warmEvent("warm", "intermediateCatchEvent"), b.End("end"),
		b.Flow("start", "warm"), b.Flow("warm", "end"))
	selection := func(deviceId string) map[string]model.Selection {
		return map[string]model.Selection{"warm": {DeviceId: deviceId, ServiceId: temperatureService}}
	}
	d := must[model.Deployment](t)(e.c.CreateDeployment(ctx, alice, processes, model.DeploymentInput{Name: "Heating", Xml: xml, Selections: selection("d1")}))
	described := must[[]model.EventDescription](t)(e.c.ListEventDescriptions(ctx, alice, processes, d.Id))
	if len(described) != 1 || described[0].DeviceId != "d1" || described[0].Topic != serviceTopic {
		t.Fatalf("the deployment describes the event of the selected device: %+v", described)
	}
	inst := must[model.Instance](t)(e.c.StartInstance(ctx, alice, processes, model.StartInstanceInput{DeploymentId: d.Id}))
	e.waitForTopic(serviceTopic)

	e.measure("d1", 290)
	e.measure("d2", 300)
	if current := must[model.Instance](t)(e.c.GetInstance(ctx, alice, processes, inst.Id)); current.State != model.StateRunning {
		t.Fatalf("16.85 °C and another device do not trigger: %v", current.State)
	}
	e.measure("d1", 300)
	e.instance(processes, inst.Id, model.StateCompleted)
	if vars := must[[]model.Variable](t)(e.c.GetVariables(ctx, alice, processes, inst.Id)); !near(variable(vars, "temperature"), 26.85) {
		t.Fatalf("the event passes the value converted to celsius: %+v", vars)
	}

	if _, err := e.c.PutDeployment(ctx, alice, processes, d.Id, model.DeploymentInput{Name: "Heating", Xml: xml, Selections: selection("d2"), UpdateStrategy: model.UpdateDrain}); err == nil {
		t.Fatal("alice has no right on d2")
	}
	if _, err := e.c.PutDeployment(ctx, alice, processes, d.Id, model.DeploymentInput{Name: "Heating", Xml: xml, Selections: map[string]model.Selection{"warm": {DeviceGroupId: thermometers}}, UpdateStrategy: model.UpdateDrain}); err != nil {
		t.Fatal(err)
	}
	second := must[model.Instance](t)(e.c.StartInstance(ctx, alice, processes, model.StartInstanceInput{DeploymentId: d.Id}))
	// the event role reads changed descriptions every second
	e.waitFor("the descriptions of the group", func() bool {
		e.measure("d2", 301)
		current, _ := e.c.GetInstance(ctx, alice, processes, second.Id)
		return current.State == model.StateCompleted
	})
}

// Every warm reading of a thermometer of the group starts an instance with the temperature.
// A switched-off deployment starts nothing; the messages are taken and not delivered again
// (EVT-01, DEP-12).
func TestDeviceEventsStartInstancesUnlessSwitchedOff(t *testing.T) {
	e := newEventEnv(t)
	d := must[model.Deployment](t)(e.c.CreateDeployment(ctx, alice, processes, model.DeploymentInput{Name: "Too warm", Xml: b.Process(
		warmEvent("warm", "startEvent"), b.Script("note", "note", "'too warm: ' + temperature"), b.End("end"),
		b.Flow("warm", "note"), b.Flow("note", "end"),
	), Selections: map[string]model.Selection{"warm": {DeviceGroupId: thermometers}}}))
	described := must[[]model.EventDescription](t)(e.c.ListEventDescriptions(ctx, alice, processes, d.Id))
	if len(described) != 2 {
		t.Fatalf("a group describes the event of each of its devices: %+v", described)
	}
	e.waitForTopic(serviceTopic)
	started := func() int {
		return must[model.Page[model.Instance]](t)(e.c.ListInstances(ctx, alice, processes, model.InstanceListOptions{DeploymentId: d.Id})).Total
	}

	e.measure("d1", 300)
	e.measure("d2", 301)
	e.measure("d1", 280)
	if n := started(); n != 2 {
		t.Fatalf("each warm reading of the group starts an instance: %v", n)
	}
	finished := must[model.Page[model.Instance]](t)(e.c.ListInstances(ctx, alice, processes, model.InstanceListOptions{DeploymentId: d.Id}))
	for _, inst := range finished.Items {
		done := e.instance(processes, inst.Id, model.StateCompleted)
		if vars := must[[]model.Variable](t)(e.c.GetVariables(ctx, alice, processes, done.Id)); !strings.HasPrefix(variable(vars, "note").(string), "too warm: 2") {
			t.Errorf("the instance has the temperature of its event: %+v", vars)
		}
	}

	if _, err := e.c.SetDeploymentDisabled(ctx, alice, processes, d.Id, model.DisabledInput{Disabled: true}); err != nil {
		t.Fatal(err)
	}
	e.measure("d1", 305)
	if n := started(); n != 2 {
		t.Fatalf("a switched-off deployment is not started by events: %v", n)
	}
	if _, err := e.c.SetDeploymentDisabled(ctx, alice, processes, d.Id, model.DisabledInput{Disabled: false}); err != nil {
		t.Fatal(err)
	}
	e.measure("d1", 305)
	if n := started(); n != 3 {
		t.Fatalf("switched on again, events start it: %v", n)
	}
}

// An import event works like a device event on the topic of the import; messages of another
// import on the same topic pass by (EVT-02).
func TestImportEventContinuesAWaitingInstance(t *testing.T) {
	e := newEventEnv(t)
	d := must[model.Deployment](t)(e.c.CreateDeployment(ctx, alice, processes, model.DeploymentInput{Name: "Weather", Xml: b.Process(
		b.Start("start"), warmEvent("warm", "intermediateCatchEvent"), b.End("end"),
		b.Flow("start", "warm"), b.Flow("warm", "end"),
	), Selections: map[string]model.Selection{"warm": {ImportId: importId}}}))
	if described := must[[]model.EventDescription](t)(e.c.ListEventDescriptions(ctx, alice, processes, d.Id)); len(described) != 1 || described[0].Topic != importTopic {
		t.Fatalf("the import names its topic: %+v", described)
	}
	inst := must[model.Instance](t)(e.c.StartInstance(ctx, alice, processes, model.StartInstanceInput{DeploymentId: d.Id}))
	e.waitForTopic(importTopic)
	weather := func(id string, kelvin float64) {
		t.Helper()
		if err := e.kafka.send(importTopic, map[string]any{"import_id": id, "value": map[string]any{"temperature": kelvin}}); err != nil {
			t.Fatal(err)
		}
	}
	weather("urn:infai:ses:import:other", 300)
	weather(importId, 280)
	if current := must[model.Instance](t)(e.c.GetInstance(ctx, alice, processes, inst.Id)); current.State != model.StateRunning {
		t.Fatalf("another import and a cold value do not trigger: %v", current.State)
	}
	weather(importId, 299.15)
	e.instance(processes, inst.Id, model.StateCompleted)
	if vars := must[[]model.Variable](t)(e.c.GetVariables(ctx, alice, processes, inst.Id)); !near(variable(vars, "temperature"), 26) {
		t.Fatalf("the import value is converted to celsius: %+v", vars)
	}
}
