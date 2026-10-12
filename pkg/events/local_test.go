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
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/devices"
	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
	"github.com/SENERGY-Platform/models/go/models"
)

var localDataSegment = events.LocalProtocol.ProtocolSegments[1].Id

// a measurement as the connectors of a gateway describe it: a structure in the data segment
func localService() models.Service {
	return models.Service{Id: "urn:infai:ses:service:event", LocalId: "temperature", Interaction: models.EVENT, Outputs: []models.Content{{
		ProtocolSegmentId: localDataSegment, Serialization: models.JSON,
		ContentVariable: models.ContentVariable{Name: "value", Type: models.Structure, SubContentVariables: []models.ContentVariable{
			{Name: "temperature", Type: models.Float, FunctionId: measuring, AspectId: "air"},
			{Name: "unit", Type: models.String, Value: "°C"},
		}},
	}}}
}

func TestDecodeLocalEventCleansAndCompletesLikePlatformConnector(t *testing.T) {
	result, err := events.DecodeLocalEvent([]byte(`{"temperature": 25.5, "noise": 1}`), localService())
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]any{"value": map[string]any{"temperature": 25.5, "unit": "°C"}}
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("unknown fields go, missing ones get their default: %#v", result)
	}
	if _, err = events.DecodeLocalEvent([]byte(`{"temperature": "warm"}`), localService()); !errors.Is(err, events.ErrUnexpectedType) {
		t.Errorf("a value of the wrong type is an error: %v", err)
	}
	list := localService()
	list.Outputs[0].ContentVariable = models.ContentVariable{Name: "value", Type: models.List, SubContentVariables: []models.ContentVariable{{Name: "0", Type: models.Float}, {Name: "1", Type: models.Float}}}
	if result, err = events.DecodeLocalEvent([]byte(`[1, 2, 3]`), list); err != nil || !reflect.DeepEqual(result["value"], []any{1.0, 2.0}) {
		t.Errorf("a list keeps the elements up to the first unknown one: %#v %v", result, err)
	}
}

// localRepo is the device-repository mirror of a gateway: devices and services with local ids
type localRepo struct{ fakeRepo }

func (this *localRepo) GetDevice(_ devices.Token, id string) (models.Device, error) {
	return models.Device{Id: id, LocalId: "local-" + id, DeviceTypeId: "dt"}, nil
}
func (this *localRepo) GetDeviceType(devices.Token, string) (models.DeviceType, error) {
	return models.DeviceType{Id: "dt", Services: []models.Service{localService()}}, nil
}

// temperatureUnmarshaller reads the temperature of the decoded message
type temperatureUnmarshaller struct{}

func (temperatureUnmarshaller) Unmarshal(desc events.MessageDesc) (any, error) {
	value, _ := desc.Message["value"].(map[string]any)
	return value["temperature"], nil
}

func newFogServer(t *testing.T) (*server.Server, *mqtt.Memory) {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	config := configuration.Default()
	config.SqlitePath = storetest.MemoryDsn()
	config.Mode, config.MqttBroker = "fog", "memory"
	broker := mqtt.NewMemory()
	srv, err := server.New(ctx, config, slog.New(slog.NewTextHandler(io.Discard, nil)), server.Dependencies{
		DeviceRepository: &localRepo{}, AspectNodes: aspects(), EventUnmarshaller: temperatureUnmarshaller{}, Broker: broker,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.Start(ctx)
	return srv, broker
}

func TestGatewayEvaluatesEventsOfItsBroker(t *testing.T) {
	srv, broker := newFogServer(t)
	c := srv.Controller
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: eventProcess(), Selections: map[string]model.Selection{"reading": {DeviceId: "d1", ServiceId: "urn:infai:ses:service:event"}}})
	if err != nil {
		t.Fatal(err)
	}
	descriptions, err := c.ListEventDescriptions(ctx, alice, "processes", d.Id)
	if err != nil || len(descriptions) != 1 || descriptions[0].Topic != "event/local-d1/temperature" {
		t.Fatalf("a gateway reads event/{local device}/{local service} (FOG-06): %+v %v", descriptions, err)
	}
	inst, err := c.StartInstance(ctx, alice, "processes", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	count := func() any {
		vars, _ := c.GetVariables(ctx, alice, "processes", inst.Id)
		for _, v := range vars {
			if v.Name == "count" {
				return v.Value
			}
		}
		return nil
	}
	deadline := time.Now().Add(5 * time.Second)
	for count() == nil && time.Now().Before(deadline) {
		// the worker syncs descriptions every second and subscribes then
		_ = broker.Publish(ctx, "event/local-d1/temperature", 0, []byte(`{"temperature": 25}`))
		time.Sleep(100 * time.Millisecond)
	}
	if count() == nil {
		t.Fatal("an event of the broker of the gateway is correlated")
	}
	before := count()
	_ = broker.Publish(ctx, "event/local-d1/temperature", 0, []byte(`{"temperature": 10}`))
	_ = broker.Publish(ctx, "event/local-d2/temperature", 0, []byte(`{"temperature": 30}`))
	if count() != before {
		t.Errorf("a false condition and another device change nothing: %v %v", before, count())
	}
}

func TestGatewayApiIsReadOnly(t *testing.T) {
	srv, _ := newFogServer(t)
	server := httptest.NewServer(srv.Handler)
	defer server.Close()
	resp, err := http.Get(server.URL + "/namespaces")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("reading needs no token on a gateway: %v %v", resp, err)
	}
	resp, err = http.Post(server.URL+"/namespaces/processes/deployments", "application/json", strings.NewReader(`{"xml":""}`))
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("changes come from the cloud: %v %v", resp, err)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "read-only") {
		t.Errorf("%s", body)
	}
}
