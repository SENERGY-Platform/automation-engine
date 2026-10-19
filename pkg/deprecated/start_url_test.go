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
	"net/http"
	"net/url"
	"testing"
)

// A start URL reads each value as JSON if it parses: a quoted number stays text, a bare one
// becomes a number, true a boolean, and text with spaces stays text.
func TestStartUrlKeepsTheJsonTypeOfEachValue(t *testing.T) {
	e := newEnv(t)
	d := e.deploy(waitingProcess())
	query := url.Values{"business_key": {"typed"}, "quoted": {`"30"`}, "number": {"30"}, "debug": {"true"}, "text": {"foo bar batz"}}
	if status, _ := e.call(alice, http.MethodGet, "/engine/v2/deployments/"+d.Id+"/start?"+query.Encode(), nil, nil); status != http.StatusOK {
		t.Fatal(status)
	}
	expected := map[string]any{"quoted": "30", "number": 30.0, "debug": true, "text": "foo bar batz"}
	variables := e.instance("typed").StartVariables
	for name, value := range expected {
		if variables[name] != value {
			t.Errorf("%v: expected %#v, got %#v", name, value, variables[name])
		}
	}
}

// Setting a variable through the old route keeps the JSON type of the body: true is a boolean,
// "true" is text.
func TestSettingAVariableThroughTheOldRouteKeepsItsJsonType(t *testing.T) {
	for name, value := range map[string]any{"boolean": true, "text": "true"} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			d := e.deploy(waitingProcess())
			if status, _ := e.call(alice, http.MethodGet, "/engine/v2/deployments/"+d.Id+"/start?business_key=edit", nil, nil); status != http.StatusOK {
				t.Fatal(status)
			}
			if status, _ := e.call(alice, http.MethodPut, "/engine/v2/process-instances/edit/variables/debug", value, nil); status != http.StatusOK {
				t.Fatal(status)
			}
			variables, err := e.srv.Controller.GetVariables(ctx, alice, "processes", "edit")
			if err != nil {
				t.Fatal(err)
			}
			var got any
			for _, v := range variables {
				if v.Name == "debug" {
					got = v.Value
				}
			}
			if got != value {
				t.Errorf("expected %#v, got %#v", value, got)
			}
		})
	}
}
