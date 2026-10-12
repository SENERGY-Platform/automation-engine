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

// The input handling follows external-task-worker v0.1.2 lib/command.go and lib/worker.go
// (setVarOnPath), Apache-2.0, adapted from camunda variables to task inputs.

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/SENERGY-Platform/automation-engine/pkg/devices"
	"github.com/SENERGY-Platform/models/go/models"

	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// command builds the command of a task: the payload of the model, the device selection of the
// deployment (DEP-05), then the runtime inputs overwrite, inputs and incident (TASK-09)
func (this *Type) command(task engine.Task, token devices.Token) (command Command, err error) {
	if incident, ok := task.Input["incident"].(string); ok {
		return command, &engine.PermanentError{Message: "user triggered incident: " + incident}
	}
	payload, ok := task.Input["payload"]
	if !ok {
		return command, &engine.PermanentError{Message: "device command needs the input payload"}
	}
	if err = decodeJson(payload, &command); err != nil {
		return command, &engine.PermanentError{Message: "invalid task format (json): " + err.Error()}
	}
	command.SetAspects()
	if task.Selection != nil {
		if err = this.applySelection(&command, *task.Selection, token); err != nil {
			return command, err
		}
	}
	if overwrite, ok := task.Input["overwrite"]; ok {
		var o Overwrite
		if err = decodeJson(overwrite, &o); err != nil {
			return command, &engine.PermanentError{Message: "invalid overwrite: " + err.Error()}
		}
		command.Service, command.Device, command.Protocol = o.Service, o.Device, o.Protocol
		command.ServiceId, command.DeviceId, command.ProtocolId = o.ServiceId, o.DeviceId, o.ProtocolId
	}
	setInputs(&command, task.Input, this.logger)
	if len(command.Aspects) > 0 {
		command.Aspects, err = this.aspects.Resolve(token, command.Aspects)
		if err != nil {
			return command, err
		}
	}
	return command, nil
}

// decodeJson reads a value that is either a JSON string or already decoded JSON
func decodeJson(value any, target any) error {
	raw, ok := value.(string)
	if !ok {
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		raw = string(b)
	}
	return json.Unmarshal([]byte(raw), target)
}

func (this *Type) applySelection(command *Command, selection model.Selection, token devices.Token) error {
	if selection.DeviceGroupId != "" {
		command.DeviceGroupId = selection.DeviceGroupId
		command.DeviceId, command.ServiceId = "", ""
	}
	if selection.DeviceId != "" {
		command.DeviceGroupId = ""
		command.DeviceId = selection.DeviceId
		command.ServiceId = selection.ServiceId
	}
	if selection.PreferEvent != nil {
		command.PreferEvent = *selection.PreferEvent
	}
	if selection.Path != nil {
		if isControllingFunction(command.Function) {
			command.InputPaths = []string{selection.Path.Path}
		} else {
			command.OutputPath = selection.Path.Path
		}
		configurables := []ConfigurableV2{}
		for _, c := range selection.Path.Configurables {
			nodes, err := this.aspects.ResolveIds(token, c.AspectNodeIds)
			if err != nil {
				return err
			}
			configurable := ConfigurableV2{Path: c.Path, CharacteristicId: c.CharacteristicId, AspectNodes: nodes, FunctionId: c.FunctionId, Value: c.Value, Type: c.Type}
			if len(nodes) > 0 {
				configurable.AspectNode = nodes[0]
			}
			configurables = append(configurables, configurable)
		}
		command.ConfigurablesV2 = configurables
		// paths are understood from version 3 on, which keeps the result a list
		if command.Version < 3 {
			command.Version = 3
		}
	}
	return nil
}

// setInputs applies the inputs "inputs" and "inputs.<path>" to the command input; string values
// are read as JSON where possible. A parameter that does not fit is ignored, as before.
func setInputs(command *Command, input map[string]any, logger interface{ Warn(string, ...any) }) {
	for key, value := range input {
		path := strings.SplitN(key, ".", 2)
		if path[0] != "inputs" {
			continue
		}
		if s, ok := value.(string); ok {
			var parsed any
			if json.Unmarshal([]byte(s), &parsed) == nil {
				value = parsed
			}
		}
		var err error
		if len(path) == 2 && path[1] != "" {
			_, err = setVarOnPath(command.Input, strings.Split(path[1], "."), value)
		} else {
			command.Input, err = setVarOnPath(command.Input, nil, value)
		}
		if err != nil {
			logger.Warn("ignore device command input", "input", key, "error", err)
		}
	}
}

// setVarOnPath converts value to the type of the existing element at path and sets it
func setVarOnPath(element any, path []string, value any) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("unable to set input at path: %v", r)
		}
	}()
	if len(path) == 0 {
		return convert(element, value)
	}
	key := path[0]
	path = path[1:]
	result = element
	switch {
	case reflect.TypeOf(result).Kind() == reflect.Map:
		keyVal := reflect.ValueOf(key)
		sub := reflect.ValueOf(result).MapIndex(keyVal).Interface()
		val, err := setVarOnPath(sub, path, value)
		if err != nil {
			return result, err
		}
		reflect.ValueOf(result).SetMapIndex(keyVal, reflect.ValueOf(val))
	case reflect.TypeOf(result).Kind() == reflect.Slice:
		index, err := strconv.Atoi(key)
		if err != nil {
			return result, err
		}
		sub := reflect.ValueOf(result).Index(index).Interface()
		val, err := setVarOnPath(sub, path, value)
		if err != nil {
			return result, err
		}
		reflect.ValueOf(result).Index(index).Set(reflect.ValueOf(val))
	default:
		err = fmt.Errorf("unknown element type %T", element)
	}
	return result, err
}

// convert keeps the type of the element the value replaces; numbers are float64 after JSON
func convert(element any, value any) (any, error) {
	switch v := value.(type) {
	case string:
		switch element.(type) {
		case string:
			return v, nil
		case bool:
			return strconv.ParseBool(v)
		case float64:
			return strconv.ParseFloat(v, 64)
		}
	case bool:
		switch element.(type) {
		case string:
			return strconv.FormatBool(v), nil
		case bool:
			return v, nil
		case float64:
			if v {
				return 1.0, nil
			}
			return 0.0, nil
		}
	case float64:
		switch element.(type) {
		case string:
			return strconv.FormatFloat(v, 'E', -1, 64), nil
		case bool:
			return v >= 1, nil
		case float64:
			return v, nil
		}
	}
	if element == nil {
		return value, nil
	}
	kind := reflect.TypeOf(element).Kind()
	if kind == reflect.Map || kind == reflect.Slice {
		return value, nil
	}
	return value, errors.New(fmt.Sprintf("unable to convert %T to %T", value, element))
}

func isControllingFunction(function models.Function) bool {
	return function.RdfType == models.SES_ONTOLOGY_CONTROLLING_FUNCTION || strings.HasPrefix(function.Id, models.CONTROLLING_FUNCTION_PREFIX)
}

func isMeasuringFunctionId(id string) bool {
	return strings.HasPrefix(id, models.MEASURING_FUNCTION_PREFIX)
}
