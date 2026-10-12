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

package devicecommand

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/models/go/models"
)

// The aspect tests are those of external-task-worker v0.1.2 lib/messages.

func TestCommandGetAspects(t *testing.T) {
	cases := []struct {
		name     string
		command  Command
		expected []models.AspectNode
	}{
		{"reads the deprecated single aspect as a list with one element", Command{Aspect: &models.AspectNode{Id: "air"}}, []models.AspectNode{{Id: "air"}}},
		{"returns the aspect list of a command that carries one", Command{Aspects: []models.AspectNode{{Id: "air"}, {Id: "humidity"}}}, []models.AspectNode{{Id: "air"}, {Id: "humidity"}}},
		{"adds a deprecated aspect the list does not name", Command{Aspect: &models.AspectNode{Id: "humidity"}, Aspects: []models.AspectNode{{Id: "air"}}}, []models.AspectNode{{Id: "air"}, {Id: "humidity"}}},
		{"does not repeat a deprecated aspect the list already names", Command{Aspect: &models.AspectNode{Id: "air"}, Aspects: []models.AspectNode{{Id: "air"}}}, []models.AspectNode{{Id: "air"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if actual := c.command.GetAspects(); !reflect.DeepEqual(actual, c.expected) {
				t.Error(actual)
			}
		})
	}
	t.Run("returns no aspect for a command without one", func(t *testing.T) {
		if actual := (Command{}).GetAspects(); len(actual) != 0 {
			t.Error(actual)
		}
	})
}

func TestCommandSetAspectsFoldsTheAspectOfOldPayloads(t *testing.T) {
	for payload, expected := range map[string][]models.AspectNode{
		`{"aspect":{"id":"air"}}`:                      {{Id: "air"}},
		`{"aspects":[{"id":"air"},{"id":"humidity"}]}`: {{Id: "air"}, {Id: "humidity"}},
	} {
		command := Command{}
		if err := json.Unmarshal([]byte(payload), &command); err != nil {
			t.Fatal(err)
		}
		command.SetAspects()
		if !reflect.DeepEqual(command.Aspects, expected) {
			t.Errorf("%v: %v", payload, command.Aspects)
		}
	}
}

func TestMetadataOutputAspectNodes(t *testing.T) {
	t.Run("sets the list and the deprecated node with the alphabetically first id", func(t *testing.T) {
		metadata := Metadata{}
		metadata.SetOutputAspectNodes([]models.AspectNode{{Id: "humidity"}, {Id: "air"}})
		if !reflect.DeepEqual(metadata.OutputAspectNodes, []models.AspectNode{{Id: "humidity"}, {Id: "air"}}) {
			t.Error(metadata.OutputAspectNodes)
		}
		if metadata.OutputAspectNode == nil || metadata.OutputAspectNode.Id != "air" {
			t.Error(metadata.OutputAspectNode)
		}
	})
	t.Run("reports no aspect without one", func(t *testing.T) {
		metadata := Metadata{}
		metadata.SetOutputAspectNodes(nil)
		if metadata.OutputAspectNode != nil || len(metadata.GetOutputAspectNodes()) != 0 {
			t.Error(metadata)
		}
	})
	t.Run("reads a response that only carries the deprecated node", func(t *testing.T) {
		msg := ProtocolMsg{}
		if err := json.Unmarshal([]byte(`{"metadata":{"output_aspect_node":{"id":"air"}}}`), &msg); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(msg.Metadata.GetOutputAspectNodes(), []models.AspectNode{{Id: "air"}}) {
			t.Error(msg.Metadata.GetOutputAspectNodes())
		}
	})
}

func TestTimescaleAsksForTheLastRowPerServiceAndAnswersInRequestOrder(t *testing.T) {
	var query []map[string]any
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &query)
		authorization = r.Header.Get("Authorization")
		if r.URL.Path != "/queries" {
			http.Error(w, "unknown path", http.StatusNotFound)
			return
		}
		// d1/s1 has a row, d2/s1 none
		_, _ = w.Write([]byte(`[[["2026-10-12T10:00:00Z", 21.5, "on"]], []]`))
	}))
	defer server.Close()
	d1, d2, s1 := models.Device{Id: "d1"}, models.Device{Id: "d2"}, models.Service{Id: "s1"}
	result, err := NewTimescale(server.URL).Query("Bearer t", []TimescaleRequest{
		{Device: d1, Service: s1, ColumnName: "value.temperature"},
		{Device: d2, Service: s1, ColumnName: "value.temperature"},
		{Device: d1, Service: s1, ColumnName: "value.state"},
	}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	values := []any{}
	for _, r := range result {
		values = append(values, r.Value)
	}
	if !reflect.DeepEqual(values, []any{21.5, nil, "on"}) {
		t.Errorf("values in the order of the request, nil without data: %v", values)
	}
	expected := []map[string]any{
		{"DeviceId": "d1", "ServiceId": "s1", "Limit": float64(1), "Columns": []any{map[string]any{"Name": "value.temperature"}, map[string]any{"Name": "value.state"}}},
		{"DeviceId": "d2", "ServiceId": "s1", "Limit": float64(1), "Columns": []any{map[string]any{"Name": "value.temperature"}}},
	}
	if !reflect.DeepEqual(query, expected) || authorization != "Bearer t" {
		t.Errorf("one query per device and service with the token: %v %v", query, authorization)
	}
}

func TestTimescaleReportsAShortAnswerInsteadOfPanicking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	_, err := NewTimescale(server.URL).Query("Bearer t", []TimescaleRequest{{Device: models.Device{Id: "d1"}, Service: models.Service{Id: "s1"}, ColumnName: "v"}}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "fewer queries") {
		t.Errorf("%v", err)
	}
}
