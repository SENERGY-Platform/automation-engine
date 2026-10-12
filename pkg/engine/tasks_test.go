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
	"sync"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

func serviceProcess(extra string) string {
	return b.Process(
		b.Start("start"),
		`<bpmn:serviceTask id="task" senergy:taskType="echo" `+extra+`>`+b.IO(b.InScript("in", "x * 2"), b.OutScript("out", "result[0]"))+`</bpmn:serviceTask>`,
		b.End("end"), b.Flow("start", "task"), b.Flow("task", "end"),
	)
}

func TestServiceTaskReceivesInputsAndMapsResult(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		var received Task
		h.e.RegisterTaskType("echo", TaskTypeFunc(func(ctx context.Context, task Task) (TaskResult, error) {
			received = task
			return TaskResult{Output: map[string]any{"result": []any{task.Input["in"]}}}, nil
		}))
		d := h.deploy(serviceProcess(`senergy:retries="2"`))
		inst := h.start(d, map[string]any{"x": 21})
		h.expectState(inst.Id, model.StateRunning)
		h.process()
		h.expectState(inst.Id, model.StateCompleted)
		h.expectVars(inst.Id, map[string]any{"out": 42.0})
		if received.Owner != "user" || received.Attempt != 1 || received.Namespace != "processes" || received.ElementId != "task" {
			t.Errorf("unexpected task %+v", received)
		}
		if _, ok := h.vars(inst.Id)["result"]; ok {
			t.Errorf("result is a local variable of the task (TASK-10)")
		}
	})
}

func TestFailingTaskIsRetriedThenBecomesIncident(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		var attempts []int
		h.e.RegisterTaskType("echo", TaskTypeFunc(func(ctx context.Context, task Task) (TaskResult, error) {
			attempts = append(attempts, task.Attempt)
			return TaskResult{}, errors.New("device offline")
		}))
		inst := h.start(h.deploy(serviceProcess(`senergy:retries="2"`)), map[string]any{"x": 1})
		h.process()
		h.advance(time.Second)     // backoff 1s after attempt 1
		h.advance(2 * time.Second) // backoff 2s after attempt 2
		h.expectState(inst.Id, model.StateIncident)
		if len(attempts) != 3 {
			t.Fatalf("expected 3 attempts, got %v", attempts)
		}
		if failed := h.activities(inst.Id, model.ActivityTaskFailed); len(failed) != 3 {
			t.Errorf("every failure must be logged, got %v", len(failed))
		}
		incidents := h.incidents(inst.Id)
		if len(incidents) != 1 || incidents[0].Message != "device offline" || incidents[0].Variables["in"] != 2.0 {
			t.Errorf("unexpected incidents %+v", incidents)
		}
	})
}

func TestAsyncTaskCompletesOnceAndIgnoresLateAnswers(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		var jobId string
		h.e.RegisterTaskType("echo", TaskTypeFunc(func(ctx context.Context, task Task) (TaskResult, error) {
			jobId = task.JobId
			return TaskResult{Async: true}, nil
		}))
		inst := h.start(h.deploy(serviceProcess(`senergy:timeout="PT30S"`)), map[string]any{"x": 1})
		h.process()
		h.expectState(inst.Id, model.StateRunning)
		ok, err := h.e.CompleteTask(ctx, jobId, map[string]any{"result": []any{"answer"}})
		if err != nil || !ok {
			t.Fatalf("complete: %v %v", ok, err)
		}
		h.expectState(inst.Id, model.StateCompleted)
		h.expectVars(inst.Id, map[string]any{"out": "answer"})
		ok, err = h.e.CompleteTask(ctx, jobId, map[string]any{"result": []any{"late"}})
		if err != nil || ok {
			t.Fatalf("a duplicate answer must be ignored (TASK-11): %v %v", ok, err)
		}
		h.expectVars(inst.Id, map[string]any{"out": "answer"})
	})
}

func TestAsyncTaskTimesOutAndIsRetried(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		var mux sync.Mutex
		attempts := 0
		h.e.RegisterTaskType("echo", TaskTypeFunc(func(ctx context.Context, task Task) (TaskResult, error) {
			mux.Lock()
			defer mux.Unlock()
			attempts++
			if attempts == 1 {
				return TaskResult{Async: true}, nil
			}
			return TaskResult{Output: map[string]any{"result": []any{"second"}}}, nil
		}))
		inst := h.start(h.deploy(serviceProcess(`senergy:timeout="PT30S" senergy:retries="1"`)), map[string]any{"x": 1})
		h.process()
		h.advance(30 * time.Second) // timeout, retry due after backoff
		h.advance(time.Second)
		h.expectState(inst.Id, model.StateCompleted)
		h.expectVars(inst.Id, map[string]any{"out": "second"})
		if failed := h.activities(inst.Id, model.ActivityTaskFailed); len(failed) != 1 || !strings.Contains(failed[0].Data["error"].(string), "timed out") {
			t.Errorf("the timeout must be logged as failed attempt: %+v", failed)
		}
	})
}

func TestTaskTypeForbiddenByNamespaceIsIncident(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		h.e.RegisterTaskType("echo", TaskTypeFunc(func(ctx context.Context, task Task) (TaskResult, error) {
			return TaskResult{}, nil
		}))
		inst := h.start(h.deployIn("restricted", serviceProcess("")), map[string]any{"x": 1})
		h.expectState(inst.Id, model.StateIncident)
		if !strings.Contains(h.incidents(inst.Id)[0].Message, "not allowed") {
			t.Errorf("unexpected %+v", h.incidents(inst.Id))
		}
	})
}

func waitingProcess() string {
	return b.Process(
		b.Message("m", "go"),
		b.StartWithForm("start", "level=5"), b.Script("init", "seen", "level"), b.CatchMessage("wait", "m"), b.End("end"),
		b.Flow("start", "init"), b.Flow("init", "wait"), b.Flow("wait", "end"),
	)
}

func TestStartWithGivenIdIsIdempotent(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(waitingProcess())
		request := StartRequest{DeploymentId: d, InstanceId: "fixed", Owner: "user", Variables: map[string]any{"a": 1}}
		first, created, err := h.e.StartInstance(ctx, request)
		if err != nil || !created {
			t.Fatal(err)
		}
		second, created, err := h.e.StartInstance(ctx, request)
		if err != nil || created || second.Id != first.Id || second.Run != 1 {
			t.Fatalf("same content must return the existing instance: %+v %v %v", second, created, err)
		}
		request.Variables = map[string]any{"a": 2}
		if _, _, err = h.e.StartInstance(ctx, request); !errors.Is(err, model.ErrConflict) {
			t.Fatalf("different content must conflict, got %v", err)
		}
		h.expectVars("fixed", map[string]any{"a": 1.0, "level": "5", "seen": "5"})
	})
}

func TestRestartStartsNewRunWithStartVariablesOnly(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(waitingProcess())
		inst := h.start(d, map[string]any{"level": 7})
		if err := h.e.SetVariable(ctx, inst.Id, "extra", "x"); err != nil {
			t.Fatal(err)
		}
		restarted, err := h.e.RestartInstance(ctx, inst.Id, 0, model.CauseRestart)
		if err != nil {
			t.Fatal(err)
		}
		if restarted.Id != inst.Id || restarted.Run != 2 || restarted.State != model.StateRunning {
			t.Fatalf("unexpected %+v", restarted)
		}
		vars := h.vars(inst.Id)
		if _, ok := vars["extra"]; ok || vars["level"] != 7.0 {
			t.Errorf("variables of the previous run must not carry over: %+v", vars)
		}
		var runs []model.Run
		_ = h.s.Tx(ctx, func(tx *storeTx) (err error) {
			runs, err = tx.ListRuns(inst.Id)
			return err
		})
		if len(runs) != 2 || runs[0].State != model.StateTerminated || runs[1].Cause != model.CauseRestart {
			t.Errorf("unexpected runs %+v", runs)
		}
	})
}

func TestUpdateStrategies(t *testing.T) {
	updated := b.Process(b.Message("m", "go"), b.Start("start"), b.Script("v2", "version", "2"), b.CatchMessage("wait", "m"), b.Flow("start", "v2"), b.Flow("v2", "wait"))
	cases := map[model.UpdateStrategy]func(t *testing.T, h *harness, inst model.Instance){
		model.UpdateStop: func(t *testing.T, h *harness, inst model.Instance) {
			h.expectState(inst.Id, model.StateTerminated)
		},
		model.UpdateDrain: func(t *testing.T, h *harness, inst model.Instance) {
			old := h.expectState(inst.Id, model.StateRunning)
			if old.DeploymentVersion != 1 {
				t.Errorf("a draining instance stays on its version")
			}
			h.correlate(model.MessageInput{Name: "go", InstanceId: inst.Id})
			h.expectState(inst.Id, model.StateCompleted)
		},
		model.UpdateRestart: func(t *testing.T, h *harness, inst model.Instance) {
			restarted := h.expectState(inst.Id, model.StateRunning)
			if restarted.DeploymentVersion != 2 || restarted.Run != 2 {
				t.Errorf("unexpected %+v", restarted)
			}
			h.expectVars(inst.Id, map[string]any{"version": 2.0, "level": 9.0})
		},
	}
	for strategy, check := range cases {
		t.Run(string(strategy), func(t *testing.T) {
			run(t, func(t *testing.T, h *harness) {
				d := h.deploy(waitingProcess())
				inst := h.start(d, map[string]any{"level": 9})
				h.update(d, updated, strategy)
				check(t, h, inst)
				fresh := h.start(d, nil)
				if got := h.instance(fresh.Id); got.DeploymentVersion != 2 {
					t.Errorf("new starts use the new version, got %v", got.DeploymentVersion)
				}
			})
		})
	}
}

func TestStopEndsRunAndKeepsHistory(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		inst := h.start(h.deploy(waitingProcess()), nil)
		if err := h.e.StopInstance(ctx, inst.Id, "stopped by user"); err != nil {
			t.Fatal(err)
		}
		stopped := h.expectState(inst.Id, model.StateTerminated)
		if stopped.EndReason != "stopped by user" || stopped.EndedAt == nil {
			t.Errorf("unexpected %+v", stopped)
		}
		if rt := h.runtime(inst.Id); rt != (runtimeCounts{}) {
			t.Errorf("runtime must be cleared: %+v", rt)
		}
		if len(h.activities(inst.Id, model.ActivityElementEntered)) == 0 {
			t.Errorf("history must stay")
		}
		if err := h.e.StopInstance(ctx, inst.Id, ""); !errors.Is(err, model.ErrConflict) {
			t.Errorf("stopping a stopped instance is a conflict, got %v", err)
		}
	})
}

func TestTokenLimitStopsUnboundedParallelLoops(t *testing.T) {
	storeRun(t, func(c *Config) { c.MaxTokens = 10 }, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(
			b.Start("start"), b.Gateway("exclusive", "loop", ""), b.CatchTimer("tick", "timeDuration", "PT1S"), b.Gateway("parallel", "fork", ""),
			b.Flow("start", "loop"), b.Flow("loop", "tick"), b.Flow("tick", "fork"), b.Flow("fork", "loop"), b.Flow("fork", "loop2"),
			b.Gateway("exclusive", "loop2", ""), b.Flow("loop2", "loop"),
		))
		inst := h.start(d, nil)
		for range 10 {
			h.advance(time.Second)
		}
		h.expectState(inst.Id, model.StateIncident)
		if !strings.Contains(h.incidents(inst.Id)[0].Message, "concurrent tokens") {
			t.Errorf("unexpected %+v", h.incidents(inst.Id))
		}
	})
}

func TestVariableSizeLimitIsIncident(t *testing.T) {
	storeRun(t, func(c *Config) { c.MaxVariableBytes = 1000; c.MaxInstanceVariablesBytes = 3000 }, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(
			b.Start("start"), b.Gateway("exclusive", "loop", ""), b.Script("grow", "list", "(typeof list == 'undefined' ? [] : list).concat(['xxxxxxxxxx'])"),
			b.Flow("start", "loop"), b.Flow("loop", "grow"), b.Flow("grow", "loop"),
		))
		inst := h.start(d, nil)
		h.expectState(inst.Id, model.StateIncident)
		if msg := h.incidents(inst.Id)[0].Message; !strings.Contains(msg, "exceeds") && !strings.Contains(msg, "limit") {
			t.Errorf("unexpected %v", msg)
		}
	})
}

func TestSetVariableRejectsOversizedValues(t *testing.T) {
	storeRun(t, func(c *Config) { c.MaxVariableBytes = 100 }, func(t *testing.T, h *harness) {
		inst := h.start(h.deploy(waitingProcess()), nil)
		if err := h.e.SetVariable(ctx, inst.Id, "big", strings.Repeat("x", 200)); !errors.Is(err, model.ErrBadRequest) {
			t.Fatalf("expected bad request, got %v", err)
		}
		h.expectState(inst.Id, model.StateRunning)
	})
}

func TestConcurrentStartsWithSameIdCreateOneInstance(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(waitingProcess())
		var wg sync.WaitGroup
		var mux sync.Mutex
		created := 0
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, isNew, err := h.e.StartInstance(ctx, StartRequest{DeploymentId: d, InstanceId: "same", Owner: "user"})
				if err != nil {
					t.Error(err)
					return
				}
				if isNew {
					mux.Lock()
					created++
					mux.Unlock()
				}
			}()
		}
		wg.Wait()
		if created != 1 {
			t.Fatalf("expected exactly one created instance, got %v", created)
		}
	})
}

func TestReusedDeploymentIdExecutesNewContent(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		first := b.Process(b.Start("s"), b.Script("v", "v", "'first'"), b.Flow("s", "v"))
		second := b.Process(b.Start("s"), b.Script("v", "v", "'second'"), b.Flow("s", "v"))
		d := h.deploy(first)
		h.expectVars(h.start(d, nil).Id, map[string]any{"v": "first"})
		h.mux.Lock()
		h.clock = h.clock.Add(time.Second)
		h.mux.Unlock()
		err := h.s.Tx(ctx, func(tx *storeTx) error {
			if err := tx.DeleteDeployment(d); err != nil {
				return err
			}
			now := h.now()
			if err := tx.InsertDeployment(model.Deployment{Id: d, Namespace: "processes", Name: "again", Owner: "owner", Version: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
				return err
			}
			return tx.InsertDeploymentVersion(model.DeploymentVersion{DeploymentId: d, Version: 1, Xml: second, CreatedAt: now})
		})
		if err != nil {
			t.Fatal(err)
		}
		h.expectVars(h.start(d, nil).Id, map[string]any{"v": "second"})
	})
}
