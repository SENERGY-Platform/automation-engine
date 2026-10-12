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

package events

// Decoding of device events on the broker of a gateway (FOG-06), following event-worker v1.2.0
// pkg/eventrepo/fog: the payload of event/{local device id}/{local service id} is the content
// of the "data" segment of the standard connector protocol. It is read with the serialization
// of each output, then cleaned of unknown fields and completed with defaults, which is what
// platform-connector does before data reaches Kafka in the cloud.

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/SENERGY-Platform/marshaller/lib/marshaller/serialization"
	"github.com/SENERGY-Platform/models/go/models"
)

// LocalEventPrefix is the topic prefix of device events on the broker of a gateway.
const LocalEventPrefix = "event/"

// LocalEventTopic is the topic of a service of a device on the broker of a gateway.
func LocalEventTopic(localDeviceId string, localServiceId string) string {
	return LocalEventPrefix + localDeviceId + "/" + localServiceId
}

var ErrUnexpectedType = errors.New("unexpected type")

// LocalProtocol is the protocol of the device connectors of a gateway.
var LocalProtocol = models.Protocol{
	Id:      "urn:infai:ses:protocol:f3a63aeb-187e-4dd9-9ef5-d97a6eb6292b",
	Name:    "standard-connector",
	Handler: "connector",
	ProtocolSegments: []models.ProtocolSegment{
		{Id: "urn:infai:ses:protocol-segment:9956d8b5-46fa-4381-a227-c1df69808997", Name: "metadata"},
		{Id: "urn:infai:ses:protocol-segment:0d211842-cef8-41ec-ab6b-9dbc31bc3a65", Name: "data"},
	},
}

// LocalDataSegment is the segment the payload of a local event is.
const LocalDataSegment = "data"

// DecodeLocalEvent turns the payload of a local event into the message of the service.
func DecodeLocalEvent(payload []byte, service models.Service) (map[string]any, error) {
	message := map[string]string{LocalDataSegment: string(payload)}
	result := map[string]any{}
	fallback, fallbackKnown := serialization.Get(models.JSON)
	for _, output := range service.Outputs {
		marshaller, ok := serialization.Get(output.Serialization)
		if !ok {
			return nil, errors.New("unknown format " + string(output.Serialization))
		}
		for _, segment := range LocalProtocol.ProtocolSegments {
			raw, ok := message[segment.Name]
			if segment.Id != output.ProtocolSegmentId || !ok {
				continue
			}
			value, err := marshaller.Unmarshal(raw, output.ContentVariable)
			if err != nil && fallbackKnown {
				value, err = fallback.Unmarshal(raw, output.ContentVariable)
			}
			if err != nil {
				return nil, err
			}
			result[output.ContentVariable.Name] = value
		}
	}
	for key, value := range result {
		variable, ok := outputVariable(service, key)
		if !ok {
			delete(result, key)
			continue
		}
		cleaned, err := cleanValue(value, variable)
		if err != nil {
			return nil, err
		}
		result[key] = cleaned
	}
	for _, output := range service.Outputs {
		variable := output.ContentVariable
		value, ok := result[variable.Name]
		if !ok {
			result[variable.Name] = variable.Value
			continue
		}
		completed, err := defaultValue(value, variable)
		if err != nil {
			return nil, err
		}
		result[variable.Name] = completed
	}
	return result, nil
}

func outputVariable(service models.Service, name string) (models.ContentVariable, bool) {
	for _, output := range service.Outputs {
		if output.ContentVariable.Name == name {
			return output.ContentVariable, true
		}
	}
	return models.ContentVariable{}, false
}

func findSubVariable(variable models.ContentVariable, name string) (models.ContentVariable, bool) {
	for _, sub := range variable.SubContentVariables {
		if sub.Name == name {
			return sub, true
		}
	}
	return models.ContentVariable{}, false
}

// checkType rejects a value whose JSON type does not fit the variable
func checkType(value any, variable models.ContentVariable) error {
	var is models.Type
	allowed := []models.Type{}
	switch value.(type) {
	case string:
		is, allowed = models.String, []models.Type{models.String}
	case int, int64, float64:
		is, allowed = models.Float, []models.Type{models.Integer, models.Float}
		if _, isFloat := value.(float64); !isFloat {
			is = models.Integer
		}
	case bool:
		is, allowed = models.Boolean, []models.Type{models.Boolean}
	case map[string]any:
		is, allowed = models.Structure, []models.Type{models.Structure}
	case []any:
		is, allowed = models.List, []models.Type{models.List}
	default:
		return nil
	}
	for _, t := range allowed {
		if variable.Type == t {
			return nil
		}
	}
	return fmt.Errorf("%v: %w (is: %v, expected: %v)", variable.Name, ErrUnexpectedType, is, variable.Type)
}

func wildcard(variable models.ContentVariable) (models.ContentVariable, bool) {
	if len(variable.SubContentVariables) > 0 && variable.SubContentVariables[0].Name == "*" {
		return variable.SubContentVariables[0], true
	}
	return models.ContentVariable{}, false
}

// cleanValue removes fields the variable does not describe
func cleanValue(value any, variable models.ContentVariable) (any, error) {
	if err := checkType(value, variable); err != nil {
		return nil, err
	}
	switch v := value.(type) {
	case map[string]any:
		for key, sub := range v {
			subVariable, ok := wildcard(variable)
			if !ok {
				subVariable, ok = findSubVariable(variable, key)
			}
			if !ok {
				delete(v, key)
				continue
			}
			cleaned, err := cleanValue(sub, subVariable)
			if err != nil {
				return nil, err
			}
			v[key] = cleaned
		}
		return v, nil
	case []any:
		result := []any{}
		for i, sub := range v {
			subVariable, ok := wildcard(variable)
			if !ok {
				subVariable, ok = findSubVariable(variable, strconv.Itoa(i))
			}
			if !ok {
				// only the elements up to the first unknown one are kept
				break
			}
			cleaned, err := cleanValue(sub, subVariable)
			if err != nil {
				return nil, err
			}
			result = append(result, cleaned)
		}
		return result, nil
	}
	return value, nil
}

// defaultValue completes missing fields with the values of their variables
func defaultValue(value any, variable models.ContentVariable) (any, error) {
	if err := checkType(value, variable); err != nil {
		return nil, err
	}
	switch v := value.(type) {
	case map[string]any:
		if subVariable, ok := wildcard(variable); ok {
			for key, sub := range v {
				completed, err := defaultValue(sub, subVariable)
				if err != nil {
					return nil, err
				}
				v[key] = completed
			}
			return v, nil
		}
		for _, subVariable := range variable.SubContentVariables {
			sub, ok := v[subVariable.Name]
			if !ok {
				v[subVariable.Name] = subVariable.Value
				continue
			}
			completed, err := defaultValue(sub, subVariable)
			if err != nil {
				return nil, err
			}
			v[subVariable.Name] = completed
		}
		return v, nil
	case []any:
		if subVariable, ok := wildcard(variable); ok {
			for i, sub := range v {
				completed, err := defaultValue(sub, subVariable)
				if err != nil {
					return nil, err
				}
				v[i] = completed
			}
			return v, nil
		}
		for _, subVariable := range variable.SubContentVariables {
			index, err := strconv.Atoi(subVariable.Name)
			if err != nil {
				return nil, fmt.Errorf("list variable name expected to be * or a number, got %v in %v", subVariable.Name, variable.Name)
			}
			if index >= len(v) {
				v = append(v, subVariable.Value)
				continue
			}
			completed, err := defaultValue(v[index], subVariable)
			if err != nil {
				return nil, err
			}
			v[index] = completed
		}
		return v, nil
	}
	return value, nil
}

// localTopicIds splits event/{device}/{service}
func localTopicIds(topic string) (deviceId string, serviceId string, ok bool) {
	parts := strings.Split(topic, "/")
	if len(parts) != 3 || parts[0]+"/" != LocalEventPrefix {
		return "", "", false
	}
	return parts[1], parts[2], true
}
