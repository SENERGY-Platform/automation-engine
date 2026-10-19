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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/SENERGY-Platform/models/go/models"
)

func hubRepository(t *testing.T, hub models.Hub) string {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hubs/"+hub.Id || r.Header.Get("Authorization") != "token" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(hub)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// A deployment for a gateway asks device-selection only for the local devices of its hub.
func TestPrepareForHubAsksForTheLocalDevicesOfTheHub(t *testing.T) {
	fake := &fakeSelection{}
	repo := hubRepository(t, models.Hub{Id: "hub", DeviceLocalIds: []string{"local-1", "local-2"}})
	if _, err := New(fake, Config{DeviceRepositoryUrl: repo}).PrepareForHub("token", testProcess(t), "hub"); err != nil {
		t.Fatal(err)
	}
	if len(fake.request) == 0 {
		t.Fatal("expected a request to device-selection")
	}
	for _, element := range fake.request {
		if !slices.Equal(element.LocalDevices, []string{"local-1", "local-2"}) {
			t.Errorf("%v: local devices %v", element.Id, element.LocalDevices)
		}
	}
}

// device-selection reads an empty list of local devices as no restriction, so a hub without
// devices is not asked about and offers nothing.
func TestPrepareForAHubWithoutDevicesOffersNothing(t *testing.T) {
	fake := &fakeSelection{}
	repo := hubRepository(t, models.Hub{Id: "hub"})
	elements, err := New(fake, Config{DeviceRepositoryUrl: repo}).PrepareForHub("token", testProcess(t), "hub")
	if err != nil {
		t.Fatal(err)
	}
	if fake.request != nil {
		t.Errorf("device-selection was asked: %+v", fake.request)
	}
	for _, e := range elements {
		if len(e.Options) > 0 {
			t.Errorf("%v offers %+v", e.ElementId, e.Options)
		}
	}
}
