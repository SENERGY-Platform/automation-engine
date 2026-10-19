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

import "github.com/SENERGY-Platform/automation-engine/pkg/timer"

const (
	NsBpmn    = "http://www.omg.org/spec/BPMN/20100524/MODEL"
	NsCamunda = "http://camunda.org/schema/1.0/bpmn"
	NsSenergy = "https://senergy.infai.org"
	NsXsi     = "http://www.w3.org/2001/XMLSchema-instance"
)

// ElementType is the local name of a supported BPMN flow node (BPMN-01 to BPMN-16).
type ElementType string

const (
	StartEvent             ElementType = "startEvent"
	EndEvent               ElementType = "endEvent"
	IntermediateCatchEvent ElementType = "intermediateCatchEvent"
	IntermediateThrowEvent ElementType = "intermediateThrowEvent"
	Task                   ElementType = "task"
	ScriptTask             ElementType = "scriptTask"
	ServiceTask            ElementType = "serviceTask"
	ExclusiveGateway       ElementType = "exclusiveGateway"
	ParallelGateway        ElementType = "parallelGateway"
	InclusiveGateway       ElementType = "inclusiveGateway"
	EventBasedGateway      ElementType = "eventBasedGateway"
)

var supportedElementTypes = map[string]ElementType{
	"startEvent":             StartEvent,
	"endEvent":               EndEvent,
	"intermediateCatchEvent": IntermediateCatchEvent,
	"intermediateThrowEvent": IntermediateThrowEvent,
	"task":                   Task,
	"scriptTask":             ScriptTask,
	"serviceTask":            ServiceTask,
	"exclusiveGateway":       ExclusiveGateway,
	"parallelGateway":        ParallelGateway,
	"inclusiveGateway":       InclusiveGateway,
	"eventBasedGateway":      EventBasedGateway,
}

// process children that carry no execution semantics and are read and ignored (BPMN-01)
var ignoredProcessChildren = map[string]bool{
	"laneSet":             true,
	"textAnnotation":      true,
	"association":         true,
	"group":               true,
	"documentation":       true,
	"extensionElements":   true,
	"dataObject":          true,
	"dataObjectReference": true,
	"dataStoreReference":  true,
	"sequenceFlow":        true,
}

type EventKind string

const (
	EventNone    EventKind = ""
	EventMessage EventKind = "message"
	EventTimer   EventKind = "timer"
)

func (this ElementType) IsGateway() bool {
	switch this {
	case ExclusiveGateway, ParallelGateway, InclusiveGateway, EventBasedGateway:
		return true
	}
	return false
}

func (this ElementType) IsEvent() bool {
	switch this {
	case StartEvent, EndEvent, IntermediateCatchEvent, IntermediateThrowEvent:
		return true
	}
	return false
}

type Process struct {
	Id   string
	Name string
	// senergy:description of the process, which the designer of smart services writes
	Description string
	Elements    map[string]*Element
	Flows       map[string]*Flow
	// element ids in document order, for deterministic iteration
	Order []string
}

func (this *Process) Element(id string) *Element {
	return this.Elements[id]
}

func (this *Process) Ordered() []*Element {
	result := make([]*Element, 0, len(this.Order))
	for _, id := range this.Order {
		result = append(result, this.Elements[id])
	}
	return result
}

func (this *Process) StartEvents() (result []*Element) {
	for _, e := range this.Ordered() {
		if e.Type == StartEvent {
			result = append(result, e)
		}
	}
	return result
}

// NoneStartEvent returns the start event used for starts through the API, or nil.
func (this *Process) NoneStartEvent() *Element {
	for _, e := range this.StartEvents() {
		if e.Kind == EventNone {
			return e
		}
	}
	return nil
}

type Element struct {
	Id          string
	Name        string
	Type        ElementType
	Kind        EventKind
	Incoming    []*Flow
	Outgoing    []*Flow
	DefaultFlow *Flow
	Message     *MessageRef
	Timer       *TimerDefinition
	Inputs      []Mapping
	Outputs     []Mapping
	// script task
	Script         string
	ResultVariable string
	scriptRejected bool
	// service task (TASK-00, TASK-07)
	TaskType string
	Retries  *int
	Timeout  string
	// start event form fields (BPMN-02)
	FormFields []FormField
	// senergy:* attributes by local name, e.g. device event descriptions
	Attributes map[string]string
	// camunda:properties of the element
	Properties map[string]string
	// id of the lane the element is in; the deploy dialog groups by lane (BPMN-01)
	Lane string
}

// IsDeviceCommand reports a service task that sends device commands (TASK-01).
func (this *Element) IsDeviceCommand() bool {
	return this.Type == ServiceTask && this.TaskType == "device_command"
}

// IsDeviceEvent reports a message event that waits for device or import data (EVT-03).
func (this *Element) IsDeviceEvent() bool {
	return this.Kind == EventMessage && this.Message != nil && this.Message.DeviceEvent
}

// Key identifies the element for namespace rules, e.g. "startEvent:timer" (NS-06).
func (this *Element) Key() string {
	if this.Kind == EventNone {
		return string(this.Type)
	}
	return string(this.Type) + ":" + string(this.Kind)
}

func (this *Element) HasMappings() bool {
	return len(this.Inputs) > 0 || len(this.Outputs) > 0
}

type MessageRef struct {
	// id of the bpmn:message the event refers to, if any
	Id string
	// literal message name; empty for device events, whose name the engine assigns per deployment
	Name        string
	DeviceEvent bool
	// senergy:messageName, a JavaScript expression for the name, evaluated when the event waits;
	// e.g. the done event of a deployment a module created (SMS-04)
	Expression string
}

type TimerDefinition struct {
	Kind  timer.Kind
	Value string
	// Value is JavaScript and evaluates to the timer value (BPMN-06, EXPR-01)
	Expression bool
	// the parser has already reported the value as not JavaScript
	rejected bool
}

type Flow struct {
	Id     string
	Name   string
	Source *Element
	Target *Element
	// JavaScript condition, empty if the flow is unconditional
	Condition string
	// the parser has already reported the condition as not JavaScript
	conditionRejected bool
}

type ValueKind string

const (
	ValueText   ValueKind = "text"
	ValueScript ValueKind = "script"
	ValueMap    ValueKind = "map"
	ValueList   ValueKind = "list"
)

// Value of an input or output mapping; text and script are explicitly different (EXPR-02).
type Value struct {
	Kind   ValueKind
	Text   string
	Script string
	Map    []MapEntry
	List   []Value
	// the parser has already reported the script as not JavaScript
	rejected bool
}

type MapEntry struct {
	Key   string
	Value Value
}

type Mapping struct {
	Name  string
	Value Value
}

type FormField struct {
	Id           string
	Label        string
	Type         string
	DefaultValue string
	// the field declares a default; tells defaultValue="" from no default at all
	HasDefault bool
	Properties map[string]string
}
