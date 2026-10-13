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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// selectables answers like device-selection (field names of models.Selectable): a device with two services, one of them with two paths, a group and an import
const selectables = `[
 {"device":{"id":"d1","name":"lamp","display_name":"Kitchen Lamp"},"services":[{"id":"s1","name":"brightness"},{"id":"s2","name":"color"}],
  "servicePathOptions":{"s1":[{"path":"value.a","characteristicId":"c1"},{"path":"value.b","characteristicId":"c1"}],"s2":[{"path":"value.c"}]}},
 {"device_group":{"id":"g1","name":"all lamps"}},
 {"import":{"id":"i1","name":"weather","import_type_id":"t1"},"servicePathOptions":{"t1":[{"path":"temperature","characteristicId":"c2"}]}}
]`

func TestIotOptionsKeepTheFormatOfReleases(t *testing.T) {
	var query, body, token string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		query, body, token = r.URL.RawQuery, string(raw), r.Header.Get("Authorization")
		_, _ = w.Write([]byte(selectables))
	}))
	defer server.Close()
	start := &bpmn.Element{Id: "start", FormFields: []bpmn.FormField{
		{Id: "devices", Label: "Devices", Type: "string", Properties: map[string]string{"iot": "device, group,import", "multiple": "true", "order": "2",
			"criteria_list": `[{"function_id":"f","aspect_id":"a1","aspect_ids":["a1","a2"]}]`}},
		{Id: "mode", Label: "Mode", Type: "string", DefaultValue: "eco", Properties: map[string]string{"options": `{"Comfort":"comfort","Eco":"eco"}`, "order": "1"}},
		{Id: "all", Type: "string", Properties: map[string]string{"iot": "group", "multiple": "true", "auto_select_all": "true"}},
		{Id: "free", Label: "Free", Type: "long", DefaultValue: "3", Properties: map[string]string{"order": "3", "description": "a number"}},
	}}
	described, err := NewDescriber(server.URL, nil).Describe(context.Background(), "Bearer user", start)
	if err != nil {
		t.Fatal(err)
	}
	if len(described) != 3 || described[0].Id != "mode" || described[1].Id != "devices" || described[2].Id != "free" {
		t.Fatalf("parameters by order, auto_select_all ones left out: %+v", described)
	}
	if !strings.Contains(query, "include_devices=true") || !strings.Contains(query, "include_groups=true") || !strings.Contains(query, "import_path_trim_first_element=true") || token != "Bearer user" {
		t.Fatalf("device-selection query %v with token %v", query, token)
	}
	if body != `[{"function_id":"f","aspect_id":"a1","aspect_ids":["a1","a2"]}]` {
		t.Fatalf("the criteria reach device-selection unchanged: %v", body)
	}
	if mode := described[0]; mode.DefaultValue != "eco" || len(mode.Options) != 2 || mode.Options[0].Label != "Comfort" || mode.Type != "https://schema.org/Text" {
		t.Fatalf("static options sorted by label, default kept: %+v", mode)
	}
	want := map[string]string{
		"Kitchen Lamp / brightness / value.a": `{"device_selection":{"device_id":"d1","service_id":"s1","path":"value.a","characteristic_id":"c1"},"label":"Kitchen Lamp / brightness / value.a"}`,
		"Kitchen Lamp / brightness / value.b": `{"device_selection":{"device_id":"d1","service_id":"s1","path":"value.b","characteristic_id":"c1"},"label":"Kitchen Lamp / brightness / value.b"}`,
		"Kitchen Lamp / color":                `{"device_selection":{"device_id":"d1","service_id":"s2","path":"value.c"},"label":"Kitchen Lamp / color"}`,
		"all lamps":                           `{"device_group_selection":{"id":"g1"},"label":"all lamps"}`,
		"weather":                             `{"import_selection":{"id":"i1","path":"temperature","characteristic_id":"c2"},"label":"weather"}`,
	}
	devices := described[1]
	if len(devices.Options) != len(want) || devices.HasNoValidOption || !devices.Multiple {
		t.Fatalf("options %+v", devices)
	}
	for _, option := range devices.Options {
		if want[option.Label] != option.Value {
			t.Errorf("option %v: %v, want %v", option.Label, option.Value, want[option.Label])
		}
	}
	if free := described[2]; free.Options != nil || free.DefaultValue != 3.0 || free.Type != "https://schema.org/Integer" || free.Description != "a number" {
		t.Fatalf("a free parameter: %+v", free)
	}

	auto, err := NewDescriber(server.URL, nil).AutoSelected(context.Background(), "Bearer user", start)
	if err != nil || len(auto["all"].([]any)) != 5 {
		t.Fatalf("auto_select_all takes every option: %v %v", auto, err)
	}
}

func TestIotParameterWithoutOptionsIsReported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`[]`)) }))
	defer server.Close()
	start := &bpmn.Element{Id: "start", FormFields: []bpmn.FormField{
		{Id: "required", Type: "string", Properties: map[string]string{"iot": "device"}},
		{Id: "optional", Type: "string", Properties: map[string]string{"iot": "device", "optional": "true"}},
	}}
	described, err := NewDescriber(server.URL, nil).Describe(context.Background(), "Bearer user", start)
	if err != nil {
		t.Fatal(err)
	}
	if !described[0].HasNoValidOption || described[1].HasNoValidOption || described[0].Options == nil {
		t.Fatalf("a required IoT parameter without options is reported, its options are an empty list: %+v", described)
	}
	if _, err = NewDescriber(server.URL, nil).Describe(context.Background(), "", &bpmn.Element{FormFields: []bpmn.FormField{{Id: "x", Properties: map[string]string{"auto_select_all": "true"}}}}); model.AsError(err).Kind != model.KindValidation {
		t.Fatalf("auto_select_all needs multiple: %v", err)
	}
}
