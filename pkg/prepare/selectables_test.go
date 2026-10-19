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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
)

// The options of IoT parameters come from the v2 selectables endpoint of device-selection,
// which knows aspect_ids.
func TestOptionsAreAskedAtTheV2SelectablesEndpoint(t *testing.T) {
	var method, path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	start := &bpmn.Element{Id: "start", FormFields: []bpmn.FormField{{Id: "device", Type: "string", Properties: map[string]string{"iot": "device"}}}}
	if _, err := NewDescriber(server.URL, nil).Describe(context.Background(), "Bearer user", start); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost || path != "/v2/query/selectables" {
		t.Fatalf("%v %v", method, path)
	}
}
