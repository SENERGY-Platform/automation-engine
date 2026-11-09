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
	"net/http"
	"net/http/httptest"
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/models/go/models"
)

// The deploy dialog of the web-ui selects the output of an analytics operator as generic event
// source; the engine describes the event on the topic of the operator and shows the selection
// again when the deployment is read (SNRGY-5113).
func TestDeployDialogSelectsAnAnalyticsOperator(t *testing.T) {
	pipelines := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pipeline/p1" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "p1", "userId": "alice", "operators": []map[string]any{
			{"id": "o1", "name": "stopping", "deploymentType": "cloud", "outputTopic": "analytics-stopping"},
		}})
	}))
	t.Cleanup(pipelines.Close)
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	srv, err := client.NewTestServer(ctx, func(config *configuration.Config, deps *server.Dependencies) {
		config.AnalyticsPipelineUrl = pipelines.URL
		// enables event descriptions; an analytics event reads no device
		deps.DeviceRepository = noDevices{}
	})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(srv.Handler)
	t.Cleanup(httpServer.Close)
	e := &env{t: t, srv: srv, url: httpServer.URL}

	xml := b.Process(b.Start("start"),
		`<bpmn:intermediateCatchEvent id="timer" name="Get New Timer"><bpmn:messageEventDefinition/></bpmn:intermediateCatchEvent>`,
		b.End("end"), b.Flow("start", "timer"), b.Flow("timer", "end"))
	var prepared models.Deployment
	if status, _ := e.call(alice, http.MethodPost, "/deployment/v3/prepared-deployments?with_options=false", map[string]string{"xml": xml, "svg": "<svg/>"}, &prepared); status != http.StatusOK {
		t.Fatalf("%v %+v", status, prepared)
	}
	prepared.Name = "Thermostat"
	for i, element := range prepared.Elements {
		if element.BpmnId == "timer" {
			if element.ConditionalEvent == nil {
				t.Fatalf("the catch event without device criteria is an event of the dialog: %+v", element)
			}
			// as the web-ui sends it: the topic by its convention, an empty path
			prepared.Elements[i].ConditionalEvent.Selection.SelectedGenericEventSource = &models.GenericEventSource{FilterType: "operatorId", FilterIds: "p1:o1", Topic: "analytics-stopping"}
			prepared.Elements[i].ConditionalEvent.Selection.SelectedPath = &models.PathOption{}
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
	if s := version.Selections["timer"]; s != (model.Selection{PipelineId: "p1", OperatorId: "o1"}) {
		t.Errorf("selection: %+v", s)
	}
	var descriptions []model.EventDescription
	_ = srv.Store.Tx(ctx, func(tx *store.Tx) (err error) {
		descriptions, err = tx.ListEventDescriptions(deployed.Id, 0)
		return err
	})
	if len(descriptions) != 1 || descriptions[0].Topic != "analytics-stopping" || descriptions[0].PipelineId != "p1" || descriptions[0].OperatorId != "o1" {
		t.Errorf("descriptions: %+v", descriptions)
	}
	var read models.Deployment
	e.call(alice, http.MethodGet, "/deployment/v3/deployments/"+deployed.Id+"?with_options=false", nil, &read)
	if source := elementById(read, "timer").ConditionalEvent.Selection.SelectedGenericEventSource; source == nil || source.FilterType != "operatorId" || source.FilterIds != "p1:o1" {
		t.Errorf("reading shows the operator: %+v", source)
	}

	for i, element := range prepared.Elements {
		if element.BpmnId == "timer" {
			prepared.Elements[i].ConditionalEvent.Selection.SelectedGenericEventSource = &models.GenericEventSource{FilterType: "operatorId", FilterIds: "p2:o1"}
		}
	}
	if status, _ := e.call(alice, http.MethodPost, "/deployment/v3/deployments?source=sepl", prepared, nil); status != http.StatusBadRequest {
		t.Errorf("an unknown pipeline is rejected: %v", status)
	}
}
