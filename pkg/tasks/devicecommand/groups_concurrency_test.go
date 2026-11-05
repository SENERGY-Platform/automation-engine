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
	"context"
	"html"
	"reflect"
	"strconv"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// With GROUP_CONCURRENCY or senergy:groupConcurrency, sequential and round robin groups keep up
// to that many tries open at once (SNRGY-5093).

// processWithConcurrency is process with senergy:groupConcurrency on the task
func processWithConcurrency(concurrency string, payloadJson string) string {
	return b.Process(
		b.Start("start"),
		`<bpmn:serviceTask id="cmd" senergy:taskType="device_command" senergy:completionStrategy="pessimistic" senergy:groupConcurrency="`+concurrency+`">`+
			`<bpmn:extensionElements><camunda:inputOutput><camunda:inputParameter name="payload">`+html.EscapeString(payloadJson)+`</camunda:inputParameter>`+
			`<camunda:outputParameter name="out"><camunda:script scriptFormat="javascript">result</camunda:script></camunda:outputParameter></camunda:inputOutput></bpmn:extensionElements></bpmn:serviceTask>`,
		b.End("end"), b.Flow("start", "cmd"), b.Flow("cmd", "end"),
	)
}

// fiveDevicesWithConcurrency is fiveDevices with GROUP_CONCURRENCY
func fiveDevicesWithConcurrency(e *env, scheduler string, concurrency int) {
	fiveDevices(e, scheduler)
	e.dc.config.GroupConcurrency = concurrency
}

func sentDevices(list []sent) []string {
	result := []string{}
	for _, s := range list {
		result = append(result, s.key)
	}
	return result
}

// answerDevice answers the commands to device that are not answered yet with its number
func answerDevice(e *env, answered map[int]bool, device string) {
	for i, s := range e.producer.messages() {
		if !answered[i] && s.key == device {
			answered[i] = true
			value, _ := strconv.Atoi(device[1:])
			e.respond(s, float64(value))
		}
	}
}

func TestGroupConcurrencyKeepsAtMostThatManyTriesOpen(t *testing.T) {
	for _, scheduler := range []string{Sequential, RoundRobin} {
		t.Run(scheduler, func(t *testing.T) {
			e := newEnv(t, scheduler)
			fiveDevicesWithConcurrency(e, scheduler, 2)
			inst := e.start(e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_group_id": "g", "retries": 0}), ""), nil))
			answered := map[int]bool{}
			for range 10 {
				messages := e.producer.messages()
				if open := len(messages) - len(answered); open > 2 {
					t.Fatalf("%v tries open at once: %v", open, sentDevices(messages))
				}
				// the oldest open try answers first
				for i, s := range messages {
					if !answered[i] {
						answerDevice(e, answered, s.key)
						break
					}
				}
			}
			if got := sentDevices(e.producer.messages()); !reflect.DeepEqual(got, []string{"d0", "d1", "d2", "d3", "d4"}) {
				t.Errorf("each device gets its command once, in order: %v", got)
			}
			e.expectState(inst.Id, model.StateCompleted)
			if out := e.variable(inst.Id, "out"); !reflect.DeepEqual(out, []any{0.0, 1.0, 2.0, 3.0, 4.0}) {
				t.Fatalf("results in device order: %#v", out)
			}
		})
	}
}

// the measured case of SNRGY-5093: a shutter without radio contact held up the others for
// timeout × (retries + 1)
func TestGroupConcurrencyASilentDeviceDoesNotHoldUpTheOthers(t *testing.T) {
	for _, scheduler := range []string{Sequential, RoundRobin} {
		t.Run(scheduler, func(t *testing.T) {
			e := newEnv(t, scheduler)
			fiveDevicesWithConcurrency(e, scheduler, 2)
			inst := e.start(e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_group_id": "g", "retries": 1}), ""), nil))
			answered := map[int]bool{}
			for range 5 {
				for _, device := range []string{"d0", "d2", "d3", "d4"} {
					answerDevice(e, answered, device)
				}
			}
			// the clock has not moved: every other device answered within the window of d1
			if got := sentDevices(e.producer.messages()); !reflect.DeepEqual(got, []string{"d0", "d1", "d2", "d3", "d4"}) {
				t.Errorf("the others are asked while d1 is silent: %v", got)
			}
			e.expectState(inst.Id, model.StateRunning)
			for range 3 {
				e.advance(2*time.Minute + time.Second)
			}
			if got := sentDevices(e.producer.messages()); !reflect.DeepEqual(got, []string{"d0", "d1", "d2", "d3", "d4", "d1"}) {
				t.Errorf("d1 gets its second try: %v", got)
			}
			e.expectState(inst.Id, model.StateCompleted)
			if out := e.variable(inst.Id, "out"); !reflect.DeepEqual(out, []any{0.0, 2.0, 3.0, 4.0}) {
				t.Fatalf("results of the answering devices: %#v", out)
			}
		})
	}
}

// a try sent after the execution gets its window, and the end of an earlier window frees its
// place at once rather than at the end of the task timeout
func TestGroupConcurrencyTheEndOfAnEarlierWindowFreesAPlace(t *testing.T) {
	e := newEnv(t, Sequential)
	fiveDevicesWithConcurrency(e, Sequential, 2)
	inst := e.start(e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_group_id": "g", "retries": 0}), ""), nil))
	answered := map[int]bool{}
	// d0 stays silent; d1 answers after 30s, which sends d2, silent as well
	e.advance(30 * time.Second)
	answerDevice(e, answered, "d1")
	// d0's window ends: d3 takes its place and stays silent
	e.advance(90*time.Second + time.Second)
	if got := sentDevices(e.producer.messages()); !reflect.DeepEqual(got, []string{"d0", "d1", "d2", "d3"}) {
		t.Fatalf("d3 is sent at the end of the window of d0: %v", got)
	}
	// d2's window ends 30s later, long before the timeout of the execution that sent d3
	e.advance(30 * time.Second)
	if got := sentDevices(e.producer.messages()); !reflect.DeepEqual(got, []string{"d0", "d1", "d2", "d3", "d4"}) {
		t.Fatalf("d4 is sent at the end of the window of d2: %v", got)
	}
	answerDevice(e, answered, "d4")
	e.advance(2 * time.Minute)
	e.expectState(inst.Id, model.StateCompleted)
	if out := e.variable(inst.Id, "out"); !reflect.DeepEqual(out, []any{1.0, 4.0}) {
		t.Fatalf("results of the answering devices: %#v", out)
	}
}

// the element overrides GROUP_CONCURRENCY in both directions, also for the sub-tasks that
// answers send
func TestGroupConcurrencyOfTheElementOverridesTheEngine(t *testing.T) {
	for _, c := range []struct {
		engine, element int
	}{{1, 2}, {3, 1}} {
		t.Run(strconv.Itoa(c.engine)+"-"+strconv.Itoa(c.element), func(t *testing.T) {
			e := newEnv(t, Sequential)
			fiveDevicesWithConcurrency(e, Sequential, c.engine)
			e.start(e.deploy(processWithConcurrency(strconv.Itoa(c.element), payload(measuring, map[string]any{"device_group_id": "g", "retries": 0})), nil))
			if got := len(e.producer.messages()); got != c.element {
				t.Fatalf("expected %v commands at the start, got %v", c.element, got)
			}
			answerDevice(e, map[int]bool{}, "d0")
			if got := len(e.producer.messages()); got != c.element+1 {
				t.Fatalf("the answer frees one place: expected %v commands, got %v", c.element+1, got)
			}
		})
	}
}

// parallel sends to every device at once, whatever the concurrency
func TestGroupConcurrencyDoesNotLimitParallel(t *testing.T) {
	e := newEnv(t, Parallel)
	fiveDevicesWithConcurrency(e, Parallel, 2)
	e.start(e.deploy(processWithConcurrency("2", payload(measuring, map[string]any{"device_group_id": "g", "retries": 0})), nil))
	if got := len(e.producer.messages()); got != 5 {
		t.Fatalf("expected 5 commands, got %v", got)
	}
}

// hookProducer sends like the fake producer and then runs hook with the number of commands,
// e.g. for an answer that arrives while the sender has more commands to send
type hookProducer struct {
	*fakeProducer
	hook func(n int)
}

func (this *hookProducer) Produce(ctx context.Context, topic string, key string, payload []byte) error {
	if err := this.fakeProducer.Produce(ctx, topic, key, payload); err != nil {
		return err
	}
	if this.hook != nil {
		this.hook(len(this.messages()))
	}
	return nil
}

func withHook(e *env, hook func(n int)) {
	e.dc.producer = &hookProducer{fakeProducer: e.producer, hook: hook}
}

// an answer that arrives while the execution still fills its places, e.g. from a fast local
// connector, sends the next sub-task; the execution does not send that one a second time
func TestGroupConcurrencyAnAnswerDuringTheExecutionIsNoSecondCommand(t *testing.T) {
	e := newEnv(t, Sequential)
	fiveDevicesWithConcurrency(e, Sequential, 2)
	answered := false
	withHook(e, func(n int) {
		if n == 1 && !answered {
			answered = true
			e.respond(e.producer.messages()[0], 0.0)
		}
	})
	e.start(e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_group_id": "g", "retries": 1}), ""), nil))
	if got := sentDevices(e.producer.messages()); !reflect.DeepEqual(got, []string{"d0", "d1", "d2"}) {
		t.Fatalf("each device once, two open: %v", got)
	}
}

// two answers handled at the same time: the handler of d1 has stored its result, the handler of
// d0 sees both places free, and the one of d1 goes on while d0's sends its first command
func TestGroupConcurrencyTwoAnswersAtOnceSendEachDeviceOnce(t *testing.T) {
	e := newEnv(t, Sequential)
	fiveDevicesWithConcurrency(e, Sequential, 2)
	e.start(e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_group_id": "g", "retries": 1}), ""), nil))
	first := e.producer.messages()
	if err := e.s.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.SetSubtaskResult(jobIdOf(first[1].msg.TaskInfo.TaskId), first[1].msg.TaskInfo.TaskId, 1.0)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	answered := false
	withHook(e, func(n int) {
		if n == 3 && !answered {
			answered = true
			e.respond(first[1], 1.0)
		}
	})
	e.respond(first[0], 0.0)
	if got := sentDevices(e.producer.messages()); !reflect.DeepEqual(got, []string{"d0", "d1", "d2", "d3"}) {
		t.Fatalf("each device once, two open: %v", got)
	}
}

// the element overrides GROUP_CONCURRENCY also when the end of a window frees a place
func TestGroupConcurrencyOfTheElementHoldsOnTimeout(t *testing.T) {
	e := newEnv(t, Sequential)
	fiveDevicesWithConcurrency(e, Sequential, 1)
	e.start(e.deploy(processWithConcurrency("2", payload(measuring, map[string]any{"device_group_id": "g", "retries": 0})), nil))
	e.advance(30 * time.Second)
	answerDevice(e, map[int]bool{}, "d1")
	// d0's window ends, d2 is still open: one place is free
	e.advance(90*time.Second + time.Second)
	if got := sentDevices(e.producer.messages()); !reflect.DeepEqual(got, []string{"d0", "d1", "d2", "d3"}) {
		t.Fatalf("d3 takes the place of d0: %v", got)
	}
}

// with free places, round robin asks the devices without a try before the retries of silent
// ones; sequential retries them first
func TestGroupConcurrencyRoundRobinAsksTheOthersBeforeARetry(t *testing.T) {
	for scheduler, expected := range map[string][]string{
		RoundRobin: {"d0", "d1", "d2", "d3"},
		Sequential: {"d0", "d1", "d0", "d1"},
	} {
		t.Run(scheduler, func(t *testing.T) {
			e := newEnv(t, scheduler)
			fiveDevicesWithConcurrency(e, scheduler, 2)
			e.start(e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_group_id": "g", "retries": 1}), ""), nil))
			e.advance(2*time.Minute + time.Second)
			if got := sentDevices(e.producer.messages()); !reflect.DeepEqual(got, expected) {
				t.Fatalf("expected %v, got %v", expected, got)
			}
		})
	}
}
