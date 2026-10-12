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

// Package bpmntest builds BPMN documents for tests.
package bpmntest

import (
	"fmt"
	"html"
	"strings"
)

// Process wraps elements into a definitions document with one executable process.
// Parts that start with "<bpmn:message " are placed on the definitions level.
func Process(parts ...string) string {
	var defs, body strings.Builder
	for _, part := range parts {
		if strings.HasPrefix(part, "<bpmn:message ") {
			defs.WriteString(part)
		} else {
			body.WriteString(part)
		}
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:camunda="http://camunda.org/schema/1.0/bpmn" xmlns:senergy="https://senergy.infai.org" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" id="Definitions_1">` +
		defs.String() + `<bpmn:process id="test_process" name="test process" isExecutable="true">` + body.String() + `</bpmn:process></bpmn:definitions>`
}

func Flow(from, to string) string {
	return fmt.Sprintf(`<bpmn:sequenceFlow id="f_%v_%v" sourceRef="%v" targetRef="%v"/>`, from, to, from, to)
}

// Cond is a sequence flow with a JavaScript condition.
func Cond(from, to, condition string) string {
	return fmt.Sprintf(`<bpmn:sequenceFlow id="f_%v_%v" sourceRef="%v" targetRef="%v"><bpmn:conditionExpression xsi:type="bpmn:tFormalExpression" language="javascript">%v</bpmn:conditionExpression></bpmn:sequenceFlow>`, from, to, from, to, html.EscapeString(condition))
}

// FlowId returns the id Flow and Cond give a flow.
func FlowId(from, to string) string {
	return "f_" + from + "_" + to
}

func Start(id string) string {
	return fmt.Sprintf(`<bpmn:startEvent id="%v"/>`, id)
}

func End(id string) string {
	return fmt.Sprintf(`<bpmn:endEvent id="%v"/>`, id)
}

func Gateway(kind string, id string, defaultFlow string) string {
	if defaultFlow != "" {
		return fmt.Sprintf(`<bpmn:%vGateway id="%v" default="%v"/>`, kind, id, defaultFlow)
	}
	return fmt.Sprintf(`<bpmn:%vGateway id="%v"/>`, kind, id)
}

func Script(id string, resultVariable string, src string) string {
	return fmt.Sprintf(`<bpmn:scriptTask id="%v" scriptFormat="javascript" camunda:resultVariable="%v"><bpmn:script>%v</bpmn:script></bpmn:scriptTask>`, id, resultVariable, html.EscapeString(src))
}

func Service(id string, taskType string, mappings ...string) string {
	return fmt.Sprintf(`<bpmn:serviceTask id="%v" senergy:taskType="%v">%v</bpmn:serviceTask>`, id, taskType, IO(mappings...))
}

// Task is a task without type, carrying mappings only.
func Task(id string, mappings ...string) string {
	return fmt.Sprintf(`<bpmn:task id="%v">%v</bpmn:task>`, id, IO(mappings...))
}

func IO(mappings ...string) string {
	if len(mappings) == 0 {
		return ""
	}
	return `<bpmn:extensionElements><camunda:inputOutput>` + strings.Join(mappings, "") + `</camunda:inputOutput></bpmn:extensionElements>`
}

func InText(name, text string) string {
	return fmt.Sprintf(`<camunda:inputParameter name="%v">%v</camunda:inputParameter>`, name, html.EscapeString(text))
}

func InScript(name, src string) string {
	return fmt.Sprintf(`<camunda:inputParameter name="%v"><camunda:script scriptFormat="javascript">%v</camunda:script></camunda:inputParameter>`, name, html.EscapeString(src))
}

func OutScript(name, src string) string {
	return fmt.Sprintf(`<camunda:outputParameter name="%v"><camunda:script scriptFormat="javascript">%v</camunda:script></camunda:outputParameter>`, name, html.EscapeString(src))
}

func Message(id, name string) string {
	return fmt.Sprintf(`<bpmn:message id="%v" name="%v"/>`, id, name)
}

// CatchMessage waits for the message defined with Message(messageId, ...).
func CatchMessage(id string, messageId string, mappings ...string) string {
	return fmt.Sprintf(`<bpmn:intermediateCatchEvent id="%v">%v<bpmn:messageEventDefinition messageRef="%v"/></bpmn:intermediateCatchEvent>`, id, IO(mappings...), messageId)
}

// CatchDeviceEvent waits for a device event; the engine assigns its message name.
func CatchDeviceEvent(id string, mappings ...string) string {
	return fmt.Sprintf(`<bpmn:intermediateCatchEvent id="%v" senergy:function="f" senergy:script="x == 42" senergy:value_variable_name="x">%v<bpmn:messageEventDefinition/></bpmn:intermediateCatchEvent>`, id, IO(mappings...))
}

func StartMessage(id string, messageId string) string {
	return fmt.Sprintf(`<bpmn:startEvent id="%v"><bpmn:messageEventDefinition messageRef="%v"/></bpmn:startEvent>`, id, messageId)
}

// kind is timeDuration, timeDate or timeCycle
func CatchTimer(id string, kind string, value string) string {
	return fmt.Sprintf(`<bpmn:intermediateCatchEvent id="%v"><bpmn:timerEventDefinition><bpmn:%v xsi:type="bpmn:tFormalExpression">%v</bpmn:%v></bpmn:timerEventDefinition></bpmn:intermediateCatchEvent>`, id, kind, value, kind)
}

func CatchTimerExpression(id string, kind string, expression string) string {
	return fmt.Sprintf(`<bpmn:intermediateCatchEvent id="%v"><bpmn:timerEventDefinition><bpmn:%v xsi:type="bpmn:tFormalExpression" language="javascript">%v</bpmn:%v></bpmn:timerEventDefinition></bpmn:intermediateCatchEvent>`, id, kind, html.EscapeString(expression), kind)
}

func StartTimer(id string, kind string, value string) string {
	return fmt.Sprintf(`<bpmn:startEvent id="%v"><bpmn:timerEventDefinition><bpmn:%v xsi:type="bpmn:tFormalExpression">%v</bpmn:%v></bpmn:timerEventDefinition></bpmn:startEvent>`, id, kind, value, kind)
}

// StartWithForm is a start event with form fields given as id=default pairs.
func StartWithForm(id string, fields ...string) string {
	var b strings.Builder
	for _, field := range fields {
		name, def, _ := strings.Cut(field, "=")
		fmt.Fprintf(&b, `<camunda:formField id="%v" label="%v" type="string" defaultValue="%v"/>`, name, name, html.EscapeString(def))
	}
	return fmt.Sprintf(`<bpmn:startEvent id="%v"><bpmn:extensionElements><camunda:formData>%v</camunda:formData></bpmn:extensionElements></bpmn:startEvent>`, id, b.String())
}
