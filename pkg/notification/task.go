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

package notification

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

const TaskType = "notification"

// Topic of process notifications in the notifier, as before.
const Topic = "processes"

// Task is the task type "notification" (BPMN-09): it sends a notification to the owner of the
// instance, which process-deployment did by filling an http-connector. The notification goes into
// the queue of the engine that also carries the incident notifications, so that a notifier out of
// reach, e.g. the cloud behind the cloud-proxy of an offline gateway, delays it instead of ending
// the process with an incident (FOG-05). The queue retries for a day.
type Task struct {
	store *store.Store
}

func NewTask(s *store.Store) *Task {
	return &Task{store: s}
}

func text(input map[string]any, name string) string {
	switch v := input[name].(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

// DuplicateWindowInput names the optional input with the seconds within which the notifier drops
// a notification the owner already has with the same title and message (SNRGY-3911). Without it
// every notification is delivered, as with process-deployment.
const DuplicateWindowInput = "ignore_duplicates_within_seconds"

// duplicateWindow reads the input as whole seconds; empty is none
func duplicateWindow(input map[string]any) (int64, error) {
	var seconds float64
	switch v := input[DuplicateWindowInput].(type) {
	case nil:
		return 0, nil
	case string:
		if strings.TrimSpace(v) == "" {
			return 0, nil
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, fmt.Errorf("%v is not a number: %q", DuplicateWindowInput, v)
		}
		seconds = parsed
	case float64:
		seconds = v
	case int:
		seconds = float64(v)
	case int64:
		seconds = float64(v)
	default:
		return 0, fmt.Errorf("%v is not a number: %v", DuplicateWindowInput, v)
	}
	if seconds < 0 || seconds != math.Trunc(seconds) || seconds > math.MaxInt32 {
		return 0, fmt.Errorf("%v needs whole seconds from 0: %v", DuplicateWindowInput, seconds)
	}
	return int64(seconds), nil
}

// Execute queues title and message. The id follows from the job, so that executing the job again,
// after a completion that did not reach the database, queues nothing a second time.
func (this *Task) Execute(ctx context.Context, task engine.Task) (engine.TaskResult, error) {
	n := store.Notification{Id: "task-" + task.JobId, UserId: task.Owner, Title: text(task.Input, "title"), Message: text(task.Input, "message"), Topic: Topic, Namespace: task.Namespace, CreatedAt: time.Now()}
	if n.Title == "" && n.Message == "" {
		return engine.TaskResult{}, &engine.PermanentError{Message: "notification needs the input title or message"}
	}
	window, err := duplicateWindow(task.Input)
	if err != nil {
		return engine.TaskResult{}, &engine.PermanentError{Message: err.Error()}
	}
	n.DuplicateWindow = window
	if err := this.store.Tx(ctx, func(tx *store.Tx) error { return tx.InsertNotification(n) }); err != nil {
		return engine.TaskResult{}, err
	}
	return engine.TaskResult{Output: map[string]any{}}, nil
}
