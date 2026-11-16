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
	"net/http"
	"net/http/httptest"
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	devicerepomodel "github.com/SENERGY-Platform/device-repository/v3/lib/model"
	"github.com/SENERGY-Platform/models/go/models"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
)

// oneDevice knows the device d1 of alice
type oneDevice struct{ noDevices }

func (oneDevice) ReadDevice(_ context.Context, id string, _ string, _ devicerepomodel.AuthAction) (models.Device, error, int) {
	if id != "d1" {
		return models.Device{}, errNotNeeded, http.StatusNotFound
	}
	return models.Device{Id: id, LocalId: "local-1", Name: "PC"}, nil, http.StatusOK
}

// The deploy dialog selects the connection state of a device for an event as generic event source
// with filter_type connectionState, and for the task that reads it as selected_device_id without
// service; the engine returns both in the same form when the deployment is read, and the prepared
// deployment lists the task (SNRGY-5151).
func TestDeployDialogSelectsTheConnectionOfADevice(t *testing.T) {
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	var deps server.Dependencies
	srv, err := client.NewTestServer(ctx, func(_ *configuration.Config, d *server.Dependencies) {
		d.DeviceRepository, d.Marshaller, d.Producer = oneDevice{}, noDevices{}, noDevices{}
		deps = *d
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err, _ = deps.Permissions.SetPermission(client.TestToken("x", "admin"), "devices", "d1", permmodel.ResourcePermissions{
		UserPermissions: map[string]permmodel.PermissionsMap{"alice": {Read: true, Execute: true, Administrate: true}},
	}); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(srv.Handler)
	t.Cleanup(httpServer.Close)
	e := &env{t: t, srv: srv, url: httpServer.URL}
	xml := b.Process(b.Start("start"), b.Service("conn", "device_connection"),
		`<bpmn:intermediateCatchEvent id="ev" name="Connection" senergy:script="value == 'online'" senergy:value_variable_name="value"><bpmn:messageEventDefinition/></bpmn:intermediateCatchEvent>`,
		b.End("end"), b.Flow("start", "conn"), b.Flow("conn", "ev"), b.Flow("ev", "end"))
	var prepared models.Deployment
	if status, _ := e.call(alice, http.MethodPost, "/deployment/v3/prepared-deployments?with_options=false", map[string]string{"xml": xml, "svg": "<svg/>"}, &prepared); status != http.StatusOK {
		t.Fatalf("%v %+v", status, prepared)
	}
	if task := elementById(prepared, "conn").Task; task == nil {
		t.Fatalf("the task that reads the connection is a task of the dialog: %+v", prepared.Elements)
	}
	if elementById(prepared, "ev").ConditionalEvent == nil {
		t.Fatalf("the event is an event of the dialog: %+v", prepared.Elements)
	}
	prepared.Name = "Connection"
	for i, element := range prepared.Elements {
		switch element.BpmnId {
		case "conn":
			device := "d1"
			prepared.Elements[i].Task.Selection.SelectedDeviceId = &device
		case "ev":
			prepared.Elements[i].ConditionalEvent.Selection.SelectedGenericEventSource = &models.GenericEventSource{FilterType: "connectionState", FilterIds: "d1"}
		}
	}
	var deployed models.Deployment
	if status, _ := e.call(alice, http.MethodPost, "/deployment/v3/deployments?source=sepl", prepared, &deployed); status != http.StatusOK || deployed.Id == "" {
		t.Fatalf("deploy: %v %+v", status, deployed)
	}
	version, err := srv.Controller.GetDeploymentVersion(ctx, alice, "processes", deployed.Id, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"conn", "ev"} {
		if s := version.Selections[id]; s != (model.Selection{ConnectionDeviceId: "d1"}) {
			t.Errorf("%v: %+v", id, s)
		}
	}
	var read models.Deployment
	e.call(alice, http.MethodGet, "/deployment/v3/deployments/"+deployed.Id+"?with_options=false", nil, &read)
	if source := elementById(read, "ev").ConditionalEvent.Selection.SelectedGenericEventSource; source == nil || source.FilterType != "connectionState" || source.FilterIds != "d1" {
		t.Errorf("reading shows the connection source: %+v", source)
	}
	if s := elementById(read, "conn").Task.Selection; s.SelectedDeviceId == nil || *s.SelectedDeviceId != "d1" || s.SelectedGenericEventSource != nil {
		t.Errorf("reading shows the device of the task: %+v", s)
	}
}
