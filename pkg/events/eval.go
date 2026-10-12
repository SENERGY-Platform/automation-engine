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

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/script"
	"github.com/SENERGY-Platform/models/go/models"
)

// Unmarshaller converts a device or import message into the characteristic of the event; the
// Marshaller does this as the event-worker did (EVT-04).
type Unmarshaller interface {
	Unmarshal(desc MessageDesc) (value any, err error)
}

// Evaluator applies the evaluation of the event-worker: unmarshalling, then the condition
// script with the value and the variables. The debug reconstruction uses the same code (EVT-10).
type Evaluator struct {
	unmarshaller Unmarshaller
	sandbox      *script.Sandbox
}

func NewEvaluator(unmarshaller Unmarshaller) *Evaluator {
	return &Evaluator{unmarshaller: unmarshaller, sandbox: script.NewSandbox()}
}

type Evaluation struct {
	Value     any
	Triggered bool
	Logs      []string
}

func (this *Evaluator) Evaluate(d model.EventDescription, message map[string]any) (result Evaluation, err error) {
	var service models.Service
	if len(d.Service) > 0 {
		if err = json.Unmarshal(d.Service, &service); err != nil {
			return result, fmt.Errorf("invalid service of event description: %w", err)
		}
	}
	result.Value, err = this.unmarshaller.Unmarshal(MessageDesc{Service: service, CharacteristicId: d.CharacteristicId, FunctionId: d.FunctionId,
		AspectIds: d.AspectIds, Path: d.Path, Message: message})
	if err != nil {
		return result, fmt.Errorf("unable to read the value: %w", err)
	}
	variables := map[string]any{d.ValueVariable: result.Value}
	for key, raw := range d.Variables {
		var value any
		if json.Unmarshal([]byte(raw), &value) != nil {
			value = raw
		}
		variables[key] = value
	}
	run, err := this.sandbox.Run(d.Script, variables)
	result.Logs = run.Logs
	if err != nil {
		return result, fmt.Errorf("condition script: %w", err)
	}
	result.Triggered, err = triggers(run.Value)
	return result, err
}

// triggers reads the result of a condition as the event-worker did: true, "true" or a number >= 1
func triggers(value any) (bool, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case string:
		return strconv.ParseBool(v)
	case float64:
		return v >= 1, nil
	default:
		return false, fmt.Errorf("unknown result type of condition script: %v", v)
	}
}
