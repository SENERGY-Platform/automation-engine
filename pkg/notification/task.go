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
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/google/uuid"
)

const TaskType = "notification"

// Topic of process notifications in the notifier, as before.
const Topic = "processes"

type Sender interface {
	Send(ctx context.Context, n store.Notification) error
}

// Task is the task type "notification" (BPMN-09): it sends a notification to the owner of the
// instance, which process-deployment did by filling an http-connector.
type Task struct {
	sender Sender
}

func NewTask(sender Sender) *Task {
	return &Task{sender: sender}
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

// Execute sends title and message; a failed delivery is retried by the engine.
func (this *Task) Execute(ctx context.Context, task engine.Task) (engine.TaskResult, error) {
	n := store.Notification{Id: uuid.NewString(), UserId: task.Owner, Title: text(task.Input, "title"), Message: text(task.Input, "message"), Topic: Topic, CreatedAt: time.Now()}
	if n.Title == "" && n.Message == "" {
		return engine.TaskResult{}, &engine.PermanentError{Message: "notification needs the input title or message"}
	}
	if err := this.sender.Send(ctx, n); err != nil {
		return engine.TaskResult{}, err
	}
	return engine.TaskResult{Output: map[string]any{}}, nil
}
