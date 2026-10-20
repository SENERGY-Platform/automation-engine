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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// engineModels are models in the format of the engine with every construct the rewrite looks
// at: task types instead of topics, JavaScript in scripts, mappings, conditions, timers and
// message names, literal timers including cycles, device events and form fields
func engineModels(t *testing.T) map[string]string {
	payload := `{"function":{"id":"urn:infai:ses:measuring-function:f"},"characteristic_id":"c","label":"get","input":{},"retries":2}`
	result := map[string]string{
		"scripts and conditions": b.Process(
			b.StartWithForm("start", "level=5", "name=${not juel in a default}"),
			b.Script("count", "count", "var myexecution = {done: 1}; run.execution = 2; Number(level) + myexecution.done"),
			b.Gateway("exclusive", "gw", b.FlowId("gw", "low")),
			b.Cond("gw", "high", "count > 3 && name !== 'x'"),
			b.Flow("gw", "low"),
			b.Task("high", b.InScript("text", "`level ${level}`"), b.OutScript("seen", "true")),
			b.Task("low", b.InText("plain", "just text"), `<camunda:inputParameter name="map"><camunda:map><camunda:entry key="a">1</camunda:entry>`+
				`<camunda:entry key="b"><camunda:script scriptFormat="javascript">level * 2</camunda:script></camunda:entry></camunda:map></camunda:inputParameter>`,
				`<camunda:inputParameter name="list"><camunda:list><camunda:value>x</camunda:value></camunda:list></camunda:inputParameter>`),
			b.End("end1"), b.End("end2"),
			b.Flow("start", "count"), b.Flow("count", "gw"), b.Flow("high", "end1"), b.Flow("low", "end2"),
		),
		"timers": b.Process(
			b.StartTimer("start", "timeCycle", "0 0 6 * * ?"),
			b.CatchTimer("duration", "timeDuration", "PT5S"),
			b.CatchTimer("cycle", "timeCycle", "R3/PT10S"),
			b.CatchTimer("date", "timeDate", "2030-01-01T00:00:00Z"),
			b.CatchTimerExpression("computed", "timeDuration", "`PT${seconds}S`"),
			b.End("end"),
			b.Flow("start", "duration"), b.Flow("duration", "cycle"), b.Flow("cycle", "date"), b.Flow("date", "computed"), b.Flow("computed", "end"),
		),
		"messages and task types": b.Process(
			b.Message("m", "go"),
			b.Start("start"),
			b.Service("cmd", "device_command", b.InText("payload", payload), b.OutScript("value", "result")),
			`<bpmn:serviceTask id="opt" senergy:taskType="device_command" senergy:completionStrategy="optimistic">`+
				b.IO(b.InText("payload", payload), b.InText("inputs.b", "100"))+`</bpmn:serviceTask>`,
			b.Service("note", "notification", b.InText("title", "Hello"), b.InScript("message", "`level ${level}`")),
			b.Service("io", "process_io", b.InText("set.key", "value")),
			b.CatchMessage("wait", "m", b.OutScript("received", "payload")),
			`<bpmn:intermediateCatchEvent id="named" senergy:messageName="'done_' + id"><bpmn:messageEventDefinition/></bpmn:intermediateCatchEvent>`,
			b.CatchDeviceEvent("device"),
			b.End("end"),
			b.Flow("start", "cmd"), b.Flow("cmd", "opt"), b.Flow("opt", "note"), b.Flow("note", "io"), b.Flow("io", "wait"),
			b.Flow("wait", "named"), b.Flow("named", "device"), b.Flow("device", "end"),
		),
		// the old designer writes device events in the format of the engine already
		"device event of the old designer": readFile(t, "../../client/testdata/process-deployment/conditional_event_3/raw.bpmn"),
	}
	examples, err := filepath.Glob("../../../examples/*.bpmn")
	if err != nil || len(examples) == 0 {
		t.Fatalf("no examples: %v", err)
	}
	for _, file := range examples {
		result["example "+filepath.Base(file)] = readFile(t, file)
	}
	return result
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// A model in the format of the engine comes back byte for byte with an empty report, so the
// rewrite tells the formats apart; deploy, prepare and validation rely on it (MIG-04).
func TestModelsOfTheEngineAreLeftAsTheyAre(t *testing.T) {
	for name, xml := range engineModels(t) {
		t.Run(name, func(t *testing.T) {
			if _, issues, err := bpmn.ParseAndValidate(xml, bpmn.Rules{}); err != nil || len(issues) > 0 {
				t.Fatalf("the model itself is valid: %v %v", issues, err)
			}
			result, report, err := Rewrite(xml)
			if err != nil {
				t.Fatal(err)
			}
			if !report.Empty() {
				t.Errorf("nothing is reported: %+v", report)
			}
			if result != xml {
				t.Errorf("the model is returned unchanged:\n%v", result)
			}
			checked, err := Check(xml, bpmn.Rules{})
			if err != nil || checked.Xml != xml || len(checked.Issues) > 0 || len(checked.Hints()) > 0 {
				t.Errorf("checking it neither converts nor hints: %+v %v", checked, err)
			}
		})
	}
}

// oldModels are models of the old designer and of Camunda; their conversion is not repeated
func oldModels(t *testing.T) map[string]string {
	result := map[string]string{
		"0pools0lanes2tasks": readFile(t, "../../client/testdata/process-deployment/0pools0lanes2tasks/raw.bpmn"),
		"juel, groovy, connectors and timers": definitions(`<bpmn:startEvent id="start"/>`+
			`<bpmn:scriptTask id="log" scriptFormat="groovy"><bpmn:script>println "started ${x}"</bpmn:script></bpmn:scriptTask>`+
			`<bpmn:serviceTask id="n"><bpmn:extensionElements><camunda:connector><camunda:inputOutput>`+
			`<camunda:inputParameter name="payload">{"message":"Level ${level}","title":"Warning"}</camunda:inputParameter>`+
			`<camunda:inputParameter name="deploymentIdentifier">notification</camunda:inputParameter></camunda:inputOutput>`+
			`<camunda:connectorId>http-connector</camunda:connectorId></camunda:connector></bpmn:extensionElements></bpmn:serviceTask>`+
			`<bpmn:intermediateCatchEvent id="wait"><bpmn:timerEventDefinition><bpmn:timeDuration xsi:type="bpmn:tFormalExpression">R/P1D</bpmn:timeDuration></bpmn:timerEventDefinition></bpmn:intermediateCatchEvent>`+
			`<bpmn:intermediateCatchEvent id="done"><bpmn:messageEventDefinition messageRef="m"/></bpmn:intermediateCatchEvent>`+
			`<bpmn:endEvent id="end"/>`+flow("f1", "start", "log")+flow("f2", "log", "n")+flow("f3", "n", "wait")+flow("f4", "wait", "done")+flow("f5", "done", "end"),
			`<bpmn:message id="m" name="${done_event}"/>`),
	}
	for _, dir := range []string{"../../deprecated/testdata/prepared/*/raw.bpmn", "../../deprecated/testdata/smartservices/*.bpmn"} {
		files, err := filepath.Glob(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			result[file] = readFile(t, file)
		}
	}
	return result
}

// Converting a converted model finds nothing, so a model the deprecated layer or the migration
// converted is not converted again when it is deployed.
func TestAConvertedModelIsNotConvertedAgain(t *testing.T) {
	converted := 0
	for name, xml := range oldModels(t) {
		t.Run(name, func(t *testing.T) {
			once, report, err := Rewrite(xml)
			if err != nil {
				t.Fatal(err)
			}
			if !report.Complete() {
				t.Skipf("not convertible: %+v", report.Unsupported)
			}
			if !report.Empty() {
				converted++
			}
			twice, again, err := Rewrite(once)
			if err != nil || !again.Empty() || twice != once {
				t.Errorf("the second conversion finds nothing: %+v %v", again, err)
			}
		})
	}
	if converted < 5 {
		t.Errorf("expected several models of the old format, %v were converted", converted)
	}
}

// In a model with old and new parts only the old ones are rewritten and reported.
func TestOnlyTheOldPartsOfAMixedModelAreRewritten(t *testing.T) {
	newTask := `<bpmn:serviceTask id="new" senergy:taskType="device_command" senergy:completionStrategy="pessimistic">` +
		b.IO(b.InText("payload", `{"function":{"id":"f"}}`), b.OutScript("value", "result")) + `</bpmn:serviceTask>`
	oldTask := `<bpmn:serviceTask id="old" camunda:type="external" camunda:topic="optimistic">` +
		b.IO(b.InText("payload", `{"function":{"id":"f"}}`), `<camunda:outputParameter name="other">${result}</camunda:outputParameter>`) + `</bpmn:serviceTask>`
	xml := b.Process(
		b.Start("start"), newTask, oldTask, b.Gateway("exclusive", "gw", b.FlowId("gw", "e1")),
		b.Cond("gw", "e2", "value > 1"),
		`<bpmn:sequenceFlow id="juel" sourceRef="gw" targetRef="e3"><bpmn:conditionExpression xsi:type="bpmn:tFormalExpression">${other lt 1}</bpmn:conditionExpression></bpmn:sequenceFlow>`,
		b.End("e1"), b.End("e2"), b.End("e3"),
		b.Flow("start", "new"), b.Flow("new", "old"), b.Flow("old", "gw"), b.Flow("gw", "e1"),
	)
	result, report, err := Rewrite(xml)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range report.Rewritten {
		if f.ElementId != "old" && f.ElementId != "juel" {
			t.Errorf("only old parts are reported: %+v", f)
		}
	}
	if len(report.Rewritten) != 3 || !report.Complete() || len(report.Check) != 0 {
		t.Fatalf("topic, output and condition are rewritten: %+v", report)
	}
	p, issues, err := bpmn.ParseAndValidate(result, bpmn.Rules{})
	if err != nil || len(issues) > 0 {
		t.Fatalf("%v %v", issues, err)
	}
	if n := p.Elements["new"]; n.TaskType != "device_command" || n.Attributes["completionStrategy"] != "pessimistic" || n.Outputs[0].Value.Script != "result" {
		t.Errorf("the new task stays: %+v", n)
	}
	if o := p.Elements["old"]; o.TaskType != "device_command" || o.Attributes["completionStrategy"] != "optimistic" || o.Outputs[0].Value.Script != "result" {
		t.Errorf("the old task is rewritten: %+v", o)
	}
	if c := p.Flows[b.FlowId("gw", "e2")].Condition; c != "value > 1" {
		t.Errorf("the JavaScript condition stays: %q", c)
	}
	if c := p.Flows["juel"].Condition; c != "other < 1" {
		t.Errorf("the JUEL condition is rewritten: %q", c)
	}
	for _, id := range []string{"new", "old"} {
		if in := p.Elements[id].Inputs[0]; in.Name != "payload" || in.Value.Text != `{"function":{"id":"f"}}` {
			t.Errorf("payloads stay as they are: %+v", in)
		}
	}
}

// Check validates the converted model: unsupported parts come first and an issue of the same
// element is left out; a save gets hints instead of the findings of the old format.
func TestCheckValidatesTheConvertedModel(t *testing.T) {
	old := readFile(t, "../../client/testdata/process-deployment/0pools0lanes2tasks/raw.bpmn")
	if _, issues, _ := bpmn.ParseAndValidate(old, bpmn.Rules{}); len(issues) == 0 {
		t.Fatal("the old model itself has issues in the engine")
	}
	checked, err := Check(old, bpmn.Rules{})
	if err != nil || len(checked.Issues) > 0 || checked.Process.Elements["Task_1nbnl8y"].TaskType != "device_command" {
		t.Fatalf("converted, the model has no issues: %+v %v", checked.Issues, err)
	}
	hints := checked.Hints()
	if len(hints) != 1 || !hints[0].Hint || !strings.Contains(hints[0].Message, "converted when deployed") {
		t.Errorf("one hint says that it is converted: %+v", hints)
	}
	mystery := definitions(around("m", `<bpmn:serviceTask id="m" camunda:type="external" camunda:topic="mystery"/>`))
	checked, err = Check(mystery, bpmn.Rules{TaskTypes: model.ListRule{Allow: []string{"device_command"}}})
	if err != nil || len(checked.Issues) != 1 || !strings.Contains(checked.Issues[0].Message, "mystery") {
		t.Errorf("the unsupported part is named once: %+v %v", checked.Issues, err)
	}
	if _, err = Check("<broken", bpmn.Rules{}); err == nil {
		t.Error("unreadable XML is an error")
	}
}
