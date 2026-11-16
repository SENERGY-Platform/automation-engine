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

package modules

import (
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// A smart service selects the output of an analytics operator for a catch event with
// {"pipeline_id": ..., "operator_id": ...}, as it gets the pipeline id from the analytics module
// (SNRGY-5113); both are needed.
func TestSelectionOfAnalyticsOperator(t *testing.T) {
	selection, err := selectionOf(`{"pipeline_id": "p1", "operator_id": "o1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if selection != (model.Selection{PipelineId: "p1", OperatorId: "o1"}) {
		t.Fatalf("%+v", selection)
	}
	for _, raw := range []string{`{"pipeline_id": "p1"}`, `{"operator_id": "o1"}`} {
		if _, err = selectionOf(raw); err == nil {
			t.Errorf("%v is rejected", raw)
		}
	}
	// the iot option of smart-service-module-worker-process
	selection, err = selectionOf(`{"generic_event_source": {"filter_type": "operatorId", "filter_ids": "p1:o1", "topic": "analytics-stopping"}}`)
	if err != nil || selection != (model.Selection{PipelineId: "p1", OperatorId: "o1"}) {
		t.Errorf("generic event source: %+v %v", selection, err)
	}
	for _, raw := range []string{
		`{"generic_event_source": {"filter_type": "deviceId", "filter_ids": "p1:o1"}}`,
		`{"generic_event_source": {"filter_type": "operatorId", "filter_ids": "p1"}}`,
		`{"generic_event_source": {"filter_type": "operatorId", "filter_ids": "p1:o1", "path": "analytics.x"}}`,
	} {
		if _, err = selectionOf(raw); err == nil {
			t.Errorf("%v is rejected", raw)
		}
	}
	if selection, err = selectionOf(`{"import_selection": {"id": "i1"}}`); err != nil || selection.ImportId != "i1" {
		t.Errorf("the iot option format stays: %+v %v", selection, err)
	}
}

// A smart service selects the connection state of a device with {"connection_device_id": ...}
// or with the iot option of a form field wrapped as {"device_connection": ...}. The service of
// the option, its path and its characteristic are ignored: a form field whose user picked a
// value of a service still names the device (SNRGY-5151).
func TestSelectionOfDeviceConnection(t *testing.T) {
	want := model.Selection{ConnectionDeviceId: "d1"}
	for name, raw := range map[string]string{
		"flat":                 `{"connection_device_id": "d1"}`,
		"iot option":           `{"device_connection": {"device_selection": {"device_id": "d1"}}}`,
		"iot option, service":  `{"device_connection": {"device_selection": {"device_id": "d1", "service_id": "s1", "characteristic_id": "c"}}}`,
		"iot option, no path":  `{"device_connection": {"device_selection": {"device_id": "d1", "path": ""}}}`,
		"iot option, null set": `{"device_connection": {"device_selection": {"device_id": "d1", "service_id": null, "path": null}}}`,
		"iot option, path":     `{"device_connection": {"device_selection": {"device_id": "d1", "service_id": "s1", "path": "root.value", "characteristic_id": "c"}, "label": "Device 1 / root.value"}}`,
	} {
		selection, err := selectionOf(raw)
		if err != nil || selection != want {
			t.Errorf("%v: %+v %v", name, selection, err)
		}
	}
	for name, raw := range map[string]string{
		"no device":       `{"device_connection": {"device_selection": {"device_id": ""}}}`,
		"empty option":    `{"device_connection": {}}`,
		"group":           `{"device_connection": {"device_group_selection": {"id": "g1"}}}`,
		"empty selection": `{}`,
	} {
		if _, err := selectionOf(raw); err == nil {
			t.Errorf("%v is rejected", name)
		}
	}
	if _, err := selectionOf(`{}`); err == nil || !strings.Contains(err.Error(), "connection_device_id") {
		t.Errorf("the error names the new source: %v", err)
	}
}
