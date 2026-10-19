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
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// stillWaiting answers its timeouts from a list: Async with WaitUntil, Async alone, or a result
type stillWaiting struct {
	mux      sync.Mutex
	attempts []int
	timeouts []func() *TaskResult
}

func (this *stillWaiting) Execute(_ context.Context, task Task) (TaskResult, error) {
	this.mux.Lock()
	defer this.mux.Unlock()
	this.attempts = append(this.attempts, task.Attempt)
	return TaskResult{Async: true}, nil
}

func (this *stillWaiting) OnTimeout(_ context.Context, _ *store.Tx, _ Task) (*TaskResult, error) {
	this.mux.Lock()
	defer this.mux.Unlock()
	next := this.timeouts[0]
	this.timeouts = this.timeouts[1:]
	return next(), nil
}

func (this *stillWaiting) executions() []int {
	this.mux.Lock()
	defer this.mux.Unlock()
	return append([]int{}, this.attempts...)
}

// a timeout handler keeps a task in progress without a failed attempt and without backoff:
// it waits longer, or the same attempt is executed again at once (TASK-06)
func TestTimeoutHandlerKeepsTheTaskWaiting(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		task := &stillWaiting{}
		task.timeouts = []func() *TaskResult{
			func() *TaskResult { return &TaskResult{Async: true, WaitUntil: h.now().Add(10 * time.Second)} },
			func() *TaskResult { return &TaskResult{Async: true} },
			func() *TaskResult { return &TaskResult{Output: map[string]any{"result": []any{"late"}}} },
		}
		h.e.RegisterTaskType("echo", task)
		inst := h.start(h.deploy(serviceProcess(`senergy:timeout="PT30S" senergy:retries="0"`)), map[string]any{"x": 1})
		h.process()
		h.advance(30 * time.Second) // waits until 40s
		h.advance(9 * time.Second)
		if got := task.executions(); len(got) != 1 {
			t.Fatalf("waiting does not execute the task: %v", got)
		}
		h.advance(time.Second) // executed again at once
		if got := task.executions(); len(got) != 2 || got[0] != 1 || got[1] != 1 {
			t.Fatalf("the same attempt is executed again: %v", got)
		}
		h.expectState(inst.Id, model.StateRunning)
		h.advance(29 * time.Second)
		h.expectState(inst.Id, model.StateRunning)
		h.advance(time.Second) // the new lock lasts the timeout
		h.expectState(inst.Id, model.StateCompleted)
		h.expectVars(inst.Id, map[string]any{"out": "late"})
		if failed := h.activities(inst.Id, model.ActivityTaskFailed); len(failed) != 0 {
			t.Errorf("no failed attempt: %+v", failed)
		}
	})
}

// a task that ran past its timeout counts it as failed attempt, even if the handler would wait
func TestTimeoutHandlerCannotKeepARunningExecution(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		task := &stillWaiting{timeouts: []func() *TaskResult{func() *TaskResult { return &TaskResult{Async: true} }}}
		h.e.RegisterTaskType("echo", task)
		inst := h.start(h.deploy(serviceProcess(`senergy:timeout="PT30S" senergy:retries="0"`)), map[string]any{"x": 1})
		err := h.s.Tx(ctx, func(tx *store.Tx) error {
			jobs, err := tx.ListJobs(inst.Id)
			if err != nil || len(jobs) != 1 {
				t.Fatalf("expected one job: %v %v", jobs, err)
			}
			job := jobs[0]
			job.State, job.Attempt, job.LockOwner, job.LockUntil = store.JobRunning, 1, "crashed", h.now().Add(30*time.Second)
			return tx.SaveJob(job)
		})
		if err != nil {
			t.Fatal(err)
		}
		h.advance(30 * time.Second)
		h.expectState(inst.Id, model.StateIncident)
		if got := task.executions(); len(got) != 0 {
			t.Errorf("not executed again: %v", got)
		}
	})
}
