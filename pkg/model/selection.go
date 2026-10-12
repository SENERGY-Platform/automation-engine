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

package model

// Selection assigns devices to a task or device event of a deployment (DEP-05). It is stored
// typed per element in the deployment version instead of as JSON in a process variable.
type Selection struct {
	DeviceId      string `json:"device_id,omitempty"`
	ServiceId     string `json:"service_id,omitempty"`
	DeviceGroupId string `json:"device_group_id,omitempty"`
	ImportId      string `json:"import_id,omitempty"`
	// the value of the function in the service and the values of its configurables
	Path *SelectedPath `json:"path,omitempty"`
	// read the last event value instead of sending a command, for services with event and request (TASK-08)
	PreferEvent *bool `json:"prefer_event,omitempty"`
}

type SelectedPath struct {
	Path             string         `json:"path"`
	CharacteristicId string         `json:"characteristic_id,omitempty"`
	Configurables    []Configurable `json:"configurables,omitempty"`
}

// Configurable is a further value a command sets, e.g. a duration besides a brightness.
type Configurable struct {
	Path             string   `json:"path"`
	CharacteristicId string   `json:"characteristic_id"`
	FunctionId       string   `json:"function_id,omitempty"`
	AspectNodeIds    []string `json:"aspect_node_ids,omitempty"`
	Value            any      `json:"value,omitempty"`
	Type             string   `json:"type,omitempty"`
}

func (this Selection) Empty() bool {
	return this.DeviceId == "" && this.DeviceGroupId == "" && this.ImportId == ""
}
