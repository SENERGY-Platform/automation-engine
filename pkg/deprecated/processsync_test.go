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
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
	"github.com/SENERGY-Platform/automation-engine/pkg/permissions"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
	"github.com/SENERGY-Platform/models/go/models"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
)

const hub = "hub-1"

// newFogEnv runs a cloud and a gateway connected like by mgw-device-cloud-connector
func newFogEnv(t *testing.T) *env {
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
	config := configuration.Default()
	config.MinTimerDuration = "0s"
	config.SqlitePath = storetest.MemoryDsn()
	config.PollInterval = "20ms"
	config.SyncResendAfter = "300ms"
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

// The fog views of the web-ui deploy to a gateway with the engine, follow the deployment and
// start, stop and delete through the routes of process-fog-deployment and process-sync.
func TestFogViewsOfTheWebUi(t *testing.T) {
	e := newFogEnv(t)
	var prepared models.Deployment
	if status, _ := e.call(alice, http.MethodPost, "/fog-deployment/prepared-deployments/"+hub, map[string]string{"xml": waitingProcess(), "svg": "<svg/>"}, &prepared); status != http.StatusOK {
		t.Fatal(status)
	}
	prepared.Name = "Fog"
	var deployed models.Deployment
	if status, _ := e.call(alice, http.MethodPost, "/fog-deployment/deployments/"+hub+"?source=sepl", prepared, &deployed); status != http.StatusOK || deployed.Id == "" {
		t.Fatalf("%v %+v", status, deployed)
	}
	var deployments []struct {
		Id            string
		NetworkId     string `json:"network_id"`
		IsPlaceholder bool   `json:"is_placeholder"`
		Diagram       string
	}
	e.waitFor("the deployment applied on the gateway", func() bool {
		e.call(alice, http.MethodGet, "/process-sync/deployments?network_id="+hub+"&extended=true&limit=10&offset=0&sort=name.asc", nil, &deployments)
		return len(deployments) == 1 && !deployments[0].IsPlaceholder
	})
	if deployments[0].Id != deployed.Id || deployments[0].NetworkId != hub || deployments[0].Diagram != "<svg/>" {
		t.Errorf("%+v", deployments)
	}
	var metadata []struct {
		CamundaDeploymentId string            `json:"camunda_deployment_id"`
		DeploymentModel     models.Deployment `json:"deployment_model"`
	}
	if e.call(alice, http.MethodGet, "/process-sync/metadata/"+hub+"?deployment_id="+deployed.Id, nil, &metadata); len(metadata) != 1 || metadata[0].CamundaDeploymentId != deployed.Id || metadata[0].DeploymentModel.Name != "Fog" {
		t.Errorf("metadata: %+v", metadata)
	}
	var started bool
	if status, _ := e.call(alice, http.MethodGet, "/process-sync/deployments/"+hub+"/"+deployed.Id+"/start?business_key=k1", nil, &started); status != http.StatusOK || !started {
		t.Fatalf("start: %v", status)
	}
	e.waitFor("the running instance", func() bool { return e.instance("k1").State == model.StateRunning })
	var history struct {
		Total int
		Data  []struct {
			Id        string
			NetworkId string `json:"network_id"`
			State     string
		}
	}
	e.call(alice, http.MethodGet, "/process-sync/history/process-instances?network_id="+hub+"&with_total=true&state=unfinished&limit=10&offset=0", nil, &history)
	if history.Total != 1 || history.Data[0].Id != "k1" || history.Data[0].NetworkId != hub || history.Data[0].State != "ACTIVE" {
		t.Errorf("history: %+v", history)
	}
	if status, _ := e.call(alice, http.MethodDelete, "/process-sync/process-instances/other-hub/k1", nil, nil); status != http.StatusNotFound {
		t.Errorf("an instance of another gateway is not found: %v", status)
	}
	if status, _ := e.call(alice, http.MethodDelete, "/process-sync/process-instances/"+hub+"/k1", nil, nil); status != http.StatusOK {
		t.Fatal(status)
	}
	e.waitFor("the stopped instance", func() bool { return e.instance("k1").State == model.StateTerminated })
	var networks []struct{ Id string }
	if e.call(alice, http.MethodGet, "/process-sync/networks", nil, &networks); len(networks) != 1 || networks[0].Id != hub {
		t.Errorf("%+v", networks)
	}
	if status, _ := e.call(alice, http.MethodDelete, "/fog-deployment/deployments/"+hub+"/"+deployed.Id, nil, nil); status != http.StatusOK {
		t.Fatal(status)
	}
	if _, err := e.srv.Controller.GetDeployment(ctx, alice, "processes", deployed.Id); err == nil {
		t.Error("deleted")
	}
}
