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

package main

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/models/go/models"
)

func TestEventsCoverEventServicesOfEveryDevice(t *testing.T) {
	reading := models.Content{ContentVariable: models.ContentVariable{Name: "reading", Type: models.Structure, SubContentVariables: []models.ContentVariable{
		{Name: "temperature", Type: models.Float},
		{Name: "unit", Type: models.String, Value: "°C"},
	}}}
	deviceType := models.DeviceType{Id: "dt", Services: []models.Service{
		{Id: "urn:infai:ses:service:event", Interaction: models.EVENT, Outputs: []models.Content{reading}},
		{Id: "urn:infai:ses:service:both", Interaction: models.EVENT_AND_REQUEST, Outputs: []models.Content{reading}},
		{Id: "urn:infai:ses:service:request", Interaction: models.REQUEST, Outputs: []models.Content{reading}},
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /token/admin", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("roles") != "admin" {
			t.Errorf("devicesim asks for an admin token, got %v", r.URL.RawQuery)
		}
		w.Write([]byte("Bearer admin\n"))
	})
	mux.HandleFunc("GET /devices", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer admin" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode([]models.Device{{Id: "d1", DeviceTypeId: "dt"}, {Id: "d2", DeviceTypeId: "dt"}})
	})
	mux.HandleFunc("GET /device-types/{id}", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(deviceType)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	messages, err := eventSource{repository: server.URL, auth: server.URL}.messages(context.Background(), 23.5)
	if err != nil {
		t.Fatal(err)
	}
	topics := map[string]int{}
	for _, m := range messages {
		topics[m.Topic]++
		var e events.DeviceMessage
		if err := json.Unmarshal(m.Value, &e); err != nil {
			t.Fatal(err)
		}
		if string(m.Key) != e.DeviceId {
			t.Errorf("the key is the device: %v %v", string(m.Key), e.DeviceId)
		}
		value, _ := e.Value["reading"].(map[string]any)
		if value["temperature"] != 23.5 || value["unit"] != "°C" {
			t.Errorf("the value follows the output with the current number: %+v", e.Value)
		}
	}
	expected := map[string]int{"urn_infai_ses_service_event": 2, "urn_infai_ses_service_both": 2}
	if !maps.Equal(topics, expected) {
		t.Fatalf("one event per device and event service on the topic of the service, got %v", topics)
	}
}
