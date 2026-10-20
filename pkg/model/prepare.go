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

import "encoding/json"

// PrepareInput names the BPMN to prepare a deployment for: a model version or own BPMN (DEP-01).
type PrepareInput struct {
	ModelId      string `json:"model_id,omitempty"`
	ModelVersion int    `json:"model_version,omitempty"`
	Xml          string `json:"xml,omitempty"`
	// a gateway: devices are offered only if they are devices of its hub, as
	// process-fog-deployment did
	NetworkId string `json:"network_id,omitempty"`
}

// PreparedDeployment lists what a deploy dialog needs: start parameters and, per device task
// and device event, the criteria and the devices, groups and imports that fulfill them.
type PreparedDeployment struct {
	Name            string            `json:"name"`
	ModelId         string            `json:"model_id,omitempty"`
	ModelVersion    int               `json:"model_version,omitempty"`
	StartParameters []StartParameter  `json:"start_parameters"`
	Elements        []PreparedElement `json:"elements"`
	// validation findings; with issues the deployment would be rejected (BPMN-18)
	Issues []Issue `json:"issues,omitempty"`
	// what the deploy converts from the format of Camunda 7 or the old designer; elements,
	// start parameters and issues are those of the converted model (MIG-04)
	Conversion *ConversionReport `json:"conversion,omitempty"`
}

const (
	PreparedTask  = "task"
	PreparedEvent = "event"
)

type PreparedElement struct {
	ElementId string `json:"element_id"`
	Name      string `json:"name"`
	// task or event
	Kind string `json:"kind"`
	// elements of a lane share their devices; the options fulfill the criteria of all of them
	Lane     string            `json:"lane,omitempty"`
	Criteria Criteria          `json:"criteria"`
	Options  []SelectionOption `json:"options"`
}

type Criteria struct {
	FunctionId       string   `json:"function_id"`
	CharacteristicId string   `json:"characteristic_id,omitempty"`
	DeviceClassId    string   `json:"device_class_id,omitempty"`
	AspectIds        []string `json:"aspect_ids,omitempty"`
}

type NamedRef struct {
	Id   string `json:"id"`
	Name string `json:"name"`
}

// SelectionOption is one device, device group or import that fulfills the criteria; its path
// options are passed on as device-selection returns them, so that no field gets lost.
type SelectionOption struct {
	Device      *NamedRef                  `json:"device,omitempty"`
	Services    []NamedRef                 `json:"services,omitempty"`
	DeviceGroup *NamedRef                  `json:"device_group,omitempty"`
	Import      *NamedRef                  `json:"import,omitempty"`
	ImportType  *NamedRef                  `json:"import_type,omitempty"`
	PathOptions map[string]json.RawMessage `json:"path_options,omitempty"`
}
