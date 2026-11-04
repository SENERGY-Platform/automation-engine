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
	"slices"
	"strings"

	"github.com/SENERGY-Platform/automation-engine/pkg/script"
)

// ElementScript is a script of a process with the element it runs at.
type ElementScript struct {
	ElementId string
	Source    string
}

// Scripts lists the JavaScript of the process as the engine runs it: script tasks, script values
// of input and output mappings, message names, timer expressions, the conditions of sequence flows
// at their source element, the conditions of device and import events and the joined pre- and
// postscripts of module tasks (EXPR-03, blocklist).
func (this *Process) Scripts() (result []ElementScript) {
	add := func(elementId string, src string) {
		if strings.TrimSpace(src) != "" {
			result = append(result, ElementScript{ElementId: elementId, Source: src})
		}
	}
	var value func(elementId string, v Value)
	value = func(elementId string, v Value) {
		switch v.Kind {
		case ValueScript:
			add(elementId, v.Script)
		case ValueMap:
			for _, entry := range v.Map {
				value(elementId, entry.Value)
			}
		case ValueList:
			for _, item := range v.List {
				value(elementId, item)
			}
		}
	}
	for _, e := range this.Ordered() {
		texts := map[string]any{}
		for _, m := range e.Inputs {
			value(e.Id, m.Value)
			if m.Value.Kind == ValueText {
				texts[m.Name] = m.Value.Text
			}
		}
		for _, m := range e.Outputs {
			value(e.Id, m.Value)
		}
		for _, prefix := range []string{"prescript", "postscript"} {
			add(e.Id, script.JoinParts(prefix, texts))
		}
		add(e.Id, e.Script)
		if e.Message != nil {
			add(e.Id, e.Message.Expression)
		}
		if e.Timer != nil && e.Timer.Expression {
			add(e.Id, e.Timer.Value)
		}
		if e.IsDeviceEvent() {
			add(e.Id, e.Attributes["script"])
		}
	}
	for _, id := range this.flowIds() {
		if f := this.Flows[id]; f.Condition != "" && f.Source != nil {
			add(f.Source.Id, f.Condition)
		}
	}
	return result
}

func (this *Process) flowIds() []string {
	ids := make([]string, 0, len(this.Flows))
	for id := range this.Flows {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}
