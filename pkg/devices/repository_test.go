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

package devices_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/devices"
	"github.com/SENERGY-Platform/models/go/models"
)

func TestSplitModifierSeparatesIdAndParameters(t *testing.T) {
	cases := []struct {
		id       string
		pure     string
		modifier map[string][]string
	}{
		{"urn:infai:ses:device:1", "urn:infai:ses:device:1", nil},
		{"urn:infai:ses:device:1$service_group_selection=sg1", "urn:infai:ses:device:1", map[string][]string{"service_group_selection": {"sg1"}}},
		{"urn:infai:ses:device:1$%zz", "urn:infai:ses:device:1", nil},
	}
	for _, c := range cases {
		pure, modifier := devices.SplitModifier(c.id)
		if pure != c.pure || !reflect.DeepEqual(modifier, c.modifier) {
			t.Errorf("%v: %v %v", c.id, pure, modifier)
		}
	}
}

func TestRepositoryReadsWithTheUserTokenCachesAndFindsServicesInTheDeviceType(t *testing.T) {
	var mux sync.Mutex
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.Lock()
		requests = append(requests, r.URL.Path+" "+r.Header.Get("Authorization"))
		mux.Unlock()
		responses := map[string]any{
			"/devices/d1":       models.Device{Id: "d1", DeviceTypeId: "dt1"},
			"/device-types/dt1": models.DeviceType{Id: "dt1", Services: []models.Service{{Id: "s1", ProtocolId: "p1"}}},
			"/protocols/p1":     models.Protocol{Id: "p1"},
			"/device-groups/g1": models.DeviceGroup{Id: "g1", DeviceIds: []string{"d1"}},
		}
		response, ok := responses[r.URL.Path]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	repo, err := devices.NewRepository(server.URL, func(user string) (string, error) { return "Bearer " + user, nil }, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, err := repo.GetToken("alice")
	if err != nil || token != "Bearer alice" {
		t.Fatalf("%v %v", token, err)
	}
	for range 2 {
		device, err := repo.GetDevice(token, "d1")
		if err != nil || device.DeviceTypeId != "dt1" {
			t.Fatalf("%+v %v", device, err)
		}
		service, err := repo.GetService(token, device, "s1")
		if err != nil || service.ProtocolId != "p1" {
			t.Fatalf("%+v %v", service, err)
		}
		if protocol, err := repo.GetProtocol(token, "p1"); err != nil || protocol.Id != "p1" {
			t.Fatalf("%+v %v", protocol, err)
		}
		if group, err := repo.GetDeviceGroup(token, "g1"); err != nil || len(group.DeviceIds) != 1 {
			t.Fatalf("%+v %v", group, err)
		}
	}
	expected := []string{"/devices/d1 Bearer alice", "/device-types/dt1 Bearer alice", "/protocols/p1 Bearer alice", "/device-groups/g1 Bearer alice"}
	if !reflect.DeepEqual(requests, expected) {
		t.Errorf("each resource is read once: %v", requests)
	}
	if _, err = repo.GetService(token, models.Device{Id: "d1", DeviceTypeId: "dt1"}, "s2"); err == nil || !strings.Contains(err.Error(), "s2") {
		t.Errorf("an unknown service is an error: %v", err)
	}
	if _, err = repo.GetDevice(token, "unknown"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("an answer other than 2xx is an error: %v", err)
	}
}
