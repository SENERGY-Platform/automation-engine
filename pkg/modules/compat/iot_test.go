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
	"reflect"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/modules/compat"
	devicemodel "github.com/SENERGY-Platform/device-repository/v3/lib/model"
	"github.com/SENERGY-Platform/models/go/models"
)

const (
	functionId   = models.URN_PREFIX + "measuring-function:getTemperature"
	airAspect    = models.URN_PREFIX + "aspect:air"
	insideAspect = models.URN_PREFIX + "aspect:inside"
)

// AspectId is the deprecated alias for an AspectIds list with one element. device-repository
// resolves the alias itself, so both spellings arrive there as written.
func TestGetDevicesWithServiceForwardsAspectsAsWritten(t *testing.T) {
	for name, criteria := range map[string][]devicemodel.FilterCriteria{
		"an aspect list":                  {{FunctionId: functionId, AspectIds: []string{airAspect, insideAspect}}},
		"the deprecated single aspect id": {{FunctionId: functionId, AspectId: airAspect}},
		"both spellings side by side":     {{FunctionId: functionId, AspectId: airAspect, AspectIds: []string{insideAspect}}},
		"no aspect at all":                {{FunctionId: functionId}},
	} {
		t.Run(name, func(t *testing.T) {
			iot := &iotStub{devices: map[string]models.Device{"device1": {Id: "device1", DeviceTypeId: "dt1"}}}
			_, err := compat.GetDevicesWithService(iot, "token", compat.IotOption{DeviceSelection: &compat.DeviceSelection{DeviceId: "device1"}}, criteria)
			if err != nil {
				t.Fatal(err)
			}
			if expected := [][]devicemodel.FilterCriteria{criteria}; !reflect.DeepEqual(iot.criteria, expected) {
				t.Errorf("device-repository was asked for %#v, not %#v", iot.criteria, expected)
			}
		})
	}
}

// A path option that matched several aspects becomes one iot option with its path and
// characteristic.
func TestGetDevicesWithServiceTurnsAMultiAspectPathOptionIntoAnIotOption(t *testing.T) {
	iot := &iotStub{
		devices: map[string]models.Device{"device1": {Id: "device1", DeviceTypeId: "dt1"}},
		selectables: []devicemodel.DeviceTypeSelectable{{
			DeviceTypeId: "dt1",
			Services:     []models.Service{{Id: "service1"}},
			ServicePathOptions: map[string][]models.ServicePathOption{
				"service1": {{
					ServiceId:        "service1",
					Path:             "value.temperature",
					CharacteristicId: "characteristic1",
					AspectNode:       models.AspectNode{Id: airAspect},
					AspectNodes:      []models.AspectNode{{Id: airAspect}, {Id: insideAspect}},
				}},
			},
		}},
	}
	options, err := compat.GetDevicesWithService(iot, "token", compat.IotOption{DeviceSelection: &compat.DeviceSelection{DeviceId: "device1"}},
		[]devicemodel.FilterCriteria{{AspectIds: []string{airAspect, insideAspect}}})
	if err != nil {
		t.Fatal(err)
	}
	serviceId, path, characteristicId := "service1", "value.temperature", "characteristic1"
	expected := []compat.IotOption{{DeviceSelection: &compat.DeviceSelection{DeviceId: "device1", ServiceId: &serviceId, Path: &path, CharacteristicId: &characteristicId}}}
	if !reflect.DeepEqual(options, expected) {
		t.Errorf("%#v", options)
	}
}

// A device group selection expands to the services of the devices in the group.
func TestGetDevicesWithServiceExpandsTheDevicesOfAGroup(t *testing.T) {
	iot := &iotStub{
		devices:     map[string]models.Device{"device1": {Id: "device1", DeviceTypeId: "dt1"}, "device2": {Id: "device2", DeviceTypeId: "dt1"}},
		groups:      map[string]models.DeviceGroup{"group1": {Id: "group1", DeviceIds: []string{"device1", "device2"}}},
		selectables: []devicemodel.DeviceTypeSelectable{{DeviceTypeId: "dt1", Services: []models.Service{{Id: "service1"}}}},
	}
	options, err := compat.GetDevicesWithService(iot, "token", compat.IotOption{DeviceGroupSelection: &compat.DeviceGroupSelection{Id: "group1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	serviceId := "service1"
	expected := []compat.IotOption{
		{DeviceSelection: &compat.DeviceSelection{DeviceId: "device1", ServiceId: &serviceId}},
		{DeviceSelection: &compat.DeviceSelection{DeviceId: "device2", ServiceId: &serviceId}},
	}
	if !reflect.DeepEqual(options, expected) {
		t.Errorf("%#v", options)
	}
}
