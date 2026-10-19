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

package modules

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

// The tests here are those of smart-service-module-worker-watcher: the hashes of its checker and
// the watches its worker stored for the test cases in tests/testcases.

const watcherDeviceSelectionUrl = "http://device-selection-url:8080"

func TestMd5HashOfAnAnswerIsThatOfTheWorker(t *testing.T) {
	for answer, expected := range map[string]string{
		"foobar123": "ae2d699aca20886f6bed96a0425c6168",
		"changed":   "8977dfac2f8e04cb96e66882235f5aba",
	} {
		if actual := hashOf("md5", []byte(answer)); actual != expected {
			t.Errorf("%v: %v", answer, actual)
		}
	}
}

func TestDeviceIdsHashOfAnAnswerIsThatOfTheWorker(t *testing.T) {
	payload, err := os.ReadFile("testdata/watcher/devices.json")
	if err != nil {
		t.Fatal(err)
	}
	if actual := hashOf("deviceids", payload); actual != "1c6b9f0708c3fbc5bb18c70122a0866a" {
		t.Error(actual)
	}
}

// The deviceids hash covers the sorted device ids found in the answer and nothing else.
func TestDeviceIdsHashIsTheHashOfTheSortedDeviceIdsOfTheAnswer(t *testing.T) {
	payload, err := os.ReadFile("testdata/watcher/devices.json")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/watcher/device_ids.json")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	if err = json.Unmarshal(raw, &ids); err != nil {
		t.Fatal(err)
	}
	list, _ := json.Marshal(ids)
	sum := md5.Sum(list)
	if actual, expected := hashOf("deviceids", payload), hex.EncodeToString(sum[:]); actual != expected {
		t.Errorf("the hash is not that of the %v expected ids: %v", len(ids), actual)
	}
}

// saveWatch runs a watcher task with the inputs in an instance of alice and returns the watch it
// stored
func saveWatch(t *testing.T, settings WatcherSettings, inputs map[string]any) (store.Watch, error) {
	t.Helper()
	s := storetest.OpenSQLite(t)
	instance := instanceWith(t, s, "smart-services")
	rt := NewRuntime(s, Settings{DeviceSelectionUrl: watcherDeviceSelectionUrl}, func(user string) (string, error) { return "Bearer token-of-" + user, nil }, logger)
	now := time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC)
	rt.now = func() time.Time { return now }
	rt.AddWatcher(nil, nil, settings)
	_, _, err := rt.watcher.Do(context.Background(), Task{Id: "task1", InstanceId: instance, RootInstanceId: instance, Namespace: "smart-services", Owner: "alice", Inputs: inputs})
	if err != nil {
		return store.Watch{}, err
	}
	var watches []store.Watch
	err = s.Tx(context.Background(), func(tx *store.Tx) (err error) {
		watches, err = tx.ClaimDueWatches(now, 10)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(watches) != 1 {
		t.Fatalf("one watch: %+v", watches)
	}
	return watches[0], nil
}

// inputs of the test case devices-by-criteria with the criteria variable and value
func watcherInputs(variable string, value string) map[string]any {
	return map[string]any{
		"watcher.maintenance_procedure":            "update",
		"watcher.watch_interval":                   "2h",
		"watcher.hash_type":                        "deviceids",
		variable:                                   value,
		"watcher.maintenance_procedure_inputs.foo": "bar",
	}
}

func TestWatchKeepsIntervalHashTypeAndProcedureOfTheTask(t *testing.T) {
	w, err := saveWatch(t, WatcherSettings{DefaultInterval: time.Hour}, watcherInputs("watcher.watch_devices_by_criteria", `[{"function_id":"fid"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if w.Id != "i-smart-services.task1" || w.Owner != "alice" || w.Interval != 2*time.Hour || w.HashType != "deviceids" ||
		w.Procedure != "update" || !reflect.DeepEqual(w.Parameters, map[string]any{"foo": "bar"}) {
		t.Errorf("%+v", w)
	}
}

func TestWatchOfDevicesAsksDeviceSelectionWithTheOwnersToken(t *testing.T) {
	for variable, endpoint := range map[string]string{
		"watcher.watch_devices_by_criteria":          watcherDeviceSelectionUrl + "/v2/query/selectables?include_devices=true",
		"watcher.watch_modified_devices_by_criteria": watcherDeviceSelectionUrl + "/v2/query/selectables?include_devices=true&include_id_modified=true",
	} {
		t.Run(variable, func(t *testing.T) {
			w, err := saveWatch(t, WatcherSettings{}, watcherInputs(variable, `[{"function_id":"fid"}]`))
			if err != nil {
				t.Fatal(err)
			}
			if w.Request.Method != http.MethodPost || w.Request.Endpoint != endpoint || !w.Request.AddAuthToken || w.Request.Generic {
				t.Errorf("%+v", w.Request)
			}
		})
	}
}

// AspectId is the deprecated alias for an AspectIds list with one element; device-selection
// resolves it, so the criteria reach it as written.
func TestWatchOfDevicesForwardsTheCriteriaUnchanged(t *testing.T) {
	for _, variable := range []string{"watcher.watch_devices_by_criteria", "watcher.watch_modified_devices_by_criteria"} {
		for name, criteria := range map[string]string{
			"aspect ids":                      `[{"function_id":"fid","aspect_ids":["aid1","aid2"]}]`,
			"the deprecated aspect id":        `[{"function_id":"fid","aspect_id":"aid"}]`,
			"aspect ids next to aspect id":    `[{"function_id":"fid","aspect_id":"aid","aspect_ids":["aid1"]}]`,
			"criteria with either spelling":   `[{"function_id":"fid","aspect_ids":["aid1","aid2"]},{"function_id":"fid2","aspect_id":"aid3"}]`,
			"criteria without aspects at all": `[{"function_id":"fid"}]`,
		} {
			t.Run(variable+"/"+name, func(t *testing.T) {
				w, err := saveWatch(t, WatcherSettings{}, watcherInputs(variable, criteria))
				if err != nil {
					t.Fatal(err)
				}
				if string(w.Request.Body) != criteria {
					t.Error(string(w.Request.Body))
				}
			})
		}
	}
}

// A generic request is sent as written, under the HTTP policy of the namespace instead of the
// isolation of the worker.
func TestGenericWatchRequestIsKeptAsWritten(t *testing.T) {
	w, err := saveWatch(t, WatcherSettings{AllowGenericRequests: true},
		watcherInputs("watcher.watch_request", `{"method":"POST","endpoint":"/query","body":"eyJmb28iOiJiYXIifQ==","add_auth_token":false,"header":null}`))
	if err != nil {
		t.Fatal(err)
	}
	expected := model.WatchRequest{Method: http.MethodPost, Endpoint: "/query", Body: []byte(`{"foo":"bar"}`), AddAuthToken: false, Generic: true}
	if !reflect.DeepEqual(w.Request, expected) {
		t.Errorf("%+v", w.Request)
	}
}
