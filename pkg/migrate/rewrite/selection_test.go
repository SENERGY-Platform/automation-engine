/*
 * Copyright (c) 2026 InfAI (CC SES)
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

package rewrite

import (
	"encoding/json"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/script"
)

// The smart service designer selects the output of an analytics operator for an event of a
// process_deployment task with the pipeline id of an earlier analytics task as expression inside
// JSON text; the rewritten mapping yields the JSON the module reads (SNRGY-5113).
func TestSelectionWithAnExpressionInJsonText(t *testing.T) {
	js, err := toScript(`{"pipeline_id":"${Task_1_pipeline_id}","operator_id":"o1"}`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := script.NewSandbox().Run(js, map[string]any{"Task_1_pipeline_id": "p1"})
	if err != nil {
		t.Fatalf("%v: %v", js, err)
	}
	text, _ := result.Value.(string)
	var selection map[string]string
	if err = json.Unmarshal([]byte(text), &selection); err != nil || selection["pipeline_id"] != "p1" || selection["operator_id"] != "o1" {
		t.Errorf("%v: %#v %v", js, result.Value, err)
	}
}
