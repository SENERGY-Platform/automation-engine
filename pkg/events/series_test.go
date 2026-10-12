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

package events_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/models/go/models"
)

func TestColumnsFollowTheTableworkerPaths(t *testing.T) {
	service := models.Service{Outputs: []models.Content{{ContentVariable: models.ContentVariable{Name: "value", Type: models.Structure, SubContentVariables: []models.ContentVariable{
		{Name: "level", Type: models.Float},
		{Name: "meta", Type: models.Structure, SubContentVariables: []models.ContentVariable{{Name: "unit", Type: models.String}}},
		{Name: "readings", Type: models.List, SubContentVariables: []models.ContentVariable{{Name: "*", Type: models.Float}}},
	}}}}}
	columns := events.Columns(service)
	sort.Strings(columns)
	if want := []string{"value.level", "value.meta.unit", "value.readings"}; !reflect.DeepEqual(columns, want) {
		t.Fatalf("columns %v, want %v", columns, want)
	}
	message := events.MessageOf(map[string]any{"value.level": 3.5, "value.meta.unit": "C", "value.readings": []any{1.0, 2.0}, "value.missing": nil})
	want := map[string]any{"value": map[string]any{"level": 3.5, "meta": map[string]any{"unit": "C"}, "readings": []any{1.0, 2.0}}}
	if !reflect.DeepEqual(message, want) {
		t.Fatalf("message %v, want %v", message, want)
	}
}

func TestTimescaleQueriesARangeAscendingWithTheOwnersToken(t *testing.T) {
	var body []map[string]any
	var token string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token = r.Header.Get("Authorization")
		if r.URL.Path != "/queries" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode([][][]any{{{"2026-10-12T10:00:01Z", 21.5}, {"2026-10-12T10:00:02.5Z", 22.0}}})
	}))
	defer server.Close()
	from, to := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC), time.Date(2026, 10, 12, 11, 0, 0, 0, time.UTC)
	rows, err := events.NewTimescale(server.URL).Range(context.Background(), "Bearer owner", "d1", "s1", []string{"value.level"}, from, to, 50)
	if err != nil {
		t.Fatal(err)
	}
	if token != "Bearer owner" || len(body) != 1 || body[0]["deviceId"] != "d1" || body[0]["serviceId"] != "s1" || body[0]["limit"] != 50.0 || body[0]["orderDirection"] != "asc" {
		t.Fatalf("request %v with token %v", body, token)
	}
	if window := body[0]["time"].(map[string]any); window["start"] != "2026-10-12T10:00:00Z" || window["end"] != "2026-10-12T11:00:00Z" {
		t.Fatalf("time window %v", window)
	}
	if len(rows) != 2 || rows[1].Values["value.level"] != 22.0 || !rows[1].Time.Equal(time.Date(2026, 10, 12, 10, 0, 2, 500000000, time.UTC)) {
		t.Fatalf("rows %+v", rows)
	}
}
