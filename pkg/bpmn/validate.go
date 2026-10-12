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

package bpmn

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/script"
	"github.com/SENERGY-Platform/automation-engine/pkg/timer"
)

// Rules restrict the supported subset per namespace (NS-03, NS-06).
type Rules struct {
	// element keys like "startEvent" or "startEvent:timer"; a rule on the type covers all kinds
	Elements  model.ListRule
	TaskTypes model.ListRule
	// reports whether the engine implements a task type; nil accepts every type
	TaskTypeAvailable func(taskType string) bool
	// reject cycles in the sequence flow, so that every run ends (SMS-03)
	ForbidCycles bool
}

// ParseAndValidate combines Parse and Validate; issues block a deploy and are hints on a model save.
func ParseAndValidate(xml string, rules Rules) (*Process, []model.Issue, error) {
	process, issues, err := Parse(xml)
	if err != nil {
		return nil, nil, err
	}
	return process, append(issues, Validate(process, rules)...), nil
}

func permits(rule model.ListRule, values ...string) bool {
	if len(rule.Allow) > 0 && !slices.ContainsFunc(values, func(v string) bool { return slices.Contains(rule.Allow, v) }) {
		return false
	}
	return !slices.ContainsFunc(values, func(v string) bool { return slices.Contains(rule.Deny, v) })
}

func Validate(p *Process, rules Rules) (issues []model.Issue) {
	add := func(elementId string, format string, args ...any) {
		issues = append(issues, model.Issue{ElementId: elementId, Message: fmt.Sprintf(format, args...)})
	}
	checkScript := func(elementId string, what string, src string) {
		if strings.TrimSpace(src) == "" {
			add(elementId, "%v is empty", what)
			return
		}
		if err := script.Check(src); err != nil {
			add(elementId, "%v: %v", what, err)
		}
	}
	var checkValue func(elementId string, mapping string, v Value)
	checkValue = func(elementId string, mapping string, v Value) {
		switch v.Kind {
		case ValueText:
			if strings.Contains(v.Text, "${") {
				add(elementId, "mapping %q uses a JUEL expression; use a JavaScript script, e.g. a template literal", mapping)
			}
		case ValueScript:
			if !v.rejected {
				checkScript(elementId, fmt.Sprintf("mapping %q", mapping), v.Script)
			}
		case ValueMap:
			for _, entry := range v.Map {
				checkValue(elementId, mapping+"."+entry.Key, entry.Value)
			}
		case ValueList:
			for _, item := range v.List {
				checkValue(elementId, mapping, item)
			}
		}
	}

	noneStarts := 0
	starts := 0
	for _, e := range p.Ordered() {
		if !permits(rules.Elements, string(e.Type), e.Key()) {
			add(e.Id, "%v is not allowed in this namespace", e.Key())
		}
		for _, m := range e.Inputs {
			if m.Name == "" {
				add(e.Id, "input mapping without name")
			}
			checkValue(e.Id, m.Name, m.Value)
		}
		for _, m := range e.Outputs {
			if m.Name == "" {
				add(e.Id, "output mapping without name")
			}
			checkValue(e.Id, m.Name, m.Value)
		}
		if e.Message != nil && e.Message.Expression != "" {
			if e.Type == StartEvent {
				add(e.Id, "a message start event needs a literal message name, senergy:messageName is for events that wait")
			} else {
				checkScript(e.Id, "message name", e.Message.Expression)
			}
		}
		if e.Timer != nil {
			if e.Timer.Expression {
				checkScript(e.Id, "timer expression", e.Timer.Value)
			} else if !e.Timer.rejected {
				if err := timer.Validate(e.Timer.Kind, e.Timer.Value); err != nil {
					add(e.Id, "timer: %v", err)
				}
			}
		}
		for _, flow := range e.Outgoing {
			if flow.Condition != "" && !flow.conditionRejected {
				checkScript(flow.Id, "condition", flow.Condition)
			}
		}
		switch e.Type {
		case StartEvent:
			starts++
			if e.Kind == EventNone {
				noneStarts++
			}
			if len(e.Incoming) > 0 {
				add(e.Id, "a start event must not have incoming flows")
			}
		case IntermediateCatchEvent:
			if e.Kind == EventNone {
				add(e.Id, "intermediate catch event needs a message or timer definition")
			}
		case IntermediateThrowEvent, EndEvent:
			if e.Kind != EventNone {
				add(e.Id, "%v with %v definition is not supported", e.Type, e.Kind)
			}
		case ScriptTask:
			if !e.scriptRejected {
				checkScript(e.Id, "script", e.Script)
			}
		case ServiceTask:
			switch {
			case e.TaskType == "":
				add(e.Id, "service task needs a task type (senergy:taskType)")
			case !permits(rules.TaskTypes, e.TaskType):
				add(e.Id, "task type %q is not allowed in this namespace", e.TaskType)
			case rules.TaskTypeAvailable != nil && !rules.TaskTypeAvailable(e.TaskType):
				add(e.Id, "task type %q is not available in this engine", e.TaskType)
			}
		case EventBasedGateway:
			if len(e.Outgoing) < 2 {
				add(e.Id, "event-based gateway needs at least two outgoing flows")
			}
			for _, flow := range e.Outgoing {
				if flow.Target.Type != IntermediateCatchEvent || flow.Target.Kind == EventNone {
					add(e.Id, "event-based gateway must lead to message or timer catch events, %v does not", flow.Target.Id)
				}
				if flow.Condition != "" {
					add(flow.Id, "flows after an event-based gateway must not have conditions")
				}
			}
		case ParallelGateway:
			for _, flow := range e.Outgoing {
				if flow.Condition != "" {
					add(flow.Id, "flows after a parallel gateway must not have conditions")
				}
			}
		}
		if e.Type != StartEvent && len(e.Incoming) == 0 {
			add(e.Id, "%v is not reachable, it has no incoming flow", e.Type)
		}
	}
	if starts == 0 {
		add(p.Id, "process needs a start event")
	}
	if noneStarts > 1 {
		add(p.Id, "process must not have more than one start event without definition")
	}
	if rules.ForbidCycles {
		for _, flow := range Cycles(p) {
			add(flow.Id, "sequence flow %v closes a loop back to %v; loops are not allowed in this namespace", flow.Id, flow.Target.Id)
		}
	}
	return issues
}

// Cycles returns one flow per loop of the sequence flow: the flow that leads back to an element
// on the current path of a depth-first search in document order.
func Cycles(p *Process) (result []*Flow) {
	const (
		unvisited = iota
		onPath
		done
	)
	state := map[string]int{}
	var visit func(e *Element)
	visit = func(e *Element) {
		state[e.Id] = onPath
		for _, flow := range e.Outgoing {
			if flow.Target == nil {
				continue
			}
			switch state[flow.Target.Id] {
			case onPath:
				result = append(result, flow)
			case unvisited:
				visit(flow.Target)
			}
		}
		state[e.Id] = done
	}
	for _, id := range p.Order {
		if e := p.Element(id); e != nil && state[id] == unvisited {
			visit(e)
		}
	}
	return result
}

// StartParameters lists the form fields of a start event without those marked ignore_on_start (RUN-02).
func StartParameters(e *Element) []model.StartParameter {
	result := []model.StartParameter{}
	if e == nil {
		return result
	}
	for _, field := range e.FormFields {
		if field.Properties["ignore_on_start"] == "true" {
			continue
		}
		result = append(result, model.StartParameter{
			Id:         field.Id,
			Label:      field.Label,
			Type:       field.Type,
			Default:    field.DefaultValue,
			Properties: field.Properties,
		})
	}
	return result
}

// ApplyParameters sets the values a deployment overrides per element: input mappings by name
// as text, senergy:variables of device events, timer definitions and defaults of start form
// fields (DEP-01). Overrides of elements, inputs or fields the process does not have are issues.
func ApplyParameters(p *Process, parameters map[string]model.ElementParameters) (issues []model.Issue) {
	add := func(elementId string, format string, args ...any) {
		issues = append(issues, model.Issue{ElementId: elementId, Message: fmt.Sprintf(format, args...)})
	}
	for _, elementId := range slices.Sorted(maps.Keys(parameters)) {
		params := parameters[elementId]
		e := p.Element(elementId)
		if e == nil {
			add(elementId, "parameters for an element the process does not have")
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(params.Inputs)) {
			i := slices.IndexFunc(e.Inputs, func(m Mapping) bool { return m.Name == name })
			if i < 0 {
				add(elementId, "the element has no input %q", name)
				continue
			}
			e.Inputs[i].Value = Value{Kind: ValueText, Text: params.Inputs[name]}
		}
		if len(params.Variables) > 0 {
			if !e.IsDeviceEvent() {
				add(elementId, "variables can only be set for device events")
			} else {
				variables := map[string]string{}
				if raw := e.Attributes["variables"]; raw != "" {
					_ = json.Unmarshal([]byte(raw), &variables)
				}
				maps.Copy(variables, params.Variables)
				raw, _ := json.Marshal(variables)
				e.Attributes["variables"] = string(raw)
			}
		}
		if params.Timer != "" {
			if e.Timer == nil {
				add(elementId, "the element has no timer")
			} else if err := timer.Validate(e.Timer.Kind, params.Timer); err != nil {
				add(elementId, "timer: %v", err)
			} else {
				e.Timer.Value, e.Timer.Expression = params.Timer, false
			}
		}
		for _, id := range slices.Sorted(maps.Keys(params.Defaults)) {
			i := slices.IndexFunc(e.FormFields, func(f FormField) bool { return f.Id == id })
			if i < 0 {
				add(elementId, "the element has no form field %q", id)
				continue
			}
			e.FormFields[i].DefaultValue = params.Defaults[id]
		}
	}
	return issues
}
