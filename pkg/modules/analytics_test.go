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
	"reflect"
	"strings"
	"sync"
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// The cases in testdata/analytics are the test cases of smart-service-module-worker-analytics
// v0.2.1: the inputs of the Camunda task, the answers of flow-parser and device-repository and
// the requests the worker sent to flow-engine and the module it stored. The engine has to send
// the same requests and store the same module.

const analyticsPipelineId = "1e138d25-d5ee-4a89-9a83-630f4308941a"

type analyticsRequest struct {
	Method   string `json:"method"`
	Endpoint string `json:"endpoint"`
	Message  string `json:"message"`
}

// analyticsServices fakes flow-parser, flow-engine, import-deploy and device-repository like the
// mocks of the worker tests
type analyticsServices struct {
	t            *testing.T
	dir          string
	mux          sync.Mutex
	pipelines    []analyticsRequest
	selectables  int
	repository   map[string][]map[string]any
	cells        json.RawMessage
	selectables1 json.RawMessage
	selectables2 json.RawMessage
}

func (this *analyticsServices) read(name string, target any) bool {
	raw, err := os.ReadFile(filepath.Join(this.dir, name))
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		this.t.Fatal(err)
	}
	if err = json.Unmarshal(raw, target); err != nil {
		this.t.Fatalf("%v: %v", name, err)
	}
	return true
}

func (this *analyticsServices) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	this.mux.Lock()
	defer this.mux.Unlock()
	path := r.URL.Path
	switch {
	case path == "/pipeline" && (r.Method == http.MethodPost || r.Method == http.MethodPut):
		this.pipelines = append(this.pipelines, analyticsRequest{Method: r.Method, Endpoint: path, Message: string(body)})
		var request struct{ Name, Description string }
		_ = json.Unmarshal(body, &request)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": analyticsPipelineId, "name": request.Name, "description": request.Description})
	case path == "/pipeline/"+analyticsPipelineId && r.Method == http.MethodGet:
		// the health check of the module may run while the test does
		_ = json.NewEncoder(w).Encode(map[string]any{"id": analyticsPipelineId, "running": true})
	case strings.HasPrefix(path, "/flow/getinputs/"):
		_, _ = w.Write(this.cells)
	case path == "/v2/query/device-type-selectables" && r.Method == http.MethodPost:
		if this.selectables > 0 && this.selectables2 != nil {
			_, _ = w.Write(this.selectables2)
		} else {
			_, _ = w.Write(this.selectables1)
		}
		this.selectables++
	case strings.HasPrefix(path, "/instances/") && r.Method == http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]any{"kafka_topic": strings.TrimPrefix(path, "/instances/") + "_topic"})
	default:
		// device groups and devices as the legacy permission responses of the worker tests
		kind, id, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
		for _, element := range this.repository[kind] {
			if id == "" {
				_ = json.NewEncoder(w).Encode(this.repository[kind])
				return
			}
			if element["id"] == id {
				_ = json.NewEncoder(w).Encode(element)
				return
			}
		}
		http.Error(w, "unknown path "+path, http.StatusInternalServerError)
	}
}

func (this *analyticsServices) requests() []analyticsRequest {
	this.mux.Lock()
	defer this.mux.Unlock()
	return append([]analyticsRequest{}, this.pipelines...)
}

// analyticsTask is a task with the analytics variables of a Camunda task
func analyticsTask(id string, variables map[string]struct{ Value any }) string {
	own := map[string]struct{ Value any }{}
	for name, variable := range variables {
		if strings.HasPrefix(name, "analytics.") {
			own[name] = variable
		}
	}
	return b.Service(id, "analytics", workerVariables(own)...)
}

func sameJson(t *testing.T, a string, b string) bool {
	var left, right any
	if err := json.Unmarshal([]byte(a), &left); err != nil {
		t.Fatalf("%v: %v", a, err)
	}
	if err := json.Unmarshal([]byte(b), &right); err != nil {
		t.Fatalf("%v: %v", b, err)
	}
	return reflect.DeepEqual(left, right)
}

func TestAnalyticsSendsThePipelineRequestsOfTheWorker(t *testing.T) {
	cases, err := os.ReadDir("testdata/analytics")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range cases {
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			services := &analyticsServices{t: t, dir: filepath.Join("testdata/analytics", name)}
			services.read("flow_model_cells.json", &services.cells)
			services.read("device_type_selectables.json", &services.selectables1)
			services.read("device_type_selectables_2.json", &services.selectables2)
			services.read("permissions_query_responses.json", &services.repository)
			var tasks []struct {
				Variables map[string]struct{ Value any } `json:"variables"`
			}
			services.read("camunda_tasks.json", &tasks)
			var expected, repository []analyticsRequest
			services.read("expected_engine_requests.json", &expected)
			services.read("expected_smart_service_repo_requests.json", &repository)

			fake := httptest.NewServer(services)
			t.Cleanup(fake.Close)
			srv := newServer(t, func(c *configuration.Config) {
				c.FlowEngineUrl, c.FlowParserUrl, c.DeviceRepositoryUrl, c.ImportDeployUrl = fake.URL, fake.URL, fake.URL, fake.URL
			})

			// the worker found the module of the key already; here a first task creates it
			update := name == "group-update"
			parts := []string{b.Start("start"), analyticsTask("task1", tasks[0].Variables), b.End("end")}
			if update {
				parts = append(parts, analyticsTask("task0", tasks[0].Variables), b.Flow("start", "task0"), b.Flow("task0", "task1"))
			} else {
				parts = append(parts, b.Flow("start", "task1"))
			}
			parts = append(parts, b.Flow("task1", "end"))
			c := srv.Controller
			d, err := c.CreateDeployment(ctx, alice, "smart-services", model.DeploymentInput{Xml: b.Process(parts...)})
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

			actual := services.requests()
			if update {
				if len(actual) == 0 || actual[0].Method != http.MethodPost {
					t.Fatalf("the first task creates the pipeline: %+v", actual)
				}
				actual = actual[1:]
			}
			if len(actual) != len(expected) {
				t.Fatalf("flow-engine requests\nexpected %+v\nactual   %+v", expected, actual)
			}
			for i := range expected {
				if actual[i].Method != expected[i].Method || actual[i].Endpoint != expected[i].Endpoint || !sameJson(t, actual[i].Message, expected[i].Message) {
					t.Fatalf("flow-engine request %v\nexpected %+v\nactual   %+v", i, expected[i], actual[i])
				}
			}

			var stored struct {
				ModuleType string         `json:"module_type"`
				ModuleData map[string]any `json:"module_data"`
				Keys       []string       `json:"keys"`
				DeleteInfo struct {
					Url string `json:"url"`
				} `json:"delete_info"`
			}
			if err = json.Unmarshal([]byte(repository[len(repository)-1].Message), &stored); err != nil {
				t.Fatal(err)
			}
			modules, err := c.ListModules(ctx, alice, "smart-services", model.ModuleListOptions{InstanceId: inst.Id})
			if err != nil || modules.Total != 1 {
				t.Fatalf("one module: %+v %v", modules, err)
			}
			module := modules.Items[0]
			if module.ModuleType != stored.ModuleType || !reflect.DeepEqual(module.Data, stored.ModuleData) || len(module.Keys) != len(stored.Keys) ||
				(len(stored.Keys) > 0 && !reflect.DeepEqual(module.Keys, stored.Keys)) {
				t.Fatalf("module\nexpected %+v\nactual   %+v", stored, module)
			}
			if module.DeleteInfo == nil || module.DeleteInfo.Url != fake.URL+"/pipeline/"+analyticsPipelineId || !strings.HasSuffix(stored.DeleteInfo.Url, "/pipeline/"+analyticsPipelineId) {
				t.Fatalf("delete info: %+v", module.DeleteInfo)
			}
		})
	}
}
