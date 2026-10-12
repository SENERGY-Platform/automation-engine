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
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/timer"
	"github.com/beevik/etree"
)

// senergy attributes that mark a message event as device or import event
var deviceEventAttributes = []string{"function", "aspect", "characteristic", "script", "value_variable_name", "qos", "device", "service", "path", "import"}

// Parse reads BPMN XML into a process graph. It returns an error only if no process can be
// read at all. Everything outside the supported subset is reported as an issue that names
// the element (BPMN-18); Validate adds the semantic checks.
func Parse(xml string) (process *Process, issues []model.Issue, err error) {
	doc := etree.NewDocument()
	if err = doc.ReadFromString(xml); err != nil {
		return nil, nil, fmt.Errorf("invalid XML: %w", err)
	}
	root := doc.Root()
	if root == nil || !is(root, NsBpmn, "definitions") {
		return nil, nil, errors.New("root element must be bpmn:definitions")
	}
	p := &parser{messages: map[string]string{}}
	var processes []*etree.Element
	for _, child := range root.ChildElements() {
		switch {
		case is(child, NsBpmn, "process"):
			processes = append(processes, child)
		case is(child, NsBpmn, "message"):
			p.messages[child.SelectAttrValue("id", "")] = child.SelectAttrValue("name", "")
		}
	}
	var executable []*etree.Element
	for _, proc := range processes {
		if proc.SelectAttrValue("isExecutable", "") == "true" {
			executable = append(executable, proc)
		}
	}
	switch {
	case len(executable) == 1:
	case len(executable) > 1:
		for _, proc := range executable[1:] {
			p.issue(proc.SelectAttrValue("id", ""), "a model must contain exactly one executable process")
		}
	case len(processes) == 1:
		executable = processes
		p.issue(processes[0].SelectAttrValue("id", ""), "process is not executable (isExecutable=\"true\" missing)")
	default:
		return nil, nil, errors.New("model contains no executable process")
	}
	return p.process(executable[0]), p.issues, nil
}

type parser struct {
	messages map[string]string
	issues   []model.Issue
}

func (this *parser) issue(elementId string, format string, args ...any) {
	this.issues = append(this.issues, model.Issue{ElementId: elementId, Message: fmt.Sprintf(format, args...)})
}

func is(e *etree.Element, ns string, local string) bool {
	return e.Tag == local && e.NamespaceURI() == ns
}

func attr(e *etree.Element, ns string, local string) (string, bool) {
	for i := range e.Attr {
		a := &e.Attr[i]
		if a.Key == local && a.NamespaceURI() == ns {
			return a.Value, true
		}
	}
	return "", false
}

func attrValue(e *etree.Element, ns string, local string) string {
	v, _ := attr(e, ns, local)
	return v
}

// innerText concatenates all text and CDATA content of e, ignoring child elements and comments
func innerText(e *etree.Element) string {
	var b strings.Builder
	for _, t := range e.Child {
		if c, ok := t.(*etree.CharData); ok {
			b.WriteString(c.Data)
		}
	}
	return b.String()
}

func (this *parser) process(e *etree.Element) *Process {
	result := &Process{
		Id:       e.SelectAttrValue("id", ""),
		Name:     e.SelectAttrValue("name", ""),
		Elements: map[string]*Element{},
		Flows:    map[string]*Flow{},
	}
	var flows []*etree.Element
	lanes := map[string]string{}
	for _, child := range e.ChildElements() {
		if is(child, NsBpmn, "laneSet") {
			collectLanes(child, lanes)
		}
	}
	for _, child := range e.ChildElements() {
		if child.NamespaceURI() != NsBpmn {
			continue
		}
		if child.Tag == "sequenceFlow" {
			flows = append(flows, child)
			continue
		}
		if ignoredProcessChildren[child.Tag] {
			continue
		}
		id := child.SelectAttrValue("id", "")
		elementType, ok := supportedElementTypes[child.Tag]
		if !ok {
			this.issue(id, "element type %v is not supported", child.Tag)
			continue
		}
		if id == "" {
			this.issue("", "%v without id", child.Tag)
			continue
		}
		if _, exists := result.Elements[id]; exists {
			this.issue(id, "duplicate element id")
			continue
		}
		result.Elements[id] = this.element(child, elementType)
		result.Elements[id].Lane = lanes[id]
		result.Order = append(result.Order, id)
	}
	for _, f := range flows {
		this.flow(result, f)
	}
	for _, id := range result.Order {
		element := result.Elements[id]
		if def := element.Attributes["\x00default"]; def != "" {
			delete(element.Attributes, "\x00default")
			flow := result.Flows[def]
			if flow == nil || flow.Source != element {
				this.issue(id, "default flow %v is not an outgoing flow of this element", def)
			} else {
				element.DefaultFlow = flow
			}
		}
	}
	return result
}

// collectLanes maps element ids to the innermost lane that references them
func collectLanes(laneSet *etree.Element, result map[string]string) {
	for _, lane := range laneSet.ChildElements() {
		if !is(lane, NsBpmn, "lane") {
			continue
		}
		laneId := lane.SelectAttrValue("id", "")
		for _, child := range lane.ChildElements() {
			switch {
			case is(child, NsBpmn, "flowNodeRef"):
				if _, nested := result[strings.TrimSpace(innerText(child))]; !nested {
					result[strings.TrimSpace(innerText(child))] = laneId
				}
			case is(child, NsBpmn, "childLaneSet"):
				collectLanes(child, result)
			}
		}
	}
}

func (this *parser) flow(p *Process, e *etree.Element) {
	id := e.SelectAttrValue("id", "")
	source := p.Elements[e.SelectAttrValue("sourceRef", "")]
	target := p.Elements[e.SelectAttrValue("targetRef", "")]
	if source == nil || target == nil {
		this.issue(id, "sequence flow references an unknown or unsupported element")
		return
	}
	flow := &Flow{Id: id, Name: e.SelectAttrValue("name", ""), Source: source, Target: target}
	for _, child := range e.ChildElements() {
		if is(child, NsBpmn, "conditionExpression") {
			flow.Condition, flow.conditionRejected = this.expression(id, child, "condition")
		}
	}
	p.Flows[id] = flow
	source.Outgoing = append(source.Outgoing, flow)
	target.Incoming = append(target.Incoming, flow)
}

// expression reads a tFormalExpression; only JavaScript is accepted (EXPR-01)
func (this *parser) expression(elementId string, e *etree.Element, what string) (body string, rejected bool) {
	if _, ok := attr(e, NsCamunda, "resource"); ok {
		this.issue(elementId, "%v from an external resource is not supported", what)
		return "", true
	}
	body = strings.TrimSpace(innerText(e))
	language := strings.ToLower(e.SelectAttrValue("language", ""))
	switch {
	case language == "javascript" || language == "js":
		return body, false
	case language == "" && strings.Contains(body, "${"):
		this.issue(elementId, "%v uses a JUEL expression; only JavaScript is supported (language=\"javascript\")", what)
	case language == "":
		this.issue(elementId, "%v needs language=\"javascript\"", what)
	default:
		this.issue(elementId, "%v uses language %q; only JavaScript is supported", what, language)
	}
	return body, true
}

func (this *parser) element(e *etree.Element, elementType ElementType) *Element {
	result := &Element{
		Id:         e.SelectAttrValue("id", ""),
		Name:       e.SelectAttrValue("name", ""),
		Type:       elementType,
		Attributes: map[string]string{},
		Properties: map[string]string{},
	}
	if def := e.SelectAttrValue("default", ""); def != "" {
		// resolved after all flows are known
		result.Attributes["\x00default"] = def
	}
	for _, a := range e.Attr {
		if a.NamespaceURI() == NsSenergy {
			result.Attributes[a.Key] = a.Value
		}
	}
	for _, unsupported := range []string{"class", "delegateExpression", "expression", "type", "topic"} {
		if v, ok := attr(e, NsCamunda, unsupported); ok {
			if unsupported == "type" || unsupported == "topic" {
				this.issue(result.Id, "camunda:%v=%q is not supported; use senergy:taskType", unsupported, v)
			} else {
				this.issue(result.Id, "camunda:%v is not supported", unsupported)
			}
		}
	}
	if elementType == ServiceTask {
		result.TaskType = attrValue(e, NsSenergy, "taskType")
		delete(result.Attributes, "taskType")
		if v, ok := attr(e, NsSenergy, "retries"); ok {
			delete(result.Attributes, "retries")
			retries, err := strconv.Atoi(v)
			if err != nil || retries < 0 {
				this.issue(result.Id, "senergy:retries must be a non-negative integer")
			} else {
				result.Retries = &retries
			}
		}
		if v, ok := attr(e, NsSenergy, "timeout"); ok {
			delete(result.Attributes, "timeout")
			if _, err := timer.ParseDuration(v); err != nil {
				this.issue(result.Id, "senergy:timeout: %v", err)
			}
			result.Timeout = v
		}
	}
	if elementType == ScriptTask {
		result.ResultVariable = attrValue(e, NsCamunda, "resultVariable")
		format := strings.ToLower(e.SelectAttrValue("scriptFormat", ""))
		if format != "javascript" && format != "js" {
			this.issue(result.Id, "script format %q is not supported; only JavaScript is supported", e.SelectAttrValue("scriptFormat", ""))
			result.scriptRejected = true
		}
		if _, ok := attr(e, NsCamunda, "resource"); ok {
			this.issue(result.Id, "scripts from an external resource are not supported")
			result.scriptRejected = true
		}
	}
	for _, child := range e.ChildElements() {
		switch {
		case is(child, NsBpmn, "extensionElements"):
			this.extensions(result, child)
		case is(child, NsBpmn, "script") && elementType == ScriptTask:
			result.Script = innerText(child)
		case is(child, NsBpmn, "messageEventDefinition"):
			this.eventDefinition(result, EventMessage)
			this.message(result, child)
		case is(child, NsBpmn, "timerEventDefinition"):
			this.eventDefinition(result, EventTimer)
			this.timer(result, child)
		case child.NamespaceURI() == NsBpmn && strings.HasSuffix(child.Tag, "EventDefinition"):
			this.issue(result.Id, "%v is not supported", child.Tag)
		case is(child, NsBpmn, "multiInstanceLoopCharacteristics"), is(child, NsBpmn, "standardLoopCharacteristics"):
			this.issue(result.Id, "loop and multi-instance characteristics are not supported")
		}
	}
	return result
}

func (this *parser) eventDefinition(e *Element, kind EventKind) {
	if !e.Type.IsEvent() {
		this.issue(e.Id, "event definition on %v is not supported", e.Type)
		return
	}
	if e.Kind != EventNone {
		this.issue(e.Id, "multiple event definitions are not supported")
		return
	}
	e.Kind = kind
}

func (this *parser) message(e *Element, definition *etree.Element) {
	ref := &MessageRef{}
	if expression := e.Attributes["messageName"]; expression != "" {
		ref.Expression = expression
		e.Message = ref
		return
	}
	for _, a := range deviceEventAttributes {
		if _, ok := e.Attributes[a]; ok {
			ref.DeviceEvent = true
		}
	}
	if !ref.DeviceEvent {
		messageId := definition.SelectAttrValue("messageRef", "")
		if messageId == "" {
			// without a message reference, the event is addressed by its engine-assigned name
			ref.DeviceEvent = true
		} else {
			name, ok := this.messages[messageId]
			switch {
			case !ok:
				this.issue(e.Id, "message %v is not defined", messageId)
			case name == "":
				this.issue(e.Id, "message %v has no name", messageId)
			case strings.Contains(name, "${"):
				this.issue(e.Id, "message name %q uses an expression; message names are literal", name)
			}
			ref.Name = name
		}
	}
	e.Message = ref
}

func (this *parser) timer(e *Element, definition *etree.Element) {
	for _, child := range definition.ChildElements() {
		if child.NamespaceURI() != NsBpmn {
			continue
		}
		var kind timer.Kind
		switch child.Tag {
		case "timeDuration":
			kind = timer.KindDuration
		case "timeDate":
			kind = timer.KindDate
		case "timeCycle":
			kind = timer.KindCycle
		default:
			continue
		}
		if e.Timer != nil {
			this.issue(e.Id, "timer must have exactly one of timeDuration, timeDate and timeCycle")
			return
		}
		value := strings.TrimSpace(innerText(child))
		language := strings.ToLower(child.SelectAttrValue("language", ""))
		definition := &TimerDefinition{Kind: kind, Value: value}
		switch {
		case language == "javascript" || language == "js":
			definition.Expression = true
		case language != "":
			this.issue(e.Id, "timer uses language %q; only JavaScript is supported", language)
			definition.rejected = true
		case strings.Contains(value, "${"):
			this.issue(e.Id, "timer uses a JUEL expression; use language=\"javascript\"")
			definition.rejected = true
		}
		e.Timer = definition
	}
	if e.Timer == nil {
		this.issue(e.Id, "timer has no definition")
	}
}

func (this *parser) extensions(e *Element, ext *etree.Element) {
	for _, child := range ext.ChildElements() {
		if child.NamespaceURI() != NsCamunda {
			continue
		}
		switch child.Tag {
		case "inputOutput":
			for _, param := range child.ChildElements() {
				switch {
				case is(param, NsCamunda, "inputParameter"):
					e.Inputs = append(e.Inputs, Mapping{Name: param.SelectAttrValue("name", ""), Value: this.value(e.Id, param)})
				case is(param, NsCamunda, "outputParameter"):
					e.Outputs = append(e.Outputs, Mapping{Name: param.SelectAttrValue("name", ""), Value: this.value(e.Id, param)})
				}
			}
		case "formData":
			for _, field := range child.ChildElements() {
				if is(field, NsCamunda, "formField") {
					e.FormFields = append(e.FormFields, formField(field))
				}
			}
		case "properties":
			for _, prop := range child.ChildElements() {
				if is(prop, NsCamunda, "property") {
					e.Properties[prop.SelectAttrValue("name", "")] = prop.SelectAttrValue("value", "")
				}
			}
		case "connector":
			this.issue(e.Id, "camunda:connector is not supported; use the task types notification or http")
		case "executionListener", "taskListener":
			this.issue(e.Id, "listeners are not supported")
		}
	}
}

func formField(e *etree.Element) FormField {
	result := FormField{
		Id:           e.SelectAttrValue("id", ""),
		Label:        e.SelectAttrValue("label", ""),
		Type:         e.SelectAttrValue("type", "string"),
		DefaultValue: e.SelectAttrValue("defaultValue", ""),
		Properties:   map[string]string{},
	}
	for _, child := range e.ChildElements() {
		if is(child, NsCamunda, "properties") {
			for _, prop := range child.ChildElements() {
				if is(prop, NsCamunda, "property") {
					result.Properties[prop.SelectAttrValue("id", "")] = prop.SelectAttrValue("value", "")
				}
			}
		}
	}
	return result
}

func (this *parser) value(elementId string, e *etree.Element) Value {
	for _, child := range e.ChildElements() {
		if child.NamespaceURI() != NsCamunda {
			continue
		}
		switch child.Tag {
		case "script":
			result := Value{Kind: ValueScript, Script: innerText(child)}
			format := strings.ToLower(child.SelectAttrValue("scriptFormat", ""))
			if format != "javascript" && format != "js" {
				this.issue(elementId, "mapping %q uses script format %q; only JavaScript is supported", e.SelectAttrValue("name", ""), child.SelectAttrValue("scriptFormat", ""))
				result.rejected = true
			}
			if _, ok := attr(child, NsCamunda, "resource"); ok || child.SelectAttrValue("resource", "") != "" {
				this.issue(elementId, "mapping %q uses a script from an external resource", e.SelectAttrValue("name", ""))
				result.rejected = true
			}
			return result
		case "map":
			result := Value{Kind: ValueMap}
			for _, entry := range child.ChildElements() {
				if is(entry, NsCamunda, "entry") {
					result.Map = append(result.Map, MapEntry{Key: entry.SelectAttrValue("key", ""), Value: this.value(elementId, entry)})
				}
			}
			return result
		case "list":
			result := Value{Kind: ValueList}
			for _, item := range child.ChildElements() {
				if is(item, NsCamunda, "value") {
					result.List = append(result.List, this.value(elementId, item))
				}
			}
			return result
		}
	}
	return Value{Kind: ValueText, Text: innerText(e)}
}
