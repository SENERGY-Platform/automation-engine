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
	"fmt"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/script"
)

// empty of JUEL is true for null, an empty text and an empty list or map, false for everything
// else, also for 0 and false; its operand is a value with property and index accesses or a
// parenthesized expression. Lists and maps are not covered by the comparison with Camunda,
// whose variables in the test are typed scalars.
func TestEmptyOperatorEvaluatesLikeJuel(t *testing.T) {
	cases := []struct {
		juel      string
		variables map[string]any
		expected  any
	}{
		{"${empty x}", map[string]any{"x": nil}, true},
		{"${empty x}", map[string]any{"x": ""}, true},
		{"${empty x}", map[string]any{"x": "a"}, false},
		{"${empty x}", map[string]any{"x": []any{}}, true},
		{"${empty x}", map[string]any{"x": []any{1.0}}, false},
		{"${empty x}", map[string]any{"x": map[string]any{}}, true},
		{"${empty x}", map[string]any{"x": map[string]any{"a": 1.0}}, false},
		{"${empty x}", map[string]any{"x": 0.0}, false},
		{"${empty x}", map[string]any{"x": false}, false},
		{"${not empty x}", map[string]any{"x": ""}, false},
		{"${!empty x.y}", map[string]any{"x": map[string]any{"y": "set"}}, true},
		{"${empty x['a'] && b}", map[string]any{"x": map[string]any{"a": nil}, "b": true}, true},
		{"${empty x.list[0].name}", map[string]any{"x": map[string]any{"list": []any{map[string]any{"name": ""}}}}, true},
		{"${empty(x)}", map[string]any{"x": []any{}}, true},
		{"${empty (x || y)}", map[string]any{"x": "", "y": ""}, true},
		{"${empty x ? 'none' : x}", map[string]any{"x": ""}, "none"},
		{"${empty x ? 'none' : x}", map[string]any{"x": "dev-1"}, "dev-1"},
		{"value: ${empty x ? '-' : x}", map[string]any{"x": nil}, "value: -"},
		{"${empty x == false}", map[string]any{"x": "a"}, true},
		{"${x.empty}", map[string]any{"x": map[string]any{"empty": "property"}}, "property"},
	}
	sandbox := script.NewSandbox()
	for _, c := range cases {
		t.Run(fmt.Sprintf("%v %v", c.juel, c.variables), func(t *testing.T) {
			js, err := toScript(c.juel)
			if err != nil {
				t.Fatal(err)
			}
			result, err := sandbox.Run(js, c.variables)
			if err != nil {
				t.Fatalf("%v: %v", js, err)
			}
			if fmt.Sprint(result.Value) != fmt.Sprint(c.expected) {
				t.Errorf("%v: %v, expected %v", js, result.Value, c.expected)
			}
		})
	}
}

func TestEmptyWithAnUnsupportedOperandIsAnError(t *testing.T) {
	for _, juel := range []string{"${empty not x}", "${empty empty x}", "${empty !x}", "${empty}", "${empty x[0}"} {
		if js, err := toScript(juel); err == nil {
			t.Errorf("%v: expected an error, got %v", juel, js)
		}
	}
}
