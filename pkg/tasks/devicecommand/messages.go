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

// The formats of this file are those of external-task-worker v0.1.2 lib/messages: the payload
// of a device command task as process-deployment writes it, and the protocol message on the
// Kafka topics of platform-connector. They are read and written by other services and keep
// their JSON names.

import (
	"slices"
	"strings"

	marshallermodel "github.com/SENERGY-Platform/marshaller/lib/marshaller/model"
	"github.com/SENERGY-Platform/models/go/models"
)

type TaskInfo struct {
	WorkerId          string `json:"worker_id"`
	TaskId            string `json:"task_id"`
	ProcessInstanceId string `json:"process_instance_id"`
	// the deployment of the instance (DATA-04)
	DeploymentId string `json:"deployment_id"`
	// Deprecated: the old name of DeploymentId, filled with the same id until every connector
	// mirrors deployment_id with platform-connector-lib (DATA-04)
	ProcessDefinitionId string `json:"process_definition_id"`
	CompletionStrategy  string `json:"completion_strategy"`
	Time                string `json:"time"`
	TenantId            string `json:"tenant_id"`
	BusinessKey         string `json:"business_key"`
}

type ProtocolRequest struct {
	Input map[string]string `json:"input"`
}

type ProtocolResponse struct {
	Output map[string]string `json:"output"`
}

type Metadata struct {
	Version  int64           `json:"version,omitempty"`
	Device   models.Device   `json:"device"`
	Service  models.Service  `json:"service"`
	Protocol models.Protocol `json:"protocol"`
	// only for version >= 3
	OutputPath string `json:"output_path,omitempty"`
	// only for version >= 3 if no OutputPath is known
	OutputFunctionId string `json:"output_function_id,omitempty"`
	// Deprecated: use OutputAspectNodes
	OutputAspectNode *models.AspectNode `json:"output_aspect_node,omitempty"`
	// only for version >= 3 if no OutputPath is known
	OutputAspectNodes    []models.AspectNode `json:"output_aspect_nodes,omitempty"`
	InputCharacteristic  string              `json:"input_characteristic,omitempty"`
	OutputCharacteristic string              `json:"output_characteristic,omitempty"`
	// only for version < 3
	ContentVariableHints []string `json:"content_variable_hints,omitempty"`
	ResponseTo           string   `json:"response_to"`
	ErrorTo              string   `json:"error_to,omitempty"`
}

type ProtocolMsg struct {
	Request  ProtocolRequest  `json:"request"`
	Response ProtocolResponse `json:"response"`
	TaskInfo TaskInfo         `json:"task_info"`
	Metadata Metadata         `json:"metadata"`
	Trace    []Trace          `json:"trace,omitempty"`
}

type Trace struct {
	TimeUnit  string `json:"time_unit"`
	Timestamp int64  `json:"timestamp"`
	Location  string `json:"location"`
}

// EventRequest reads the last value of an event service instead of sending a request
type EventRequest struct {
	Device           models.Device
	Service          models.Service
	Protocol         models.Protocol
	CharacteristicId string
	FunctionId       string
	AspectNodes      []models.AspectNode
}

type TimescaleRequest struct {
	Device     models.Device
	Service    models.Service
	ColumnName string
}

type TimescaleResponse struct {
	Time  *string `json:"time"`
	Value any     `json:"value"`
}

type Command struct {
	Version int64 `json:"version"`

	// modeling time
	Function         models.Function `json:"function"`
	CharacteristicId string          `json:"characteristic_id"`

	// optional modeling time, limits the device and service selection in the deployment
	DeviceClass *models.DeviceClass `json:"device_class,omitempty"`
	// Deprecated: use Aspects
	Aspect  *models.AspectNode  `json:"aspect,omitempty"`
	Aspects []models.AspectNode `json:"aspects,omitempty"`

	// deployment time
	DeviceGroupId string           `json:"device_group_id"`
	DeviceId      string           `json:"device_id,omitempty"`
	Device        *models.Device   `json:"device,omitempty"`
	ServiceId     string           `json:"service_id,omitempty"`
	Service       *models.Service  `json:"service,omitempty"`
	Protocol      *models.Protocol `json:"protocol,omitempty"`
	ProtocolId    string           `json:"protocol_id,omitempty"`

	// version <= 2
	Configurables        []Configurable `json:"configurables,omitempty"`
	ContentVariableHints []string       `json:"content_variable_hints,omitempty"`

	// version >= 3
	InputPaths      []string         `json:"input_paths,omitempty"`
	OutputPath      string           `json:"output_path,omitempty"`
	ConfigurablesV2 []ConfigurableV2 `json:"configurables_v2,omitempty"`

	PreferEvent bool `json:"prefer_event"`

	// runtime
	Input  any `json:"input,omitempty"`
	Output any `json:"output,omitempty"`

	Retries int64 `json:"retries,omitempty"`
}

type Overwrite struct {
	DeviceId   string           `json:"device_id,omitempty"`
	Device     *models.Device   `json:"device,omitempty"`
	ServiceId  string           `json:"service_id,omitempty"`
	Service    *models.Service  `json:"service,omitempty"`
	Protocol   *models.Protocol `json:"protocol,omitempty"`
	ProtocolId string           `json:"protocol_id,omitempty"`
}

// The single aspect of a command and of a protocol message is deprecated in favor of a list.
// It stays an alias for a list with one element: read messages fold it into the list, written
// ones fill it from the list, for readers that know only the single field.

// GetAspects returns the aspects the command asks for; a service has to serve all of them.
func (this Command) GetAspects() []models.AspectNode {
	if this.Aspect == nil {
		return this.Aspects
	}
	return marshallermodel.AspectNodesAlias(*this.Aspect, this.Aspects)
}

// SetAspects folds the deprecated Aspect into Aspects.
func (this *Command) SetAspects() {
	this.Aspects = this.GetAspects()
}

// GetOutputAspectNodes returns the aspects of the expected response.
func (this Metadata) GetOutputAspectNodes() []models.AspectNode {
	if this.OutputAspectNode == nil {
		return this.OutputAspectNodes
	}
	return marshallermodel.AspectNodesAlias(*this.OutputAspectNode, this.OutputAspectNodes)
}

// SetOutputAspectNodes sets the aspects of the expected response and the deprecated single
// node: the one with the alphabetically first id, as device-repository fills it.
func (this *Metadata) SetOutputAspectNodes(aspectNodes []models.AspectNode) {
	this.OutputAspectNodes = aspectNodes
	this.OutputAspectNode = nil
	if len(aspectNodes) > 0 {
		first := slices.MinFunc(aspectNodes, func(a models.AspectNode, b models.AspectNode) int { return strings.Compare(a.Id, b.Id) })
		this.OutputAspectNode = &first
	}
}
