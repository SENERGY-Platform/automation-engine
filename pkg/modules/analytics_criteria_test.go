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
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
)

// The tests here are those of smart-service-module-worker-analytics on aspect lists in criteria
// and on the output of the task, with the test cases in testdata/analytics.

const groupCaseInput = "373808f2-848a-4446-8062-abd973dc96d3"

// selectablesLog records the criteria sent to device-repository as JSON and passes every
// request on to the fake services of the worker tests
type selectablesLog struct {
	next     http.Handler
	mux      sync.Mutex
	criteria [][]map[string]any
}

func (this *selectablesLog) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v2/query/device-type-selectables" {
		body, _ := io.ReadAll(r.Body)
		var criteria []map[string]any
		_ = json.Unmarshal(body, &criteria)
		this.mux.Lock()
		this.criteria = append(this.criteria, criteria)
		this.mux.Unlock()
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	this.next.ServeHTTP(w, r)
}

// analyticsCase reads the inputs of a test case and serves its answers
func analyticsCase(t *testing.T, name string) (*analyticsServices, map[string]struct{ Value any }) {
	t.Helper()
	services := &analyticsServices{t: t, dir: filepath.Join("testdata/analytics", name)}
	services.read("flow_model_cells.json", &services.cells)
	services.read("device_type_selectables.json", &services.selectables1)
	services.read("device_type_selectables_2.json", &services.selectables2)
	services.read("permissions_query_responses.json", &services.repository)
	var tasks []struct {
		Variables map[string]struct{ Value any } `json:"variables"`
	}
	services.read("camunda_tasks.json", &tasks)
	return services, tasks[0].Variables
}

func analyticsServer(t *testing.T, handler http.Handler) (*server.Server, string) {
	t.Helper()
	fake := httptest.NewServer(handler)
	t.Cleanup(fake.Close)
	return newServer(t, func(c *configuration.Config) {
		c.FlowEngineUrl, c.FlowParserUrl, c.DeviceRepositoryUrl, c.ImportDeployUrl = fake.URL, fake.URL, fake.URL, fake.URL
	}), fake.URL
}

// runAnalytics runs the analytics task with the variables of a Camunda task and the extra
// mappings, and waits until the smart service is ready
func runAnalytics(t *testing.T, srv *server.Server, variables map[string]struct{ Value any }, mappings ...string) model.Instance {
	t.Helper()
	own := map[string]struct{ Value any }{}
	for name, variable := range variables {
		if strings.HasPrefix(name, "analytics.") {
			own[name] = variable
		}
	}
	c := srv.Controller
	d, err := c.CreateDeployment(ctx, alice, "smart-services", model.DeploymentInput{Xml: b.Process(
		b.Start("start"), b.Service("task1", "analytics", append(workerVariables(own), mappings...)...), b.End("end"),
		b.Flow("start", "task1"), b.Flow("task1", "end"),
	)})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "smart-services", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	var current model.Instance
	waitFor(t, "the smart service", func() bool {
		current, _ = c.GetInstance(ctx, alice, "smart-services", inst.Id)
		return current.State != model.StateRunning || current.Status == model.StatusReady
	})
	if current.Status != model.StatusReady {
		incidents, _ := c.ListIncidents(ctx, alice, "smart-services", model.IncidentListOptions{InstanceId: inst.Id})
		t.Fatalf("instance %+v: %+v", current, incidents)
	}
	return current
}

// A group selection asks device-repository first with the criteria of the port, then with its
// service criteria. An empty interaction means event; the deprecated aspect id joins the list
// and stays for device-repositories that predate the lists.
func TestGroupSelectionSendsAspectListsToDeviceRepository(t *testing.T) {
	services, variables := analyticsCase(t, "group-with-service-criteria")
	variables["analytics.criteria."+groupCaseInput+".port-name"] = struct{ Value any }{`[{"function_id":"foo","aspect_ids":["a1","a2"]}]`}
	variables["analytics.service_criteria."+groupCaseInput+".port-name"] = struct{ Value any }{
		`[{"function_id":"foo","aspect_id":"a3"},{"function_id":"bar","aspect_id":"a5","aspect_ids":["a4"]},{"function_id":"baz","aspect_id":"a6","aspect_ids":["a6","a7"]},{"function_id":"qux","interaction":"request"}]`}
	log := &selectablesLog{next: services}
	srv, _ := analyticsServer(t, log)
	runAnalytics(t, srv, variables)

	criterion := func(interaction string, functionId string, aspectId string, aspectIds ...any) map[string]any {
		result := map[string]any{"interaction": interaction, "function_id": functionId, "device_class_id": "", "aspect_id": aspectId}
		if len(aspectIds) > 0 {
			result["aspect_ids"] = aspectIds
		}
		return result
	}
	expected := [][]map[string]any{
		{criterion("event", "foo", "", "a1", "a2")},
		{
			criterion("event", "foo", "a3", "a3"),
			criterion("event", "bar", "a5", "a4", "a5"),
			criterion("event", "baz", "a6", "a6", "a7"),
			criterion("request", "qux", ""),
		},
	}
	log.mux.Lock()
	defer log.mux.Unlock()
	if !reflect.DeepEqual(log.criteria, expected) {
		actual, _ := json.Marshal(log.criteria)
		wanted, _ := json.Marshal(expected)
		t.Errorf("device-repository was asked\n%s\nnot\n%s", actual, wanted)
	}
}

// The task completes with the id of the pipeline it created.
func TestAnalyticsOutputsThePipelineIdOfACreatedPipeline(t *testing.T) {
	services, variables := analyticsCase(t, "device")
	srv, _ := analyticsServer(t, services)
	inst := runAnalytics(t, srv, variables, b.OutScript("pipeline", "pipeline_id"))
	vars, err := srv.Controller.GetVariables(ctx, alice, "smart-services", inst.Id)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]any{}
	for _, v := range vars {
		values[v.Name] = v.Value
	}
	if values["pipeline"] != analyticsPipelineId {
		t.Errorf("%v", values)
	}
}

// A task with the key of an existing module updates its pipeline and completes with its id.
func TestAnalyticsOutputsThePipelineIdOfAnUpdatedPipeline(t *testing.T) {
	services, variables := analyticsCase(t, "group-update")
	srv, base := analyticsServer(t, services)
	variables["postscript"] = struct{ Value any }{`variables.write("pipeline", outputs.get("pipeline_id"));`}
	existing := []model.Module{{Id: "process-instance-1.task0", ModuleType: "analytics", Keys: []string{"updatekey"},
		Data: map[string]any{"pipeline_id": "existing-pipeline"}, DeleteInfo: &model.ModuleDeleteInfo{Url: "http://localhost/pipeline/existing-pipeline"}}}
	inst, _ := runWorkerTask(t, srv, "analytics", variables, existing, base)
	vars, err := srv.Controller.GetVariables(ctx, alice, "smart-services", inst.Id)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]any{}
	for _, v := range vars {
		values[v.Name] = v.Value
	}
	if values["pipeline"] != "existing-pipeline" {
		t.Errorf("%v", values)
	}
}
