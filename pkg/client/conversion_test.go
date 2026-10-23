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

package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/devices"
	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/automation-engine/pkg/tasks/devicecommand"
	"github.com/SENERGY-Platform/models/go/models"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
)

// Models of the old designer are deployed through the new API without a call of /convert: the
// deploy converts them, the stored model stays as it was saved (MIG-04). The models in
// testdata/process-deployment are those of process-deployment lib/tests/resources (Apache-2.0).

const (
	getColorFunction  = "urn:infai:ses:measuring-function:bdb6a7c8-4a3d-4fe0-bab3-ce02e09b5869"
	setColorFunction  = "urn:infai:ses:controlling-function:c54e2a89-1fb8-4ecb-8993-a7b40b355599"
	rgbCharacteristic = "urn:infai:ses:characteristic:5b4eea52-e8e5-4e80-9455-0382f81a1b43"
	lightingAspect    = "urn:infai:ses:aspect:a7470d73-dde3-41fc-92bd-f16bb28f2da6"
	getStatusService  = "urn:infai:ses:service:99614933-4734-41b6-a131-3f96f134ee69"
	setColorService   = "urn:infai:ses:service:67789396-d1ca-4ea9-9147-0614c6d68a2f"
	batteryFunction   = "urn:infai:ses:measuring-function:00549f18-88b5-44c7-adb1-f558e8d53d1d"
	batteryAspect     = "urn:infai:ses:aspect:d4625151-ce27-4620-9b7e-93ded78484f8"
	percentage        = "urn:infai:ses:characteristic:46f808f4-bb9e-4cc2-bd50-dc33ca74f273"
	batteryService    = "urn:infai:ses:service:battery-event"
	getColorTask      = "Task_1nbnl8y"
	setColorTask      = "Task_1lhzy95"
	batteryEvent      = "IntermediateThrowEvent_01fl1l6"
)

type marshalled struct {
	characteristicId string
	value            any
}

// lamp is the device hue with the services of the models: device-repository, marshaller of
// the payload versions before 3, and the topic of the protocol handler
type lamp struct {
	mux          sync.Mutex
	sent         []devicecommand.ProtocolMsg
	marshalled   []marshalled
	unmarshalled []string
	v2           int
	aspects      []string
}

func content(functionId string, aspectId string) []models.Content {
	return []models.Content{{ProtocolSegmentId: "seg", Serialization: "json",
		ContentVariable: models.ContentVariable{Name: "value", Type: models.Structure, FunctionId: functionId, AspectId: aspectId}}}
}

func (this *lamp) GetToken(user string) (devices.Token, error) {
	return devices.Token("Bearer " + user), nil
}
func (this *lamp) GetDevice(_ devices.Token, id string) (models.Device, error) {
	if id != "hue" {
		return models.Device{}, errors.New("device not found")
	}
	return models.Device{Id: "hue", LocalId: "hue", Name: "hue", DeviceTypeId: "lamp"}, nil
}
func (this *lamp) GetService(token devices.Token, device models.Device, id string) (models.Service, error) {
	deviceType, _ := this.GetDeviceType(token, device.DeviceTypeId)
	for _, s := range deviceType.Services {
		if s.Id == id {
			return s, nil
		}
	}
	return models.Service{}, errors.New("service not found")
}
func (this *lamp) GetProtocol(_ devices.Token, id string) (models.Protocol, error) {
	return models.Protocol{Id: id, Handler: "connector", ProtocolSegments: []models.ProtocolSegment{{Id: "seg", Name: "data"}}}, nil
}
func (this *lamp) GetDeviceType(devices.Token, string) (models.DeviceType, error) {
	return models.DeviceType{Id: "lamp", Services: []models.Service{
		{Id: getStatusService, LocalId: "getStatus", ProtocolId: "p", Interaction: models.REQUEST, Outputs: content(getColorFunction, lightingAspect)},
		{Id: setColorService, LocalId: "setColor", ProtocolId: "p", Interaction: models.REQUEST, Inputs: content(setColorFunction, lightingAspect)},
		{Id: batteryService, LocalId: "battery", ProtocolId: "p", Interaction: models.EVENT, Outputs: content(batteryFunction, batteryAspect)},
	}}, nil
}
func (this *lamp) GetDeviceGroup(devices.Token, string) (models.DeviceGroup, error) {
	return models.DeviceGroup{}, errors.New("no device groups")
}
func (this *lamp) aspect(_ devices.Token, id string) (models.AspectNode, error) {
	this.mux.Lock()
	defer this.mux.Unlock()
	this.aspects = append(this.aspects, id)
	return models.AspectNode{Id: id, RootId: id}, nil
}
func (this *lamp) MarshalV2(models.Service, models.Protocol, []devicecommand.MarshallingV2RequestData) (map[string]string, error) {
	this.mux.Lock()
	defer this.mux.Unlock()
	this.v2++
	return nil, errors.New("payloads before version 3 are marshalled without paths")
}
func (this *lamp) UnmarshalV2(devicecommand.UnmarshallingV2Request) (any, error) {
	this.mux.Lock()
	defer this.mux.Unlock()
	this.v2++
	return nil, errors.New("payloads before version 3 are unmarshalled without paths")
}
func (this *lamp) MarshalFromServiceAndProtocol(characteristicId string, _ models.Service, _ models.Protocol, data any, _ []devicecommand.Configurable) (map[string]string, error) {
	this.mux.Lock()
	defer this.mux.Unlock()
	this.marshalled = append(this.marshalled, marshalled{characteristicId: characteristicId, value: data})
	raw, err := json.Marshal(data)
	return map[string]string{"data": string(raw)}, err
}
func (this *lamp) UnmarshalFromServiceAndProtocol(characteristicId string, _ models.Service, _ models.Protocol, message map[string]string, _ []string) (any, error) {
	this.mux.Lock()
	this.unmarshalled = append(this.unmarshalled, characteristicId)
	this.mux.Unlock()
	var result any
	err := json.Unmarshal([]byte(message["data"]), &result)
	return result, err
}
func (this *lamp) Produce(_ context.Context, _ string, _ string, payload []byte) error {
	var msg devicecommand.ProtocolMsg
	if err := json.Unmarshal(payload, &msg); err != nil {
		return err
	}
	this.mux.Lock()
	defer this.mux.Unlock()
	this.sent = append(this.sent, msg)
	return nil
}
func (this *lamp) messages() []devicecommand.ProtocolMsg {
	this.mux.Lock()
	defer this.mux.Unlock()
	return append([]devicecommand.ProtocolMsg{}, this.sent...)
}

// lampServer runs the engine with the lamp, which alice may use, and answers over HTTP
func lampServer(t *testing.T) (*server.Server, client.Client, *lamp) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	l := &lamp{}
	var deps server.Dependencies
	srv, err := client.NewTestServer(ctx, func(_ *configuration.Config, d *server.Dependencies) {
		d.DeviceRepository, d.Marshaller, d.Producer = l, l, l
		d.AspectNodes = devices.NewAspectNodesWith(l.aspect)
		deps = *d
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err, _ = deps.Permissions.SetPermission(admin, "devices", "hue", permmodel.ResourcePermissions{
		UserPermissions: map[string]permmodel.PermissionsMap{"alice": {Read: true, Execute: true, Administrate: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(srv.Handler)
	t.Cleanup(httpServer.Close)
	return srv, client.New(httpServer.URL), l
}

func testdata(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/process-deployment/" + name + "/raw.bpmn")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func lampSelections() map[string]model.Selection {
	return map[string]model.Selection{
		getColorTask: {DeviceId: "hue", ServiceId: getStatusService},
		setColorTask: {DeviceId: "hue", ServiceId: setColorService},
	}
}

func named(issues []model.Issue, elementId string, part string) bool {
	for _, issue := range issues {
		if issue.ElementId == elementId && strings.Contains(issue.Message, part) {
			return true
		}
	}
	return false
}

// payloads reads the payload inputs of the device tasks
func payloads(t *testing.T, xml string) map[string]string {
	t.Helper()
	process, _, err := bpmn.Parse(xml)
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]string{}
	for _, id := range []string{getColorTask, setColorTask} {
		for _, in := range process.Elements[id].Inputs {
			if in.Name == "payload" {
				result[id] = in.Value.Text
			}
		}
	}
	return result
}

// The model 0pools0lanes2tasks of the old designer deploys through the new API as it is, by
// BPMN and by model id; the version holds the converted BPMN with the payloads unchanged, and
// the answer names what was converted. A model of the engine reports nothing.
func TestOldDesignerModelDeploysThroughTheNewApi(t *testing.T) {
	_, c, _ := lampServer(t)
	raw := testdata(t, "0pools0lanes2tasks")
	d, err := c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: raw, Selections: lampSelections()})
	if err != nil {
		t.Fatal(err)
	}
	if d.Conversion == nil || !d.Conversion.Complete() || len(d.Conversion.Check) != 0 ||
		!named(d.Conversion.Rewritten, getColorTask, "pessimistic became task type device_command") ||
		!named(d.Conversion.Rewritten, setColorTask, "optimistic became task type device_command") ||
		!named(d.Conversion.Rewritten, getColorTask, "mapping outputs.r: JUEL became JavaScript") || len(d.Conversion.Rewritten) != 5 {
		t.Fatalf("the answer names the rewrites: %+v", d.Conversion)
	}
	v, err := c.GetDeploymentVersion(ctx, alice, ns, d.Id, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(v.Xml, "camunda:topic") || strings.Count(v.Xml, `senergy:taskType="device_command"`) != 2 {
		t.Errorf("the version holds the converted BPMN:\n%v", v.Xml)
	}
	if got, want := payloads(t, v.Xml), payloads(t, raw); !reflect.DeepEqual(got, want) || len(want) != 2 {
		t.Errorf("the payloads stay as the designer wrote them: %v", got)
	}
	if stored, err := c.GetDeployment(ctx, alice, ns, d.Id); err != nil || stored.Conversion != nil {
		t.Errorf("the conversion is only in the answer of the deploy: %+v %v", stored.Conversion, err)
	}
	again, err := c.PutDeployment(ctx, alice, ns, d.Id, model.DeploymentInput{Xml: raw, Selections: lampSelections()})
	if err != nil || again.Version != 1 || again.Conversion == nil {
		t.Errorf("the same BPMN again changes nothing, the conversion is the same: %+v %v", again, err)
	}

	m, err := c.CreateModel(ctx, alice, ns, model.ModelInput{Xml: raw})
	if err != nil {
		t.Fatal(err)
	}
	byModel, err := c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{ModelId: m.Id, Selections: lampSelections()})
	if err != nil || byModel.ModelId != m.Id || byModel.Conversion == nil || len(byModel.Conversion.Rewritten) != 5 {
		t.Fatalf("a stored model of the old designer deploys by its id: %+v %v", byModel, err)
	}
	if mv, err := c.GetModelVersion(ctx, alice, ns, m.Id, 1); err != nil || mv.Xml != raw {
		t.Errorf("the model stays as it was saved: %v", err)
	}

	engine, err := c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: waitingModel()})
	if err != nil || engine.Conversion != nil {
		t.Errorf("a model of the engine is not converted: %+v %v", engine.Conversion, err)
	}
	if v, err := c.GetDeploymentVersion(ctx, alice, ns, engine.Id, 1); err != nil || v.Xml != withDiagram(t, waitingModel()) {
		t.Errorf("and deployed as sent, with a diagram added: %v", err)
	}

	unsupported := b.Process(b.Start("start"), `<bpmn:serviceTask id="mystery" camunda:type="external" camunda:topic="mystery"/>`, b.End("end"),
		b.Flow("start", "mystery"), b.Flow("mystery", "end"))
	_, err = c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: unsupported})
	if !errors.Is(err, model.ErrValidation) || !named(model.AsError(err).Issues, "mystery", `camunda:topic="mystery" has no task type`) {
		t.Errorf("what cannot be converted rejects the deploy naming it: %v", err)
	}
}

// End to end with the payloads of the old designer, which are not changed by the conversion:
// version-less (0) payloads with function, aspect object, device_class, label, input and
// retries. getColor asks the device in the characteristic of the payload and its answer goes
// through the converted output mappings into outputs.r, .g and .b; setColor sends the inputs of
// the model. As in external-task-worker before version 3, the marshaller gets the
// characteristic only, and the result of one device is the value, not a list.
func TestCommandsOfAnOldDesignerModelGoOutWithItsPayloads(t *testing.T) {
	srv, c, l := lampServer(t)
	d, err := c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: testdata(t, "0pools0lanes2tasks"), Selections: lampSelections()})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, ns, model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the getColor command", func() bool { return len(l.messages()) == 1 })
	get := l.messages()[0]
	if get.Metadata.Version != 0 || get.Metadata.Device.Id != "hue" || get.Metadata.Service.Id != getStatusService ||
		get.Metadata.OutputCharacteristic != rgbCharacteristic || get.Metadata.InputCharacteristic != "" ||
		get.TaskInfo.CompletionStrategy != "pessimistic" || get.TaskInfo.ProcessInstanceId != inst.Id {
		t.Errorf("getColor asks for the color in rgb: %+v %+v", get.Metadata, get.TaskInfo)
	}
	get.Response.Output = map[string]string{"data": `{"r":255,"g":100,"b":50}`}
	raw, _ := json.Marshal(get)
	if err = srv.DeviceCommands.HandleResponse(ctx, raw); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the setColor command", func() bool { return len(l.messages()) == 2 })
	set := l.messages()[1]
	if set.Metadata.Version != 0 || set.Metadata.Service.Id != setColorService || set.Metadata.InputCharacteristic != rgbCharacteristic ||
		set.Metadata.OutputCharacteristic != "" || set.TaskInfo.CompletionStrategy != "optimistic" {
		t.Errorf("setColor sets the color in rgb: %+v %+v", set.Metadata, set.TaskInfo)
	}
	var sentColor map[string]any
	if err = json.Unmarshal([]byte(set.Request.Input["data"]), &sentColor); err != nil || !reflect.DeepEqual(sentColor, map[string]any{"r": 0.0, "g": 200.0, "b": 100.0}) {
		t.Errorf("the inputs of the model replace the input of the payload: %v %v", set.Request.Input, err)
	}
	waitUntil(t, "the end of the instance", func() bool {
		i, err := c.GetInstance(ctx, alice, ns, inst.Id)
		return err == nil && i.State == model.StateCompleted
	})
	vars, err := c.GetVariables(ctx, alice, ns, inst.Id)
	if err != nil {
		t.Fatal(err)
	}
	if variable(vars, "outputs.r") != 255.0 || variable(vars, "outputs.g") != 100.0 || variable(vars, "outputs.b") != 50.0 {
		t.Errorf("the answer is mapped by the converted outputs: %+v", vars)
	}
	l.mux.Lock()
	defer l.mux.Unlock()
	want := []marshalled{{"", map[string]any{}}, {rgbCharacteristic, map[string]any{"r": 0.0, "g": 200.0, "b": 100.0}}}
	if !reflect.DeepEqual(l.marshalled, want) || !reflect.DeepEqual(l.unmarshalled, []string{rgbCharacteristic}) || l.v2 != 0 {
		t.Errorf("the marshaller gets the characteristics of the payloads: %+v %v %v", l.marshalled, l.unmarshalled, l.v2)
	}
	if !contains(l.aspects, lightingAspect) {
		t.Errorf("the aspect object of the payload is read as the aspect of the command: %v", l.aspects)
	}
}

func contains(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}

func waitUntil(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %v timed out", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// level reads the battery level of a device message, as the marshaller would in percent
type level struct{}

func (level) Unmarshal(desc events.MessageDesc) (any, error) {
	return desc.Message["level"], nil
}

// conditional_event_3 is a device event of the old designer, already in the format of the
// engine: x == foo with the variables {"foo": "42"}. foo is read as JSON, so 42; with loose
// equality, a value 42 triggers as a number and as a string, as in event-worker.
// senergy:use_marshaller is kept as an unknown attribute and has no effect: in process-deployment
// it belonged to the message events of analytics pipelines, which the engine dropped (EVT-03).
func TestDeviceEventOfAnOldDesignerModelTriggersLikeTheEventWorker(t *testing.T) {
	srv, c, _ := lampServer(t)
	d, err := c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: testdata(t, "conditional_event_3"),
		Selections: map[string]model.Selection{batteryEvent: {DeviceId: "hue", ServiceId: batteryService}}})
	if err != nil {
		t.Fatal(err)
	}
	if d.Conversion != nil {
		t.Errorf("nothing is converted: %+v", d.Conversion)
	}
	descriptions, err := c.ListEventDescriptions(ctx, alice, ns, d.Id)
	if err != nil || len(descriptions) != 1 {
		t.Fatalf("%+v %v", descriptions, err)
	}
	desc := descriptions[0]
	if desc.ElementId != batteryEvent || desc.FunctionId != batteryFunction || !reflect.DeepEqual(desc.AspectIds, []string{batteryAspect}) ||
		desc.CharacteristicId != percentage || desc.Script != "x == foo" || desc.ValueVariable != "x" ||
		!reflect.DeepEqual(desc.Variables, map[string]string{"foo": "42"}) || desc.DeviceId != "hue" || desc.ServiceId != batteryService ||
		desc.Topic != devices.ServiceIdToTopic(batteryService) || desc.Qos != 0 {
		t.Errorf("the description holds criteria, condition and variables of the model: %+v", desc)
	}
	evaluator := events.NewEvaluator(level{})
	for value, triggers := range map[any]bool{42: true, 42.0: true, "42": true, 41: false} {
		result, err := evaluator.Evaluate(desc, map[string]any{"level": value})
		if err != nil || result.Triggered != triggers {
			t.Errorf("%#v: expected %v, got %+v %v", value, triggers, result, err)
		}
	}

	inst, err := c.StartInstance(ctx, alice, ns, model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	none := func(context.Context, []string, func(events.Message) error) error { return nil }
	worker := events.NewWorker(events.DefaultWorkerConfig(), srv.Store, evaluator, srv.Engine, none, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err = worker.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the instance to wait", func() bool {
		i, err := c.GetInstance(ctx, alice, ns, inst.Id)
		return err == nil && i.State == model.StateRunning
	})
	message, _ := json.Marshal(map[string]any{"device_id": "hue", "service_id": batteryService, "value": map[string]any{"level": 42}})
	if err = worker.Handle(ctx, events.Message{Topic: desc.Topic, Offset: 1, Value: message}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the event to continue the instance", func() bool {
		i, err := c.GetInstance(ctx, alice, ns, inst.Id)
		return err == nil && i.State == model.StateCompleted
	})
}

// Saving a model of the old designer stores it unchanged and answers with a hint instead of the
// findings of the old format; validation and the prepared deployment see the converted model.
func TestOldDesignerModelIsSavedAsItIsWithAHint(t *testing.T) {
	_, c, _ := lampServer(t)
	raw := testdata(t, "0pools0lanes2tasks")
	onlyHint := func(what string, issues []model.Issue) {
		t.Helper()
		if len(issues) != 1 || !issues[0].Hint || !strings.Contains(issues[0].Message, "converted when deployed: 5 parts rewritten, 0 to check") {
			t.Errorf("%v: a hint instead of findings of the old format: %+v", what, issues)
		}
	}
	m, err := c.CreateModel(ctx, alice, ns, model.ModelInput{Xml: raw})
	if err != nil {
		t.Fatal(err)
	}
	onlyHint("create", m.Issues)
	if v, err := c.GetModelVersion(ctx, alice, ns, m.Id, 1); err != nil || v.Xml != raw {
		t.Errorf("the XML is stored as it was sent: %v", err)
	}
	if m, err = c.UpdateModel(ctx, alice, ns, m.Id, model.ModelInput{Xml: raw}); err != nil {
		t.Fatal(err)
	}
	onlyHint("update", m.Issues)
	if m, err = c.GetModel(ctx, alice, ns, m.Id); err != nil {
		t.Fatal(err)
	}
	onlyHint("get", m.Issues)
	issues, err := c.Validate(ctx, alice, ns, raw)
	if err != nil {
		t.Fatal(err)
	}
	onlyHint("validate", issues)
	if issues, err = c.Validate(ctx, alice, ns, waitingModel()); err != nil || len(issues) != 0 {
		t.Errorf("a model of the engine gets no hint: %+v %v", issues, err)
	}

	prepared, err := c.PrepareDeployment(ctx, alice, ns, model.PrepareInput{ModelId: m.Id})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Issues) != 0 || prepared.Conversion == nil || len(prepared.Conversion.Rewritten) != 5 {
		t.Errorf("the prepared deployment is that of the converted model: %+v %+v", prepared.Issues, prepared.Conversion)
	}
	criteria := map[string]model.Criteria{}
	for _, e := range prepared.Elements {
		criteria[e.ElementId] = e.Criteria
	}
	want := map[string]model.Criteria{
		getColorTask: {FunctionId: getColorFunction, CharacteristicId: rgbCharacteristic, AspectIds: []string{lightingAspect}},
		setColorTask: {FunctionId: setColorFunction, CharacteristicId: rgbCharacteristic, DeviceClassId: "urn:infai:ses:device-class:14e56881-16f9-4120-bb41-270a43070c86"},
	}
	if !reflect.DeepEqual(criteria, want) {
		t.Errorf("both device tasks are offered with the criteria of their payloads: %+v", criteria)
	}
}
