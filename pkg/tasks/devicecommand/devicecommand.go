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

// Package devicecommand is the task type "device_command" (TASK-01 to TASK-12): it resolves
// device, service and protocol, marshals the command, sends it to the protocol handler and
// completes the task with the unmarshalled answer, for single devices and device groups.
package devicecommand

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/devices"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/google/uuid"
)

const TaskType = "device_command"

const (
	Optimistic  = "optimistic"
	Pessimistic = "pessimistic"

	Parallel   = "parallel"
	Sequential = "sequential"
	RoundRobin = "round_robin"
)

type Config struct {
	// parallel, sequential or round_robin for device groups (TASK-06)
	GroupScheduler string
	// echoed in the metadata of each message; the handlers answer there (TASK-05)
	ResponseTo       string
	ErrorTo          string
	TimescaleTimeout time.Duration
	// on a gateway: the prefix of the command ids on its broker, which tells answers to this
	// engine apart from those to device-command and the cloud (FOG-01)
	LocalCommandPrefix string
}

// Completer delivers asynchronous answers to the engine.
type Completer interface {
	CompleteTask(ctx context.Context, jobId string, output map[string]any) (bool, error)
	FailTask(ctx context.Context, jobId string, message string) (bool, error)
}

// Metrics keep the names and labels the cost-calculator reads (TASK-12).
type Metrics interface {
	CommandSent(userId string, deploymentId string)
	MarshallingLatency(endpoint string, userId string, serviceId string, functionIds string, duration time.Duration)
}

type Type struct {
	config     Config
	store      *store.Store
	repo       devices.Repository
	aspects    *devices.AspectNodes
	marshaller Marshaller
	// nil: values of event services cannot be read
	timescale Timescale
	producer  Producer
	completer Completer
	metrics   Metrics
	logger    *slog.Logger
	workerId  string
	now       func() time.Time
}

func New(config Config, s *store.Store, repo devices.Repository, aspects *devices.AspectNodes, m Marshaller, ts Timescale, producer Producer, metrics Metrics, logger *slog.Logger) *Type {
	if config.GroupScheduler == "" {
		config.GroupScheduler = Sequential
	}
	if config.TimescaleTimeout == 0 {
		config.TimescaleTimeout = 10 * time.Second
	}
	return &Type{config: config, store: s, repo: repo, aspects: aspects, marshaller: m, timescale: ts, producer: producer, metrics: metrics, logger: logger, workerId: uuid.NewString(), now: time.Now}
}

// SetCompleter connects the type with the engine, which is created after it. The type takes
// the clock of the engine, since the tries of sub-tasks are compared with its timeouts.
func (this *Type) SetCompleter(c Completer) {
	this.completer = c
	if clock, ok := c.(interface{ Now() time.Time }); ok {
		this.now = clock.Now
	}
}

func strategy(task engine.Task) string {
	if task.Attributes["completionStrategy"] == Optimistic {
		return Optimistic
	}
	return Pessimistic
}

// Defaults takes the retries of the payload, as the external-task-worker did (TASK-07). The
// sequential and round robin schedulers count the retries of the task per sub-task (TASK-06):
// a try waits for the answer of one device, so the task timeout is the answer timeout of each
// try, and a group takes up to sub-tasks × (retries + 1) timeouts (see OnTimeout).
func (this *Type) Defaults(input map[string]any) (*int, *time.Duration) {
	var command Command
	if payload, ok := input["payload"]; ok && decodeJson(payload, &command) == nil && command.Retries > 0 {
		retries := int(command.Retries)
		return &retries, nil
	}
	return nil, nil
}

func (this *Type) Execute(ctx context.Context, task engine.Task) (engine.TaskResult, error) {
	token, err := this.repo.GetToken(task.Owner)
	if err != nil {
		return engine.TaskResult{}, fmt.Errorf("unable to get a token of the owner: %w", err)
	}
	command, err := this.command(task, token)
	if err != nil {
		return engine.TaskResult{}, err
	}
	children, err := this.subtasks(token, command, task.JobId)
	if err != nil {
		return engine.TaskResult{}, err
	}
	info := TaskInfo{
		WorkerId: this.workerId, ProcessInstanceId: task.InstanceId,
		// connectors mirror it back; the old name until all of them know the new one (DATA-04)
		DeploymentId: task.DeploymentId, ProcessDefinitionId: task.DeploymentId,
		CompletionStrategy:  strategy(task), TenantId: task.Owner,
	}
	if info.CompletionStrategy == Optimistic {
		for _, c := range children {
			info.TaskId = c.subId
			if _, err = this.send(ctx, token, c.command, info); err != nil {
				return engine.TaskResult{}, err
			}
		}
		return engine.TaskResult{Output: map[string]any{"result": nil}}, nil
	}
	subtasks := make([]store.Subtask, 0, len(children))
	for i, c := range children {
		info.TaskId = c.subId
		commandJson, _ := json.Marshal(c.command)
		infoJson, _ := json.Marshal(info)
		subtasks = append(subtasks, store.Subtask{JobId: task.JobId, SubId: c.subId, Position: i, Command: commandJson, TaskInfo: infoJson})
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		return tx.EnsureSubtasks(subtasks)
	})
	if err != nil {
		return engine.TaskResult{}, err
	}
	// every attempt sends what has no result yet; a retry resends lost commands
	done, result, err := this.advance(ctx, task.JobId, command.Version, token, true, task.Retries, task.Timeout)
	if err != nil {
		return engine.TaskResult{}, err
	}
	if done {
		return engine.TaskResult{Output: map[string]any{"result": result}}, nil
	}
	return engine.TaskResult{Async: true}, nil
}

// advance sends the next sub-tasks without result: all of them in parallel mode, one at a time
// otherwise. Values read from timescale count as results at once. done reports that every
// sub-task has a result, or one at a time, that the others have used up their tries. Without
// resend, parallel sub-tasks still in flight are not sent again. retries and timeout are those
// of the task; one at a time they apply to each try.
func (this *Type) advance(ctx context.Context, jobId string, version int64, token devices.Token, resend bool, retries int, timeout time.Duration) (done bool, result any, err error) {
	for {
		var subtasks []store.Subtask
		err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
			subtasks, err = tx.ListSubtasks(jobId)
			return err
		})
		if err != nil {
			return false, nil, err
		}
		pending := []store.Subtask{}
		for _, s := range subtasks {
			if !s.HasResult {
				pending = append(pending, s)
			}
		}
		if len(pending) == 0 {
			return true, results(version, subtasks), nil
		}
		if this.config.GroupScheduler == Parallel && !resend {
			return false, nil, nil
		}
		if this.config.GroupScheduler != Parallel {
			next, waiting := this.next(pending, retries, timeout)
			if waiting {
				return false, nil, nil
			}
			if next == nil {
				if len(results(2, subtasks).([]any)) > 0 {
					return true, results(version, subtasks), nil
				}
				return false, nil, &engine.PermanentError{Message: fmt.Sprintf("no answer from any device within %v, %v tries each", timeout, retries+1)}
			}
			pending = []store.Subtask{*next}
		}
		readValues := false
		for _, s := range pending {
			var command Command
			var info TaskInfo
			if err = json.Unmarshal(s.Command, &command); err != nil {
				return false, nil, err
			}
			if err = json.Unmarshal(s.TaskInfo, &info); err != nil {
				return false, nil, err
			}
			if err = this.store.Tx(ctx, func(tx *store.Tx) error { return tx.CountSubtaskTry(jobId, s.SubId, this.now()) }); err != nil {
				return false, nil, err
			}
			value, err := this.send(ctx, token, command, info)
			if err != nil {
				return false, nil, err
			}
			if value != nil {
				readValues = true
				if err = this.store.Tx(ctx, func(tx *store.Tx) error {
					_, err := tx.SetSubtaskResult(jobId, s.SubId, value.value)
					return err
				}); err != nil {
					return false, nil, err
				}
			}
		}
		if !readValues {
			return false, nil, nil
		}
	}
}

// next picks the pending sub-task to send one at a time, as external-task-worker
// lib/devicegroups does: waiting while a sent command is not older than timeout, a sub-task is
// given up after retries + 1 tries, and round robin prefers the fewest tries. nil without
// waiting means that every pending sub-task is given up.
func (this *Type) next(pending []store.Subtask, retries int, timeout time.Duration) (next *store.Subtask, waiting bool) {
	if running(pending, this.now(), timeout) != nil {
		return nil, true
	}
	candidates := []store.Subtask{}
	for _, s := range pending {
		if s.Tries <= retries {
			candidates = append(candidates, s)
		}
	}
	if len(candidates) == 0 {
		return nil, false
	}
	if this.config.GroupScheduler == RoundRobin {
		slices.SortStableFunc(candidates, func(a, b store.Subtask) int { return a.Tries - b.Tries })
	}
	return &candidates[0], false
}

// running returns the last sent pending sub-task whose command still waits for its answer
func running(pending []store.Subtask, now time.Time, timeout time.Duration) (result *store.Subtask) {
	for _, s := range pending {
		if s.Tries > 0 && now.Sub(s.LastTry) <= timeout && (result == nil || s.LastTry.After(result.LastTry)) {
			result = &s
		}
	}
	return result
}

type readValue struct {
	value any
}

// send sends the command of a sub-task, or reads the last value for an event service
func (this *Type) send(ctx context.Context, token devices.Token, command Command, info TaskInfo) (*readValue, error) {
	msg, event, err := this.message(command, info, token)
	if err != nil {
		return nil, err
	}
	if event != nil {
		if info.CompletionStrategy == Optimistic {
			return nil, nil
		}
		value, err := this.lastValue(token, *event)
		if err != nil {
			return nil, err
		}
		return &readValue{value: value}, nil
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	if err = this.producer.Produce(ctx, msg.Metadata.Protocol.Handler, msg.Metadata.Device.Id, payload); err != nil {
		return nil, fmt.Errorf("unable to send command: %w", err)
	}
	this.metrics.CommandSent(info.TenantId, info.ProcessDefinitionId)
	return nil, nil
}

// results lists the results in sub-task order; before payload version 2 the result is the
// first value instead of a list (TASK-10)
func results(version int64, subtasks []store.Subtask) any {
	list := []any{}
	for _, s := range subtasks {
		if s.HasResult {
			list = append(list, s.Result)
		}
	}
	if version < 2 && len(list) > 0 {
		return list[0]
	}
	return list
}

func jobIdOf(subId string) string {
	jobId, _, _ := strings.Cut(subId, "_")
	return jobId
}

// HandleResponse processes an answer of a protocol handler from Kafka or HTTP (TASK-05). An
// answer for an unknown or finished task is dropped, so that every task completes at most once.
func (this *Type) HandleResponse(ctx context.Context, raw []byte) error {
	var msg ProtocolMsg
	if err := json.Unmarshal(raw, &msg); err != nil {
		this.logger.Warn("drop unreadable device response", "error", err)
		return nil
	}
	if msg.TaskInfo.CompletionStrategy == Optimistic {
		return nil
	}
	output, err := this.output(msg)
	if err != nil {
		jobId := jobIdOf(msg.TaskInfo.TaskId)
		_, failErr := this.completer.FailTask(ctx, jobId, "unable to unmarshal device response: "+err.Error())
		return failErr
	}
	jobId := jobIdOf(msg.TaskInfo.TaskId)
	var known bool
	err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		known, err = tx.SetSubtaskResult(jobId, msg.TaskInfo.TaskId, output)
		return err
	})
	if err != nil || !known {
		return err
	}
	var command Command
	var retries int
	var timeout time.Duration
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		job, err := tx.GetJob(jobId)
		if err != nil || job == nil {
			return err
		}
		retries, timeout = job.Retries, job.Timeout
		subtasks, err := tx.ListSubtasks(jobId)
		if err != nil || len(subtasks) == 0 {
			return err
		}
		return json.Unmarshal(subtasks[0].Command, &command)
	})
	if err != nil {
		return err
	}
	token, err := this.repo.GetToken(msg.TaskInfo.TenantId)
	if err != nil {
		return err
	}
	done, result, err := this.advance(ctx, jobId, command.Version, token, false, retries, timeout)
	if err != nil {
		_, failErr := this.completer.FailTask(ctx, jobId, err.Error())
		return errors.Join(err, failErr)
	}
	if done {
		_, err = this.completer.CompleteTask(ctx, jobId, map[string]any{"result": result})
	}
	return err
}

// HandleError processes an error of a protocol handler; the task is retried or becomes an incident.
func (this *Type) HandleError(ctx context.Context, raw []byte) error {
	var msg ProtocolMsg
	if err := json.Unmarshal(raw, &msg); err != nil {
		this.logger.Warn("drop unreadable device error", "error", err)
		return nil
	}
	detail := []string{}
	for key, value := range msg.Response.Output {
		detail = append(detail, key+": "+value)
	}
	slices.Sort(detail)
	_, err := this.completer.FailTask(ctx, jobIdOf(msg.TaskInfo.TaskId), "device command failed: "+strings.Join(detail, ", "))
	return err
}

// OnTimeout handles the timeout of a task with several sub-tasks (TASK-06). One at a time, the
// task timeout is the answer timeout of each try and does not count as failed attempt: a command
// sent after the task was locked, e.g. on the answer of the previous device, gets its full
// timeout; otherwise the task is executed again and asks the next device or ends with the
// answers it has. In parallel, the last attempt completes with the results it has; without any
// result the timeout is a failure.
func (this *Type) OnTimeout(ctx context.Context, tx *store.Tx, task engine.Task) (*engine.TaskResult, error) {
	subtasks, err := tx.ListSubtasks(task.JobId)
	if err != nil || len(subtasks) < 2 {
		return nil, err
	}
	if this.config.GroupScheduler != Parallel {
		pending := []store.Subtask{}
		for _, s := range subtasks {
			if !s.HasResult {
				pending = append(pending, s)
			}
		}
		result := &engine.TaskResult{Async: true}
		if s := running(pending, this.now(), task.Timeout); s != nil {
			// the first instant after the timeout of the try; the store keeps milliseconds
			result.WaitUntil = s.LastTry.Add(task.Timeout + time.Millisecond)
		}
		return result, nil
	}
	if task.Attempt <= task.Retries {
		return nil, nil
	}
	var command Command
	if err = json.Unmarshal(subtasks[0].Command, &command); err != nil {
		return nil, err
	}
	partial := results(2, subtasks).([]any)
	if len(partial) == 0 {
		return nil, nil
	}
	return &engine.TaskResult{Output: map[string]any{"result": results(command.Version, subtasks)}}, nil
}
