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

package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// api-docs-provider reads GET /doc of every service behind Kong; without it the engine is missing
// from API Docs and the API Playground of the web-ui
func TestDocServesTheOpenApiDocument(t *testing.T) {
	handler := New(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), Settings{})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/doc", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %v", w.Code)
	}
	var doc struct {
		Swagger string `json:"swagger"`
		Info    struct {
			Title string `json:"title"`
		} `json:"info"`
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Swagger != "2.0" || doc.Info.Title != "Automation-Engine API" {
		t.Fatalf("not the document of this API: %+v", doc.Info)
	}
	// the provider takes the routes from the paths; Ladon then decides which of them a user sees
	if _, ok := doc.Paths["/namespaces/{ns}/instances/{id}/live"]; !ok {
		t.Fatalf("paths are missing, got %v of them", len(doc.Paths))
	}
}
