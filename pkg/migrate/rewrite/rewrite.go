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

// Package rewrite turns models written for Camunda 7 into the model format of this engine
// (MIG-04): JUEL becomes JavaScript, Groovy logging becomes log, external task topics become
// task types, the http-connector becomes a notification or an HTTP call, and message names
// with expressions become senergy:messageName. It works on the XML, so that layout and
// diagram stay untouched, and reports what it rewrote, what needs a manual check and what it
// cannot rewrite.
package rewrite

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/beevik/etree"
	"github.com/dop251/goja"
)

// Finding names an element and what happened to it.
type Finding = model.Issue

// Report lists what the rewrite changed (Rewritten), what runs but may behave differently and
// should be checked (Check), and what keeps the model from running (Unsupported).
type Report struct {
	Rewritten   []Finding `json:"rewritten,omitempty"`
	Check       []Finding `json:"check,omitempty"`
	Unsupported []Finding `json:"unsupported,omitempty"`
}

// Complete reports whether the rewritten model can be deployed.
func (this Report) Complete() bool {
	return len(this.Unsupported) == 0
}

// external task topics of the platform and their task types
var topics = map[string]string{
	"pessimistic": "device_command", "optimistic": "device_command", "process_io": "process_io",
	"process_deployment": "process_deployment", "process_deployment_start": "process_deployment_start",
	"analytics": "analytics", "import": "import", "export": "export", "device_repository": "device_repository",
	"watcher": "watcher", "info": "info",
}

// prefix the camunda-engine-wrapper put in front of every script
const securedScriptPrefix = "var java = {}; var execution = {}; "

var repeating = regexp.MustCompile(`^R\d*/`)

type rewriter struct {
	report  Report
	camunda string
	senergy string
	// message id -> name, for message names with expressions
	messages map[string]string
}

// Rewrite rewrites a model. An error means the XML could not be read at all.
func Rewrite(xml string) (string, Report, error) {
	doc := etree.NewDocument()
	doc.ReadSettings.PreserveCData = true
	if err := doc.ReadFromString(xml); err != nil {
		return "", Report{}, fmt.Errorf("invalid XML: %w", err)
	}
	root := doc.Root()
	if root == nil || !is(root, bpmn.NsBpmn, "definitions") {
		return "", Report{}, errors.New("root element must be bpmn:definitions")
	}
	this := &rewriter{messages: map[string]string{}}
	this.camunda = declare(root, bpmn.NsCamunda, "camunda")
	this.senergy = declare(root, bpmn.NsSenergy, "senergy")
	for _, child := range root.ChildElements() {
		if is(child, bpmn.NsBpmn, "message") {
			this.messages[child.SelectAttrValue("id", "")] = child.SelectAttrValue("name", "")
		}
	}
	for _, child := range root.ChildElements() {
		if is(child, bpmn.NsBpmn, "process") {
			this.walk(child)
		}
	}
	result, err := doc.WriteToString()
	if err != nil {
		return "", this.report, err
	}
	this.validate(result)
	return result, this.report, nil
}

func (this *rewriter) rewritten(id string, format string, args ...any) {
	this.report.Rewritten = append(this.report.Rewritten, Finding{ElementId: id, Message: fmt.Sprintf(format, args...)})
}

func (this *rewriter) check(id string, format string, args ...any) {
	this.report.Check = append(this.report.Check, Finding{ElementId: id, Message: fmt.Sprintf(format, args...)})
}

func (this *rewriter) unsupported(id string, format string, args ...any) {
	this.report.Unsupported = append(this.report.Unsupported, Finding{ElementId: id, Message: fmt.Sprintf(format, args...)})
}

// validate reads the result like a deploy does; what remains and was not reported yet keeps
// the model from running
func (this *rewriter) validate(xml string) {
	_, issues, err := bpmn.ParseAndValidate(xml, bpmn.Rules{})
	if err != nil {
		this.unsupported("", "%v", err)
		return
	}
	for _, issue := range issues {
		known := slices.ContainsFunc(this.report.Unsupported, func(f Finding) bool { return f.ElementId == issue.ElementId })
		if !known {
			this.unsupported(issue.ElementId, "%v", issue.Message)
		}
	}
}

func (this *rewriter) walk(e *etree.Element) {
	for _, child := range e.ChildElements() {
		if child.NamespaceURI() == bpmn.NsBpmn {
			this.element(child)
		}
	}
}

func (this *rewriter) element(e *etree.Element) {
	id := e.SelectAttrValue("id", "")
	this.taskType(e, id)
	if e.Tag == "scriptTask" {
		this.scriptTask(e, id)
	}
	for _, child := range e.ChildElements() {
		switch {
		case is(child, bpmn.NsBpmn, "extensionElements"):
			this.extensions(e, child, id)
		case is(child, bpmn.NsBpmn, "conditionExpression"):
			this.condition(child, id)
		case is(child, bpmn.NsBpmn, "timerEventDefinition"):
			this.timer(child, id)
		case is(child, bpmn.NsBpmn, "messageEventDefinition"):
			this.message(e, child, id)
		}
	}
}

// taskType replaces camunda:type="external" with camunda:topic by senergy:taskType
func (this *rewriter) taskType(e *etree.Element, id string) {
	kind, hasType := attr(e, bpmn.NsCamunda, "type")
	topic, hasTopic := attr(e, bpmn.NsCamunda, "topic")
	if !hasType && !hasTopic {
		return
	}
	taskType, known := topics[topic]
	if kind != "external" || !known {
		this.unsupported(id, "camunda:type=%q with camunda:topic=%q has no task type", kind, topic)
		return
	}
	removeAttr(e, bpmn.NsCamunda, "type")
	removeAttr(e, bpmn.NsCamunda, "topic")
	e.CreateAttr(this.senergy+":taskType", taskType)
	if taskType == "device_command" {
		e.CreateAttr(this.senergy+":completionStrategy", topic)
	}
	this.rewritten(id, "external task topic %v became task type %v", topic, taskType)
}

func (this *rewriter) scriptTask(e *etree.Element, id string) {
	format := strings.ToLower(e.SelectAttrValue("scriptFormat", ""))
	for _, script := range e.ChildElements() {
		if !is(script, bpmn.NsBpmn, "script") {
			continue
		}
		if js, ok := this.script(script.Text(), format, id); ok {
			script.SetText(js)
			if format != "javascript" {
				e.CreateAttr("scriptFormat", "javascript")
			}
		}
	}
}

// script returns the JavaScript for a script of a format, and false if it cannot be used
func (this *rewriter) script(src string, format string, id string) (string, bool) {
	if strings.HasPrefix(src, securedScriptPrefix) {
		src = strings.TrimPrefix(src, securedScriptPrefix)
		this.rewritten(id, "removed the script prefix of camunda-engine-wrapper")
	}
	switch format {
	case "javascript", "js":
		if strings.Contains(src, "execution.") || strings.Contains(src, "Java.type") {
			this.unsupported(id, "the script uses the Camunda or Java API")
		}
		return src, true
	case "groovy":
		js, err := groovyToJS(src)
		if err != nil {
			this.unsupported(id, "Groovy script not translated: %v", err)
			return src, false
		}
		this.rewritten(id, "Groovy script became JavaScript with log")
		return js, true
	}
	this.unsupported(id, "script format %q is not supported", format)
	return src, false
}

func (this *rewriter) extensions(e *etree.Element, ext *etree.Element, id string) {
	for _, child := range ext.ChildElements() {
		if is(child, bpmn.NsCamunda, "connector") {
			this.connector(e, ext, child, id)
		}
	}
	for _, child := range ext.ChildElements() {
		if !is(child, bpmn.NsCamunda, "inputOutput") {
			continue
		}
		for _, param := range child.ChildElements() {
			if is(param, bpmn.NsCamunda, "inputParameter") || is(param, bpmn.NsCamunda, "outputParameter") {
				this.value(param, id, param.SelectAttrValue("name", ""))
			}
		}
	}
}

// value rewrites a mapping value: text with JUEL becomes a JavaScript script, scripts in other
// formats are translated, maps and lists are rewritten entry by entry
func (this *rewriter) value(e *etree.Element, id string, name string) {
	for _, child := range e.ChildElements() {
		switch {
		case is(child, bpmn.NsCamunda, "script"):
			format := strings.ToLower(child.SelectAttrValue("scriptFormat", ""))
			if js, ok := this.script(child.Text(), format, id); ok {
				child.SetText(js)
				child.CreateAttr("scriptFormat", "javascript")
			}
			return
		case is(child, bpmn.NsCamunda, "map"):
			for _, entry := range child.ChildElements() {
				this.value(entry, id, name+"."+entry.SelectAttrValue("key", ""))
			}
			return
		case is(child, bpmn.NsCamunda, "list"):
			for _, item := range child.ChildElements() {
				this.value(item, id, name+"[]")
			}
			return
		}
	}
	text := e.Text()
	if !hasExpression(text) {
		return
	}
	js, err := toScript(text)
	if err != nil {
		this.unsupported(id, "mapping %v: %v", name, err)
		return
	}
	e.SetText("")
	script := e.CreateElement(this.camunda + ":script")
	script.CreateAttr("scriptFormat", "javascript")
	script.SetText(js)
	this.rewritten(id, "mapping %v: JUEL became JavaScript", name)
}

// condition rewrites a JUEL condition and wraps JavaScript with top-level return
func (this *rewriter) condition(e *etree.Element, id string) {
	body := strings.TrimSpace(e.Text())
	language := strings.ToLower(e.SelectAttrValue("language", ""))
	switch {
	case language == "" && hasExpression(body):
		js, err := toScript(body)
		if err != nil {
			this.unsupported(id, "condition: %v", err)
			return
		}
		e.SetText(js)
		e.CreateAttr("language", "javascript")
		this.rewritten(id, "condition: JUEL became JavaScript")
	case language == "":
		this.unsupported(id, "condition %q is neither JUEL nor has a language; in Camunda it was a constant text", body)
	case language == "javascript" || language == "js":
		if _, err := goja.Compile("", body, false); err != nil && strings.Contains(err.Error(), "Illegal return") {
			e.SetText("(function () {\n" + body + "\n})()")
			this.rewritten(id, "condition: wrapped in a function because of return statements")
		}
	}
}

func (this *rewriter) timer(definition *etree.Element, id string) {
	for _, child := range definition.ChildElements() {
		if child.NamespaceURI() != bpmn.NsBpmn || !slices.Contains([]string{"timeDuration", "timeDate", "timeCycle"}, child.Tag) {
			continue
		}
		value := strings.TrimSpace(child.Text())
		if child.SelectAttrValue("language", "") != "" {
			continue
		}
		switch {
		case hasExpression(value):
			js, err := toScript(value)
			if err != nil {
				this.unsupported(id, "timer: %v", err)
				continue
			}
			child.SetText(js)
			child.CreateAttr("language", "javascript")
			this.rewritten(id, "timer: JUEL became JavaScript")
		case child.Tag == "timeDuration" && repeating.MatchString(value):
			// Camunda waits once for the period of a repeating interval used as duration
			child.SetText(repeating.ReplaceAllString(value, ""))
			this.check(id, "timer: the duration %v became %v; Camunda waited once as well", value, child.Text())
		case child.Tag == "timeCycle" && !strings.HasPrefix(value, "R") && len(strings.Fields(value)) == 5:
			child.SetText("0 " + value)
			this.check(id, "timer: the cron expression %q has 5 fields, which Camunda rejected; it became %q with seconds", value, child.Text())
		}
	}
}

// message replaces a message name with an expression, e.g. ${done_event}, by senergy:messageName
func (this *rewriter) message(e *etree.Element, definition *etree.Element, id string) {
	name := this.messages[definition.SelectAttrValue("messageRef", "")]
	if !hasExpression(name) {
		return
	}
	js, err := toScript(name)
	if err != nil {
		this.unsupported(id, "message name %q: %v", name, err)
		return
	}
	e.CreateAttr(this.senergy+":messageName", js)
	removeAttr(definition, "", "messageRef")
	this.rewritten(id, "message name %v became senergy:messageName", name)
}

// connector replaces the http-connector: a notification of the platform becomes the task type
// notification, any other call the task type http
func (this *rewriter) connector(e *etree.Element, ext *etree.Element, connector *etree.Element, id string) {
	connectorId := ""
	var inputs, outputs []*etree.Element
	for _, child := range connector.ChildElements() {
		switch {
		case is(child, bpmn.NsCamunda, "connectorId"):
			connectorId = strings.TrimSpace(child.Text())
		case is(child, bpmn.NsCamunda, "inputOutput"):
			for _, param := range child.ChildElements() {
				if is(param, bpmn.NsCamunda, "inputParameter") {
					inputs = append(inputs, param)
				} else if is(param, bpmn.NsCamunda, "outputParameter") {
					outputs = append(outputs, param)
				}
			}
		}
	}
	if connectorId != "http-connector" {
		this.unsupported(id, "connector %v is not supported", connectorId)
		return
	}
	byName := map[string]*etree.Element{}
	for _, param := range inputs {
		byName[param.SelectAttrValue("name", "")] = param
	}
	io := this.inputOutput(ext)
	var position int
	add := func(kind string, name string, text string) *etree.Element {
		param := etree.NewElement(this.camunda + ":" + kind)
		param.CreateAttr("name", name)
		param.SetText(text)
		if kind == "inputParameter" {
			io.InsertChildAt(position, param)
			position++
		} else {
			io.AddChild(param)
		}
		return param
	}
	if identifier := byName["deploymentIdentifier"]; identifier != nil && strings.TrimSpace(identifier.Text()) == "notification" {
		var payload map[string]any
		if p := byName["payload"]; p == nil || json.Unmarshal([]byte(p.Text()), &payload) != nil {
			this.unsupported(id, "the payload of the notification is not JSON")
			return
		}
		for _, key := range []string{"title", "message"} {
			if text, ok := payload[key].(string); ok {
				this.value(add("inputParameter", key, text), id, key)
			}
		}
		e.CreateAttr(this.senergy+":taskType", "notification")
		this.rewritten(id, "http-connector to the notifier became task type notification")
	} else {
		for _, param := range inputs {
			name := param.SelectAttrValue("name", "")
			target := map[string]string{"url": "url", "method": "method", "headers": "headers", "payload": "body"}[name]
			if target == "" {
				this.unsupported(id, "input %v of the http-connector has no counterpart", name)
				continue
			}
			param.CreateAttr("name", target)
			param.Parent().RemoveChild(param)
			io.InsertChildAt(position, param)
			position++
			this.value(param, id, target)
		}
		for _, param := range outputs {
			param.Parent().RemoveChild(param)
			io.AddChild(param)
			this.value(param, id, param.SelectAttrValue("name", ""))
		}
		e.CreateAttr(this.senergy+":taskType", "http")
		this.rewritten(id, "http-connector became task type http")
		this.check(id, "HTTP calls need the target allowed in the HTTP policy of the namespace")
	}
	ext.RemoveChild(connector)
}

// inputOutput returns the camunda:inputOutput of the extension elements, creating it if missing
func (this *rewriter) inputOutput(ext *etree.Element) *etree.Element {
	for _, child := range ext.ChildElements() {
		if is(child, bpmn.NsCamunda, "inputOutput") {
			return child
		}
	}
	return ext.CreateElement(this.camunda + ":inputOutput")
}

func is(e *etree.Element, ns string, local string) bool {
	return e.Tag == local && e.NamespaceURI() == ns
}

func attr(e *etree.Element, ns string, local string) (string, bool) {
	for _, a := range e.Attr {
		if a.Key == local && a.NamespaceURI() == ns {
			return a.Value, true
		}
	}
	return "", false
}

// removeAttr removes an attribute by namespace; "" means an attribute without prefix
func removeAttr(e *etree.Element, ns string, local string) {
	for i, a := range e.Attr {
		if a.Key == local && ((ns == "" && a.Space == "") || (ns != "" && a.NamespaceURI() == ns)) {
			e.Attr = append(e.Attr[:i], e.Attr[i+1:]...)
			return
		}
	}
}

// declare returns the prefix of a namespace on the root, declaring it if missing
func declare(root *etree.Element, ns string, preferred string) string {
	for _, a := range root.Attr {
		if a.Space == "xmlns" && a.Value == ns {
			return a.Key
		}
	}
	root.CreateAttr("xmlns:"+preferred, ns)
	return preferred
}
