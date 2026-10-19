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
	"errors"
	"sync"

	"github.com/SENERGY-Platform/automation-engine/pkg/devices"
	"github.com/SENERGY-Platform/models/go/models"
)

// The tests of payload building, answers and device groups follow those of
// external-task-worker v0.1.2 lib/test and mgw-external-task-worker v1.2.2 pkg/tests. The
// conversions themselves belong to the marshaller; these tests check what the engine hands
// to it and what it does with the answer.

type marshalCall struct {
	characteristicId string
	data             any
	configurables    []Configurable
}

type unmarshalCall struct {
	characteristicId string
	hints            []string
}

// recordingMarshaller records the requests and answers like fakeMarshaller; with fail set,
// every marshalling is rejected
type recordingMarshaller struct {
	fakeMarshaller
	fail error

	mux         sync.Mutex
	marshalV1   []marshalCall
	marshalV2   [][]MarshallingV2RequestData
	unmarshalV1 []unmarshalCall
	unmarshalV2 []UnmarshallingV2Request
}

func (this *recordingMarshaller) MarshalV2(service models.Service, protocol models.Protocol, data []MarshallingV2RequestData) (map[string]string, error) {
	this.mux.Lock()
	this.marshalV2 = append(this.marshalV2, data)
	this.mux.Unlock()
	if this.fail != nil {
		return nil, this.fail
	}
	return this.fakeMarshaller.MarshalV2(service, protocol, data)
}

func (this *recordingMarshaller) UnmarshalV2(request UnmarshallingV2Request) (any, error) {
	this.mux.Lock()
	this.unmarshalV2 = append(this.unmarshalV2, request)
	this.mux.Unlock()
	return this.fakeMarshaller.UnmarshalV2(request)
}

func (this *recordingMarshaller) MarshalFromServiceAndProtocol(characteristicId string, service models.Service, protocol models.Protocol, data any, configurables []Configurable) (map[string]string, error) {
	this.mux.Lock()
	this.marshalV1 = append(this.marshalV1, marshalCall{characteristicId: characteristicId, data: data, configurables: configurables})
	this.mux.Unlock()
	if this.fail != nil {
		return nil, this.fail
	}
	return this.fakeMarshaller.MarshalFromServiceAndProtocol(characteristicId, service, protocol, data, configurables)
}

func (this *recordingMarshaller) UnmarshalFromServiceAndProtocol(characteristicId string, service models.Service, protocol models.Protocol, message map[string]string, hints []string) (any, error) {
	this.mux.Lock()
	this.unmarshalV1 = append(this.unmarshalV1, unmarshalCall{characteristicId: characteristicId, hints: hints})
	this.mux.Unlock()
	return this.fakeMarshaller.UnmarshalFromServiceAndProtocol(characteristicId, service, protocol, message, hints)
}

func (this *recordingMarshaller) v1Calls() []marshalCall {
	this.mux.Lock()
	defer this.mux.Unlock()
	return append([]marshalCall{}, this.marshalV1...)
}

func (this *recordingMarshaller) v2Calls() [][]MarshallingV2RequestData {
	this.mux.Lock()
	defer this.mux.Unlock()
	return append([][]MarshallingV2RequestData{}, this.marshalV2...)
}

func (this *recordingMarshaller) v1Answers() []unmarshalCall {
	this.mux.Lock()
	defer this.mux.Unlock()
	return append([]unmarshalCall{}, this.unmarshalV1...)
}

func (this *recordingMarshaller) v2Answers() []UnmarshallingV2Request {
	this.mux.Lock()
	defer this.mux.Unlock()
	return append([]UnmarshallingV2Request{}, this.unmarshalV2...)
}

// use replaces the device command type of the environment, e.g. by one with other devices,
// another marshaller or another group scheduler
func (this *env) use(repo *fakeRepo, aspects *devices.AspectNodes, m Marshaller, scheduler string) {
	this.dc = New(Config{GroupScheduler: scheduler, ResponseTo: "response", ErrorTo: "errors"}, this.s, repo, aspects, m, fakeTimescale{}, this.producer, this.metrics, this.logger)
	this.dc.SetCompleter(this.e)
	this.e.RegisterTaskType(TaskType, this.dc)
}

// aspectTree serves an air hierarchy (air > inside_air > kitchen_air, air > outside_air) and
// the unrelated roots humidity and light
func aspectTree() *devices.AspectNodes {
	nodes := map[string]models.AspectNode{
		"air":         {Id: "air", RootId: "air", ChildIds: []string{"inside_air", "outside_air"}, DescendentIds: []string{"inside_air", "outside_air", "kitchen_air"}},
		"inside_air":  {Id: "inside_air", RootId: "air", ParentId: "air", AncestorIds: []string{"air"}, ChildIds: []string{"kitchen_air"}, DescendentIds: []string{"kitchen_air"}},
		"kitchen_air": {Id: "kitchen_air", RootId: "air", ParentId: "inside_air", AncestorIds: []string{"air", "inside_air"}},
		"outside_air": {Id: "outside_air", RootId: "air", ParentId: "air", AncestorIds: []string{"air"}},
		"humidity":    {Id: "humidity", RootId: "humidity"},
		"light":       {Id: "light", RootId: "light"},
	}
	return devices.NewAspectNodesWith(func(_ devices.Token, id string) (models.AspectNode, error) {
		node, ok := nodes[id]
		if !ok {
			return node, errors.New("unknown aspect node " + id)
		}
		return node, nil
	})
}

// variable is a content variable of the function with the given aspects
func variable(name string, functionId string, aspectIds ...string) models.ContentVariable {
	return models.ContentVariable{Name: name, Type: models.Float, FunctionId: functionId, AspectIds: aspectIds}
}

// serviceWith carries the variables in one structure of the segment "seg"; measuring functions
// read outputs, the others write inputs
func serviceWith(id string, interaction models.Interaction, measuringFunction bool, variables ...models.ContentVariable) models.Service {
	content := models.Content{ProtocolSegmentId: "seg", Serialization: "json", ContentVariable: models.ContentVariable{Name: "value", Type: models.Structure, SubContentVariables: variables}}
	s := models.Service{Id: id, LocalId: "local-" + id, ProtocolId: "p", Interaction: interaction}
	if measuringFunction {
		s.Outputs = []models.Content{content}
	} else {
		s.Inputs = []models.Content{content}
	}
	return s
}

// repoWith serves one device type per device, named dt-<device>, and the group "g" of all
// devices in the given order
func repoWith(deviceIds []string, services map[string][]models.Service) *fakeRepo {
	repo := &fakeRepo{devices: map[string]models.Device{}, deviceTypes: map[string]models.DeviceType{}, groups: map[string]models.DeviceGroup{}}
	for _, id := range deviceIds {
		repo.devices[id] = models.Device{Id: id, LocalId: "local-" + id, DeviceTypeId: "dt-" + id}
		repo.deviceTypes["dt-"+id] = models.DeviceType{Id: "dt-" + id, Services: services[id]}
	}
	repo.groups["g"] = models.DeviceGroup{Id: "g", DeviceIds: deviceIds}
	return repo
}
