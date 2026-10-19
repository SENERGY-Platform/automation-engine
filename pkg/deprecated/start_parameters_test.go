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
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
)

type formVariable struct {
	Value     any
	Type      string
	ValueInfo map[string]any
}

// A process without a start form has no parameters: an empty object, not null.
func TestProcessWithoutStartFormHasAnEmptyParameterObject(t *testing.T) {
	e := newEnv(t)
	d := e.deploy(b.Process(b.Start("start"), b.End("end"), b.Flow("start", "end")))
	var parameters map[string]formVariable
	if status, _ := e.call(alice, http.MethodGet, "/engine/v2/deployments/"+d.Id+"/parameter", nil, &parameters); status != http.StatusOK {
		t.Fatal(status)
	}
	if parameters == nil || len(parameters) != 0 {
		t.Errorf("%#v", parameters)
	}
}

// A form field without default is a null value with the type name of Camunda, Long for long.
func TestStartParameterWithoutDefaultIsNullWithTheTypeOfCamunda(t *testing.T) {
	e := newEnv(t)
	d := e.deploy(b.Process(
		`<bpmn:startEvent id="start"><bpmn:extensionElements><camunda:formData>`+
			`<camunda:formField id="inputTemperature" label="inputTemperature" type="long"/>`+
			`</camunda:formData></bpmn:extensionElements></bpmn:startEvent>`,
		b.End("end"), b.Flow("start", "end"),
	))
	var parameters map[string]formVariable
	if status, _ := e.call(alice, http.MethodGet, "/engine/v2/deployments/"+d.Id+"/parameter", nil, &parameters); status != http.StatusOK {
		t.Fatal(status)
	}
	p, ok := parameters["inputTemperature"]
	if len(parameters) != 1 || !ok || p.Value != nil || p.Type != "Long" || p.ValueInfo == nil {
		t.Errorf("%#v", parameters)
	}
}
