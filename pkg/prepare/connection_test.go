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

package prepare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	devicerepository "github.com/SENERGY-Platform/device-repository/v3/lib/client"
	"github.com/SENERGY-Platform/models/go/models"
)

// connectionRepository answers the hub and the device list of device-repository and records the
// device queries
func connectionRepository(t *testing.T, hub models.Hub, queries *[]string) string {
	devices := []models.Device{{Id: "d2", Name: "Zeta"}, {Id: "d1", Name: "Alpha"}, {Id: "d3", Name: "Mid"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token" {
			http.Error(w, "no token", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/hubs/" + hub.Id:
			_ = json.NewEncoder(w).Encode(hub)
		case "/devices":
			*queries = append(*queries, r.URL.RawQuery)
			result := []models.Device{}
			if ids, ok := r.URL.Query()["ids"]; ok {
				for _, d := range devices {
					if strings.Contains(","+ids[0]+",", ","+d.Id+",") {
						result = append(result, d)
					}
				}
			} else {
				result = devices
			}
			_ = json.NewEncoder(w).Encode(result)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func connectionProcess(t *testing.T) *bpmn.Process {
	xml := b.Process(b.Start("start"), b.Service("conn", "device_connection"), b.End("end"), b.Flow("start", "conn"), b.Flow("conn", "end"))
	p, issues, err := bpmn.ParseAndValidate(xml, bpmn.Rules{})
	if err != nil || len(issues) > 0 {
		t.Fatal(err, issues)
	}
	return p
}

// A task that reads the connection state is a task of the dialog without criteria; its options
// are the devices the owner may read, sorted by name, from device-repository alone (SNRGY-5151).
func TestPrepareOffersTheDevicesToAConnectionTask(t *testing.T) {
	var queries []string
	repo := connectionRepository(t, models.Hub{Id: "hub"}, &queries)
	// without device-selection: the task needs only device-repository
	elements, err := New(nil, Config{DeviceRepository: devicerepository.NewClient(repo, devicerepository.Options{})}).Prepare(context.Background(), "token", connectionProcess(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(elements) != 1 || elements[0].ElementId != "conn" || elements[0].Kind != model.PreparedTask || elements[0].Criteria.FunctionId != "" {
		t.Fatalf("%+v", elements)
	}
	var names []string
	for _, o := range elements[0].Options {
		if o.Device == nil || len(o.Services) > 0 {
			t.Errorf("the device alone: %+v", o)
		}
		names = append(names, o.Device.Name)
	}
	if strings.Join(names, ",") != "Alpha,Mid,Zeta" {
		t.Errorf("options %v", names)
	}
	// without device-repository the task is listed without options
	elements, err = New(nil, Config{}).Prepare(context.Background(), "token", connectionProcess(t))
	if err != nil || len(elements) != 1 || len(elements[0].Options) != 0 {
		t.Errorf("%+v %v", elements, err)
	}
}

// For a gateway only the devices of its hub are offered; a hub without devices offers none and is
// not asked about the devices.
func TestPrepareForHubOffersOnlyTheDevicesOfTheHubToAConnectionTask(t *testing.T) {
	var queries []string
	repo := connectionRepository(t, models.Hub{Id: "hub", DeviceIds: []string{"d1", "d3"}, DeviceLocalIds: []string{"l1", "l3"}}, &queries)
	elements, err := New(nil, Config{DeviceRepository: devicerepository.NewClient(repo, devicerepository.Options{})}).PrepareForHub(context.Background(), "token", connectionProcess(t), "hub")
	if err != nil {
		t.Fatal(err)
	}
	if len(elements) != 1 || len(elements[0].Options) != 2 || elements[0].Options[0].Device.Id != "d1" || elements[0].Options[1].Device.Id != "d3" {
		t.Fatalf("%+v", elements)
	}
	if len(queries) != 1 || !strings.Contains(queries[0], "ids=d1%2Cd3") {
		t.Errorf("the hub devices are asked for by id: %v", queries)
	}

	queries = nil
	repo = connectionRepository(t, models.Hub{Id: "hub"}, &queries)
	elements, err = New(nil, Config{DeviceRepository: devicerepository.NewClient(repo, devicerepository.Options{})}).PrepareForHub(context.Background(), "token", connectionProcess(t), "hub")
	if err != nil || len(elements) != 1 || len(elements[0].Options) != 0 || len(queries) != 0 {
		t.Errorf("a hub without devices offers none: %+v %v %v", elements, err, queries)
	}
}
