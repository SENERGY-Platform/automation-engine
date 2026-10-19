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

package client_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

const smartServices = "smart-services"

// readySmartService starts a smart service with the given start variables and waits for its end
func readySmartService(t *testing.T, c client.Client, xml string, variables map[string]any) model.Instance {
	t.Helper()
	d, err := c.CreateDeployment(ctx, alice, smartServices, model.DeploymentInput{Xml: xml})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, smartServices, model.StartInstanceInput{DeploymentId: d.Id, Variables: variables})
	if err != nil {
		t.Fatal(err)
	}
	if ended := finished(t, c, smartServices, inst.Id); ended.State != model.StateCompleted {
		t.Fatalf("the smart service completes: %+v", ended)
	}
	return inst
}

func variableMap(vars []model.Variable) map[string]any {
	result := map[string]any{}
	for _, v := range vars {
		result[v.Name] = v.Value
	}
	return result
}

// Variables of a smart service keep their JSON type, also null and objects, and a start
// parameter longer than Camunda's 4000 characters for strings (SMS-10).
func TestSmartServiceVariablesKeepTheirJsonTypes(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		large := strings.Repeat("a", 5000)
		inst := readySmartService(t, c, b.Process(b.StartWithForm("start", "large="), b.End("end"), b.Flow("start", "end")), map[string]any{"large": large})
		values := map[string]any{"str": "str", "int": 13.0, "bool": true, "float": 2.4, "object": map[string]any{"foo": "bar", "batz": 42.0}, "null": nil}
		for name, value := range values {
			if err := c.SetVariable(ctx, alice, smartServices, inst.Id, name, value); err != nil {
				t.Fatalf("%v: %v", name, err)
			}
		}
		vars, err := c.GetVariables(ctx, alice, smartServices, inst.Id)
		if err != nil {
			t.Fatal(err)
		}
		values["large"] = large
		if actual := variableMap(vars); !reflect.DeepEqual(actual, values) {
			t.Fatalf("variables %v, want %v", actual, values)
		}
	})
}

// A deleted variable is no longer listed; the others stay.
func TestDeletedSmartServiceVariableIsNoLongerListed(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		inst := readySmartService(t, c, b.Process(b.Start("start"), b.End("end"), b.Flow("start", "end")), nil)
		for _, name := range []string{"kept", "deleted"} {
			if err := c.SetVariable(ctx, alice, smartServices, inst.Id, name, name); err != nil {
				t.Fatal(err)
			}
		}
		if err := c.DeleteVariable(ctx, alice, smartServices, inst.Id, "deleted"); err != nil {
			t.Fatal(err)
		}
		vars, err := c.GetVariables(ctx, alice, smartServices, inst.Id)
		if actual := variableMap(vars); err != nil || !reflect.DeepEqual(actual, map[string]any{"kept": "kept"}) {
			t.Fatalf("variables %v %v", actual, err)
		}
	})
}

// The variables of a deleted smart service are gone with it.
func TestVariablesGoWithTheirSmartService(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		inst := readySmartService(t, c, b.Process(b.Start("start"), b.End("end"), b.Flow("start", "end")), nil)
		if err := c.SetVariable(ctx, alice, smartServices, inst.Id, "x", 1); err != nil {
			t.Fatal(err)
		}
		if err := c.DeleteInstances(ctx, alice, smartServices, []string{inst.Id}); err != nil {
			t.Fatal(err)
		}
		vars, err := c.GetVariables(ctx, alice, smartServices, inst.Id)
		if len(vars) != 0 || (err != nil && !errors.Is(err, model.ErrNotFound)) {
			t.Fatalf("variables %v %v", vars, err)
		}
	})
}

// An object given for a string form field reaches the process as its JSON text, which scripts
// parse, as Camunda handed it to the models of smart services (SMS-08).
func TestObjectParameterOfAStringFieldReachesScriptsAsJson(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		inst := readySmartService(t, c, b.Process(
			b.StartWithForm("start", "location="),
			b.Task("extract", b.OutScript("lat", "JSON.parse(location).Latitude"), b.OutScript("lon", "JSON.parse(location).Longitude")),
			b.End("end"),
			b.Flow("start", "extract"), b.Flow("extract", "end"),
		), map[string]any{"location": map[string]any{"Latitude": 51.338527718877394, "Longitude": 12.38074998525586}})
		vars, err := c.GetVariables(ctx, alice, smartServices, inst.Id)
		if err != nil {
			t.Fatal(err)
		}
		expected := map[string]any{"location": `{"Latitude":51.338527718877394,"Longitude":12.38074998525586}`, "lat": 51.338527718877394, "lon": 12.38074998525586}
		if actual := variableMap(vars); !reflect.DeepEqual(actual, expected) {
			t.Fatalf("variables %v, want %v", actual, expected)
		}
	})
}
