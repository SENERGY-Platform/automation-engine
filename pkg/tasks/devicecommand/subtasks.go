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
	"context"
	"sort"
	"strconv"

	"fmt"

	"github.com/SENERGY-Platform/models/go/models"

	"github.com/SENERGY-Platform/automation-engine/pkg/commons"
	devicerepository "github.com/SENERGY-Platform/device-repository/v3/lib/client"
	devicerepomodel "github.com/SENERGY-Platform/device-repository/v3/lib/model"
)

type child struct {
	subId   string
	command Command
}

// subtasks expands a device group to device × matching service and a device without service
// to its matching services; ids are <job>_<device index>_<service index> (TASK-06)
func (this *Type) subtasks(ctx context.Context, token string, command Command, jobId string) ([]child, error) {
	var result []child
	switch {
	case command.DeviceGroupId != "":
		group, err, _ := devicerepository.WithCache(this.iot, this.config.DeviceCacheTimeout).ReadDeviceGroup(ctx, command.DeviceGroupId, token, false)
		if err != nil {
			return nil, fmt.Errorf("unable to load device group %v: %w", command.DeviceGroupId, err)
		}
		for i, deviceId := range group.DeviceIds {
			children, err := this.serviceChildren(ctx, token, command, jobId, i, deviceId)
			if err != nil {
				return nil, err
			}
			result = append(result, children...)
		}
	case command.DeviceId != "" && command.ServiceId == "":
		children, err := this.serviceChildren(ctx, token, command, jobId, 0, command.DeviceId)
		if err != nil {
			return nil, err
		}
		result = children
	default:
		result = []child{{subId: jobId, command: command}}
	}
	for i, c := range result {
		if c.command.Device != nil {
			c.command.Device.Id, _ = commons.SplitModifier(c.command.Device.Id)
			c.command.Device.DeviceTypeId, _ = commons.SplitModifier(c.command.Device.DeviceTypeId)
		}
		if c.command.DeviceId != "" {
			c.command.DeviceId, _ = commons.SplitModifier(c.command.DeviceId)
		}
		result[i] = c
	}
	return result, nil
}

func (this *Type) serviceChildren(ctx context.Context, token string, command Command, jobId string, deviceIndex int, deviceId string) ([]child, error) {
	cached := devicerepository.WithCache(this.iot, this.config.DeviceCacheTimeout)
	device, err, _ := cached.ReadDevice(ctx, deviceId, token, devicerepomodel.READ)
	if err != nil {
		return nil, fmt.Errorf("unable to load device %v: %w", deviceId, err)
	}
	deviceType, err, _ := cached.ReadDeviceType(ctx, device.DeviceTypeId, token)
	if err != nil {
		return nil, fmt.Errorf("unable to load device type %v: %w", device.DeviceTypeId, err)
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
		if commons.ContentsMatch(contents, command.Function.Id, aspects) && (this.timescale != nil || !isEvent) {
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
