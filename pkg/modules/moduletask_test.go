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
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

// The tests here are those of the middleware of smart-service-module-worker-lib: what a handler
// sees of the inputs and what becomes of the outputs and variable changes of its scripts.

// recordingHandler remembers the task it got and answers with fixed outputs
type recordingHandler struct {
	task    Task
	outputs map[string]any
}

func (this *recordingHandler) Do(_ context.Context, task Task) ([]model.Module, map[string]any, error) {
	this.task = task
	return nil, this.outputs, nil
}

// executeModuleTask runs a task of the handler in an instance with the variables
func executeModuleTask(t *testing.T, handler Handler, variables map[string]any, input map[string]any) engine.TaskResult {
	t.Helper()
	s := storetest.OpenSQLite(t)
	instance := instanceWith(t, s, "smart-services")
	err := s.Tx(context.Background(), func(tx *store.Tx) error {
		for name, value := range variables {
			if err := tx.SetVariable(instance, name, value, "start", time.Now()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rt := NewRuntime(s, Settings{}, func(user string) (string, error) { return "Bearer token-of-" + user, nil }, logger)
	rt.add("recording", handler)
	result, err := rt.types["recording"].Execute(context.Background(), engine.Task{JobId: "task1", InstanceId: instance, RootInstanceId: instance,
		Namespace: "smart-services", DeploymentId: "d-smart-services", ElementId: "task1", TaskType: "recording", Owner: "alice", Input: input})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

var middlewareVariables = map[string]any{
	"v1": "str",
	"v2": float64(42),
	"v3": true,
	"v4": nil,
	"v5": map[string]any{"foo": "bar", "batz": float64(42)},
}

// String inputs are templates over the instance variables; other values stay as they are.
func TestHandlerGetsTheInputsRenderedAgainstTheVariables(t *testing.T) {
	handler := &recordingHandler{}
	executeModuleTask(t, handler, middlewareVariables, map[string]any{
		"templ":     "{{.brl}}placeholder{{.brr}}",
		"str":       "{{.v1}}",
		"number":    "{{.v2}}",
		"bool":      "{{.v3}}",
		"null":      "{{.v4}}",
		"obj":       "{{.v5}}",
		"unknown":   "{{.unknown}}",
		"rawStr":    "raw",
		"rawNumber": 13,
		"rawBool":   true,
		"rawNull":   nil,
	})
	expected := map[string]any{
		"templ":     "{{placeholder}}",
		"str":       "str",
		"number":    "42",
		"bool":      "true",
		"null":      "null",
		"obj":       `{"batz":42,"foo":"bar"}`,
		"unknown":   "",
		"rawStr":    "raw",
		"rawNumber": 13,
		"rawBool":   true,
		"rawNull":   nil,
	}
	if !reflect.DeepEqual(handler.task.Inputs, expected) {
		t.Errorf("\n%#v\n%#v", handler.task.Inputs, expected)
	}
}

// the scripts of TestMiddlewareScripts of the worker library; prescript_1 ends within a word
// that prescript_2 completes
var middlewareScripts = map[string]any{
	"inp1": "42",
	"inp2": 43,
	"prescript_1": `
		variables.write("toBeUpdatesInPre", variables.read("toBeUpdatesInPre") + 1);
		variables.write('added', 'foo');
		outputs.set("overwriting", 1);
		outputs.set("inp`,
	"prescript_2": `ut_v1", inputs.get("inp1"));
		variables.write("toBeUpdatesInPreAndPost", variables.read("toBeUpdatesInPreAndPost") + 1);
		variables.write("addedJson", JSON.stringify("foo"));
		variables.write("long_result", "a long text");
		outputs.set("long_result_output", variables.ref("long_result"));`,
	"postscript": `
		variables.write("toBeUpdatesInPreAndPost", variables.read("toBeUpdatesInPreAndPost") + 1);
		variables.write("toBeUpdatesInPost", variables.read("toBeUpdatesInPost") + 1);
		outputs.setJson("input_list", inputs.list());
		outputs.setJson("input_listNames", inputs.listNames());
		outputs.set("overwritten", 3);
		outputs.set("outputsGet", outputs.get("bar") + "_read");`,
}

func middlewareScriptVariables() map[string]any {
	result := map[string]any{"toBeUpdatesInPre": 1, "toBeUpdatesInPost": 1, "toBeUpdatesInPreAndPost": 1}
	for name, value := range middlewareVariables {
		result[name] = value
	}
	return result
}

// sameJsonValue compares values as the engine stores them, as JSON
func sameJsonValue(t *testing.T, actual any, expected any) bool {
	t.Helper()
	a, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	e, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	var left, right any
	_ = json.Unmarshal(a, &left)
	_ = json.Unmarshal(e, &right)
	return reflect.DeepEqual(left, right)
}

// Outputs of the handler override those of prescripts; postscripts see and override both.
func TestHandlerOutputsOverridePrescriptsAndPostscriptsOverrideBoth(t *testing.T) {
	handler := &recordingHandler{outputs: map[string]any{"bar": "batz", "overwriting": 2, "overwritten": 2}}
	result := executeModuleTask(t, handler, middlewareScriptVariables(), middlewareScripts)
	expected := map[string]any{
		"bar":                "batz",
		"overwriting":        2,
		"overwritten":        3,
		"input_v1":           "42",
		"input_list":         `["42",43]`,
		"input_listNames":    `["inp1","inp2"]`,
		"long_result_output": "{{.long_result}}",
		"outputsGet":         "batz_read",
	}
	if !sameJsonValue(t, result.Output, expected) {
		t.Errorf("\n%#v\n%#v", result.Output, expected)
	}
}

// Variables written by prescripts and postscripts become changes of the instance; a postscript
// reads what a prescript wrote.
func TestVariablesWrittenByScriptsBecomeChangesOfTheInstance(t *testing.T) {
	handler := &recordingHandler{outputs: map[string]any{}}
	result := executeModuleTask(t, handler, middlewareScriptVariables(), middlewareScripts)
	expected := map[string]any{
		"toBeUpdatesInPre":        2,
		"toBeUpdatesInPost":       2,
		"toBeUpdatesInPreAndPost": 3,
		"added":                   "foo",
		"addedJson":               `"foo"`,
		"long_result":             "a long text",
	}
	if result.Effects == nil || !sameJsonValue(t, result.Effects.Variables, expected) {
		t.Errorf("\n%#v\n%#v", result.Effects, expected)
	}
}
