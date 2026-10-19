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
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// TaskType executes service tasks of one type inside the engine (TASK-00). New types are
// code in the engine, not a network protocol; which types a model may use is decided by its
// namespace.
type TaskType interface {
	Execute(ctx context.Context, task Task) (TaskResult, error)
}

// TaskTypeFunc adapts a function to TaskType.
type TaskTypeFunc func(ctx context.Context, task Task) (TaskResult, error)

func (this TaskTypeFunc) Execute(ctx context.Context, task Task) (TaskResult, error) {
	return this(ctx, task)
}

// Task is one execution attempt of a service task.
type Task struct {
	JobId        string
	InstanceId   string
	Run          int
	Namespace    string
	DeploymentId string
	ElementId    string
	TaskType     string
	// the owner of the instance; task types act with the owner's rights (SEC-06)
	Owner string
	// the local variables of the task after its input mappings
	Input map[string]any
	// senergy:* attributes of the element
	Attributes map[string]string
	// 1 for the first execution; Attempt > Retries is the last one
	Attempt int
	Retries int
	// the device selection of the element in the deployment (DEP-05), nil if there is none
	Selection *model.Selection
	// the engine cancels ctx after this duration, and an asynchronous task fails after it (TASK-07)
	Timeout time.Duration
	// the instance modules and shared variables belong to: the parent of a maintenance procedure,
	// otherwise the instance itself (SMS-13)
	RootInstanceId string
}

// TaskResult completes a task with output variables, or announces an asynchronous answer
// which the task type delivers later through Engine.CompleteTask or Engine.FailTask.
type TaskResult struct {
	// local variables of the task, e.g. "result" (TASK-10); the output mappings read them
	Output map[string]any
	Async  bool
	// with Async from TimeoutHandler.OnTimeout: the task keeps waiting for its answer until
	// then; zero executes it again at once
	WaitUntil time.Time
	// changes besides the output, applied in the transaction that completes the task
	Effects *Effects
}

// Effects are what a task changes besides its output (SMS-04, SMS-12, SMS-13).
type Effects struct {
	// modules to create or replace; the engine sets instance, namespace, owner, creator, element
	// and times
	Modules []model.Module
	// variables of the root instance
	Variables map[string]any
	// messages for the instance of the task, delivered at once when it waits for one of them
	Messages []string
}

// Undoer is implemented by task types whose effects outlive a failed completion, e.g. an
// external resource; the engine calls Undo when it could not apply them.
type Undoer interface {
	Undo(ctx context.Context, task Task, effects *Effects, reason error)
}

// PermanentError fails a task without retries, e.g. an incident the process raises itself.
type PermanentError struct {
	Message string
}

func (this *PermanentError) Error() string {
	return this.Message
}

func IsPermanent(err error) bool {
	var p *PermanentError
	return errors.As(err, &p)
}

// TimeoutHandler is implemented by task types that decide what a timeout of an asynchronous
// task means, e.g. a device group command (TASK-06). The engine calls it on every timeout;
// Task.Attempt > Task.Retries marks the last attempt. A nil result counts the timeout as failed
// attempt. A result with Async keeps a waiting task in progress without counting an attempt and
// without backoff: it waits until WaitUntil, or with a zero WaitUntil the engine executes it
// again at once in the same attempt with a new lock for Task.Timeout, e.g. to ask the next
// device of a group. Any other result completes the task. It runs inside the
// transaction of the engine and must use tx instead of opening its own.
type TimeoutHandler interface {
	OnTimeout(ctx context.Context, tx *store.Tx, task Task) (*TaskResult, error)
}

// JobDefaults is implemented by task types that derive retries or a timeout from the task
// input, e.g. the retries of a device command payload; element attributes take precedence.
type JobDefaults interface {
	Defaults(input map[string]any) (retries *int, timeout *time.Duration)
}
