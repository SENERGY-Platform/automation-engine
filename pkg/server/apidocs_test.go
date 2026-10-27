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

package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/docs"
)

func TestPublishAsyncApiPutsTheDocumentUnderTheIdOfTheEngine(t *testing.T) {
	var method, path string
	var body []byte
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		body, _ = io.ReadAll(r.Body)
	}))
	defer provider.Close()
	if err := PublishAsyncApi(context.Background(), provider.Client(), provider.URL, docs.AsyncApiDoc); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPut || path != "/storage/asyncapi/github_com_SENERGY-Platform_automation-engine" {
		t.Fatalf("got %v %v", method, path)
	}
	// the embedded document is the generated asyncapi.json, which the provider checks for its keys
	var doc struct {
		Asyncapi string `json:"asyncapi"`
		Info     struct {
			Title string `json:"title"`
		} `json:"info"`
	}
	if err := json.Unmarshal(body, &doc); err != nil || doc.Asyncapi == "" || doc.Info.Title != "Automation-Engine" {
		t.Fatalf("not the asyncapi document: %+v %v", doc, err)
	}
}

func TestPublishAsyncApiReportsARefusal(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing required keys", http.StatusBadRequest)
	}))
	defer provider.Close()
	err := PublishAsyncApi(context.Background(), provider.Client(), provider.URL, []byte("{}"))
	if err == nil || !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "missing required keys") {
		t.Fatalf("got %v", err)
	}
}
