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
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/script"
)

// Every script the engine runs is listed with its element; module scripts joined as they run.
func TestScriptsListsWhatTheEngineRuns(t *testing.T) {
	p, issues, err := Parse(b.Process(
		b.Message("m", "go"),
		b.StartTimerExpression("start", "timeCycle", `"R/PT1H"`),
		b.Service("module", "info", b.InText("prescript_2", "b();"), b.InText("prescript_1", "a();"), b.InText("postscript", "c();"), b.InScript("in", "1 + 1"), b.OutScript("out", "2 + 2")),
		b.Script("calc", "x", "x * 2"),
		b.Gateway("exclusive", "gw", ""), b.CatchDeviceEvent("event"), b.End("end"),
		`<bpmn:intermediateCatchEvent id="named" senergy:messageName="'done_' + x"><bpmn:messageEventDefinition/></bpmn:intermediateCatchEvent>`,
		b.Flow("start", "module"), b.Flow("module", "calc"), b.Flow("calc", "gw"), b.Cond("gw", "event", "x > 1"), b.Cond("gw", "named", "x <= 1"), b.Flow("event", "end"), b.Flow("named", "end"),
	))
	if err != nil || len(issues) > 0 {
		t.Fatal(err, issues)
	}
	got := []string{}
	for _, s := range p.Scripts() {
		got = append(got, s.ElementId+": "+s.Source)
	}
	for _, expected := range []string{`start: "R/PT1H"`, "module: a();b();", "module: c();", "module: 1 + 1", "module: 2 + 2", "calc: x * 2", "gw: x > 1", "gw: x <= 1", "event: x == 42", "named: 'done_' + x"} {
		if !slices.Contains(got, expected) {
			t.Errorf("missing %q in %q", expected, got)
		}
	}
}

// A copy of a blocked script, also formatted differently, is refused with the element.
func TestValidationRefusesBlockedScripts(t *testing.T) {
	blocklist := script.NewBlocklist()
	blocklist.Add(script.Fingerprint(`var o = {}; o.toString = String.prototype.trim; String(o)`), "fatal error: stack overflow")
	_, issues, err := ParseAndValidate(b.Process(b.Start("start"), b.Script("boom", "x", "// harmless\nvar o = {}; o.toString = String.prototype.trim; String(o) // copy"), b.End("end"),
		b.Flow("start", "boom"), b.Flow("boom", "end")), Rules{Blocklist: blocklist})
	if err != nil || len(issues) != 1 || issues[0].ElementId != "boom" || !strings.Contains(issues[0].Message, "stack overflow") {
		t.Fatalf("%+v %v", issues, err)
	}
}
