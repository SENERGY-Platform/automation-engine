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

import (
	"context"

	"github.com/SENERGY-Platform/marshaller/lib/api/messages"
	"github.com/SENERGY-Platform/marshaller/lib/configurables"
	marshallermodel "github.com/SENERGY-Platform/marshaller/lib/marshaller/model"
	"github.com/SENERGY-Platform/models/go/models"
)

// the request types are those of the marshaller itself

type MarshallingRequest = messages.MarshallingRequest

type UnmarshallingRequest = messages.UnmarshallingRequest

type MarshallingV2Request = messages.MarshallingV2Request

type MarshallingV2RequestData = marshallermodel.MarshallingV2RequestData

type UnmarshallingV2Request = messages.UnmarshallingV2Request

type Configurable = configurables.Configurable

// ConfigurableV2 is the configurable as process-deployment writes it into the task payload; it
// mirrors the configurable of a device-repository path option.
type ConfigurableV2 struct {
	Path             string              `json:"path"`
	CharacteristicId string              `json:"characteristic_id"`
	AspectNode       models.AspectNode   `json:"aspect_node"` // Deprecated: use AspectNodes
	AspectNodes      []models.AspectNode `json:"aspect_nodes,omitempty"`
	FunctionId       string              `json:"function_id"`
	Value            any                 `json:"value,omitempty"`
	Type             string              `json:"type,omitempty"`
}

// GetAspectNodes returns the aspect nodes; the deprecated AspectNode is a list with one element.
func (this ConfigurableV2) GetAspectNodes() []models.AspectNode {
	return marshallermodel.AspectNodesAlias(this.AspectNode, this.AspectNodes)
}

// Marshaller is the part of the marshaller service the device commands use, in the signatures of
// its client (marshaller/lib/client), which implements it; on gateways NewLocalMarshaller does.
// Commands of payload version 3 use the V2 methods. The code is the http status of the answer.
type Marshaller interface {
	Marshal(ctx context.Context, request MarshallingRequest) (result map[string]string, err error, code int)
	Unmarshal(ctx context.Context, request UnmarshallingRequest) (result interface{}, err error, code int)
	MarshalV2(ctx context.Context, request MarshallingV2Request) (result map[string]string, err error, code int)
	UnmarshalV2(ctx context.Context, request UnmarshallingV2Request) (result interface{}, err error, code int)
}
