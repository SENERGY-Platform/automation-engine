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

package compat_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/modules/compat"
	"github.com/SENERGY-Platform/device-repository/v3/lib/client"
	devicemodel "github.com/SENERGY-Platform/device-repository/v3/lib/model"
	"github.com/SENERGY-Platform/models/go/models"
)

// Most tests here are those of the middleware of smart-service-module-worker-lib.

// iotStub answers the device-repository calls of the script API and records the criteria it was
// asked with; every other method of the interface is unimplemented, so an unexpected call fails
type iotStub struct {
	client.Interface
	devices     map[string]models.Device
	groups      map[string]models.DeviceGroup
	selectables []devicemodel.DeviceTypeSelectable
	criteria    [][]devicemodel.FilterCriteria
}

func (this *iotStub) ReadDevice(id string, token string, action devicemodel.AuthAction) (models.Device, error, int) {
	device, ok := this.devices[id]
	if !ok {
		return models.Device{}, errors.New("unknown device " + id), http.StatusNotFound
	}
	return device, nil, http.StatusOK
}

func (this *iotStub) ReadDeviceGroup(id string, token string, filterGenericDuplicateCriteria bool) (models.DeviceGroup, error, int) {
	group, ok := this.groups[id]
	if !ok {
		return models.DeviceGroup{}, errors.New("unknown device group " + id), http.StatusNotFound
	}
	return group, nil, http.StatusOK
}

func (this *iotStub) GetDeviceTypeSelectablesV2(query []devicemodel.FilterCriteria, pathPrefix string, includeModified bool, servicesMustMatchAllCriteria bool) ([]devicemodel.DeviceTypeSelectable, error, int) {
	this.criteria = append(this.criteria, query)
	return this.selectables, nil, http.StatusOK
}

func scripts(iot client.Interface, logger *slog.Logger) compat.Scripts {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return compat.Scripts{Logger: logger, Tokens: func(string) (string, error) { return "Bearer token", nil }, Iot: iot}
}

// runPrescripts runs the prescript inputs with the variables, without outputs of a handler
func runPrescripts(iot client.Interface, inputs map[string]any, variables map[string]any) (map[string]any, map[string]any, error) {
	if variables == nil {
		variables = map[string]any{}
	}
	return scripts(iot, nil).Run(context.Background(), "user", compat.PreScriptPrefix, inputs, nil, variables, nil)
}

func knownDevice() *iotStub {
	return &iotStub{devices: map[string]models.Device{"device1": {Id: "device1", LocalId: "device1lid", Name: "device1name", DeviceTypeId: "dtid"}}}
}

func TestScriptReadsADeviceOfTheRepository(t *testing.T) {
	_, outputs, err := runPrescripts(knownDevice(), map[string]any{compat.PreScriptPrefix + "_1": `
		var device = deviceRepo.readDevice("device1");
		outputs.set("device_name", device.name);`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outputs["device_name"] != "device1name" {
		t.Errorf("%#v", outputs)
	}
}

func TestUncaughtScriptErrorsFailTheScript(t *testing.T) {
	for name, script := range map[string]string{
		"unknown device":   `var device = deviceRepo.readDevice("unknown");`,
		"null device id":   `var device = deviceRepo.readDevice(null);`,
		"null dereference": `var device = null; outputs.set("device_name", device.name);`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := runPrescripts(knownDevice(), map[string]any{compat.PreScriptPrefix + "_1": script}, nil); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestScriptCatchesAnErrorOfTheRepository(t *testing.T) {
	_, outputs, err := runPrescripts(knownDevice(), map[string]any{compat.PreScriptPrefix + "_1": `
		try {
			var device = deviceRepo.readDevice("unknown");
			outputs.set("device_name", device.name);
		} catch (error) {
			outputs.set("error", error);
		}`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := outputs["error"]; !ok {
		t.Errorf("%#v", outputs)
	}
}

// Script inputs with the same prefix form one script in the order of their names, so that a
// long script may be split anywhere, even within a word.
func TestScriptPartsRunConcatenatedInTheOrderOfTheirNames(t *testing.T) {
	_, outputs, err := runPrescripts(nil, map[string]any{
		"inp1":         "42",
		"prescript_2":  `ut_v1", inputs.get("inp1"));`,
		"prescript_1":  `outputs.set("inp`,
		"postscript_1": `outputs.set("post", true);`,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(outputs, map[string]any{"input_v1": "42"}) {
		t.Errorf("%#v", outputs)
	}
}

func TestScriptInputsAreTheOtherInputsSortedByName(t *testing.T) {
	_, outputs, err := runPrescripts(nil, map[string]any{
		"inp2":       43,
		"inp1":       "42",
		"prescript":  `outputs.setJson("input_list", inputs.list()); outputs.setJson("input_list_names", inputs.listNames());`,
		"postscript": `outputs.set("post", true);`,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]any{"input_list": `["42",43]`, "input_list_names": `["inp1","inp2"]`}
	if !reflect.DeepEqual(outputs, expected) {
		t.Errorf("%#v", outputs)
	}
}

func TestVariablesRefIsATemplateReferenceOfAKnownVariable(t *testing.T) {
	_, outputs, err := runPrescripts(nil, map[string]any{"prescript": `
		variables.write("long_result", "a long text");
		outputs.set("long_result_output", variables.ref("long_result"));`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outputs["long_result_output"] != "{{.long_result}}" {
		t.Errorf("%#v", outputs)
	}
}

// A criteria without aspects reaches device-repository without aspects; the aspect variants are
// in TestPrescriptSendsAspectIdsToDeviceRepository of pkg/modules.
func TestScriptCriteriaWithoutAspectReachDeviceRepositoryWithoutAspect(t *testing.T) {
	const functionId = models.URN_PREFIX + "measuring-function:getTemperature"
	iot := &iotStub{}
	_, _, err := runPrescripts(iot, map[string]any{"prescript": `deviceRepo.getDeviceTypeSelectables([{function_id: "` + functionId + `"}], "", true, true);`}, nil)
	if err != nil {
		t.Fatal(err)
	}
	expected := [][]devicemodel.FilterCriteria{{{FunctionId: functionId}}}
	if !reflect.DeepEqual(iot.criteria, expected) {
		t.Errorf("device-repository was asked for %#v, not %#v", iot.criteria, expected)
	}
}

// Module scripts parse as BPMN scripts do: a value that is no string comes back as it is.
func TestModuleScriptsParseLikeBpmnScripts(t *testing.T) {
	changes, _, err := scripts(nil, nil).Run(context.Background(), "user", compat.PreScriptPrefix,
		map[string]any{"prescript": `variables.write("lat", JSON.parse(variables.read("location")).Latitude)`}, nil,
		map[string]any{"location": map[string]any{"Latitude": 51.3}}, nil)
	if err != nil || changes["lat"] != 51.3 {
		t.Fatalf("%v %v", changes, err)
	}
}
