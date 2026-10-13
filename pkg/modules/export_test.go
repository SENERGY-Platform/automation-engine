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

package modules_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

const exportId = "8c5c5a52-2f1b-4bbb-9d6c-0b7f2e2c1e11"

// serving fakes the export service and remembers requests with their user header
type serving struct {
	mux      sync.Mutex
	requests []string
	bodies   []map[string]any
}

func (this *serving) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	this.mux.Lock()
	this.requests = append(this.requests, r.Method+" "+r.URL.Path+" "+tokenOf(r.Header.Get("Authorization"))+" "+r.Header.Get("X-UserId"))
	this.bodies = append(this.bodies, body)
	this.mux.Unlock()
	if r.Method == http.MethodPost {
		_ = json.NewEncoder(w).Encode(map[string]any{"ID": exportId, "Name": body["Name"], "Topic": body["Topic"]})
	}
}

func (this *serving) all() ([]string, []map[string]any) {
	this.mux.Lock()
	defer this.mux.Unlock()
	return append([]string{}, this.requests...), append([]map[string]any{}, this.bodies...)
}

func exportServer(t *testing.T) (*serving, string, func(c *configuration.Config)) {
	fake := &serving{}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return fake, server.URL, func(c *configuration.Config) { c.ServingServiceUrl = server.URL }
}

func TestExportCreatesTheExportWithTheOwnersTokenAndRemovesItWithTheInstance(t *testing.T) {
	fake, url, configure := exportServer(t)
	srv := newServer(t, configure)
	c := srv.Controller
	request := `{"FilterType":"operatorId","Filter":"p1:o1","Name":"{{.room}} export","EntityName":"e","ServiceName":"s","Topic":"analytics-o1","Offset":"largest",` +
		`"Values":[{"Name":"value","Type":"float","Path":"value"}]}`
	d, err := c.CreateDeployment(ctx, alice, "smart-services", model.DeploymentInput{Xml: b.Process(
		b.Start("start"),
		b.Service("export", "export", b.InText("export.request", request), b.InText("export.module_data", `{"widget":"chart"}`), b.OutScript("export_id", "export_id")),
		b.End("end"), b.Flow("start", "export"), b.Flow("export", "end"),
	)})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "smart-services", model.StartInstanceInput{DeploymentId: d.Id, Variables: map[string]any{"room": "kitchen"}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the smart service", func() bool {
		current, _ := c.GetInstance(ctx, alice, "smart-services", inst.Id)
		return current.Status == model.StatusReady
	})
	requests, bodies := fake.all()
	if len(requests) != 1 || requests[0] != "POST /instance "+"token of alice"+" alice" {
		t.Fatalf("serving is asked with the owner's token and user id: %v", requests)
	}
	values, _ := bodies[0]["Values"].([]any)
	if bodies[0]["Name"] != "kitchen export" || bodies[0]["Offset"] != "largest" || len(values) != 1 || values[0].(map[string]any)["Tag"] != false {
		t.Fatalf("the rendered request goes to serving as the worker sent it: %v", bodies[0])
	}
	if variable, err := c.GetVariable(ctx, alice, "smart-services", inst.Id, "export_id", ""); err != nil || variable.Value != exportId {
		t.Fatalf("export_id is an output: %+v %v", variable, err)
	}
	modules, err := c.ListModules(ctx, alice, "smart-services", model.ModuleListOptions{InstanceId: inst.Id})
	if err != nil || modules.Total != 1 {
		t.Fatalf("modules: %+v %v", modules, err)
	}
	m := modules.Items[0]
	export, _ := m.Data["export"].(map[string]any)
	if m.ModuleType != "export" || m.Data["widget"] != "chart" || export["ID"] != exportId || export["Topic"] != "analytics-o1" ||
		m.DeleteInfo == nil || m.DeleteInfo.Url != url+"/instance/"+exportId {
		t.Fatalf("the module keeps the export of serving next to the module data: %+v", m)
	}

	if err = c.DeleteInstances(ctx, alice, "smart-services", []string{inst.Id}); err != nil {
		t.Fatal(err)
	}
	if err = srv.ModuleRemover.Process(ctx); err != nil {
		t.Fatal(err)
	}
	if requests, _ = fake.all(); len(requests) != 2 || !strings.HasPrefix(requests[1], "DELETE /instance/"+exportId+" "+"token of alice") {
		t.Fatalf("deleting the instance removes the export: %v", requests)
	}
}

func TestExportRejectsAnIncompleteRequestWithoutCallingServing(t *testing.T) {
	fake, _, configure := exportServer(t)
	srv := newServer(t, configure)
	c := srv.Controller
	d, err := c.CreateDeployment(ctx, alice, "smart-services", model.DeploymentInput{Xml: b.Process(
		b.Start("start"),
		b.Service("export", "export", b.InText("export.request", `{"FilterType":"operatorId","Filter":"p1:o1","Name":"n"}`)),
		b.End("end"), b.Flow("start", "export"), b.Flow("export", "end"),
	)})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "smart-services", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	var incidents model.Page[model.Incident]
	waitFor(t, "the incident", func() bool {
		incidents, _ = c.ListIncidents(ctx, alice, "smart-services", model.IncidentListOptions{InstanceId: inst.Id})
		return incidents.Total > 0
	})
	if !strings.Contains(incidents.Items[0].Message, "invalid export request") {
		t.Fatalf("the validation error is the incident: %+v", incidents.Items)
	}
	if requests, _ := fake.all(); len(requests) != 0 {
		t.Fatalf("serving is not called: %v", requests)
	}
}
