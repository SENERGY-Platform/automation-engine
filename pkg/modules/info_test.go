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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	devicemodel "github.com/SENERGY-Platform/device-repository/v2/lib/model"
	"github.com/SENERGY-Platform/models/go/models"
)

// The cases in testdata/info are the test cases of smart-service-module-worker-info v0.2.1:
// the variables of the Camunda task and the variables and modules the worker stored in
// smart-service-repository.
func TestInfoStoresTheModulesOfTheWorker(t *testing.T) {
	cases, err := os.ReadDir("testdata/info")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range cases {
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("testdata/info", name)
			var tasks []struct {
				Variables map[string]struct{ Value any } `json:"variables"`
			}
			readJson(t, dir, "camunda_tasks.json", &tasks)
			var repository []analyticsRequest
			readJson(t, dir, "expected_smart_service_repo_requests.json", &repository)
			var existing []model.Module
			readJson(t, dir, "module_list_response.json", &existing)

			// the configuration of the worker tests
			srv := newServer(t, func(c *configuration.Config) { c.InfoAdditionalModuleDataFields = true })
			c := srv.Controller
			inst, _ := runWorkerTask(t, srv, "info", tasks[0].Variables, existing, "")

			// the job of the task is the Camunda task, a new module is named after it
			jobId := ""
			modules, _ := c.ListModules(ctx, alice, "smart-services", model.ModuleListOptions{InstanceId: inst.Id})
			for _, m := range modules.Items {
				if rest, ok := strings.CutPrefix(m.Id, inst.Id+"."); ok && !strings.Contains(rest, ".") {
					jobId = rest
				}
			}
			replace := strings.NewReplacer("process-instance-1", inst.Id, ".task1", "."+jobId)
			sameModules(t, c, inst.Id, repository, existing, replace, "")

			variables, err := c.GetVariables(ctx, alice, "smart-services", inst.Id)
			if err != nil {
				t.Fatal(err)
			}
			values := map[string]any{}
			for _, v := range variables {
				values[v.Name] = v.Value
			}
			for _, request := range repository {
				if request.Method != http.MethodPut || !strings.HasSuffix(request.Endpoint, "/variables-map") {
					continue
				}
				var written map[string]any
				if err = json.Unmarshal([]byte(request.Message), &written); err != nil {
					t.Fatal(err)
				}
				for key, value := range written {
					expected, _ := json.Marshal(value)
					actual, _ := json.Marshal(values[key])
					if !sameJson(t, string(actual), string(expected)) {
						t.Fatalf("variable %v: expected %s, actual %s", key, expected, actual)
					}
				}
			}
		})
	}
}

const (
	testFunctionId = models.URN_PREFIX + "measuring-function:getTemperature"
	testAirAspect  = models.URN_PREFIX + "aspect:air"
	testRoomAspect = models.URN_PREFIX + "aspect:room"
)

// aspectIdConstants gives the scripts the ids as variables
const aspectIdConstants = `
	var fid = "` + testFunctionId + `";
	var air = "` + testAirAspect + `";
	var room = "` + testRoomAspect + `";`

// selectablesRepository answers device type selectables and remembers the criteria
type selectablesRepository struct {
	mux         sync.Mutex
	criteria    [][]devicemodel.FilterCriteria
	selectables []devicemodel.DeviceTypeSelectable
}

func (this *selectablesRepository) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/v2/query/device-type-selectables" {
		http.Error(w, "unknown path "+r.URL.Path, http.StatusNotFound)
		return
	}
	criteria := []devicemodel.FilterCriteria{}
	if err := json.NewDecoder(r.Body).Decode(&criteria); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	this.mux.Lock()
	this.criteria = append(this.criteria, criteria)
	this.mux.Unlock()
	_ = json.NewEncoder(w).Encode(this.selectables)
}

// runAspectScript runs an info task whose prescript is the script and whose module data is the
// variable "aspects" the script may write, as the tests of the info worker did
func runAspectScript(t *testing.T, repository *selectablesRepository, script string) model.Module {
	t.Helper()
	fake := httptest.NewServer(repository)
	t.Cleanup(fake.Close)
	srv := newServer(t, func(c *configuration.Config) { c.DeviceRepositoryUrl = fake.URL })
	inst, _ := runWorkerTask(t, srv, "info", map[string]struct{ Value any }{
		"prescript":        {Value: script},
		"info.module_data": {Value: "{{.aspects}}"},
	}, nil, "")
	modules, err := srv.Controller.ListModules(ctx, alice, "smart-services", model.ModuleListOptions{InstanceId: inst.Id})
	if err != nil || len(modules.Items) != 1 {
		t.Fatalf("one module: %+v %v", modules, err)
	}
	return modules.Items[0]
}

// Both spellings of aspects in filter criteria reach device-repository as the prescript wrote
// them, because device-repository folds the alias itself.
func TestPrescriptSendsAspectIdsToDeviceRepository(t *testing.T) {
	tests := []struct {
		name     string
		criteria string
		expected []devicemodel.FilterCriteria
	}{
		{"an aspect list", `{function_id: fid, aspect_ids: [air, room]}`, []devicemodel.FilterCriteria{{FunctionId: testFunctionId, AspectIds: []string{testAirAspect, testRoomAspect}}}},
		{"an aspect list with one element", `{function_id: fid, aspect_ids: [air]}`, []devicemodel.FilterCriteria{{FunctionId: testFunctionId, AspectIds: []string{testAirAspect}}}},
		{"the deprecated single aspect id", `{function_id: fid, aspect_id: air}`, []devicemodel.FilterCriteria{{FunctionId: testFunctionId, AspectId: testAirAspect}}},
		{"both spellings side by side", `{function_id: fid, aspect_id: air, aspect_ids: [room]}`, []devicemodel.FilterCriteria{{FunctionId: testFunctionId, AspectId: testAirAspect, AspectIds: []string{testRoomAspect}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &selectablesRepository{selectables: []devicemodel.DeviceTypeSelectable{}}
			runAspectScript(t, repository, aspectIdConstants+`
				deviceRepo.getDeviceTypeSelectables([`+test.criteria+`], "", true, true);
				variables.write("aspects", "{}");`)
			repository.mux.Lock()
			defer repository.mux.Unlock()
			if expected := [][]devicemodel.FilterCriteria{test.expected}; !reflect.DeepEqual(repository.criteria, expected) {
				t.Errorf("device-repository was asked for %#v, not %#v", repository.criteria, expected)
			}
		})
	}
}

// A path option names every matched aspect in aspect_nodes and the alphabetically first in the
// deprecated aspect_node; a prescript reads both.
func TestPrescriptReadsAspectNodesIntoModuleData(t *testing.T) {
	repository := &selectablesRepository{selectables: []devicemodel.DeviceTypeSelectable{{
		DeviceTypeId: "dt1",
		Services:     []models.Service{{Id: "service1"}},
		ServicePathOptions: map[string][]models.ServicePathOption{
			"service1": {{
				ServiceId:   "service1",
				Path:        "value.temperature",
				AspectNode:  models.AspectNode{Id: testAirAspect},
				AspectNodes: []models.AspectNode{{Id: testAirAspect}, {Id: testRoomAspect}},
			}},
		},
	}}}
	module := runAspectScript(t, repository, aspectIdConstants+`
		var selectables = deviceRepo.getDeviceTypeSelectables([{function_id: fid, aspect_ids: [air, room]}], "", true, true);
		var option = selectables[0].service_path_options["service1"][0];
		variables.write("aspects", JSON.stringify({
			"aspect_nodes": option.aspect_nodes.map(function(node) { return node.id; }),
			"aspect_node": option.aspect_node.id
		}));`)
	expected := map[string]any{"aspect_nodes": []any{testAirAspect, testRoomAspect}, "aspect_node": testAirAspect}
	if !reflect.DeepEqual(module.Data, expected) {
		t.Errorf("%#v", module.Data)
	}
}
