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

package deprecated_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"testing"
)

// smart-service-repository answered with options [] for an IoT parameter without any device
// option, and with the default value "" a form field declares with defaultValue="".
func TestReleaseParametersOfAnOptionalIotParameterWithoutOptions(t *testing.T) {
	sameReleaseParameters(t, "no_device_option.bpmn", "[]", "expected_no_device_option_params.json")
}

// extendedParameters reads parsed_info.parameter_descriptions of the extended release of a
// design, as raw JSON objects, so that fields the engine does not know show as missing
func extendedParameters(t *testing.T, e *env, xml string) []map[string]any {
	t.Helper()
	release := e.createRelease(alice, e.createDesign(alice, "Parameters", xml).Id, "release name")
	var extended struct {
		ParsedInfo struct {
			ParameterDescriptions []map[string]any `json:"parameter_descriptions"`
		} `json:"parsed_info"`
	}
	if status, _ := e.call(alice, http.MethodGet, "/smart-services/extended-releases/"+url.PathEscape(release.Id), nil, &extended); status != http.StatusOK {
		t.Fatal(status)
	}
	return extended.ParsedInfo.ParameterDescriptions
}

// The device replace dialog of the web-ui reads parsed_info.parameter_descriptions[].iot_description
// of the extended release (type filter, criteria, entity_only), to find the criteria a smart
// service selects device groups with.
func TestExtendedReleaseDescribesIotParameters(t *testing.T) {
	e := newEnv(t)
	parameters := extendedParameters(t, e, fixture(t, "maintenance.bpmn"))
	expected := map[string]any{
		"iot_input":  map[string]any{"type_filter": []any{"device"}, "criteria": []any{}, "entity_only": false},
		"auto_input": map[string]any{"type_filter": []any{"device"}, "criteria": []any{}, "entity_only": true},
	}
	for _, p := range parameters {
		want, ok := expected[p["id"].(string)]
		if !ok {
			continue
		}
		got, _ := p["iot_description"].(map[string]any)
		for key, value := range want.(map[string]any) {
			if !reflect.DeepEqual(got[key], value) {
				raw, _ := json.Marshal(p)
				t.Errorf("parameter %v: iot_description.%v = %v, want %v; parameter %s", p["id"], key, got[key], value, raw)
			}
		}
	}
}

// A criteria property names aspects as the deprecated aspect_id or as aspect_ids; both reach
// the iot_description of the extended release as written.
func TestExtendedReleaseKeepsBothAspectSpellingsOfTheCriteria(t *testing.T) {
	e := newEnv(t)
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:camunda="http://camunda.org/schema/1.0/bpmn" id="Definitions_1" targetNamespace="http://bpmn.io/schema/bpmn">
	<bpmn:process id="criteria_aspect_ids_test" isExecutable="true">
		<bpmn:startEvent id="StartEvent_1"><bpmn:extensionElements><camunda:formData>
			<camunda:formField id="device" label="device" type="string"><camunda:properties>
				<camunda:property id="iot" value="device" />
				<camunda:property id="criteria_list" value="[{&#34;function_id&#34;:&#34;f1&#34;,&#34;aspect_id&#34;:&#34;aid1&#34;},{&#34;function_id&#34;:&#34;f2&#34;,&#34;aspect_ids&#34;:[&#34;aid2&#34;,&#34;aid3&#34;]}]" />
			</camunda:properties></camunda:formField>
		</camunda:formData></bpmn:extensionElements></bpmn:startEvent>
	</bpmn:process>
</bpmn:definitions>`
	parameters := extendedParameters(t, e, xml)
	if len(parameters) != 1 {
		t.Fatalf("parameters %v", parameters)
	}
	iot, _ := parameters[0]["iot_description"].(map[string]any)
	expected := []any{
		map[string]any{"function_id": "f1", "aspect_id": "aid1"},
		map[string]any{"function_id": "f2", "aspect_ids": []any{"aid2", "aid3"}},
	}
	if !reflect.DeepEqual(iot["criteria"], expected) {
		t.Fatalf("criteria %v, want %v", iot["criteria"], expected)
	}
}

// A maintenance procedure of the extended release names the message of its start event.
func TestExtendedReleaseNamesTheMessageOfAMaintenanceProcedure(t *testing.T) {
	e := newEnv(t)
	release := e.createRelease(alice, e.createDesign(alice, "Maintenance", fixture(t, "maintenance.bpmn")).Id, "release name")
	var extended struct {
		ParsedInfo struct {
			MaintenanceProcedures []struct {
				MessageRef string `json:"message_ref"`
			} `json:"maintenance_procedures"`
		} `json:"parsed_info"`
	}
	if status, _ := e.call(alice, http.MethodGet, "/smart-services/extended-releases/"+url.PathEscape(release.Id), nil, &extended); status != http.StatusOK {
		t.Fatal(status)
	}
	if procedures := extended.ParsedInfo.MaintenanceProcedures; len(procedures) != 1 || procedures[0].MessageRef != "Message_0ufoygk" {
		t.Fatalf("procedures %+v, want message_ref Message_0ufoygk", procedures)
	}
}

// smart-service-repository described a form field without defaultValue with default_value null
// in parsed_info, unlike one with defaultValue="".
func TestExtendedReleaseDescribesAFieldWithoutDefaultWithNull(t *testing.T) {
	e := newEnv(t)
	release := e.createRelease(alice, e.createDesign(alice, "Maintenance", fixture(t, "maintenance.bpmn")).Id, "release name")
	var extended struct {
		ParsedInfo struct {
			MaintenanceProcedures []struct {
				ParameterDescriptions []map[string]any `json:"parameter_descriptions"`
			} `json:"maintenance_procedures"`
		} `json:"parsed_info"`
	}
	if status, _ := e.call(alice, http.MethodGet, "/smart-services/extended-releases/"+url.PathEscape(release.Id), nil, &extended); status != http.StatusOK {
		t.Fatal(status)
	}
	procedures := extended.ParsedInfo.MaintenanceProcedures
	if len(procedures) != 1 || len(procedures[0].ParameterDescriptions) != 1 {
		t.Fatalf("procedures %+v", procedures)
	}
	if value, ok := procedures[0].ParameterDescriptions[0]["default_value"]; !ok || value != nil {
		t.Fatalf("pipeline_id declares no default: default_value %#v", value)
	}
}
