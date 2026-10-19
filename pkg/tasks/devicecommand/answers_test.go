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

package devicecommand

import (
	"reflect"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// answerEnv runs a pessimistic command and answers it with 21.5
func answerEnv(t *testing.T, extra map[string]any) (*env, *recordingMarshaller, model.Instance) {
	e := newEnv(t, Parallel)
	m := &recordingMarshaller{}
	e.use(newRepo(), aspectTree(), m, Parallel)
	fields := map[string]any{"device_id": "d1", "service_id": "s-get"}
	for k, v := range extra {
		fields[k] = v
	}
	d := e.deploy(process(Pessimistic, payload(measuring, fields), ""), nil)
	inst := e.start(d)
	e.respond(e.producer.messages()[0], 21.5)
	return e, m, inst
}

// external-task-worker TestResponse (SNRGY-1807)
func TestAnswerOfAVersion3CommandIsReadAtItsOutputPath(t *testing.T) {
	_, m, _ := answerEnv(t, map[string]any{"version": 3, "output_path": "value.level", "aspect": map[string]any{"id": "inside_air"}})
	answers := m.v2Answers()
	if len(answers) != 1 {
		t.Fatalf("%+v", answers)
	}
	a := answers[0]
	if a.Path != "value.level" || a.CharacteristicId != "c" || a.FunctionId != measuring || !reflect.DeepEqual(aspectIds(a.GetAspectNodes()), []string{"inside_air"}) {
		t.Errorf("the answer is read at the output path of the command: %+v", a)
	}
}

// external-task-worker TestMultiAspectDeviceResponseUnmarshalsTheVariableServingEveryAspect
func TestAnswerWithoutOutputPathIsFoundByFunctionAndEveryAspect(t *testing.T) {
	_, m, _ := answerEnv(t, map[string]any{"version": 3, "aspects": []map[string]any{{"id": "inside_air"}, {"id": "light"}}})
	answers := m.v2Answers()
	if len(answers) != 1 {
		t.Fatalf("%+v", answers)
	}
	a := answers[0]
	if a.Path != "" || a.FunctionId != measuring || !reflect.DeepEqual(aspectIds(a.GetAspectNodes()), []string{"inside_air", "light"}) {
		t.Errorf("without path the marshaller searches by function and all aspects: %+v", a)
	}
}

// external-task-worker TestResponseWithHints
func TestAnswerBeforeVersion3IsReadWithTheHintsOfTheCommand(t *testing.T) {
	_, m, _ := answerEnv(t, map[string]any{"content_variable_hints": []string{"level1", "foo"}})
	answers := m.v1Answers()
	if len(answers) != 1 || answers[0].characteristicId != "c" || !reflect.DeepEqual(answers[0].hints, []string{"level1", "foo"}) {
		t.Fatalf("the hints choose the variable to read: %+v", answers)
	}
}

// external-task-worker TestWorkerResponse: payloads without version complete with the value
func TestResultBeforeVersion2IsTheValueItself(t *testing.T) {
	for _, version := range []int{0, 1} {
		e, _, inst := answerEnv(t, map[string]any{"version": version})
		e.expectState(inst.Id, model.StateCompleted)
		if out := e.variable(inst.Id, "out"); out != 21.5 {
			t.Errorf("version %v: %#v", version, out)
		}
	}
}

// external-task-worker TestWorkerNullResponse, TestWorkerNullResponse2
func TestControllingCommandCompletesWithNullWhateverTheAnswer(t *testing.T) {
	e := newEnv(t, Parallel)
	m := &recordingMarshaller{}
	e.use(newRepo(), e.aspects, m, Parallel)
	d := e.deploy(process(Pessimistic, payload(controlling, map[string]any{"version": 0, "device_id": "d1", "service_id": "s-set", "input": true}), ""), nil)
	inst := e.start(d)
	e.respond(e.producer.messages()[0], "ignored")
	e.expectState(inst.Id, model.StateCompleted)
	if out := e.variable(inst.Id, "out"); out != nil {
		t.Errorf("a command without output characteristic has no result: %#v", out)
	}
	if len(m.v1Answers()) != 0 || len(m.v2Answers()) != 0 {
		t.Errorf("the answer is not unmarshalled: %+v %+v", m.v1Answers(), m.v2Answers())
	}
}
