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
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// caughtProcess runs task, whose error boundary event "caught" maps its local variables to
// instance variables and leads to "failed"; without error the process ends at "end"
func caughtProcess(task string, messageVariable string) string {
	return b.Process(
		b.Start("start"), task,
		b.ErrorBoundary("caught", "task", messageVariable, b.OutScript("caught_message", "error_message"),
			b.OutScript("caught_device", "typeof device_id === 'undefined' ? 'undefined' : device_id")),
		b.End("end"), b.End("failed"),
		b.Flow("start", "task"), b.Flow("task", "end"), b.Flow("caught", "failed"),
	)
}

// expectCaught checks that the run went through the boundary event without incident
func expectCaught(t *testing.T, h *harness, instanceId string, message string) {
	t.Helper()
	h.expectState(instanceId, model.StateCompleted)
	if incidents := h.incidents(instanceId); len(incidents) != 0 {
		t.Fatalf("a caught error is no incident: %+v", incidents)
	}
	if got, _ := h.vars(instanceId)["caught_message"].(string); !strings.Contains(got, message) {
		t.Errorf("error_message %q does not contain %q", got, message)
	}
	var caught []model.Activity
	for _, a := range h.activities(instanceId, model.ActivityTaskFailed) {
		if a.Data["caught_by"] == "caught" {
			caught = append(caught, a)
		}
	}
	if len(caught) != 1 || caught[0].ElementId != "task" {
		t.Errorf("the history shows the failure of the task once with caught_by: %+v", caught)
	}
	entered := h.activities(instanceId, model.ActivityElementEntered)
	path := []string{}
	for _, a := range entered {
		path = append(path, a.ElementId)
	}
	if strings.Join(path, ",") != "start,task,caught,failed" {
		t.Errorf("path %v", path)
	}
	if r := h.runtime(instanceId); r.jobs != 0 || r.tokens != 0 {
		t.Errorf("runtime left %+v", r)
	}
}

func TestErrorBoundaryCatchesScriptError(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		task := `<bpmn:scriptTask id="task" scriptFormat="javascript"><bpmn:script>throw new Error("boom")</bpmn:script></bpmn:scriptTask>`
		inst := h.start(h.deploy(caughtProcess(task, "failure")), nil)
		expectCaught(t, h, inst.Id, "boom")
		vars := h.vars(inst.Id)
		if failure, _ := vars["failure"].(string); !strings.Contains(failure, "boom") {
			t.Errorf("camunda:errorMessageVariable names an instance variable with the message: %#v", vars)
		}
		if _, ok := vars["error_message"]; ok {
			t.Errorf("error_message is local to the boundary event")
		}
		if vars["caught_device"] != "undefined" {
			t.Errorf("only device commands get device_id: %#v", vars["caught_device"])
		}
	})
}

func TestErrorBoundaryCatchesMappingErrorOfTask(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		inst := h.start(h.deploy(caughtProcess(b.Task("task", b.OutScript("x", "missing.field")), "")), nil)
		expectCaught(t, h, inst.Id, "missing")
	})
}

func TestErrorBoundaryCatchesLastFailedAttempt(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		var attempts []int
		h.e.RegisterTaskType("echo", TaskTypeFunc(func(ctx context.Context, task Task) (TaskResult, error) {
			attempts = append(attempts, task.Attempt)
			return TaskResult{}, WithVariables(errors.New("device offline"), map[string]any{"device_id": "d1"})
		}))
		task := `<bpmn:serviceTask id="task" senergy:taskType="echo" senergy:retries="2"/>`
		inst := h.start(h.deploy(caughtProcess(task, "")), nil)
		h.process()
		h.expectState(inst.Id, model.StateRunning)
		h.advance(time.Second)
		h.advance(2 * time.Second)
		if len(attempts) != 3 {
			t.Fatalf("earlier attempts are retried as without boundary event: %v", attempts)
		}
		expectCaught(t, h, inst.Id, "device offline")
		h.expectVars(inst.Id, map[string]any{"caught_device": "d1"})
		if failed := h.activities(inst.Id, model.ActivityTaskFailed); len(failed) != 3 || failed[0].Data["caught_by"] != nil {
			t.Errorf("every attempt is logged, only the last one is caught: %+v", failed)
		}
	})
}

func TestErrorBoundaryCatchesPermanentErrorAtOnce(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		attempts := 0
		h.e.RegisterTaskType("echo", TaskTypeFunc(func(ctx context.Context, task Task) (TaskResult, error) {
			attempts++
			return TaskResult{}, WithVariables(&PermanentError{Message: "invalid payload"}, nil)
		}))
		inst := h.start(h.deploy(caughtProcess(b.Service("task", "echo"), "")), nil)
		h.process()
		if attempts != 1 {
			t.Errorf("a permanent error skips the retries: %v", attempts)
		}
		expectCaught(t, h, inst.Id, "invalid payload")
	})
}

type namingTaskType struct{}

func (namingTaskType) Execute(ctx context.Context, task Task) (TaskResult, error) {
	return TaskResult{Async: true}, nil
}

func (namingTaskType) FailureVariables(tx *store.Tx, job store.Job) (map[string]any, error) {
	return map[string]any{"device_id": "from-" + job.ElementId}, nil
}

func TestErrorBoundaryCatchesTimeoutWithVariablesOfTaskType(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		h.e.RegisterTaskType("echo", namingTaskType{})
		task := `<bpmn:serviceTask id="task" senergy:taskType="echo" senergy:retries="0" senergy:timeout="PT30S"/>`
		inst := h.start(h.deploy(caughtProcess(task, "")), nil)
		h.process()
		h.advance(30 * time.Second)
		expectCaught(t, h, inst.Id, "timed out")
		h.expectVars(inst.Id, map[string]any{"caught_device": "from-task"})
	})
}

func TestErrorBoundaryCatchesAsyncFailureAndIgnoresLateAnswer(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		var jobId string
		h.e.RegisterTaskType("echo", TaskTypeFunc(func(ctx context.Context, task Task) (TaskResult, error) {
			jobId = task.JobId
			return TaskResult{Async: true}, nil
		}))
		task := `<bpmn:serviceTask id="task" senergy:taskType="echo" senergy:retries="0" senergy:timeout="PT30S"/>`
		inst := h.start(h.deploy(caughtProcess(task, "")), nil)
		h.process()
		ok, err := h.e.FailTaskError(ctx, jobId, WithVariables(errors.New("no matching state of device"), map[string]any{"device_id": "d2", "service_id": "s2"}))
		if err != nil || !ok {
			t.Fatalf("fail: %v %v", ok, err)
		}
		expectCaught(t, h, inst.Id, "no matching state")
		h.expectVars(inst.Id, map[string]any{"caught_device": "d2"})
		if ok, err = h.e.CompleteTask(ctx, jobId, map[string]any{"result": 1}); err != nil || ok {
			t.Errorf("the answer to an interrupted task is ignored: %v %v", ok, err)
		}
	})
}

func TestErrorBoundaryCatchesForbiddenTaskType(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		h.e.RegisterTaskType("echo", TaskTypeFunc(func(ctx context.Context, task Task) (TaskResult, error) {
			return TaskResult{}, nil
		}))
		inst := h.start(h.deployIn("restricted", caughtProcess(b.Service("task", "echo"), "")), nil)
		expectCaught(t, h, inst.Id, "not allowed")
	})
}

func TestErrorInBoundaryEventIsIncidentThere(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		xml := b.Process(
			b.Start("start"), b.Script("task", "", `throw new Error("boom")`),
			b.ErrorBoundary("caught", "task", "", b.OutScript("x", "missing.field")), b.End("end"),
			b.Flow("start", "task"), b.Flow("task", "end"),
		)
		inst := h.start(h.deploy(xml), nil)
		h.expectState(inst.Id, model.StateIncident)
		incidents := h.incidents(inst.Id)
		if len(incidents) != 1 || incidents[0].ElementId != "caught" || !strings.Contains(incidents[0].Message, "missing") {
			t.Errorf("incidents %+v", incidents)
		}
	})
}

func TestBoundaryEventWithoutFlowEndsToken(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		xml := b.Process(
			b.Start("start"), b.Script("task", "", `throw new Error("boom")`), b.ErrorBoundary("caught", "task", "failure"), b.End("end"),
			b.Flow("start", "task"), b.Flow("task", "end"),
		)
		inst := h.start(h.deploy(xml), nil)
		h.expectState(inst.Id, model.StateCompleted)
		if failure, _ := h.vars(inst.Id)["failure"].(string); !strings.Contains(failure, "boom") {
			t.Errorf("failure %q", failure)
		}
	})
}

func TestInclusiveJoinWaitsForTokenOnErrorPath(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		var jobId string
		h.e.RegisterTaskType("echo", TaskTypeFunc(func(ctx context.Context, task Task) (TaskResult, error) {
			jobId = task.JobId
			return TaskResult{Async: true}, nil
		}))
		// the task reaches the join only over its error path
		xml := b.Process(
			b.Start("start"), b.Gateway("inclusive", "fork", ""),
			`<bpmn:serviceTask id="a" senergy:taskType="echo" senergy:retries="0" senergy:timeout="PT1H"/>`, b.End("aDone"),
			b.ErrorBoundary("aFailed", "a", ""), b.Task("b"), b.Gateway("inclusive", "join", ""), b.Task("after"), b.End("end"),
			b.Flow("start", "fork"), b.Flow("fork", "a"), b.Flow("fork", "b"), b.Flow("a", "aDone"),
			b.Flow("aFailed", "join"), b.Flow("b", "join"), b.Flow("join", "after"), b.Flow("after", "end"),
		)
		inst := h.start(h.deploy(xml), nil)
		h.process()
		if entered := h.activities(inst.Id, model.ActivityElementEntered); hasElement(entered, "after") {
			t.Fatal("the join fired although the task can still arrive over its boundary event")
		}
		if _, err := h.e.FailTask(ctx, jobId, "offline"); err != nil {
			t.Fatal(err)
		}
		h.expectState(inst.Id, model.StateCompleted)
		count := 0
		for _, a := range h.activities(inst.Id, model.ActivityElementEntered) {
			if a.ElementId == "after" {
				count++
			}
		}
		if count != 1 {
			t.Errorf("the join fires once with both tokens: %v", count)
		}
	})
}

func hasElement(activities []model.Activity, elementId string) bool {
	for _, a := range activities {
		if a.ElementId == elementId {
			return true
		}
	}
	return false
}

func TestBlockedScriptIsNotCaught(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		// a script that ended the engine must not run on over the error path (EXPR-03)
		xml := b.Process(
			b.Message("m", "go"), b.Start("start"), b.CatchMessage("wait", "m"), b.Script("boom", "x", crashingCopy),
			b.ErrorBoundary("caught", "boom", "failure"), b.End("end"), b.End("failed"),
			b.Flow("start", "wait"), b.Flow("wait", "boom"), b.Flow("boom", "end"), b.Flow("caught", "failed"),
		)
		inst := h.start(h.deploy(xml), nil)
		h.block(crashingScript)
		h.correlate(model.MessageInput{Name: "go", InstanceId: inst.Id})
		h.expectState(inst.Id, model.StateIncident)
		if incidents := h.incidents(inst.Id); len(incidents) != 1 || incidents[0].ElementId != "boom" || !strings.Contains(incidents[0].Message, "blocked") {
			t.Errorf("incidents %+v", incidents)
		}
	})
}

func TestErrorBoundaryCatchesInputMappingErrors(t *testing.T) {
	for name, task := range map[string]string{
		"task":    b.Task("task", b.InScript("x", "missing.field")),
		"service": b.Service("task", "echo", b.InScript("x", "missing.field")),
	} {
		t.Run(name, func(t *testing.T) {
			run(t, func(t *testing.T, h *harness) {
				executed := false
				h.e.RegisterTaskType("echo", TaskTypeFunc(func(ctx context.Context, task Task) (TaskResult, error) {
					executed = true
					return TaskResult{}, nil
				}))
				inst := h.start(h.deploy(caughtProcess(task, "")), nil)
				h.process()
				expectCaught(t, h, inst.Id, "missing")
				if executed {
					t.Error("a task whose inputs failed is not executed")
				}
			})
		})
	}
}

func TestErrorBoundaryCatchesOutputErrorAfterAsyncAnswer(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		var jobId string
		h.e.RegisterTaskType("echo", TaskTypeFunc(func(ctx context.Context, task Task) (TaskResult, error) {
			jobId = task.JobId
			return TaskResult{Async: true}, nil
		}))
		task := `<bpmn:serviceTask id="task" senergy:taskType="echo" senergy:timeout="PT30S">` + b.IO(b.OutScript("out", "result.missing.field")) + `</bpmn:serviceTask>`
		inst := h.start(h.deploy(caughtProcess(task, "")), nil)
		h.process()
		if ok, err := h.e.CompleteTask(ctx, jobId, map[string]any{"result": map[string]any{}}); err != nil || !ok {
			t.Fatalf("complete: %v %v", ok, err)
		}
		expectCaught(t, h, inst.Id, "Cannot read property 'field' of undefined")
	})
}

func TestBoundaryEventStartsWithoutLocalsOfTaskAndRunsItsInputs(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		xml := b.Process(
			b.Start("start"),
			`<bpmn:scriptTask id="task" scriptFormat="javascript">`+b.IO(b.InText("secret", "s"))+`<bpmn:script>throw new Error("boom")</bpmn:script></bpmn:scriptTask>`,
			b.ErrorBoundary("caught", "task", "",
				b.InScript("seen", "typeof secret === 'undefined' ? 'gone' : secret"), b.InScript("upper", "error_message.toUpperCase()"),
				b.OutScript("seen_out", "seen"), b.OutScript("upper_out", "upper")),
			b.End("end"), b.Flow("start", "task"), b.Flow("task", "end"),
		)
		inst := h.start(h.deploy(xml), nil)
		h.expectState(inst.Id, model.StateCompleted)
		h.expectVars(inst.Id, map[string]any{"seen_out": "gone"})
		if upper, _ := h.vars(inst.Id)["upper_out"].(string); !strings.Contains(upper, "BOOM") {
			t.Errorf("input mappings of the boundary event read error_message: %q", upper)
		}
	})
}

func TestCaughtErrorShowsAtTheTask(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		inst := h.start(h.deploy(caughtProcess(b.Script("task", "", `throw new Error("boom")`), "")), nil)
		h.expectState(inst.Id, model.StateCompleted)
		var stats []model.ElementStats
		err := h.s.Tx(ctx, func(tx *store.Tx) (err error) {
			stats, err = tx.ListElementStats(inst.Id, 1)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range stats {
			if s.ElementId == "task" && strings.Contains(s.LastError, "boom") {
				return
			}
		}
		t.Errorf("the debug view shows the error at the task: %+v", stats)
	})
}

func TestFailureVariablesDoNotReplaceTheMessage(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		h.e.RegisterTaskType("echo", TaskTypeFunc(func(ctx context.Context, task Task) (TaskResult, error) {
			return TaskResult{}, WithVariables(&PermanentError{Message: "real"}, map[string]any{"error_message": "fake", "detail": map[string]any{"n": 1}})
		}))
		inst := h.start(h.deploy(caughtProcess(b.Service("task", "echo"), "")), nil)
		h.process()
		expectCaught(t, h, inst.Id, "real")
	})
}

func TestLoopBackFromBoundaryLeavesNoStaleJob(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		var jobs []string
		h.e.RegisterTaskType("echo", TaskTypeFunc(func(ctx context.Context, task Task) (TaskResult, error) {
			jobs = append(jobs, task.JobId)
			return TaskResult{Async: true}, nil
		}))
		xml := b.Process(
			b.Start("start"), `<bpmn:serviceTask id="task" senergy:taskType="echo" senergy:retries="0" senergy:timeout="PT1H"/>`,
			b.ErrorBoundary("caught", "task", ""), b.End("end"),
			b.Flow("start", "task"), b.Flow("task", "end"), b.Flow("caught", "task"),
		)
		inst := h.start(h.deploy(xml), nil)
		h.process()
		if _, err := h.e.FailTask(ctx, jobs[0], "offline"); err != nil {
			t.Fatal(err)
		}
		h.process()
		if len(jobs) != 2 {
			t.Fatalf("the token is back at the task with a new job: %v", jobs)
		}
		if r := h.runtime(inst.Id); r.jobs != 1 || r.tokens != 1 {
			t.Fatalf("only the new job remains: %+v", r)
		}
		if ok, err := h.e.CompleteTask(ctx, jobs[0], map[string]any{}); err != nil || ok {
			t.Errorf("the interrupted job cannot move the token again: %v %v", ok, err)
		}
		if ok, err := h.e.CompleteTask(ctx, jobs[1], map[string]any{}); err != nil || !ok {
			t.Fatalf("complete: %v %v", ok, err)
		}
		h.expectState(inst.Id, model.StateCompleted)
	})
}
