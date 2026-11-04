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

package events

import (
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

type valueUnmarshaller struct{}

func (valueUnmarshaller) Unmarshal(desc MessageDesc) (any, error) {
	return desc.Message["value"], nil
}

// A condition that is a boolean literal, as describe makes of an empty one, is decided without a
// script runtime; the evaluator here has none, so evaluating a script would panic.
func TestConstantConditionsNeedNoScript(t *testing.T) {
	e := &Evaluator{unmarshaller: valueUnmarshaller{}}
	for script, expected := range map[string]bool{"true": true, " true\n": true, "false": false} {
		result, err := e.Evaluate(model.EventDescription{Script: script, ValueVariable: "x"}, map[string]any{"value": 42.0})
		if err != nil || result.Triggered != expected || result.Value != 42.0 {
			t.Errorf("%q: %+v %v", script, result, err)
		}
	}
}
