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

package devicecommand

// The sub-task construction follows external-task-worker v0.1.2 lib/devicegroups, Apache-2.0,
// with sub-task ids and the service filter unchanged.

import (
	"sort"
	"strconv"

	"github.com/SENERGY-Platform/models/go/models"

	"github.com/SENERGY-Platform/automation-engine/pkg/devices"
)

type child struct {
	subId   string
	command Command
}

// subtasks expands a device group to device × matching service and a device without service
// to its matching services; ids are <job>_<device index>_<service index> (TASK-06)
func (this *Type) subtasks(token devices.Token, command Command, jobId string) ([]child, error) {
	var result []child
	switch {
	case command.DeviceGroupId != "":
		group, err := this.repo.GetDeviceGroup(token, command.DeviceGroupId)
		if err != nil {
			return nil, err
		}
		for i, deviceId := range group.DeviceIds {
			children, err := this.serviceChildren(token, command, jobId, i, deviceId)
			if err != nil {
				return nil, err
			}
			result = append(result, children...)
		}
	case command.DeviceId != "" && command.ServiceId == "":
		children, err := this.serviceChildren(token, command, jobId, 0, command.DeviceId)
		if err != nil {
			return nil, err
		}
		result = children
	default:
		result = []child{{subId: jobId, command: command}}
	}
	for i, c := range result {
		if c.command.Device != nil {
			c.command.Device.Id, _ = devices.SplitModifier(c.command.Device.Id)
			c.command.Device.DeviceTypeId, _ = devices.SplitModifier(c.command.Device.DeviceTypeId)
		}
		if c.command.DeviceId != "" {
			c.command.DeviceId, _ = devices.SplitModifier(c.command.DeviceId)
		}
		result[i] = c
	}
	return result, nil
}

func (this *Type) serviceChildren(token devices.Token, command Command, jobId string, deviceIndex int, deviceId string) ([]child, error) {
	device, err := this.repo.GetDevice(token, deviceId)
	if err != nil {
		return nil, err
	}
	deviceType, err := this.repo.GetDeviceType(token, device.DeviceTypeId)
	if err != nil {
		return nil, err
	}
	var result []child
	for j, service := range this.filterServices(command, deviceType.Services) {
		c := command
		d := device
		s := service
		c.DeviceGroupId = ""
		c.DeviceId = deviceId
		c.Device = &d
		c.ServiceId = service.Id
		c.Service = &s
		result = append(result, child{subId: jobId + "_" + strconv.Itoa(deviceIndex) + "_" + strconv.Itoa(j), command: c})
	}
	return result, nil
}

// filterServices keeps services whose inputs (outputs for measuring functions) serve the
// function and all aspects; event-only services are dropped when no last values can be read
func (this *Type) filterServices(command Command, services []models.Service) []models.Service {
	index := map[string]models.Service{}
	aspects := command.GetAspects()
	for _, service := range services {
		contents := service.Inputs
		if isMeasuringFunctionId(command.Function.Id) {
			contents = service.Outputs
		}
		isEvent := isMeasuringFunctionId(command.Function.Id) && service.Interaction == models.EVENT
		if devices.ContentsMatch(contents, command.Function.Id, aspects) && (this.timescale != nil || !isEvent) {
			index[service.Id] = service
		}
	}
	result := make([]models.Service, 0, len(index))
	for _, s := range index {
		result = append(result, s)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Id < result[j].Id })
	return result
}
