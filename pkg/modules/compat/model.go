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

type IotOption struct {
	DeviceSelection      *DeviceSelection      `json:"device_selection,omitempty"`
	DeviceGroupSelection *DeviceGroupSelection `json:"device_group_selection,omitempty"`
	ImportSelection      *ImportSelection      `json:"import_selection,omitempty"`
	GenericEventSource   *GenericEventSource   `json:"generic_event_source,omitempty"`
}

type GenericEventSource struct {
	FilterType       string  `json:"filter_type"`
	FilterIds        string  `json:"filter_ids"`
	Topic            string  `json:"topic"`
	Path             string  `json:"path"`
	CharacteristicId *string `json:"characteristic_id,omitempty"`
}

type DeviceSelection struct {
	DeviceId         string  `json:"device_id"`
	ServiceId        *string `json:"service_id"`
	Path             *string `json:"path"`
	CharacteristicId *string `json:"characteristic_id,omitempty"`
}

type DeviceGroupSelection struct {
	Id string `json:"id"`
}

type ImportSelection struct {
	Id               string  `json:"id"`
	Path             *string `json:"path"`
	CharacteristicId *string `json:"characteristic_id,omitempty"`
}
