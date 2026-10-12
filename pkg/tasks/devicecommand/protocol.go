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

// Message creation, last event values and output unmarshalling follow external-task-worker
// v0.1.2 lib/protocol.go, lib/event.go and lib/worker.go (HandleTaskResponse), Apache-2.0. The
// ProtocolMsg and its task_info stay byte-compatible for the protocol handlers (TASK-04, TASK-05).

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/devices"
	"github.com/SENERGY-Platform/models/go/models"

	marshallermodel "github.com/SENERGY-Platform/marshaller/lib/marshaller/model"
	"github.com/SENERGY-Platform/marshaller/lib/marshaller/serialization"
)

// message creates the protocol message of a command, or an event request when the value is
// read instead of requested (TASK-08)
func (this *Type) message(command Command, info TaskInfo, token devices.Token) (*ProtocolMsg, *EventRequest, error) {
	trace := []Trace{{Timestamp: time.Now().UnixNano(), TimeUnit: "unix_nano", Location: "github.com/SENERGY-Platform/automation-engine device command start"}}
	device, service, protocol := command.Device, command.Service, command.Protocol
	if device == nil {
		d, err := this.repo.GetDevice(token, command.DeviceId)
		if err != nil {
			return nil, nil, fmt.Errorf("unable to load device %v: %w", command.DeviceId, err)
		}
		device = &d
	}
	if service == nil {
		s, err := this.repo.GetService(token, *device, command.ServiceId)
		if err != nil {
			return nil, nil, fmt.Errorf("unable to load service %v: %w", command.ServiceId, err)
		}
		service = &s
	}
	if protocol == nil {
		protocolId := command.ProtocolId
		if protocolId == "" {
			protocolId = service.ProtocolId
		}
		p, err := this.repo.GetProtocol(token, protocolId)
		if err != nil {
			return nil, nil, fmt.Errorf("unable to load protocol %v: %w", protocolId, err)
		}
		protocol = &p
	}

	var inputCharacteristic, outputCharacteristic string
	if isControllingFunction(command.Function) {
		inputCharacteristic = command.CharacteristicId
	} else {
		outputCharacteristic = command.CharacteristicId
		if service.Interaction == models.EVENT || (service.Interaction == models.EVENT_AND_REQUEST && command.PreferEvent) {
			return nil, &EventRequest{Device: *device, Service: *service, Protocol: *protocol, CharacteristicId: command.CharacteristicId, FunctionId: command.Function.Id, AspectNodes: command.GetAspects()}, nil
		}
	}

	var input map[string]string
	var err error
	if command.Version < 3 {
		input, err = this.marshaller.MarshalFromServiceAndProtocol(inputCharacteristic, *service, *protocol, command.Input, command.Configurables)
	} else {
		data := []MarshallingV2RequestData{}
		somethingToSet := command.Function.ConceptId != "" || command.Input != nil
		if somethingToSet && (len(command.InputPaths) > 0 || isControllingFunction(command.Function)) {
			data = append(data, MarshallingV2RequestData{Value: command.Input, CharacteristicId: inputCharacteristic, Paths: command.InputPaths, FunctionId: command.Function.Id, AspectNodes: command.GetAspects()})
		}
		for _, c := range command.ConfigurablesV2 {
			paths := []string{}
			if c.Path != "" {
				paths = []string{c.Path}
			}
			data = append(data, MarshallingV2RequestData{Value: c.Value, CharacteristicId: c.CharacteristicId, Paths: paths, FunctionId: c.FunctionId, AspectNodes: c.GetAspectNodes()})
		}
		start := time.Now()
		input, err = this.marshaller.MarshalV2(*service, *protocol, data)
		if err == nil {
			functionIds := []string{}
			for _, d := range data {
				if d.FunctionId != "" {
					functionIds = append(functionIds, d.FunctionId)
				}
			}
			sort.Strings(functionIds)
			this.metrics.MarshallingLatency("MarshalV2", info.TenantId, service.Id, strings.Join(functionIds, ","), time.Since(start))
		}
	}
	if err != nil {
		return nil, nil, fmt.Errorf("unable to marshal command: %w", err)
	}
	trace = append(trace, Trace{Timestamp: time.Now().UnixNano(), TimeUnit: "unix_nano", Location: "github.com/SENERGY-Platform/automation-engine device command end"})
	info.Time = strconv.FormatInt(this.now().Unix(), 10)
	msg := &ProtocolMsg{
		TaskInfo: info,
		Request:  ProtocolRequest{Input: input},
		Metadata: Metadata{
			Device: *device, Service: *service, Protocol: *protocol,
			InputCharacteristic: inputCharacteristic, OutputCharacteristic: outputCharacteristic,
			ContentVariableHints: command.ContentVariableHints, ResponseTo: this.config.ResponseTo, ErrorTo: this.config.ErrorTo,
		},
		Trace: trace,
	}
	if command.Version >= 3 {
		msg.Metadata.Version = command.Version
		if outputCharacteristic != "" {
			msg.Metadata.OutputPath = command.OutputPath
			msg.Metadata.OutputFunctionId = command.Function.Id
			msg.Metadata.SetOutputAspectNodes(command.GetAspects())
		}
	}
	return msg, nil, nil
}

// nullCharacteristicId marks a command without output, as in external-task-worker
const nullCharacteristicId = "null"

// output unmarshals the response of a protocol handler into the characteristic of the task
func (this *Type) output(msg ProtocolMsg) (any, error) {
	if msg.Metadata.OutputCharacteristic == nullCharacteristicId || msg.Metadata.OutputCharacteristic == "" {
		return nil, nil
	}
	if msg.Metadata.Version < 3 {
		return this.marshaller.UnmarshalFromServiceAndProtocol(msg.Metadata.OutputCharacteristic, msg.Metadata.Service, msg.Metadata.Protocol, msg.Response.Output, msg.Metadata.ContentVariableHints)
	}
	start := time.Now()
	result, err := this.marshaller.UnmarshalV2(UnmarshallingV2Request{
		Service: msg.Metadata.Service, Protocol: msg.Metadata.Protocol, CharacteristicId: msg.Metadata.OutputCharacteristic, Message: msg.Response.Output,
		Path: msg.Metadata.OutputPath, FunctionId: msg.Metadata.OutputFunctionId, AspectNodes: msg.Metadata.GetOutputAspectNodes(),
	})
	if err == nil {
		this.metrics.MarshallingLatency("UnmarshalV2", msg.TaskInfo.TenantId, msg.Metadata.Service.Id, msg.Metadata.OutputFunctionId, time.Since(start))
	}
	return result, err
}

// lastValue reads the last event value of a service through timescale-wrapper (TASK-08)
func (this *Type) lastValue(token devices.Token, event EventRequest) (any, error) {
	if this.timescale == nil {
		return nil, errors.New("reading event values needs a timescale-wrapper url")
	}
	request := timescaleRequest(event.Device, event.Service)
	response, err := this.timescale.Query(string(token), request, this.config.TimescaleTimeout)
	if err != nil {
		return nil, fmt.Errorf("unable to read last value: %w", err)
	}
	if len(request) != len(response) {
		return nil, errors.New("timescale response has less elements than the request")
	}
	values := map[string]any{}
	paths := make([]string, 0, len(request))
	byPath := map[string]any{}
	for i, r := range request {
		byPath[r.ColumnName] = response[i].Value
		paths = append(paths, r.ColumnName)
	}
	sort.Strings(paths)
	for _, p := range paths {
		values = setPath(values, strings.Split(p, "."), byPath[p])
	}
	message := map[string]string{}
	for _, content := range event.Service.Outputs {
		segment := segmentName(event.Protocol, content.ProtocolSegmentId)
		value := values[content.ContentVariable.Name]
		if segment == "" || value == nil {
			continue
		}
		m, ok := serialization.Get(content.Serialization)
		if !ok {
			return nil, errors.New("unknown serialization")
		}
		message[segment], err = m.Marshal(value, marshallermodel.ContentVariable{Name: content.ContentVariable.Name})
		if err != nil {
			return nil, err
		}
	}
	return this.marshaller.UnmarshalV2(UnmarshallingV2Request{
		Service: event.Service, Protocol: event.Protocol, CharacteristicId: event.CharacteristicId, Message: message, FunctionId: event.FunctionId, AspectNodes: event.AspectNodes,
	})
}

func timescaleRequest(device models.Device, service models.Service) (result []TimescaleRequest) {
	for _, content := range service.Outputs {
		for _, p := range contentPaths(nil, content.ContentVariable) {
			result = append(result, TimescaleRequest{Device: device, Service: service, ColumnName: p})
		}
	}
	return result
}

// contentPaths lists the leaf paths of a content variable; lists are skipped
func contentPaths(current []string, variable models.ContentVariable) (result []string) {
	if variable.Name == "" || variable.Type == models.List {
		return nil
	}
	if len(variable.SubContentVariables) == 0 {
		return []string{strings.Join(append(append([]string{}, current...), variable.Name), ".")}
	}
	for _, sub := range variable.SubContentVariables {
		result = append(result, contentPaths(append(append([]string{}, current...), variable.Name), sub)...)
	}
	return result
}

func setPath(orig map[string]any, path []string, value any) map[string]any {
	if len(path) == 0 {
		return orig
	}
	if len(path) == 1 {
		orig[path[0]] = value
		return orig
	}
	sub, ok := orig[path[0]]
	if !ok {
		orig[path[0]] = setPath(map[string]any{}, path[1:], value)
		return orig
	}
	subMap, ok := sub.(map[string]any)
	if !ok {
		slog.Default().Warn("expected map in path of last value", "path", strings.Join(path, "."))
		return orig
	}
	orig[path[0]] = setPath(subMap, path[1:], value)
	return orig
}

func segmentName(protocol models.Protocol, id string) string {
	for _, segment := range protocol.ProtocolSegments {
		if segment.Id == id {
			return segment.Name
		}
	}
	return ""
}
