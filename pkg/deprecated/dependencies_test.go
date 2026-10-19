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
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/deprecated"
	"github.com/SENERGY-Platform/automation-engine/pkg/devices"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/automation-engine/pkg/tasks/devicecommand"
	"github.com/SENERGY-Platform/models/go/models"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
)

var errNotNeeded = errors.New("not needed by the test")

// noDevices registers the task type device_command; no command is executed in these tests
type noDevices struct{}

func (noDevices) GetToken(user string) (devices.Token, error) {
	return devices.Token("Bearer " + user), nil
}
func (noDevices) GetDevice(devices.Token, string) (models.Device, error) {
	return models.Device{}, errNotNeeded
}
func (noDevices) GetService(devices.Token, models.Device, string) (models.Service, error) {
	return models.Service{}, errNotNeeded
}
func (noDevices) GetProtocol(devices.Token, string) (models.Protocol, error) {
	return models.Protocol{}, errNotNeeded
}
func (noDevices) GetDeviceType(devices.Token, string) (models.DeviceType, error) {
	return models.DeviceType{}, errNotNeeded
}
func (noDevices) GetDeviceGroup(devices.Token, string) (models.DeviceGroup, error) {
	return models.DeviceGroup{}, errNotNeeded
}
func (noDevices) MarshalV2(models.Service, models.Protocol, []devicecommand.MarshallingV2RequestData) (map[string]string, error) {
	return nil, errNotNeeded
}
func (noDevices) UnmarshalV2(devicecommand.UnmarshallingV2Request) (any, error) {
	return nil, errNotNeeded
}
func (noDevices) MarshalFromServiceAndProtocol(string, models.Service, models.Protocol, any, []devicecommand.Configurable) (map[string]string, error) {
	return nil, errNotNeeded
}
func (noDevices) UnmarshalFromServiceAndProtocol(string, models.Service, models.Protocol, map[string]string, []string) (any, error) {
	return nil, errNotNeeded
}
func (noDevices) Produce(context.Context, string, string, []byte) error { return errNotNeeded }

// newEnvWithDevices is newEnv with device tasks, where alice may execute the given devices
func newEnvWithDevices(t *testing.T, deviceIds ...string) *env {
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	var deps server.Dependencies
	srv, err := client.NewTestServer(ctx, func(_ *configuration.Config, d *server.Dependencies) {
		d.DeviceRepository, d.Marshaller, d.Producer = noDevices{}, noDevices{}, noDevices{}
		deps = *d
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range deviceIds {
		_, err, _ = deps.Permissions.SetPermission(client.TestToken("x", "admin"), "devices", id, permmodel.ResourcePermissions{
			UserPermissions: map[string]permmodel.PermissionsMap{"alice": {Read: true, Execute: true, Administrate: true}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	httpServer := httptest.NewServer(srv.Handler)
	t.Cleanup(httpServer.Close)
	return &env{t: t, srv: srv, url: httpServer.URL}
}

// The dependencies of a deployment name the selected devices with the elements that use them
// (process-deployment TestDeploymentHandler, case 0pools0lanes2tasks). How the elements of one
// device are grouped into entries is not compared.
func TestDependenciesNameTheSelectedDevicesWithTheirElements(t *testing.T) {
	e := newEnvWithDevices(t, "hue")
	selected := readFixture[models.Deployment](t, "testdata/dependencies/selected.json")
	raw, err := os.ReadFile("testdata/dependencies/raw.bpmn")
	if err != nil {
		t.Fatal(err)
	}
	selected.Diagram.XmlRaw = string(raw)
	var deployed models.Deployment
	if status, _ := e.call(alice, http.MethodPost, "/deployment/v3/deployments", selected, &deployed); status != http.StatusOK {
		t.Fatalf("deploy: %v", status)
	}
	var dependencies deprecated.Dependencies
	if status, _ := e.call(alice, http.MethodGet, "/deployment/dependencies/"+deployed.Id, nil, &dependencies); status != http.StatusOK {
		t.Fatalf("dependencies: %v", status)
	}
	expected := readFixture[deprecated.Dependencies](t, "testdata/dependencies/dependencies.json")
	if got, want := elementsPerDevice(dependencies), elementsPerDevice(expected); !reflect.DeepEqual(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func elementsPerDevice(d deprecated.Dependencies) map[string][]string {
	result := map[string][]string{}
	for _, device := range d.Devices {
		for _, r := range device.BpmnResources {
			result[device.DeviceId] = append(result[device.DeviceId], r.Id)
		}
	}
	for _, ids := range result {
		sort.Strings(ids)
	}
	return result
}
