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
	"fmt"
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
// event role on the same database, as two deployments of the binary would. In the fakes
// variant, an in-memory broker replaces Kafka behind the consumer of the event role, and one
// fake service answers as device-repository, import-deploy, import-repository and Keycloak;
// in the containers variant, both roles use Kafka and the services. Descriptions, the
// unmarshalling with its conversion and the correlation are the real ones in both.
//
// With Kafka, sending does not wait for the event role. The scenarios therefore never assert
// that a message did nothing right after sending it; the message that triggers afterwards
// carries a value that only it can have produced, and one partition keeps the order.

// the meaning of a temperature, the same in both variants
const (
	temperatureFunction = "urn:infai:ses:measuring-function:test-temperature"
	airAspect           = "urn:infai:ses:aspect:test-air"
	kelvin              = "urn:infai:ses:characteristic:test-kelvin"
	celsius             = "urn:infai:ses:characteristic:test-celsius"
	temperatureConcept  = "urn:infai:ses:concept:test-temperature"
)

func temperatureModels() ([]models.Characteristic, models.Concept, models.Function) {
	return []models.Characteristic{{Id: kelvin, Name: "kelvin", Type: models.Float}, {Id: celsius, Name: "celsius", Type: models.Float}},
		models.Concept{Id: temperatureConcept, Name: "temperature", CharacteristicIds: []string{celsius, kelvin}, BaseCharacteristicId: celsius,
			Conversions: []models.ConverterExtension{
				{From: kelvin, To: celsius, Formula: "x - 273.15", PlaceholderName: "x"},
				{From: celsius, To: kelvin, Formula: "x + 273.15", PlaceholderName: "x"},
			}},
		models.Function{Id: temperatureFunction, Name: "temperature", ConceptId: temperatureConcept, RdfType: models.SES_ONTOLOGY_MEASURING_FUNCTION}
}

// reading is the output of the thermometers: a temperature in kelvin of the aspect air
func reading() models.ContentVariable {
	return models.ContentVariable{Id: "reading", Name: "reading", Type: models.Structure, SubContentVariables: []models.ContentVariable{
		{Id: "temperature", Name: "temperature", Type: models.Float, CharacteristicId: kelvin, FunctionId: temperatureFunction, AspectId: airAspect, AspectIds: []string{airAspect}},
		{Id: "time", Name: "time", Type: models.String},
	}}
}

// weather is the output of the weather import, with the same temperature
func weather() models.ImportContentVariable {
	return models.ImportContentVariable{Name: "output", Type: models.Structure, SubContentVariables: []models.ImportContentVariable{
		{Name: "import_id", Type: models.String},
		{Name: "time", Type: models.String},
		{Name: "value", Type: models.Structure, SubContentVariables: []models.ImportContentVariable{
			{Name: "temperature", Type: models.Float, CharacteristicId: kelvin, FunctionId: temperatureFunction, AspectIds: []string{airAspect}},
		}},
	}}
}

// fixture names what an event scenario selects
type fixture struct {
	service string
	// thermometers of alice, and one she may not use
	d1, d2, foreign string
	// a group of d1 and d2
	group       string
	importId    string
	importTopic string
}

func (this fixture) serviceTopic() string {
	return devices.ServiceIdToTopic(this.service)
}

// bus is how messages reach the event role
type bus interface {
	send(topic string, key string, value any) error
	// ready waits until the event role reads the topic and takes what is sent from now on;
	// probe is a message that triggers nothing
	ready(t *testing.T, topic string, probe any)
	// drain waits until the event role took everything sent to the topic
	drain(t *testing.T, topic string)
}

type eventEnv struct {
	*env
	fixture
	bus bus
}

// eachEventVariant runs an event scenario with stand-ins and, without -short, with the real
// dependencies in containers
func eachEventVariant(t *testing.T, scenario func(t *testing.T, e *eventEnv)) {
	t.Run("fakes", func(t *testing.T) { scenario(t, newFakeEventEnv(t)) })
	t.Run("containers", func(t *testing.T) { scenario(t, newContainerEventEnv(t, containers(t))) })
}

// startRoles starts the engine role with configure and the event role on the same database
func startRoles(t *testing.T, configure func(c *configuration.Config, deps *server.Dependencies), consumer events.Consumer) *env {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	var config configuration.Config
	srv, err := client.NewTestServer(ctx, func(c *configuration.Config, deps *server.Dependencies) {
		configure(c, deps)
		config = *c
	})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(srv.Handler)
	t.Cleanup(httpServer.Close)

	config.InternalPort = 0
	eventRole, err := server.NewEventServer(ctx, config, slog.New(slog.NewTextHandler(io.Discard, nil)), server.EventDependencies{Consumer: consumer})
	if err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- eventRole.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-stopped; err != nil {
			t.Errorf("the event role stopped: %v", err)
		}
		_ = eventRole.Close()
	})
	return &env{t: t, c: client.New(httpServer.URL), url: httpServer.URL, srv: srv}
}

// measure sends the temperature in kelvin of a thermometer as platform-connector does
func (this *eventEnv) measure(deviceId string, kelvin float64) {
	this.t.Helper()
	err := this.bus.send(this.serviceTopic(), deviceId, events.DeviceMessage{DeviceId: deviceId, ServiceId: this.service,
		Value: map[string]any{"reading": map[string]any{"temperature": kelvin, "time": time.Now().Format(time.RFC3339)}}})
	if err != nil {
		this.t.Fatalf("the event role takes the message: %v", err)
	}
}

// report sends the temperature in kelvin of an import as the import does
func (this *eventEnv) report(importId string, kelvin float64) {
	this.t.Helper()
	err := this.bus.send(this.importTopic, importId, map[string]any{"import_id": importId, "time": time.Now().Format(time.RFC3339),
		"value": map[string]any{"temperature": kelvin}})
	if err != nil {
		this.t.Fatalf("the event role takes the message: %v", err)
	}
}

func (this *eventEnv) readyForDevices() {
	this.t.Helper()
	this.bus.ready(this.t, this.serviceTopic(), events.DeviceMessage{DeviceId: "probe", ServiceId: this.service, Value: map[string]any{}})
}

func (this *eventEnv) readyForImports() {
	this.t.Helper()
	this.bus.ready(this.t, this.importTopic, map[string]any{"import_id": "probe"})
}

// warmEvent is a device event that triggers above 20 °C and passes the value in celsius
func warmEvent(id string, kind string) string {
	return `<bpmn:` + kind + ` id="` + id + `" senergy:function="` + temperatureFunction + `" senergy:aspect="` + airAspect + `" senergy:characteristic="` + celsius + `" ` +
		`senergy:script="x &gt; 20" senergy:value_variable_name="x" senergy:qos="1">` +
		b.IO(b.OutScript("temperature", "event")) + `<bpmn:messageEventDefinition/></bpmn:` + kind + `>`
}

func near(value any, want float64) bool {
	f, ok := value.(float64)
	return ok && math.Abs(f-want) < 1e-6
}

// A process waits until a thermometer in the room reports more than 20 °C. The device reports
// kelvin; the event converts and checks the value; other devices and colder values pass by.
// Moving the deployment to a group moves the subscription (EVT-01, DEP-08).
func TestDeviceEventContinuesAWaitingInstance(t *testing.T) {
	eachEventVariant(t, func(t *testing.T, e *eventEnv) {
		xml := b.Process(b.Start("start"), warmEvent("warm", "intermediateCatchEvent"), b.End("end"),
			b.Flow("start", "warm"), b.Flow("warm", "end"))
		device := func(deviceId string) map[string]model.Selection {
			return map[string]model.Selection{"warm": {DeviceId: deviceId, ServiceId: e.service}}
		}
		if _, err := e.c.CreateDeployment(ctx, alice, processes, model.DeploymentInput{Name: "Heating", Xml: xml, Selections: device(e.foreign)}); err == nil {
			t.Fatal("a device alice may not use is not selected")
		}
		d := must[model.Deployment](t)(e.c.CreateDeployment(ctx, alice, processes, model.DeploymentInput{Name: "Heating", Xml: xml, Selections: device(e.d1)}))
		described := must[[]model.EventDescription](t)(e.c.ListEventDescriptions(ctx, alice, processes, d.Id))
		if len(described) != 1 || described[0].DeviceId != e.d1 || described[0].Topic != e.serviceTopic() {
			t.Fatalf("the deployment describes the event of the selected device: %+v", described)
		}
		inst := must[model.Instance](t)(e.c.StartInstance(ctx, alice, processes, model.StartInstanceInput{DeploymentId: d.Id}))
		e.readyForDevices()

		e.measure(e.d1, 290)
		e.measure(e.d2, 310)
		e.measure(e.d1, 300)
		e.instance(processes, inst.Id, model.StateCompleted)
		if vars := must[[]model.Variable](t)(e.c.GetVariables(ctx, alice, processes, inst.Id)); !near(variable(vars, "temperature"), 26.85) {
			t.Fatalf("only the warm value of the device triggers, converted to celsius: %+v", vars)
		}

		_, err := e.c.PutDeployment(ctx, alice, processes, d.Id, model.DeploymentInput{Name: "Heating", Xml: xml,
			Selections: map[string]model.Selection{"warm": {DeviceGroupId: e.group}}, UpdateStrategy: model.UpdateDrain})
		if err != nil {
			t.Fatal(err)
		}
		second := must[model.Instance](t)(e.c.StartInstance(ctx, alice, processes, model.StartInstanceInput{DeploymentId: d.Id}))
		// the event role reads changed descriptions every second
		e.waitFor("the descriptions of the group", func() bool {
			e.measure(e.d2, 301)
			time.Sleep(100 * time.Millisecond)
			current, _ := e.c.GetInstance(ctx, alice, processes, second.Id)
			return current.State == model.StateCompleted
		})
	})
}

// Every warm reading of a thermometer of the group starts an instance with the temperature.
// A switched-off deployment starts nothing; the messages are taken and not delivered again
// (EVT-01, DEP-12).
func TestDeviceEventsStartInstancesUnlessSwitchedOff(t *testing.T) {
	eachEventVariant(t, func(t *testing.T, e *eventEnv) {
		d := must[model.Deployment](t)(e.c.CreateDeployment(ctx, alice, processes, model.DeploymentInput{Name: "Too warm", Xml: b.Process(
			warmEvent("warm", "startEvent"), b.Script("note", "note", "'too warm: ' + temperature.toFixed(2)"), b.End("end"),
			b.Flow("warm", "note"), b.Flow("note", "end"),
		), Selections: map[string]model.Selection{"warm": {DeviceGroupId: e.group}}}))
		described := must[[]model.EventDescription](t)(e.c.ListEventDescriptions(ctx, alice, processes, d.Id))
		if len(described) != 2 {
			t.Fatalf("a group describes the event of each of its devices: %+v", described)
		}
		e.readyForDevices()
		notes := func() []string {
			result := []string{}
			for _, inst := range must[model.Page[model.Instance]](t)(e.c.ListInstances(ctx, alice, processes, model.InstanceListOptions{DeploymentId: d.Id})).Items {
				vars, _ := e.c.GetVariables(ctx, alice, processes, inst.Id)
				note, _ := variable(vars, "note").(string)
				result = append(result, note)
			}
			return result
		}

		e.measure(e.d1, 300)
		e.measure(e.d2, 301)
		e.measure(e.d1, 280)
		if _, err := e.c.SetDeploymentDisabled(ctx, alice, processes, d.Id, model.DisabledInput{Disabled: true}); err != nil {
			t.Fatal(err)
		}
		e.measure(e.d1, 305)
		// with Kafka, the reading would otherwise arrive after the deployment is on again
		e.bus.drain(t, e.serviceTopic())
		if _, err := e.c.SetDeploymentDisabled(ctx, alice, processes, d.Id, model.DisabledInput{Disabled: false}); err != nil {
			t.Fatal(err)
		}
		e.measure(e.d1, 306)
		e.waitFor("the start of the last reading", func() bool { return strings.Contains(fmt.Sprint(notes()), "32.85") })
		got := notes()
		want := map[string]bool{"too warm: 26.85": true, "too warm: 27.85": true, "too warm: 32.85": true}
		if len(got) != len(want) {
			t.Fatalf("each warm reading of the group starts an instance, a cold one and one while switched off do not: %v", got)
		}
		for _, note := range got {
			if !want[note] {
				t.Fatalf("each instance has the temperature of its event: %v", got)
			}
		}
	})
}

// An import event works like a device event on the topic of the import; messages of another
// import on the same topic pass by (EVT-02).
func TestImportEventContinuesAWaitingInstance(t *testing.T) {
	eachEventVariant(t, func(t *testing.T, e *eventEnv) {
		d := must[model.Deployment](t)(e.c.CreateDeployment(ctx, alice, processes, model.DeploymentInput{Name: "Weather", Xml: b.Process(
			b.Start("start"), warmEvent("warm", "intermediateCatchEvent"), b.End("end"),
			b.Flow("start", "warm"), b.Flow("warm", "end"),
		), Selections: map[string]model.Selection{"warm": {ImportId: e.importId}}}))
		if described := must[[]model.EventDescription](t)(e.c.ListEventDescriptions(ctx, alice, processes, d.Id)); len(described) != 1 || described[0].Topic != e.importTopic {
			t.Fatalf("the import names its topic: %+v", described)
		}
		inst := must[model.Instance](t)(e.c.StartInstance(ctx, alice, processes, model.StartInstanceInput{DeploymentId: d.Id}))
		e.readyForImports()

		e.report("urn:infai:ses:import:other", 310)
		e.report(e.importId, 280)
		e.report(e.importId, 299.15)
		e.instance(processes, inst.Id, model.StateCompleted)
		if vars := must[[]model.Variable](t)(e.c.GetVariables(ctx, alice, processes, inst.Id)); !near(variable(vars, "temperature"), 26) {
			t.Fatalf("only the warm value of the import triggers, converted to celsius: %+v", vars)
		}
	})
}

// --- fakes -----------------------------------------------------------------------------------

func newFakeEventEnv(t *testing.T) *eventEnv {
	t.Helper()
	f := fixture{service: "urn:infai:ses:service:temperature", d1: "d1", d2: "d2", foreign: "d3",
		group: "urn:infai:ses:device-group:thermometers", importId: "urn:infai:ses:import:weather", importTopic: "import_weather"}
	platform := platformFake(t, f)
	broker := &kafkaFake{topics: map[string]*subscription{}}
	e := startRoles(t, func(c *configuration.Config, deps *server.Dependencies) {
		// a file both roles open, as two processes would; an in-memory database with a shared
		// cache locks whole tables against a second connection pool
		c.SqlitePath = filepath.Join(t.TempDir(), "engine.db")
		c.DeviceRepositoryUrl, c.ImportDeployUrl, c.ImportRepositoryUrl = platform, platform, platform
		c.AuthEndpoint, c.AuthClientId = platform, "automation-engine"
		admin := auth.UnsignedToken("device-repository", "admin")
		for topic, id := range map[string]string{"devices": f.d1, "device-groups": f.group} {
			if _, err, _ := deps.Permissions.SetPermission(admin, topic, id, permmodel.ResourcePermissions{UserPermissions: map[string]permmodel.PermissionsMap{
				"alice": {Read: true, Write: true, Execute: true, Administrate: true},
			}}); err != nil {
				t.Fatal(err)
			}
		}
	}, broker.consume)
	return &eventEnv{env: e, fixture: f, bus: broker}
}

// platformFake answers as device-repository, import-deploy, import-repository and Keycloak
func platformFake(t *testing.T, f fixture) string {
	service := models.Service{Id: f.service, LocalId: "temperature", Name: "temperature", Interaction: models.EVENT, ProtocolId: "p",
		Outputs: []models.Content{{Id: "out", ContentVariable: reading(), Serialization: models.JSON, ProtocolSegmentId: "data"}}}
	air := models.AspectNode{Id: airAspect, Name: "air", RootId: airAspect, AncestorIds: []string{}, DescendentIds: []string{}, ChildIds: []string{}}
	characteristics, concept, function := temperatureModels()
	importType := models.ImportType{Id: "weather", Name: "Weather", Output: weather()}
	// import-repository fills the deprecated single aspect on read
	importType.Output.SubContentVariables[2].SubContentVariables[0].AspectId = airAspect
	instance := models.Import{Id: f.importId, Name: "Weather", ImportTypeId: "weather", KafkaTopic: f.importTopic}
	resources := map[string]any{
		"/devices/" + f.d1:                  models.Device{Id: f.d1, LocalId: "l1", Name: "Thermometer 1", DeviceTypeId: "thermometer", OwnerId: "alice"},
		"/devices/" + f.d2:                  models.Device{Id: f.d2, LocalId: "l2", Name: "Thermometer 2", DeviceTypeId: "thermometer", OwnerId: "alice"},
		"/device-types/thermometer":         models.DeviceType{Id: "thermometer", Name: "Thermometer", Services: []models.Service{service}},
		"/device-groups/" + f.group:         models.DeviceGroup{Id: f.group, Name: "Thermometers", DeviceIds: []string{f.d1, f.d2}},
		"/aspect-nodes/" + airAspect:        air,
		"/characteristics/" + kelvin:        characteristics[0],
		"/characteristics/" + celsius:       characteristics[1],
		"/functions/" + temperatureFunction: function,
		"/concepts/" + temperatureConcept:   concept,
		"/instances":                        []models.Import{instance},
		"/instances/" + f.importId:          instance,
		"/import-types/weather":             importType,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/token"):
			keycloakStandIn().ServeHTTP(w, r)
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

func (this *kafkaFake) ready(t *testing.T, topic string, _ any) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		this.mux.Lock()
		subscribed := this.topics[topic] != nil
		this.mux.Unlock()
		if subscribed {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the event role did not subscribe %v", topic)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// drain has nothing to wait for, sending is synchronous
func (this *kafkaFake) drain(*testing.T, string) {}

// send delivers a message like Kafka to the event role, synchronously; the error is what
// makes Kafka deliver it again
func (this *kafkaFake) send(topic string, _ string, value any) error {
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
