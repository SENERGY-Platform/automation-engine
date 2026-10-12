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

package compat

import (
	"errors"

	"github.com/SENERGY-Platform/device-repository/v3/lib/client"
	devicemodel "github.com/SENERGY-Platform/device-repository/v3/lib/model"
)

var ErrNoDeviceOrGroupSelection = errors.New("expect device or group selection")

func GetDevicesWithService(iotClient client.Interface, token string, entity IotOption, criteria []devicemodel.FilterCriteria) (result []IotOption, err error) {
	if entity.DeviceSelection != nil {
		if entity.DeviceSelection.ServiceId != nil {
			return []IotOption{entity}, nil //nothing to do
		}
		return getDevicesWithService(iotClient, token, []string{entity.DeviceSelection.DeviceId}, criteria)
	}
	if entity.DeviceGroupSelection != nil {
		groupDevices, err, _ := iotClient.ListHubDeviceIds(entity.DeviceGroupSelection.Id, token, devicemodel.READ, false)
		if err != nil {
			return result, err
		}
		return getDevicesWithService(iotClient, token, groupDevices, criteria)
	}
	return result, ErrNoDeviceOrGroupSelection
}

// GroupIotOptionsByDevice groups a list of IotOption by their device id
// none device options will be grouped under the "" key
func GroupIotOptionsByDevice(options []IotOption) (result map[string][]IotOption) {
	result = map[string][]IotOption{}
	for _, option := range options {
		if option.DeviceSelection != nil {
			result[option.DeviceSelection.DeviceId] = append(result[option.DeviceSelection.DeviceId], option)
		} else {
			result[""] = append(result[""], option)
		}
	}
	return result
}

// GroupIotOptionsByService groups a list of IotOption by their service id
// device options without serviceId will be grouped under the "" key
// none device options will be grouped under the "" key
func GroupIotOptionsByService(options []IotOption) (result map[string][]IotOption) {
	result = map[string][]IotOption{}
	for _, option := range options {
		if option.DeviceSelection != nil && option.DeviceSelection.ServiceId != nil {
			result[*option.DeviceSelection.ServiceId] = append(result[*option.DeviceSelection.ServiceId], option)
		} else {
			result[""] = append(result[""], option)
		}
	}
	return result
}

func getDevicesWithService(iotClient client.Interface, token string, deviceIds []string, criteria []devicemodel.FilterCriteria) (result []IotOption, err error) {
	dtSelectables, err, _ := iotClient.GetDeviceTypeSelectablesV2(criteria, "", true, true)
	if err != nil {
		return result, err
	}
	for _, deviceId := range deviceIds {
		device, err, _ := iotClient.ReadDevice(deviceId, token, devicemodel.READ)
		if err != nil {
			return result, err
		}
		for _, dtSelectable := range dtSelectables {
			if device.DeviceTypeId == dtSelectable.DeviceTypeId {
				for _, service := range dtSelectable.Services {
					serviceId := service.Id
					pathOptions, ok := dtSelectable.ServicePathOptions[service.Id]
					if !ok || len(pathOptions) == 0 {
						result = append(result, IotOption{
							DeviceSelection: &DeviceSelection{
								DeviceId:  device.Id,
								ServiceId: &serviceId,
							},
						})
					} else {
						for _, pathOption := range pathOptions {
							element := IotOption{
								DeviceSelection: &DeviceSelection{
									DeviceId:  device.Id,
									ServiceId: &serviceId,
								},
							}
							path := pathOption.Path
							if path != "" {
								element.DeviceSelection.Path = &path
							}
							characteristicId := pathOption.CharacteristicId
							if characteristicId != "" {
								element.DeviceSelection.CharacteristicId = &characteristicId
							}
							result = append(result, element)
						}
					}
				}
			}
		}
	}
	return result, nil
}
