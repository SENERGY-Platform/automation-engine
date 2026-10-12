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
	"strings"
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/timer"
)

func parseValid(t *testing.T, xml string, rules Rules) *Process {
	t.Helper()
	p, issues, err := ParseAndValidate(xml, rules)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) > 0 {
		t.Fatalf("unexpected issues %v", issues)
	}
	return p
}

func hasIssue(issues []model.Issue, elementId string, fragment string) bool {
	for _, issue := range issues {
		if issue.ElementId == elementId && strings.Contains(issue.Message, fragment) {
			return true
		}
	}
	return false
}

func issuesOf(t *testing.T, xml string, rules Rules) []model.Issue {
	t.Helper()
	_, issues, err := ParseAndValidate(xml, rules)
	if err != nil {
		t.Fatal(err)
	}
	return issues
}

func TestParseBuildsGraphOfSupportedElements(t *testing.T) {
	xml := b.Process(
		b.Message("m1", "update"),
		b.StartWithForm("start", "a=1", "b="),
		b.Script("script", "x", "1 + 1"),
		b.Gateway("exclusive", "xor", b.FlowId("xor", "end")),
		b.Task("task", b.InText("t", "text"), b.InScript("s", "x * 2"), b.OutScript("out", "s + 1")),
		b.CatchMessage("msg", "m1"),
		b.CatchDeviceEvent("dev"),
		b.CatchTimer("timer", "timeCycle", "0 0 11 * * ?"),
		b.Gateway("eventBased", "ebg", ""),
		b.Gateway("parallel", "fork", ""),
		b.Gateway("inclusive", "or", ""),
		b.End("end"),
		b.Flow("start", "script"), b.Flow("script", "xor"),
		b.Cond("xor", "task", "x > 1"), b.Flow("xor", "end"),
		b.Flow("task", "ebg"), b.Flow("ebg", "msg"), b.Flow("ebg", "timer"), b.Flow("ebg", "dev"),
		b.Flow("msg", "fork"), b.Flow("timer", "fork"), b.Flow("dev", "fork"),
		b.Flow("fork", "or"), b.Flow("fork", "end"), b.Flow("or", "end"),
	)
	p := parseValid(t, xml, Rules{})
	if p.Id != "test_process" || len(p.Elements) != 11 {
		t.Fatalf("unexpected process %v with %v elements", p.Id, len(p.Elements))
	}
	if xor := p.Element("xor"); xor.DefaultFlow == nil || xor.DefaultFlow.Target.Id != "end" || len(xor.Outgoing) != 2 {
		t.Errorf("default flow not resolved")
	}
	if f := p.Flows[b.FlowId("xor", "task")]; f.Condition != "x > 1" {
		t.Errorf("condition %q", f.Condition)
	}
	if s := p.Element("script"); s.Script != "1 + 1" || s.ResultVariable != "x" {
		t.Errorf("script %#v", s)
	}
	task := p.Element("task")
	if len(task.Inputs) != 2 || task.Inputs[0].Value.Kind != ValueText || task.Inputs[1].Value.Kind != ValueScript || task.Outputs[0].Name != "out" {
		t.Errorf("mappings %#v", task.Inputs)
	}
	if m := p.Element("msg"); m.Kind != EventMessage || m.Message.Name != "update" || m.Message.DeviceEvent {
		t.Errorf("message %#v", m.Message)
	}
	if d := p.Element("dev"); !d.Message.DeviceEvent || d.Attributes["script"] != "x == 42" {
		t.Errorf("device event %#v", d.Message)
	}
	if tm := p.Element("timer"); tm.Kind != EventTimer || tm.Timer.Kind != timer.KindCycle || tm.Key() != "intermediateCatchEvent:timer" {
		t.Errorf("timer %#v", tm.Timer)
	}
	params := StartParameters(p.NoneStartEvent())
	if len(params) != 2 || params[0].Id != "a" || params[0].Default != "1" {
		t.Errorf("start parameters %#v", params)
	}
}

func TestParseRejectsUnsupportedElementsNamingThem(t *testing.T) {
	xml := b.Process(
		b.Start("start"),
		`<bpmn:userTask id="user"/>`,
		`<bpmn:subProcess id="sub"/>`,
		`<bpmn:boundaryEvent id="boundary" attachedToRef="user"><bpmn:timerEventDefinition/></bpmn:boundaryEvent>`,
		`<bpmn:endEvent id="end"><bpmn:terminateEventDefinition/></bpmn:endEvent>`,
		`<bpmn:serviceTask id="ext" camunda:type="external" camunda:topic="pessimistic"/>`,
		`<bpmn:task id="multi"><bpmn:multiInstanceLoopCharacteristics/></bpmn:task>`,
		b.Flow("start", "end"), b.Flow("start", "ext"), b.Flow("start", "multi"),
	)
	issues := issuesOf(t, xml, Rules{})
	for id, fragment := range map[string]string{
		"user":     "userTask is not supported",
		"sub":      "subProcess is not supported",
		"boundary": "boundaryEvent is not supported",
		"end":      "terminateEventDefinition is not supported",
		"ext":      "camunda:topic",
		"multi":    "multi-instance",
	} {
		if !hasIssue(issues, id, fragment) {
			t.Errorf("missing issue %q for %v in %v", fragment, id, issues)
		}
	}
}

func TestParseRejectsJuelAndOtherLanguages(t *testing.T) {
	xml := b.Process(
		b.Start("start"),
		`<bpmn:scriptTask id="groovy" scriptFormat="groovy"><bpmn:script>println "x"</bpmn:script></bpmn:scriptTask>`,
		b.Task("task", b.InText("url", "http://${ip}/ta")),
		`<bpmn:intermediateCatchEvent id="timer"><bpmn:timerEventDefinition><bpmn:timeDuration>${cycle}</bpmn:timeDuration></bpmn:timerEventDefinition></bpmn:intermediateCatchEvent>`,
		b.End("end"),
		b.Flow("start", "groovy"), b.Flow("groovy", "task"), b.Flow("task", "timer"),
		`<bpmn:sequenceFlow id="juel" sourceRef="timer" targetRef="end"><bpmn:conditionExpression>${a &gt; 1}</bpmn:conditionExpression></bpmn:sequenceFlow>`,
	)
	issues := issuesOf(t, xml, Rules{})
	for id, fragment := range map[string]string{
		"groovy": "only JavaScript",
		"task":   "JUEL",
		"timer":  "JUEL",
		"juel":   "JUEL",
	} {
		if !hasIssue(issues, id, fragment) {
			t.Errorf("missing issue %q for %v in %v", fragment, id, issues)
		}
	}
	if len(issues) != 4 {
		t.Errorf("rejected expressions must be reported once, got %v", issues)
	}
}

func TestValidateReportsJavaScriptSyntaxErrors(t *testing.T) {
	xml := b.Process(b.Start("start"), b.Script("s", "x", "1 +"), b.End("end"), b.Flow("start", "s"), b.Cond("s", "end", "a &&"))
	issues := issuesOf(t, xml, Rules{})
	if !hasIssue(issues, "s", "script") || !hasIssue(issues, b.FlowId("s", "end"), "condition") {
		t.Fatalf("unexpected %v", issues)
	}
}

func TestValidateAppliesNamespaceRules(t *testing.T) {
	xml := b.Process(
		b.StartTimer("start", "timeCycle", "0 0 * * * ?"),
		b.Service("io", "process_io"),
		b.Service("cmd", "device_command"),
		b.End("end"),
		b.Flow("start", "io"), b.Flow("io", "cmd"), b.Flow("cmd", "end"),
	)
	rules := Rules{
		Elements:          model.ListRule{Deny: []string{"startEvent:timer"}},
		TaskTypes:         model.ListRule{Allow: []string{"process_io", "device_command"}, Deny: []string{"process_io"}},
		TaskTypeAvailable: func(taskType string) bool { return taskType != "device_command" },
	}
	issues := issuesOf(t, xml, rules)
	if !hasIssue(issues, "start", "startEvent:timer is not allowed") ||
		!hasIssue(issues, "io", "not allowed in this namespace") ||
		!hasIssue(issues, "cmd", "not available") {
		t.Fatalf("unexpected %v", issues)
	}
	rules.Elements = model.ListRule{Allow: []string{"startEvent", "serviceTask", "endEvent"}}
	if hasIssue(issuesOf(t, xml, rules), "start", "not allowed") {
		t.Fatal("an allowed element type must cover all its kinds")
	}
}

func TestValidateChecksStructure(t *testing.T) {
	xml := b.Process(
		b.Start("s1"), b.Start("s2"),
		b.Gateway("eventBased", "ebg", ""),
		b.Script("task", "x", "1"),
		b.CatchTimer("timer", "timeDuration", "PT5S"),
		`<bpmn:intermediateCatchEvent id="nothing"/>`,
		`<bpmn:exclusiveGateway id="bad_default" default="missing"/>`,
		b.Flow("s1", "ebg"), b.Flow("ebg", "task"), b.Flow("ebg", "timer"), b.Flow("timer", "nothing"), b.Flow("s2", "bad_default"),
	)
	issues := issuesOf(t, xml, Rules{})
	for id, fragment := range map[string]string{
		"test_process": "more than one start event",
		"ebg":          "must lead to message or timer catch events",
		"nothing":      "needs a message or timer definition",
		"bad_default":  "default flow",
	} {
		if !hasIssue(issues, id, fragment) {
			t.Errorf("missing issue %q for %v in %v", fragment, id, issues)
		}
	}
}

func TestParseReportsMultipleExecutableProcesses(t *testing.T) {
	xml := strings.Replace(b.Process(b.Start("s")), "</bpmn:definitions>", `<bpmn:process id="second" isExecutable="true"/></bpmn:definitions>`, 1)
	if !hasIssue(issuesOf(t, xml, Rules{}), "second", "exactly one executable process") {
		t.Fatal("expected issue")
	}
}

func TestParseFailsOnInvalidDocuments(t *testing.T) {
	for _, xml := range []string{"", "<a/>", "<bpmn:definitions xmlns:bpmn=\"" + NsBpmn + "\"/>", "<x"} {
		if _, _, err := Parse(xml); err == nil {
			t.Errorf("expected error for %q", xml)
		}
	}
}

func TestStartParametersSkipIgnoredFields(t *testing.T) {
	xml := b.Process(`<bpmn:startEvent id="start"><bpmn:extensionElements><camunda:formData>
		<camunda:formField id="a" label="A" type="long" defaultValue="5"><camunda:properties><camunda:property id="order" value="1"/></camunda:properties></camunda:formField>
		<camunda:formField id="hidden" type="string"><camunda:properties><camunda:property id="ignore_on_start" value="true"/></camunda:properties></camunda:formField>
		</camunda:formData></bpmn:extensionElements></bpmn:startEvent>`)
	p := parseValid(t, xml, Rules{})
	params := StartParameters(p.NoneStartEvent())
	if len(params) != 1 || params[0].Type != "long" || params[0].Properties["order"] != "1" {
		t.Fatalf("unexpected %#v", params)
	}
}

func TestForbiddenCyclesNameTheFlowClosingTheLoop(t *testing.T) {
	loop := b.Process(
		b.Start("start"), b.Gateway("exclusive", "loop", ""), b.Script("work", "n", "1"), b.Gateway("exclusive", "check", ""), b.End("end"),
		b.Flow("start", "loop"), b.Flow("loop", "work"), b.Flow("work", "check"), b.Cond("check", "loop", "n < 3"), b.Cond("check", "end", "n >= 3"),
	)
	parseValid(t, loop, Rules{})
	_, issues, err := ParseAndValidate(loop, Rules{ForbidCycles: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].ElementId != b.FlowId("check", "loop") || !strings.Contains(issues[0].Message, "loop") {
		t.Fatalf("the flow back to the loop start is reported (SMS-03): %+v", issues)
	}
	parallel := b.Process(
		b.Start("start"), b.Gateway("parallel", "fork", ""), b.Script("a", "x", "1"), b.Script("b", "y", "2"), b.Gateway("parallel", "join", ""), b.End("end"),
		b.Flow("start", "fork"), b.Flow("fork", "a"), b.Flow("fork", "b"), b.Flow("a", "join"), b.Flow("b", "join"), b.Flow("join", "end"),
	)
	parseValid(t, parallel, Rules{ForbidCycles: true})
}
