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

package deprecated_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
	"github.com/SENERGY-Platform/automation-engine/pkg/permissions"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
	dsmodel "github.com/SENERGY-Platform/device-selection/v2/pkg/model"
	"github.com/SENERGY-Platform/device-selection/v2/pkg/model/devicemodel"
	"github.com/SENERGY-Platform/models/go/models"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
)

// hubServices stands in for device-repository and device-selection: the hub has the device
// on-hub; device-selection knows on-hub and elsewhere and restricts its answer to the devices
// or local devices a request names, as the real one does
func hubServices(t *testing.T) *httptest.Server {
	devicesOf := map[string]string{"on-hub": "local-on-hub", "elsewhere": "local-elsewhere"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/hubs/"+hub:
			_ = json.NewEncoder(w).Encode(models.Hub{Id: hub, Name: hub, DeviceIds: []string{"on-hub"}, DeviceLocalIds: []string{"local-on-hub"}, OwnerId: "alice"})
		case r.URL.Path == "/v2/bulk/selectables":
			var request dsmodel.BulkRequestV2
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			result := dsmodel.BulkResult{}
			for _, element := range request {
				answer := dsmodel.BulkResultElement{Id: element.Id, Selectables: []dsmodel.Selectable{}}
				for id, localId := range devicesOf {
					if (element.Devices != nil && !slices.Contains(element.Devices, id)) || (element.LocalDevices != nil && !slices.Contains(element.LocalDevices, localId)) {
						continue
					}
					device := &dsmodel.PermSearchDevice{DisplayName: id}
					device.Id, device.LocalId = id, localId
					answer.Selectables = append(answer.Selectables, dsmodel.Selectable{Device: device, Services: []devicemodel.Service{{Id: "s", Name: "s"}},
						ServicePathOptions: map[string][]dsmodel.PathOption{"s": {{Path: "value", FunctionId: "f"}}}})
				}
				result = append(result, answer)
			}
			_ = json.NewEncoder(w).Encode(result)
		default:
			http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// newFogEnvWithSelection is newFogEnv with device-selection and device-repository in the cloud
func newFogEnvWithSelection(t *testing.T) *env {
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cloudBroker, gatewayBroker := mqtt.NewMemory(), mqtt.NewMemory()
	_ = cloudBroker.Subscribe("processes/"+hub+"/cmd/#", 1, func(topic string, payload []byte) error {
		return gatewayBroker.Publish(ctx, "processes/cmd/"+strings.TrimPrefix(topic, "processes/"+hub+"/cmd/"), 1, payload)
	})
	_ = gatewayBroker.Subscribe("processes/state/#", 1, func(topic string, payload []byte) error {
		return cloudBroker.Publish(ctx, "processes/"+hub+"/state/"+strings.TrimPrefix(topic, "processes/state/"), 1, payload)
	})
	services := hubServices(t)
	config := configuration.Default()
	config.MinTimerDuration = "0s"
	config.SqlitePath = storetest.MemoryDsn()
	config.PollInterval = "20ms"
	config.DeviceSelectionUrl, config.DeviceRepositoryUrl = services.URL, services.URL
	deps, err := client.TestDependencies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	admin := auth.UnsignedToken("device-repository", "admin")
	if _, err, _ = deps.Permissions.SetTopic(admin, permmodel.Topic{Id: permissions.HubsTopic}); err != nil {
		t.Fatal(err)
	}
	if _, err, _ = deps.Permissions.SetPermission(admin, permissions.HubsTopic, hub, permmodel.ResourcePermissions{UserPermissions: map[string]permmodel.PermissionsMap{
		"alice": {Read: true, Write: true, Execute: true, Administrate: true},
	}}); err != nil {
		t.Fatal(err)
	}
	deps.SyncBroker = cloudBroker
	cloud, err := server.New(ctx, config, logger, deps)
	if err != nil {
		t.Fatal(err)
	}
	cloud.Start(ctx)
	gatewayConfig := configuration.Default()
	gatewayConfig.MinTimerDuration = "0s"
	gatewayConfig.SqlitePath = storetest.MemoryDsn()
	gatewayConfig.PollInterval = "20ms"
	gatewayConfig.Mode, gatewayConfig.MqttBroker = "fog", "memory"
	gateway, err := server.New(ctx, gatewayConfig, logger, server.Dependencies{Broker: gatewayBroker})
	if err != nil {
		t.Fatal(err)
	}
	gateway.Start(ctx)
	httpServer := httptest.NewServer(cloud.Handler)
	t.Cleanup(httpServer.Close)
	e := &env{t: t, srv: cloud, url: httpServer.URL}
	e.waitFor("the status of the gateway", func() bool {
		networks, err := cloud.Controller.ListNetworks(ctx, alice)
		return err == nil && len(networks) == 1
	})
	return e
}

// process-fog-deployment asked device-selection only for the devices of the hub
// (local_devices in the bulk request, TestController), so the deploy dialog of a gateway
// offers no device the gateway cannot reach.
func TestFogPreparedDeploymentOffersOnlyTheDevicesOfTheGateway(t *testing.T) {
	e := newFogEnvWithSelection(t)
	xml := b.Process(b.Start("start"), `<bpmn:serviceTask id="task" senergy:taskType="device_command"><bpmn:extensionElements><camunda:inputOutput>`+
		`<camunda:inputParameter name="payload">{"function":{"id":"f"}}</camunda:inputParameter></camunda:inputOutput></bpmn:extensionElements></bpmn:serviceTask>`,
		b.End("end"), b.Flow("start", "task"), b.Flow("task", "end"))
	var prepared models.Deployment
	if status, _ := e.call(alice, http.MethodPost, "/fog-deployment/prepared-deployments/"+hub, map[string]string{"xml": xml, "svg": "<svg/>"}, &prepared); status != http.StatusOK {
		t.Fatalf("prepare: %v", status)
	}
	if len(prepared.Elements) != 1 || prepared.Elements[0].Task == nil {
		t.Fatalf("expected the device task, got %+v", prepared.Elements)
	}
	offered := []string{}
	for _, option := range prepared.Elements[0].Task.Selection.SelectionOptions {
		if option.Device != nil {
			offered = append(offered, option.Device.Id)
		}
	}
	if !slices.Equal(offered, []string{"on-hub"}) {
		t.Errorf("expected only the device of the gateway on-hub, got %v", offered)
	}
}
