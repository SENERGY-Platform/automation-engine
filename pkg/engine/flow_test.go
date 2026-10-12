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

package engine

import (
	"strings"
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

func TestScriptsAndExclusiveGatewayCompleteTheRun(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(
			b.Start("start"),
			b.Script("double", "x", "n * 2"),
			b.Gateway("exclusive", "xor", b.FlowId("xor", "small")),
			b.Script("big", "size", "'big'"),
			b.Script("small", "size", "'small'"),
			b.Gateway("exclusive", "merge", ""),
			b.End("end"),
			b.Flow("start", "double"), b.Flow("double", "xor"),
			b.Cond("xor", "big", "x > 10"), b.Flow("xor", "small"),
			b.Flow("big", "merge"), b.Flow("small", "merge"), b.Flow("merge", "end"),
		))
		big := h.start(d, map[string]any{"n": 6})
		small := h.start(d, map[string]any{"n": 2})
		h.expectState(big.Id, model.StateCompleted)
		h.expectVars(big.Id, map[string]any{"n": 6.0, "x": 12.0, "size": "big"})
		h.expectVars(small.Id, map[string]any{"size": "small"})
		if rt := h.runtime(big.Id); rt != (runtimeCounts{}) {
			t.Errorf("runtime state must be removed at the end, got %+v", rt)
		}
		conditions := h.activities(big.Id, model.ActivityCondition)
		if len(conditions) != 1 || conditions[0].Data["result"] != true {
			t.Errorf("condition must be logged: %+v", conditions)
		}
		if ended := h.activities(big.Id, model.ActivityRunEnded); len(ended) != 1 || ended[0].Data["state"] != "completed" {
			t.Errorf("run end must be logged: %+v", ended)
		}
	})
}

func TestGatewayWithoutMatchingFlowCreatesIncidentWithVariables(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(
			b.Start("start"), b.Gateway("exclusive", "xor", ""), b.End("a"), b.End("b"),
			b.Flow("start", "xor"), b.Cond("xor", "a", "x == 1"), b.Cond("xor", "b", "x == 2"),
		))
		inst := h.start(d, map[string]any{"x": 3})
		h.expectState(inst.Id, model.StateIncident)
		incidents := h.incidents(inst.Id)
		if len(incidents) != 1 || incidents[0].ElementId != "xor" || incidents[0].Variables["x"] != 3.0 || incidents[0].Run != 1 {
			t.Fatalf("unexpected incidents %+v", incidents)
		}
		if rt := h.runtime(inst.Id); rt != (runtimeCounts{}) {
			t.Errorf("an incident ends the run, got runtime %+v", rt)
		}
	})
}

func TestScriptErrorsAndNonBooleanConditionsAreIncidents(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		failing := h.deploy(b.Process(b.Start("s"), b.Script("bad", "x", "log('before'); undefinedVar + 1"), b.Flow("s", "bad")))
		inst := h.start(failing, nil)
		h.expectState(inst.Id, model.StateIncident)
		if !strings.Contains(h.incidents(inst.Id)[0].Message, "undefinedVar") {
			t.Errorf("unexpected %+v", h.incidents(inst.Id))
		}
		if logs := h.activities(inst.Id, model.ActivityScriptLog); len(logs) != 1 || logs[0].Data["message"] != "before" || logs[0].ElementId != "bad" {
			t.Errorf("log output before the failure must be kept at the element: %+v", logs)
		}
		nonBool := h.deploy(b.Process(b.Start("s"), b.Gateway("exclusive", "xor", ""), b.End("e"), b.Flow("s", "xor"), b.Cond("xor", "e", "'yes'")))
		inst = h.start(nonBool, nil)
		h.expectState(inst.Id, model.StateIncident)
	})
}

func TestParallelGatewayJoinsAllBranches(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(
			b.Start("start"), b.Gateway("parallel", "fork", ""),
			b.Script("a", "a", "1"), b.CatchMessage("wait", "m1"), b.Script("c", "c", "3"),
			b.Gateway("parallel", "join", ""), b.Script("after", "done", "a + c"), b.End("end"),
			b.Message("m1", "go"),
			b.Flow("start", "fork"), b.Flow("fork", "a"), b.Flow("fork", "wait"), b.Flow("fork", "c"),
			b.Flow("a", "join"), b.Flow("wait", "join"), b.Flow("c", "join"), b.Flow("join", "after"), b.Flow("after", "end"),
		))
		inst := h.start(d, nil)
		h.expectState(inst.Id, model.StateRunning)
		if rt := h.runtime(inst.Id); rt.tokens != 3 || rt.subscriptions != 1 {
			t.Fatalf("two tokens wait at the join, one at the message: %+v", rt)
		}
		h.correlate(model.MessageInput{Name: "go", All: true})
		h.expectState(inst.Id, model.StateCompleted)
		h.expectVars(inst.Id, map[string]any{"done": 4.0})
		if entered := h.activities(inst.Id, model.ActivityElementEntered); countElement(entered, "after") != 1 {
			t.Errorf("the join must continue exactly once")
		}
	})
}

func countElement(activities []model.Activity, elementId string) (n int) {
	for _, a := range activities {
		if a.ElementId == elementId {
			n++
		}
	}
	return n
}

func TestInclusiveGatewayWaitsForReachableTokensOnly(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(
			b.Start("start"), b.Gateway("inclusive", "fork", ""),
			b.CatchMessage("wa", "ma"), b.CatchMessage("wb", "mb"), b.Script("c", "c", "'c'"),
			b.Gateway("inclusive", "join", ""), b.Script("after", "joined", "true"), b.End("end"),
			b.Message("ma", "a"), b.Message("mb", "b"),
			b.Flow("start", "fork"),
			b.Cond("fork", "wa", "useA"), b.Cond("fork", "wb", "useB"), b.Cond("fork", "c", "useC"),
			b.Flow("wa", "join"), b.Flow("wb", "join"), b.Flow("c", "join"), b.Flow("join", "after"), b.Flow("after", "end"),
		))
		// only a and c are taken; the join waits for a, but not for b
		inst := h.start(d, map[string]any{"useA": true, "useB": false, "useC": true})
		h.expectState(inst.Id, model.StateRunning)
		h.correlate(model.MessageInput{Name: "a", All: true})
		h.expectState(inst.Id, model.StateCompleted)
		h.expectVars(inst.Id, map[string]any{"joined": true, "c": "c"})
	})
}

func TestInclusiveJoinFiresWhenOtherTokenEndsElsewhere(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(
			b.Start("start"), b.Gateway("parallel", "fork", ""),
			b.CatchMessage("wait", "m"), b.Gateway("exclusive", "choose", ""), b.End("elsewhere"),
			b.Script("direct", "d", "1"),
			b.Gateway("inclusive", "join", ""), b.Script("after", "joined", "true"),
			b.Message("m", "go"),
			b.Flow("start", "fork"), b.Flow("fork", "wait"), b.Flow("fork", "direct"),
			b.Flow("wait", "choose"), b.Cond("choose", "join", "toJoin"), b.Cond("choose", "elsewhere", "!toJoin"),
			b.Flow("direct", "join"), b.Flow("join", "after"),
		))
		inst := h.start(d, map[string]any{"toJoin": false})
		h.expectState(inst.Id, model.StateRunning)
		// the waiting token could still reach the join, so the join waits
		if rt := h.runtime(inst.Id); rt.tokens != 2 {
			t.Fatalf("unexpected runtime %+v", rt)
		}
		h.correlate(model.MessageInput{Name: "go", All: true})
		h.expectState(inst.Id, model.StateCompleted)
		h.expectVars(inst.Id, map[string]any{"joined": true})
	})
}

func TestVariableScopesFollowCamunda(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(
			b.Start("start"),
			b.Task("map", b.InText("local", "text"), b.InScript("calc", "x + 1"), b.OutScript("out", "local + calc")),
			`<bpmn:scriptTask id="script" scriptFormat="javascript" camunda:resultVariable="r"><bpmn:extensionElements><camunda:inputOutput><camunda:inputParameter name="r">0</camunda:inputParameter><camunda:outputParameter name="fromLocal"><camunda:script scriptFormat="javascript">r</camunda:script></camunda:outputParameter></camunda:inputOutput></bpmn:extensionElements><bpmn:script>setVariable("global", 5); typeof local</bpmn:script></bpmn:scriptTask>`,
			`<bpmn:task id="nested"><bpmn:extensionElements><camunda:inputOutput><camunda:outputParameter name="obj"><camunda:map><camunda:entry key="a">1</camunda:entry><camunda:entry key="b"><camunda:script scriptFormat="javascript">x</camunda:script></camunda:entry></camunda:map></camunda:outputParameter></camunda:inputOutput></bpmn:extensionElements></bpmn:task>`,
			b.Flow("start", "map"), b.Flow("map", "script"), b.Flow("script", "nested"),
		))
		inst := h.start(d, map[string]any{"x": 1})
		h.expectState(inst.Id, model.StateCompleted)
		vars := h.vars(inst.Id)
		h.expectVars(inst.Id, map[string]any{
			"out": "text2", "global": 5.0,
			// r exists locally through the input mapping, so the result stays local and leaves through the output
			"fromLocal": "undefined",
			"obj":       map[string]any{"a": "1", "b": 1.0},
		})
		for _, local := range []string{"local", "calc", "r"} {
			if _, ok := vars[local]; ok {
				t.Errorf("local variable %v must not become an instance variable", local)
			}
		}
	})
}
