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
	"github.com/SENERGY-Platform/marshaller/lib/api/messages"
	marshallerclient "github.com/SENERGY-Platform/marshaller/lib/client"
	"github.com/SENERGY-Platform/marshaller/lib/configurables"
	marshallermodel "github.com/SENERGY-Platform/marshaller/lib/marshaller/model"
	"github.com/SENERGY-Platform/models/go/models"
)

// the request types are those of the marshaller itself

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

// Marshaller is the part of the marshaller service the device commands use.
type Marshaller interface {
	MarshalV2(service models.Service, protocol models.Protocol, data []MarshallingV2RequestData) (map[string]string, error)
	UnmarshalV2(request UnmarshallingV2Request) (any, error)

	MarshalFromServiceAndProtocol(characteristicId string, service models.Service, protocol models.Protocol, characteristicData any, configurables []Configurable) (map[string]string, error)
	UnmarshalFromServiceAndProtocol(characteristicId string, service models.Service, protocol models.Protocol, message map[string]string, hints []string) (any, error)
}

type marshallerService struct {
	client marshallerclient.Interface
}

// NewMarshaller calls the marshaller service internally, without token.
func NewMarshaller(url string) Marshaller {
	return &marshallerService{client: marshallerclient.NewClient(url, nil)}
}

func (this *marshallerService) MarshalV2(service models.Service, protocol models.Protocol, data []MarshallingV2RequestData) (map[string]string, error) {
	result, err, _ := this.client.MarshalV2(messages.MarshallingV2Request{Service: service, Protocol: protocol, Data: data})
	return result, err
}

func (this *marshallerService) UnmarshalV2(request UnmarshallingV2Request) (any, error) {
	result, err, _ := this.client.UnmarshalV2(request)
	return result, err
}

func (this *marshallerService) MarshalFromServiceAndProtocol(characteristicId string, service models.Service, protocol models.Protocol, data any, configurables []Configurable) (map[string]string, error) {
	result, err, _ := this.client.Marshal(messages.MarshallingRequest{CharacteristicId: characteristicId, Service: service, Protocol: &protocol, Configurables: configurables, Data: data})
	return result, err
}

func (this *marshallerService) UnmarshalFromServiceAndProtocol(characteristicId string, service models.Service, protocol models.Protocol, message map[string]string, hints []string) (any, error) {
	result, err, _ := this.client.Unmarshal(messages.UnmarshallingRequest{CharacteristicId: characteristicId, Service: service, Protocol: &protocol, Message: message, ContentVariableHints: hints})
	return result, err
}
