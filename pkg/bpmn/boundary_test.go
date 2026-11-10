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
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

func TestErrorBoundaryEventOnEveryTaskKind(t *testing.T) {
	xml := b.Process(
		b.Start("start"),
		b.Service("service", "device_command"),
		b.Script("script", "", "1"),
		b.Task("task"),
		b.ErrorBoundary("onService", "service", "failure"),
		b.ErrorBoundary("onScript", "script", ""),
		b.ErrorBoundary("onTask", "task", ""),
		b.End("end"), b.End("failed"),
		b.Flow("start", "service"), b.Flow("service", "script"), b.Flow("script", "task"), b.Flow("task", "end"),
		b.Flow("onService", "failed"), b.Flow("onScript", "failed"),
	)
	p := parseValid(t, xml, Rules{})
	boundary := p.Element("onService")
	if boundary.Type != BoundaryEvent || boundary.Kind != EventError || boundary.Key() != "boundaryEvent:error" {
		t.Errorf("boundary %v %v %v", boundary.Type, boundary.Kind, boundary.Key())
	}
	if boundary.AttachedTo != p.Element("service") || p.Element("service").ErrorBoundary() != boundary {
		t.Errorf("not attached: %v", boundary.AttachedTo)
	}
	if boundary.ErrorMessageVariable != "failure" || p.Element("onScript").ErrorMessageVariable != "" {
		t.Errorf("error message variable %q %q", boundary.ErrorMessageVariable, p.Element("onScript").ErrorMessageVariable)
	}
	if p.Element("end").Type != EndEvent || p.Element("task").ErrorBoundary() == nil {
		t.Error("graph incomplete")
	}
	// the boundary event without outgoing flow ends its token, like any element (BPMN-05)
	if len(p.Element("onTask").Outgoing) != 0 {
		t.Error("onTask has flows")
	}
}

func TestErrorBoundaryEventRejections(t *testing.T) {
	cases := map[string]struct {
		parts    []string
		id       string
		fragment string
	}{
		"timer": {[]string{b.Task("task"), `<bpmn:boundaryEvent id="x" attachedToRef="task"><bpmn:timerEventDefinition><bpmn:timeDuration>PT1M</bpmn:timeDuration></bpmn:timerEventDefinition></bpmn:boundaryEvent>`},
			"x", "boundary event with timer definition is not supported"},
		"message": {[]string{b.Task("task"), b.Message("m", "name"), `<bpmn:boundaryEvent id="x" attachedToRef="task"><bpmn:messageEventDefinition messageRef="m"/></bpmn:boundaryEvent>`},
			"x", "boundary event with message definition is not supported"},
		"no definition": {[]string{b.Task("task"), `<bpmn:boundaryEvent id="x" attachedToRef="task"/>`},
			"x", "boundary event needs an error definition"},
		"non-interrupting": {[]string{b.Task("task"), `<bpmn:boundaryEvent id="x" attachedToRef="task" cancelActivity="false"><bpmn:errorEventDefinition/></bpmn:boundaryEvent>`},
			"x", "non-interrupting boundary events are not supported"},
		"errorRef": {[]string{b.Task("task"), `<bpmn:boundaryEvent id="x" attachedToRef="task"><bpmn:errorEventDefinition errorRef="e"/></bpmn:boundaryEvent>`},
			"x", "errorRef is not supported"},
		"errorCodeVariable": {[]string{b.Task("task"), `<bpmn:boundaryEvent id="x" attachedToRef="task"><bpmn:errorEventDefinition camunda:errorCodeVariable="code"/></bpmn:boundaryEvent>`},
			"x", "camunda:errorCodeVariable is not supported"},
		"on a gateway": {[]string{b.Task("task"), b.Gateway("exclusive", "gw", ""), b.Flow("task", "gw"), b.ErrorBoundary("x", "gw", "")},
			"x", "boundary event on exclusiveGateway is not supported"},
		"on an event": {[]string{b.Task("task"), b.CatchTimer("wait", "timeDuration", "PT1M"), b.Flow("task", "wait"), b.ErrorBoundary("x", "wait", "")},
			"x", "boundary event on intermediateCatchEvent is not supported"},
		"unknown host": {[]string{b.Task("task"), b.ErrorBoundary("x", "missing", "")},
			"x", `attached to an unknown or unsupported element "missing"`},
		"two on one task": {[]string{b.Task("task"), b.ErrorBoundary("x", "task", ""), b.ErrorBoundary("y", "task", "")},
			"task", "at most one boundary event"},
		"incoming flow": {[]string{b.Task("task"), b.ErrorBoundary("x", "task", ""), b.Flow("task", "x")},
			"x", "must not have incoming flows"},
		"error end event": {[]string{b.Task("task"), `<bpmn:endEvent id="x"><bpmn:errorEventDefinition/></bpmn:endEvent>`, b.Flow("task", "x")},
			"x", "endEvent with error definition is not supported"},
		"error start event": {[]string{b.Task("task"), `<bpmn:startEvent id="x"><bpmn:errorEventDefinition/></bpmn:startEvent>`},
			"x", "startEvent with error definition is not supported"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			parts := append([]string{b.Start("start"), b.Flow("start", "task")}, c.parts...)
			issues := issuesOf(t, b.Process(parts...), Rules{})
			if !hasIssue(issues, c.id, c.fragment) {
				t.Errorf("missing issue %q for %v in %v", c.fragment, c.id, issues)
			}
		})
	}
}

func TestErrorBoundaryEventNamespaceRule(t *testing.T) {
	xml := b.Process(b.Start("start"), b.Task("task"), b.ErrorBoundary("x", "task", ""), b.Flow("start", "task"))
	issues := issuesOf(t, xml, Rules{Elements: model.ListRule{Deny: []string{"boundaryEvent:error"}}})
	if !hasIssue(issues, "x", "boundaryEvent:error is not allowed in this namespace") {
		t.Errorf("issues %v", issues)
	}
}

func TestCyclesFollowBoundaryEvents(t *testing.T) {
	xml := b.Process(
		b.Start("start"), b.Task("task"), b.ErrorBoundary("x", "task", ""), b.End("end"),
		b.Flow("start", "task"), b.Flow("task", "end"), b.Flow("x", "task"),
	)
	p := parseValid(t, xml, Rules{})
	cycles := Cycles(p)
	if len(cycles) != 1 || cycles[0].Source.Id != "x" || cycles[0].Target.Id != "task" {
		t.Fatalf("cycles %v", cycles)
	}
	if issues := issuesOf(t, xml, Rules{ForbidCycles: true}); !hasIssue(issues, cycles[0].Id, "closes a loop back to task") {
		t.Errorf("issues %v", issues)
	}
}

func TestCyclesFollowBoundaryEventsBeforeTheirTask(t *testing.T) {
	// the boundary event first in the document; the loop is still closed by a flow
	xml := b.Process(
		b.ErrorBoundary("x", "task", ""), b.Start("start"), b.Task("task"), b.Task("y"), b.End("end"),
		b.Flow("start", "task"), b.Flow("task", "end"), b.Flow("x", "y"), b.Flow("y", "task"),
	)
	cycles := Cycles(parseValid(t, xml, Rules{}))
	if len(cycles) != 1 || cycles[0].Source.Id != "y" || cycles[0].Target.Id != "task" {
		t.Fatalf("cycles %v", cycles)
	}
	if issues := issuesOf(t, xml, Rules{ForbidCycles: true}); len(issues) != 1 {
		t.Errorf("issues %v", issues)
	}
}
