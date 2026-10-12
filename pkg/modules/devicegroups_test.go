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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/controller"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// The cases in testdata/devicegroups are the test cases of
// smart-service-module-worker-device-repository v0.1.2: the inputs of the Camunda task, the
// answers of device-manager and device-selection, the requests the worker sent to them and the
// module it stored. Ids of Camunda are replaced by those of the engine before comparing.

type replayResponse struct {
	StatusCode int    `json:"status_code"`
	Message    string `json:"message"`
}

// replay answers requests with the recorded responses per method and path, in order, and logs
// the requests with their query
type replay struct {
	mux       sync.Mutex
	responses map[string]map[string][]replayResponse
	log       []analyticsRequest
}

func (this *replay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	this.mux.Lock()
	defer this.mux.Unlock()
	endpoint := r.URL.Path
	if r.URL.RawQuery != "" {
		endpoint += "?" + r.URL.RawQuery
	}
	this.log = append(this.log, analyticsRequest{Method: r.Method, Endpoint: endpoint, Message: string(body)})
	responses := this.responses[r.Method][r.URL.Path]
	if len(responses) == 0 {
		http.Error(w, "no response stored", http.StatusInternalServerError)
		return
	}
	this.responses[r.Method][r.URL.Path] = responses[1:]
	w.WriteHeader(responses[0].StatusCode)
	_, _ = w.Write([]byte(responses[0].Message))
}

func (this *replay) requests() []analyticsRequest {
	this.mux.Lock()
	defer this.mux.Unlock()
	return append([]analyticsRequest{}, this.log...)
}

func readJson(t *testing.T, dir string, name string, target any) bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, target); err != nil {
		t.Fatalf("%v: %v", name, err)
	}
	return true
}

// sameRequests compares requests with the JSON of their bodies; replace maps Camunda ids to
// those of the engine
func sameRequests(t *testing.T, what string, expected []analyticsRequest, actual []analyticsRequest, replace *strings.Replacer) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("%v requests\nexpected %+v\nactual   %+v", what, expected, actual)
	}
	for i := range expected {
		e := expected[i]
		e.Endpoint, e.Message = replace.Replace(e.Endpoint), replace.Replace(e.Message)
		if actual[i].Method != e.Method || actual[i].Endpoint != e.Endpoint || !sameJson(t, actual[i].Message, e.Message) {
			t.Fatalf("%v request %v\nexpected %+v\nactual   %+v", what, i, e, actual[i])
		}
	}
}

func TestDeviceGroupsSendTheRequestsOfTheWorker(t *testing.T) {
	cases, err := os.ReadDir("testdata/devicegroups")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range cases {
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("testdata/devicegroups", name)
			manager, selection := &replay{}, &replay{}
			readJson(t, dir, "device_repository_responses.json", &manager.responses)
			readJson(t, dir, "device_selection_responses.json", &selection.responses)
			var tasks []struct {
				Variables map[string]struct{ Value any } `json:"variables"`
			}
			readJson(t, dir, "camunda_tasks.json", &tasks)
			var managerRequests, selectionRequests, repository []analyticsRequest
			readJson(t, dir, "device_repository_requests.json", &managerRequests)
			readJson(t, dir, "device_selection_requests.json", &selectionRequests)
			readJson(t, dir, "expected_smart_service_repo_requests.json", &repository)
			var existing []model.Module
			readJson(t, dir, "module_list_response.json", &existing)

			managerServer, selectionServer := httptest.NewServer(manager), httptest.NewServer(selection)
			t.Cleanup(managerServer.Close)
			t.Cleanup(selectionServer.Close)
			srv := newServer(t, func(c *configuration.Config) {
				c.DeviceManagerUrl, c.DeviceSelectionUrl = managerServer.URL, selectionServer.URL
			})

			c := srv.Controller
			inst, d := runWorkerTask(t, srv, "device_repository", tasks[0].Variables, existing, managerServer.URL)
			// the job of the task is the Camunda task; its id is part of the group attributes
			jobId := ""
			for _, request := range manager.requests() {
				var group struct {
					Attributes []struct{ Key, Value string }
				}
				_ = json.Unmarshal([]byte(request.Message), &group)
				for _, attribute := range group.Attributes {
					if attribute.Key == "platform/smart_service_task" {
						jobId = attribute.Value
					}
				}
			}
			if len(managerRequests) > 0 && jobId == "" {
				t.Fatalf("no task attribute: %+v", manager.requests())
			}
			replace := strings.NewReplacer("process-instance-1", inst.Id, "process-definition-1", d.Id, `"task1"`, `"`+jobId+`"`, ".task1", "."+jobId)
			sameRequests(t, "device-manager", managerRequests, manager.requests(), replace)
			sameRequests(t, "device-selection", selectionRequests, selection.requests(), replace)

			sameModules(t, c, inst.Id, repository, existing, replace, managerServer.URL)
		})
	}
}

// workerVariables turns the variables of a Camunda task into input mappings: strings as text,
// other values as script, so that they keep their type
func workerVariables(variables map[string]struct{ Value any }) []string {
	mappings := []string{}
	for name, variable := range variables {
		if s, ok := variable.Value.(string); ok {
			mappings = append(mappings, b.InText(name, s))
			continue
		}
		raw, _ := json.Marshal(variable.Value)
		mappings = append(mappings, b.InScript(name, string(raw)))
	}
	return mappings
}

// runWorkerTask runs a task with the variables of a Camunda task in a smart service and waits
// until the instance is done. The instance waits for a message first, so that the modules the
// worker found already exist before the task; their delete urls point to base.
func runWorkerTask(t *testing.T, srv *server.Server, taskType string, variables map[string]struct{ Value any }, existing []model.Module, base string) (model.Instance, model.Deployment) {
	t.Helper()
	c := srv.Controller
	d, err := c.CreateDeployment(ctx, alice, "smart-services", model.DeploymentInput{Xml: b.Process(
		b.Message("m", "go"), b.Start("start"), b.CatchMessage("wait", "m"), b.Service("task1", taskType, workerVariables(variables)...), b.End("end"),
		b.Flow("start", "wait"), b.Flow("wait", "task1"), b.Flow("task1", "end"),
	)})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "smart-services", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range existing {
		m.Id, m.Namespace, m.InstanceId, m.Owner = strings.ReplaceAll(m.Id, "process-instance-1", inst.Id), "smart-services", inst.Id, "alice"
		if m.DeleteInfo != nil {
			m.DeleteInfo.Url = strings.Replace(m.DeleteInfo.Url, "http://localhost", base, 1)
		}
		if err = srv.Store.Tx(ctx, func(tx *store.Tx) error { return tx.UpsertModule(m) }); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = c.CorrelateMessage(ctx, alice, model.MessageInput{Name: "go", All: true}); err != nil {
		t.Fatal(err)
	}
	var current model.Instance
	waitFor(t, "the smart service", func() bool {
		current, _ = c.GetInstance(ctx, alice, "smart-services", inst.Id)
		return current.State != model.StateRunning
	})
	if current.State != model.StateCompleted {
		incidents, _ := c.ListIncidents(ctx, alice, "smart-services", model.IncidentListOptions{InstanceId: inst.Id})
		t.Fatalf("instance %+v: %+v", current, incidents)
	}
	return current, d
}

// sameModules compares the modules of an instance with those the worker stored by PUT in
// smart-service-repository; replace maps the Camunda ids in the ids of new modules, an updated
// module keeps the id it was created with
func sameModules(t *testing.T, c *controller.Controller, instanceId string, repository []analyticsRequest, existing []model.Module, replace *strings.Replacer, base string) {
	t.Helper()
	modules, err := c.ListModules(ctx, alice, "smart-services", model.ModuleListOptions{InstanceId: instanceId})
	if err != nil {
		t.Fatal(err)
	}
	var stored []model.Module
	for _, request := range repository {
		if request.Method != http.MethodPut || !strings.Contains(request.Endpoint, "/modules/") {
			continue
		}
		var m model.Module
		if err = json.Unmarshal([]byte(request.Message), &m); err != nil {
			t.Fatal(err)
		}
		_, id, _ := strings.Cut(request.Endpoint, "/modules/")
		m.Id = replace.Replace(id)
		for _, e := range existing {
			if e.Id == id {
				m.Id = strings.ReplaceAll(id, "process-instance-1", instanceId)
			}
		}
		stored = append(stored, m)
	}
	if len(modules.Items) != len(stored) {
		t.Fatalf("modules\nexpected %+v\nactual   %+v", stored, modules.Items)
	}
	for i, m := range stored {
		actual := modules.Items[i]
		expectedData, _ := json.Marshal(m.Data)
		actualData, _ := json.Marshal(actual.Data)
		sameDelete := (m.DeleteInfo == nil) == (actual.DeleteInfo == nil)
		if sameDelete && m.DeleteInfo != nil {
			sameDelete = strings.TrimPrefix(actual.DeleteInfo.Url, base) == strings.TrimPrefix(m.DeleteInfo.Url, "http://localhost")
		}
		if actual.Id != m.Id || actual.ModuleType != m.ModuleType || !sameJson(t, string(actualData), string(expectedData)) ||
			strings.Join(actual.Keys, ",") != strings.Join(m.Keys, ",") || !sameDelete {
			t.Fatalf("module\nexpected %+v\nactual   %+v", m, actual)
		}
	}
}
