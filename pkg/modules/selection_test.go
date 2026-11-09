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
	if selection, err = selectionOf(`{"import_selection": {"id": "i1"}}`); err != nil || selection.ImportId != "i1" {
		t.Errorf("the iot option format stays: %+v %v", selection, err)
	}
}
