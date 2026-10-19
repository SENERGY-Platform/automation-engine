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
	"sync/atomic"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
	"github.com/SENERGY-Platform/automation-engine/pkg/permissions"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
)

// The behaviour of process-sync that the fog views of the web-ui rely on, through the old
// routes: placeholders until the gateway reports, metadata, starts with parameters, history
// and incidents of gateways.

// newSwitchableFogEnv is newFogEnv with a connector that drops everything while online is false
func newSwitchableFogEnv(t *testing.T) (*env, *atomic.Bool) {
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	online := &atomic.Bool{}
	online.Store(true)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cloudBroker, gatewayBroker := mqtt.NewMemory(), mqtt.NewMemory()
	_ = cloudBroker.Subscribe("processes/"+hub+"/cmd/#", 1, func(topic string, payload []byte) error {
		if !online.Load() {
			return nil
		}
		return gatewayBroker.Publish(ctx, "processes/cmd/"+strings.TrimPrefix(topic, "processes/"+hub+"/cmd/"), 1, payload)
	})
	_ = gatewayBroker.Subscribe("processes/state/#", 1, func(topic string, payload []byte) error {
		if !online.Load() {
			return nil
		}
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
	return e, online
}

func (this *env) deployOnHub(name string, xml string) model.Deployment {
	this.t.Helper()
	d, err := this.srv.Controller.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Name: name, Xml: xml, NetworkId: hub})
	if err != nil {
		this.t.Fatal(err)
	}
	return d
}

type syncDeployment struct {
	Id            string `json:"id"`
	Name          string `json:"name"`
	IsPlaceholder bool   `json:"is_placeholder"`
}

func (this *env) hubDeployments(query string) (result []syncDeployment) {
	this.t.Helper()
	if status, _ := this.call(alice, http.MethodGet, "/process-sync/deployments?network_id="+hub+query, nil, &result); status != http.StatusOK {
		this.t.Fatal(status)
	}
	return result
}

// waitApplied waits until the gateway has applied a deployment
func (this *env) waitApplied(id string) {
	this.t.Helper()
	this.waitFor("the deployment applied on the gateway", func() bool {
		list := this.hubDeployments("")
		for _, d := range list {
			if d.Id == id {
				return !d.IsPlaceholder
			}
		}
		return false
	})
}

type syncHistoric struct {
	Id            string `json:"id"`
	BusinessKey   string `json:"businessKey"`
	State         string `json:"state"`
	IsPlaceholder bool   `json:"is_placeholder"`
}

func (this *env) hubHistory(query string) (result []syncHistoric) {
	this.t.Helper()
	if status, _ := this.call(alice, http.MethodGet, "/process-sync/history/process-instances?network_id="+hub+query, nil, &result); status != http.StatusOK {
		this.t.Fatal(status)
	}
	return result
}

func (this *env) startOnHub(deploymentId string, query string) {
	this.t.Helper()
	if status, _ := this.call(alice, http.MethodGet, "/process-sync/deployments/"+hub+"/"+deploymentId+"/start?"+query, nil, nil); status != http.StatusOK {
		this.t.Fatal(status)
	}
}

func TestAFogDeploymentIsAPlaceholderUntilTheGatewayAppliesIt(t *testing.T) {
	e, online := newSwitchableFogEnv(t)
	online.Store(false)
	d := e.deployOnHub("Fog", waitingProcess())
	if list := e.hubDeployments(""); len(list) != 1 || list[0].Id != d.Id || !list[0].IsPlaceholder {
		t.Fatalf("a deployment the gateway has not applied is a placeholder: %+v", list)
	}
	online.Store(true)
	e.waitApplied(d.Id)
}

func TestAFogStartIsAPlaceholderInTheHistoryUntilTheGatewayReportsIt(t *testing.T) {
	e, online := newSwitchableFogEnv(t)
	d := e.deployOnHub("Fog", waitingProcess())
	e.waitApplied(d.Id)
	online.Store(false)
	e.startOnHub(d.Id, "business_key=k1")
	if history := e.hubHistory(""); len(history) != 1 || history[0].Id != "k1" || !history[0].IsPlaceholder {
		t.Fatalf("a start the gateway has not reported is a placeholder: %+v", history)
	}
	online.Store(true)
	e.waitFor("the start reported by the gateway", func() bool {
		history := e.hubHistory("")
		return len(history) == 1 && !history[0].IsPlaceholder && history[0].State == "ACTIVE"
	})
}

func TestMetadataOfAnUnknownFogDeploymentIsAnEmptyList(t *testing.T) {
	e := newFogEnv(t)
	var metadata []syncMetadata
	if status, _ := e.call(alice, http.MethodGet, "/process-sync/metadata/"+hub+"?deployment_id=unknown", nil, &metadata); status != http.StatusOK || metadata == nil || len(metadata) != 0 {
		t.Errorf("an unknown deployment has no metadata: %v %+v", status, metadata)
	}
}

func TestMetadataOfADeletedFogDeploymentIsNotFound(t *testing.T) {
	e := newFogEnv(t)
	d := e.deployOnHub("Fog", waitingProcess())
	e.waitApplied(d.Id)
	if status, _ := e.call(alice, http.MethodDelete, "/process-sync/deployments/"+hub+"/"+d.Id, nil, nil); status != http.StatusOK {
		t.Fatal(status)
	}
	if status, _ := e.call(alice, http.MethodGet, "/process-sync/deployments/"+hub+"/"+d.Id+"/metadata", nil, nil); status != http.StatusNotFound {
		t.Errorf("the metadata of a deleted deployment is gone: %v", status)
	}
}

// syncMetadata is the part of the metadata the fog views read
type syncMetadata struct {
	CamundaDeploymentId string `json:"camunda_deployment_id"`
	NetworkId           string `json:"network_id"`
	ProcessParameter    map[string]struct {
		Value any    `json:"value"`
		Type  string `json:"type"`
	} `json:"process_parameter"`
}

func TestFogMetadataNamesTheStartParameters(t *testing.T) {
	e := newFogEnv(t)
	d := e.deployOnHub("Fog", waitingProcess())
	var metadata syncMetadata
	if status, _ := e.call(alice, http.MethodGet, "/process-sync/deployments/"+hub+"/"+d.Id+"/metadata", nil, &metadata); status != http.StatusOK {
		t.Fatal(status)
	}
	if _, ok := metadata.ProcessParameter["level"]; !ok || len(metadata.ProcessParameter) != 1 || metadata.NetworkId != hub {
		t.Errorf("the start form field is the one parameter: %+v", metadata)
	}
}

func TestAFogStartPassesItsParametersAsStartVariables(t *testing.T) {
	e := newFogEnv(t)
	d := e.deployOnHub("Fog", waitingProcess())
	e.startOnHub(d.Id, "business_key=k1&level=7")
	if inst := e.instance("k1"); inst.StartVariables["level"] != 7.0 {
		t.Errorf("the parameter is a start variable, read as JSON: %+v", inst.StartVariables)
	}
}

func TestDeletingAFogInstanceRemovesItFromTheHistory(t *testing.T) {
	e := newFogEnv(t)
	d := e.deployOnHub("Fog", waitingProcess())
	e.startOnHub(d.Id, "business_key=k1")
	e.waitFor("the running instance", func() bool { return e.instance("k1").State == model.StateRunning })
	if status, _ := e.call(alice, http.MethodDelete, "/process-sync/history/process-instances/"+hub+"/k1", nil, nil); status != http.StatusOK {
		t.Fatal(status)
	}
	if history := e.hubHistory(""); len(history) != 0 {
		t.Errorf("the deleted instance is gone from the history: %+v", history)
	}
}

func TestFogHistoryIsFilteredByState(t *testing.T) {
	e := newFogEnv(t)
	waiting := e.deployOnHub("Waiting", waitingProcess())
	ending := e.deployOnHub("Ending", b.Process(b.Start("start"), b.End("end"), b.Flow("start", "end")))
	e.startOnHub(waiting.Id, "business_key=running")
	e.startOnHub(ending.Id, "business_key=ended")
	e.waitFor("both instances reported", func() bool {
		return e.instance("running").State == model.StateRunning && e.instance("ended").State == model.StateCompleted
	})
	finished, unfinished := e.hubHistory("&state=finished"), e.hubHistory("&state=unfinished")
	if len(finished) != 1 || finished[0].Id != "ended" || len(unfinished) != 1 || unfinished[0].Id != "running" {
		t.Errorf("finished %+v, unfinished %+v", finished, unfinished)
	}
}

func TestFogDeploymentsAreFoundByPartOfTheirName(t *testing.T) {
	e := newFogEnv(t)
	kitchen := e.deployOnHub("Kitchen lights", waitingProcess())
	e.deployOnHub("Garage door", waitingProcess())
	if found := e.hubDeployments("&search=itchen"); len(found) != 1 || found[0].Id != kitchen.Id {
		t.Errorf("%+v", found)
	}
}

// the sync client sent incidents with the business key of their instance (TestIncidentBusinessKey)
func TestIncidentsOfAFogInstanceCarryTheInstanceAsBusinessKey(t *testing.T) {
	e := newFogEnv(t)
	d := e.deployOnHub("Failing", b.Process(b.Start("start"), b.Script("bad", "x", "undefinedVar + 1"), b.End("end"), b.Flow("start", "bad"), b.Flow("bad", "end")))
	e.startOnHub(d.Id, "business_key=k1")
	var incidents []syncIncident
	e.waitFor("the incident of the gateway", func() bool {
		incidents = nil
		e.call(alice, http.MethodGet, "/process-sync/incidents?network_id="+hub, nil, &incidents)
		return len(incidents) > 0
	})
	if incidents[0].BusinessKey != "k1" || incidents[0].ProcessInstanceId != "k1" || !strings.Contains(incidents[0].ErrorMessage, "undefinedVar") {
		t.Errorf("%+v", incidents[0])
	}
}

// syncIncident mirrors the fields of an incident of the old route
type syncIncident struct {
	ProcessInstanceId string `json:"process_instance_id"`
	ErrorMessage      string `json:"error_message"`
	BusinessKey       string `json:"business_key"`
}
