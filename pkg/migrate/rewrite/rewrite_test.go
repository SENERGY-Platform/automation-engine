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

package rewrite

import (
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
)

// definitions wraps process content the way the designer of the platform writes it; the senergy
// namespace is left out on purpose, as in older models
func definitions(content string, extra ...string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:camunda="http://camunda.org/schema/1.0/bpmn" ` +
		`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" id="d" targetNamespace="http://bpmn.io/schema/bpmn">` +
		strings.Join(extra, "") +
		`<bpmn:process id="p" isExecutable="true">` + content + `</bpmn:process></bpmn:definitions>`
}

func flow(id, from, to string) string {
	return `<bpmn:sequenceFlow id="` + id + `" sourceRef="` + from + `" targetRef="` + to + `"/>`
}

// around puts an element between a start and an end event
func around(id string, element string) string {
	return `<bpmn:startEvent id="start"/>` + element + `<bpmn:endEvent id="end"/>` + flow("f1", "start", id) + flow("f2", id, "end")
}

func rewrite(t *testing.T, xml string) (*bpmn.Process, Report) {
	t.Helper()
	result, report, err := Rewrite(xml)
	if err != nil {
		t.Fatal(err)
	}
	process, issues, err := bpmn.ParseAndValidate(result, bpmn.Rules{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Complete() && len(issues) > 0 {
		t.Fatalf("a complete rewrite deploys without issues: %v\n%v", issues, result)
	}
	return process, report
}

func has(findings []Finding, part string) bool {
	for _, f := range findings {
		if strings.Contains(f.Message, part) {
			return true
		}
	}
	return false
}

func TestExternalTopicsBecomeTaskTypes(t *testing.T) {
	p, report := rewrite(t, definitions(around("cmd", `<bpmn:serviceTask id="cmd" camunda:type="external" camunda:topic="optimistic">`+
		`<bpmn:extensionElements><camunda:inputOutput><camunda:inputParameter name="payload">{"function":{"id":"f"}}</camunda:inputParameter>`+
		`<camunda:outputParameter name="value">${result}</camunda:outputParameter></camunda:inputOutput></bpmn:extensionElements></bpmn:serviceTask>`)))
	cmd := p.Elements["cmd"]
	if cmd.TaskType != "device_command" || cmd.Attributes["completionStrategy"] != "optimistic" {
		t.Errorf("optimistic is a device command with that strategy: %+v", cmd)
	}
	if out := cmd.Outputs[0].Value; out.Kind != bpmn.ValueScript || out.Script != "result" {
		t.Errorf("the output ${result} is the script result: %+v", out)
	}
	if !report.Complete() || !has(report.Rewritten, "optimistic") {
		t.Errorf("%+v", report)
	}
}

func TestUnknownTopicIsReported(t *testing.T) {
	_, report := rewrite(t, definitions(around("t", `<bpmn:serviceTask id="t" camunda:type="external" camunda:topic="mystery"/>`)))
	if report.Complete() || !has(report.Unsupported, "mystery") {
		t.Errorf("%+v", report)
	}
}

func TestNotificationConnectorBecomesNotificationTask(t *testing.T) {
	p, report := rewrite(t, definitions(around("n", `<bpmn:serviceTask id="n"><bpmn:extensionElements><camunda:connector><camunda:inputOutput>`+
		`<camunda:inputParameter name="payload">{"message":"Level ${level} at ${device}","title":"Warning"}</camunda:inputParameter>`+
		`<camunda:inputParameter name="deploymentIdentifier">notification</camunda:inputParameter></camunda:inputOutput>`+
		`<camunda:connectorId>http-connector</camunda:connectorId></camunda:connector></bpmn:extensionElements></bpmn:serviceTask>`)))
	n := p.Elements["n"]
	inputs := map[string]bpmn.Value{}
	for _, m := range n.Inputs {
		inputs[m.Name] = m.Value
	}
	if n.TaskType != "notification" || inputs["title"].Text != "Warning" || !strings.Contains(inputs["message"].Script, "`Level ${(level)") {
		t.Errorf("title stays text, the message becomes a template literal: %+v %+v", n, inputs)
	}
	if !report.Complete() {
		t.Errorf("%+v", report)
	}
}

func TestHttpConnectorBecomesHttpTask(t *testing.T) {
	p, report := rewrite(t, definitions(around("h", `<bpmn:serviceTask id="h"><bpmn:extensionElements><camunda:connector><camunda:inputOutput>`+
		`<camunda:inputParameter name="url">http://${ip}/cm</camunda:inputParameter><camunda:inputParameter name="method">POST</camunda:inputParameter>`+
		`<camunda:inputParameter name="headers"><camunda:map><camunda:entry key="Authorization">Basic ${auth}</camunda:entry></camunda:map></camunda:inputParameter>`+
		`<camunda:inputParameter name="payload">c1=on</camunda:inputParameter><camunda:outputParameter name="code">${statusCode}</camunda:outputParameter>`+
		`</camunda:inputOutput><camunda:connectorId>http-connector</camunda:connectorId></camunda:connector></bpmn:extensionElements></bpmn:serviceTask>`)))
	h := p.Elements["h"]
	inputs := map[string]bpmn.Value{}
	for _, m := range h.Inputs {
		inputs[m.Name] = m.Value
	}
	if h.TaskType != "http" || inputs["body"].Text != "c1=on" || inputs["method"].Text != "POST" || inputs["url"].Kind != bpmn.ValueScript || inputs["headers"].Kind != bpmn.ValueMap {
		t.Errorf("url, method, headers and payload become the inputs of the http task: %+v", inputs)
	}
	if len(h.Outputs) != 1 || h.Outputs[0].Name != "code" || h.Outputs[0].Value.Script != "statusCode" {
		t.Errorf("connector outputs become outputs of the task: %+v", h.Outputs)
	}
	if !report.Complete() || !has(report.Check, "HTTP policy") {
		t.Errorf("an allowed target has to be checked: %+v", report)
	}
}

func TestMailConnectorIsUnsupported(t *testing.T) {
	_, report := rewrite(t, definitions(around("m", `<bpmn:serviceTask id="m"><bpmn:extensionElements><camunda:connector>`+
		`<camunda:connectorId>mail-send</camunda:connectorId></camunda:connector></bpmn:extensionElements></bpmn:serviceTask>`)))
	if report.Complete() || !has(report.Unsupported, "mail-send") {
		t.Errorf("%+v", report)
	}
}

func TestGroovyLoggingBecomesJavaScript(t *testing.T) {
	p, report := rewrite(t, definitions(around("s", `<bpmn:scriptTask id="s" scriptFormat="groovy"><bpmn:script>println "power: " + power
println("value ${x}")</bpmn:script></bpmn:scriptTask>`)))
	if script := p.Elements["s"].Script; script != "log(\"power: \" + power);\nlog(`value ${x}`);" {
		t.Errorf("%q", script)
	}
	if !report.Complete() {
		t.Errorf("%+v", report)
	}
	_, report = rewrite(t, definitions(around("s", `<bpmn:scriptTask id="s" scriptFormat="groovy"><bpmn:script>def x = 1</bpmn:script></bpmn:scriptTask>`)))
	if report.Complete() || !has(report.Unsupported, "Groovy") {
		t.Errorf("other Groovy stays unsupported: %+v", report)
	}
}

func TestWrapperScriptPrefixIsRemoved(t *testing.T) {
	p, report := rewrite(t, definitions(around("s", `<bpmn:scriptTask id="s" scriptFormat="javascript"><bpmn:script>var java = {}; var execution = {}; 1 + 1</bpmn:script></bpmn:scriptTask>`)))
	if p.Elements["s"].Script != "1 + 1" || !report.Complete() {
		t.Errorf("%q %+v", p.Elements["s"].Script, report)
	}
}

func gateway(condition string) string {
	return `<bpmn:startEvent id="start"/><bpmn:exclusiveGateway id="gw" default="no"/><bpmn:endEvent id="a"/><bpmn:endEvent id="b"/>` +
		flow("f", "start", "gw") + `<bpmn:sequenceFlow id="yes" sourceRef="gw" targetRef="a">` + condition + `</bpmn:sequenceFlow>` + flow("no", "gw", "b")
}

func TestJuelConditionsBecomeJavaScript(t *testing.T) {
	p, report := rewrite(t, definitions(gateway(`<bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">${a gt 1 &amp;&amp; s eq 'and'}</bpmn:conditionExpression>`)))
	if c := p.Flows["yes"].Condition; c != "a > 1 && s == 'and'" {
		t.Errorf("operators are translated outside strings only: %q", c)
	}
	if !report.Complete() {
		t.Errorf("%+v", report)
	}
}

func TestConditionWithReturnIsWrapped(t *testing.T) {
	p, report := rewrite(t, definitions(gateway(`<bpmn:conditionExpression xsi:type="bpmn:tFormalExpression" language="javascript">if (x) { return true }
return false</bpmn:conditionExpression>`)))
	if c := p.Flows["yes"].Condition; !strings.HasPrefix(c, "(function () {") || !report.Complete() {
		t.Errorf("%q %+v", c, report)
	}
}

func TestConstantConditionIsReported(t *testing.T) {
	_, report := rewrite(t, definitions(gateway(`<bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">was_active || true</bpmn:conditionExpression>`)))
	if report.Complete() || !has(report.Unsupported, "neither JUEL") {
		t.Errorf("%+v", report)
	}
}

func TestEmptyOperatorIsReported(t *testing.T) {
	_, report := rewrite(t, definitions(gateway(`<bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">${empty list}</bpmn:conditionExpression>`)))
	if report.Complete() || !has(report.Unsupported, "empty") {
		t.Errorf("%+v", report)
	}
}

func timerEvent(kind, value string) string {
	return around("t", `<bpmn:intermediateCatchEvent id="t"><bpmn:timerEventDefinition><bpmn:`+kind+` xsi:type="bpmn:tFormalExpression">`+value+`</bpmn:`+kind+`></bpmn:timerEventDefinition></bpmn:intermediateCatchEvent>`)
}

func TestTimers(t *testing.T) {
	p, report := rewrite(t, definitions(timerEvent("timeDuration", "PT${seconds}S")))
	if d := p.Elements["t"].Timer; !d.Expression || !strings.HasPrefix(d.Value, "`PT${(seconds)") || !report.Complete() {
		t.Errorf("a JUEL timer becomes a template literal: %+v %+v", d, report)
	}
	p, report = rewrite(t, definitions(timerEvent("timeDuration", "R/P1D")))
	if d := p.Elements["t"].Timer; d.Value != "P1D" || !report.Complete() || !has(report.Check, "R/P1D") {
		t.Errorf("a repeating duration waits once, as in Camunda: %+v %+v", d, report)
	}
	p, report = rewrite(t, definitions(timerEvent("timeCycle", "0/1 * * * *")))
	if d := p.Elements["t"].Timer; d.Value != "0 0/1 * * * *" || !has(report.Check, "5 fields") {
		t.Errorf("a cron without seconds gets them: %+v %+v", d, report)
	}
}

func TestMessageNameExpressionBecomesMessageNameAttribute(t *testing.T) {
	p, report := rewrite(t, definitions(around("w", `<bpmn:intermediateCatchEvent id="w"><bpmn:messageEventDefinition messageRef="m"/></bpmn:intermediateCatchEvent>`),
		`<bpmn:message id="m" name="${done_event}"/>`))
	if ref := p.Elements["w"].Message; ref == nil || ref.Expression != "done_event" || !report.Complete() {
		t.Errorf("%+v %+v", ref, report)
	}
}

func TestMappingsInMapsAndTextKeepLiterals(t *testing.T) {
	js, err := toScript("a `b` \\ ${x}")
	if err != nil || js != "`a \\`b\\` \\\\ ${(x) ?? \"\"}`" {
		t.Errorf("backticks and backslashes of the text are escaped: %q %v", js, err)
	}
	if _, err = toScript("${a == '}'"); err == nil {
		t.Error("an unterminated expression is an error")
	}
	if js, _ = toScript("${x.and}"); js != "x.and" {
		t.Errorf("a property named like an operator stays: %q", js)
	}
}
