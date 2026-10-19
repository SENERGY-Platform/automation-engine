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
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/tracing"
	"go.opentelemetry.io/otel/baggage"
)

// undoContexts is the creating task type that remembers the contexts of Undo
type undoContexts struct {
	creating
	undoCtx context.Context
}

func (this *undoContexts) Undo(ctx context.Context, task Task, effects *Effects, reason error) {
	this.undoCtx = ctx
	this.creating.Undo(ctx, task, effects, reason)
}

// The rollback of a task stays in the trace of the task, as with the worker library
// (TestExecuteTaskUndoGetsTaskContext): its requests can be traced back to the instance.
func TestUndoRunsInTheContextOfItsTask(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		task := &undoContexts{}
		h.e.RegisterTaskType("creating", task)
		d := h.deploy(moduleProcess())
		inst := h.start(d, nil)
		task.before = func(Task) {
			if err := h.e.StopInstance(context.Background(), inst.Id, "stopped while the task ran"); err != nil {
				t.Error(err)
			}
		}
		h.process()
		if task.undoCtx == nil {
			t.Fatal("Undo was not called")
		}
		if bag := baggage.FromContext(task.undoCtx); bag.Member(tracing.KeyInstanceId).Value() != inst.Id || bag.Member(tracing.KeyDeploymentId).Value() != d {
			t.Errorf("baggage of the rollback: %v", bag)
		}
	})
}
