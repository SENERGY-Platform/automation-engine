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
	"strconv"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	devicerepository "github.com/SENERGY-Platform/device-repository/v3/lib/client"
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
	// sequential and round_robin: sub-tasks with an open try at the same time, unless the
	// element sets senergy:groupConcurrency (SNRGY-5093); default 1
	GroupConcurrency int
	// echoed in the metadata of each message; the handlers answer there (TASK-05)
	ResponseTo       string
	ErrorTo          string
	TimescaleTimeout time.Duration
	// on a gateway: the prefix of the command ids on its broker, which tells answers to this
	// engine apart from those to device-command and the cloud (FOG-01)
	LocalCommandPrefix string
	// how long reads of device-repository are cached, per user (device_cache_timeout)
	DeviceCacheTimeout time.Duration
}

// Completer delivers asynchronous answers to the engine.
type Completer interface {
	CompleteTask(ctx context.Context, jobId string, output map[string]any) (bool, error)
	FailTask(ctx context.Context, jobId string, message string) (bool, error)
}

// processes gives the element of a job to an answer, which arrives without the task; the
// engine implements it
type processes interface {
	Process(tx *store.Tx, deploymentId string, version int) (*bpmn.Process, error)
}

// Metrics keep the names and labels the cost-calculator reads (TASK-12).
type Metrics interface {
	CommandSent(userId string, deploymentId string)
	MarshallingLatency(endpoint string, userId string, serviceId string, functionIds string, duration time.Duration)
}

type Type struct {
	config Config
	store  *store.Store
	// read through devicerepository.WithCache with DeviceCacheTimeout
	iot devicerepository.Interface
	// tokens of the owners, e.g. by token exchange
	tokens     auth.UserTokenSource
	marshaller Marshaller
	// nil: values of event services cannot be read
	timescale Timescale
	producer  Producer
	completer Completer
	// nil: answers use GroupConcurrency instead of the attribute of the element
	processes processes
	metrics   Metrics
	logger    *slog.Logger
	workerId  string
	now       func() time.Time
}

func New(config Config, s *store.Store, iot devicerepository.Interface, tokens auth.UserTokenSource, m Marshaller, ts Timescale, producer Producer, metrics Metrics, logger *slog.Logger) *Type {
	if config.GroupScheduler == "" {
		config.GroupScheduler = Sequential
	}
	if config.GroupConcurrency < 1 {
		config.GroupConcurrency = 1
	}
	if config.TimescaleTimeout == 0 {
		config.TimescaleTimeout = 10 * time.Second
	}
	return &Type{config: config, store: s, iot: iot, tokens: tokens, marshaller: m, timescale: ts, producer: producer, metrics: metrics, logger: logger, workerId: uuid.NewString(), now: time.Now}
}

// token is the token of the user, from the token source of the owners
func (this *Type) token(user string) (string, error) {
	if this.tokens == nil {
		return "", fmt.Errorf("no token source for user %v", user)
	}
	return this.tokens(user)
}

// SetCompleter connects the type with the engine, which is created after it. The type takes
// the clock of the engine, since the tries of sub-tasks are compared with its timeouts, and
// its processes for the attributes of elements.
func (this *Type) SetCompleter(c Completer) {
	this.completer = c
	if clock, ok := c.(interface{ Now() time.Time }); ok {
		this.now = clock.Now
	}
	if p, ok := c.(processes); ok {
		this.processes = p
	}
}

// concurrency is the number of sub-tasks with an open try at the same time for sequential and
// round robin: senergy:groupConcurrency of the element, otherwise GROUP_CONCURRENCY (SNRGY-5093).
// The parser rejects other values than positive integers at deploy.
func (this *Type) concurrency(attributes map[string]string) int {
	if n, err := strconv.Atoi(attributes["groupConcurrency"]); err == nil && n > 0 {
		return n
	}
	return this.config.GroupConcurrency
}

// attributes are those of the element of a job, nil without processes
func (this *Type) attributes(tx *store.Tx, job store.Job) (map[string]string, error) {
	if this.processes == nil {
		return nil, nil
	}
	inst, err := tx.GetInstance(job.InstanceId, false)
	if errors.Is(err, model.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	proc, err := this.processes.Process(tx, inst.DeploymentId, inst.DeploymentVersion)
	if err != nil {
		return nil, err
	}
	if element := proc.Element(job.ElementId); element != nil {
		return element.Attributes, nil
	}
	return nil, nil
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
// try, and a group takes up to sub-tasks × (retries + 1) timeouts, divided by the concurrency
// (see OnTimeout).
func (this *Type) Defaults(input map[string]any) (*int, *time.Duration) {
	var command Command
	if payload, ok := input["payload"]; ok && decodeJson(payload, &command) == nil && command.Retries > 0 {
		retries := int(command.Retries)
		return &retries, nil
	}
	return nil, nil
}

func (this *Type) Execute(ctx context.Context, task engine.Task) (engine.TaskResult, error) {
	token, err := this.token(task.Owner)
	if err != nil {
		return engine.TaskResult{}, fmt.Errorf("unable to get a token of the owner: %w", err)
	}
	command, err := this.command(ctx, task, token)
	if err != nil {
		return engine.TaskResult{}, err
	}
	children, err := this.subtasks(ctx, token, command, task.JobId)
	if err != nil {
		return engine.TaskResult{}, err
	}
	info := TaskInfo{
		WorkerId: this.workerId, ProcessInstanceId: task.InstanceId,
		// connectors mirror it back; the old name until all of them know the new one (DATA-04)
		DeploymentId: task.DeploymentId, ProcessDefinitionId: task.DeploymentId,
		CompletionStrategy: strategy(task), TenantId: task.Owner,
	}
	if info.CompletionStrategy == Optimistic {
		for _, c := range children {
			info.TaskId = c.subId
			if _, err = this.send(ctx, token, c.command, info); err != nil {
				return engine.TaskResult{}, err
			}
		}
		return engine.TaskResult{Output: map[string]any{"result": nil, "results": []any{}}}, nil
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
	concurrency := this.concurrency(task.Attributes)
	done, out, err := this.advance(ctx, task.JobId, command.Version, token, true, task.Retries, task.Timeout, concurrency)
	if err != nil {
		return engine.TaskResult{}, err
	}
	if done {
		return engine.TaskResult{Output: out}, nil
	}
	if this.config.GroupScheduler == Parallel || concurrency == 1 || len(subtasks) < 2 {
		return engine.TaskResult{Async: true}, nil
	}
	// with several places, a try sent before this execution ends its window before the task
	// timeout, and its end frees a place for the next sub-task (SNRGY-5093)
	var current []store.Subtask
	err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		current, err = tx.ListSubtasks(task.JobId)
		return err
	})
	if err != nil {
		return engine.TaskResult{}, err
	}
	return engine.TaskResult{Async: true, WaitUntil: windowEnd(running(current, this.now(), task.Timeout), task.Timeout)}, nil
}

// advance sends the next sub-tasks without result: all of them in parallel mode, otherwise up
// to concurrency at a time. Values read from timescale count as results at once. done reports
// that every sub-task has a result, or one at a time, that the others have used up their tries.
// Without resend, parallel sub-tasks still in flight are not sent again. retries and timeout are
// those of the task; one at a time they apply to each try.
func (this *Type) advance(ctx context.Context, jobId string, version int64, token string, resend bool, retries int, timeout time.Duration, concurrency int) (done bool, out map[string]any, err error) {
	if this.config.GroupScheduler != Parallel {
		return this.advanceInTurn(ctx, jobId, version, token, retries, timeout, concurrency)
	}
	for {
		var subtasks []store.Subtask
		err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
			subtasks, err = tx.ListSubtasks(jobId)
			return err
		})
		if err != nil {
			return false, nil, err
		}
		pending := pendingOf(subtasks)
		if len(pending) == 0 {
			return true, output(version, subtasks), nil
		}
		if !resend {
			return false, nil, nil
		}
		readValues := false
		for _, s := range pending {
			if err = this.store.Tx(ctx, func(tx *store.Tx) error { return tx.CountSubtaskTry(jobId, s.SubId, this.now()) }); err != nil {
				return false, nil, err
			}
			read, err := this.sendSubtask(ctx, jobId, token, s)
			if err != nil {
				return false, nil, err
			}
			readValues = readValues || read
		}
		if !readValues {
			return false, nil, nil
		}
	}
}

// advanceInTurn sends the sub-tasks one at a time, up to concurrency with an open try at once
// (SNRGY-5093). Choosing a sub-task and counting its try are one step under the lock of the
// instance, and the command goes out after it: answers that arrive together and an execution
// then see each other's tries, so that no sub-task is sent twice and no more than concurrency
// tries are open. A sub-task goes out at most once per call, also when its window is over at
// once, and nothing goes out for a job that is gone.
func (this *Type) advanceInTurn(ctx context.Context, jobId string, version int64, token string, retries int, timeout time.Duration, concurrency int) (done bool, out map[string]any, err error) {
	sent := map[string]bool{}
	for {
		var subtasks, next []store.Subtask
		var waiting, stop bool
		err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
			exists, err := lockInstanceOf(tx, jobId)
			if err != nil || !exists {
				stop = true
				return err
			}
			if subtasks, err = tx.ListSubtasks(jobId); err != nil {
				return err
			}
			next, waiting = this.next(pendingOf(subtasks), retries, timeout, concurrency)
			if len(next) == 0 {
				return nil
			}
			if sent[next[0].SubId] {
				stop = true
				return nil
			}
			return tx.CountSubtaskTry(jobId, next[0].SubId, this.now())
		})
		if err != nil || stop {
			return false, nil, err
		}
		switch {
		case len(pendingOf(subtasks)) == 0:
			return true, output(version, subtasks), nil
		case waiting:
			return false, nil, nil
		case len(next) == 0:
			if len(results(2, subtasks).([]any)) > 0 {
				return true, output(version, subtasks), nil
			}
			return false, nil, &engine.PermanentError{Message: fmt.Sprintf("no answer from any device within %v, %v tries each", timeout, retries+1)}
		}
		sent[next[0].SubId] = true
		if _, err = this.sendSubtask(ctx, jobId, token, next[0]); err != nil {
			return false, nil, err
		}
	}
}

// lockInstanceOf locks the instance of a job, as the engine does for every change of it; false
// if the job or its instance is gone
func lockInstanceOf(tx *store.Tx, jobId string) (bool, error) {
	job, err := tx.GetJob(jobId)
	if err != nil || job == nil {
		return false, err
	}
	_, err = tx.GetInstance(job.InstanceId, true)
	if errors.Is(err, model.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func pendingOf(subtasks []store.Subtask) []store.Subtask {
	pending := []store.Subtask{}
	for _, s := range subtasks {
		if !s.HasResult {
			pending = append(pending, s)
		}
	}
	return pending
}

// sendSubtask sends the command of a sub-task whose try is counted; read reports a value read
// from timescale, which is stored as its result at once
func (this *Type) sendSubtask(ctx context.Context, jobId string, token string, s store.Subtask) (read bool, err error) {
	var command Command
	var info TaskInfo
	if err = json.Unmarshal(s.Command, &command); err != nil {
		return false, err
	}
	if err = json.Unmarshal(s.TaskInfo, &info); err != nil {
		return false, err
	}
	value, err := this.send(ctx, token, command, info)
	if err != nil || value == nil {
		return false, err
	}
	return true, this.store.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.SetSubtaskResult(jobId, s.SubId, value.value)
		return err
	})
}

// next picks the pending sub-tasks to send one at a time, as external-task-worker
// lib/devicegroups does, with up to concurrency open tries at once (SNRGY-5093): a sent command
// waits for its answer while it is not older than timeout, a sub-task is given up after
// retries + 1 tries, sequential takes the sub-tasks in their order and round robin those with
// the fewest tries first. waiting reports that open tries leave no place or nothing else to
// send; nothing without waiting means that every pending sub-task is given up.
func (this *Type) next(pending []store.Subtask, retries int, timeout time.Duration, concurrency int) (next []store.Subtask, waiting bool) {
	now := this.now()
	open := running(pending, now, timeout)
	candidates := []store.Subtask{}
	for _, s := range pending {
		if s.Tries <= retries && !waitsForAnswer(s, now, timeout) {
			candidates = append(candidates, s)
		}
	}
	free := concurrency - len(open)
	if free <= 0 || len(candidates) == 0 {
		return nil, len(open) > 0
	}
	if this.config.GroupScheduler == RoundRobin {
		slices.SortStableFunc(candidates, func(a, b store.Subtask) int { return a.Tries - b.Tries })
	}
	return candidates[:min(free, len(candidates))], false
}

// running returns the pending sub-tasks whose last command still waits for its answer
func running(pending []store.Subtask, now time.Time, timeout time.Duration) (result []store.Subtask) {
	for _, s := range pending {
		if waitsForAnswer(s, now, timeout) {
			result = append(result, s)
		}
	}
	return result
}

func waitsForAnswer(s store.Subtask, now time.Time, timeout time.Duration) bool {
	return !s.HasResult && s.Tries > 0 && now.Sub(s.LastTry) <= timeout
}

// windowEnd is the first instant after the earliest answer window of open tries ends, zero
// without one; the store keeps milliseconds
func windowEnd(open []store.Subtask, timeout time.Duration) (result time.Time) {
	for _, s := range open {
		if end := s.LastTry.Add(timeout + time.Millisecond); result.IsZero() || end.Before(result) {
			result = end
		}
	}
	return result
}

type readValue struct {
	value any
}

// send sends the command of a sub-task, or reads the last value for an event service
func (this *Type) send(ctx context.Context, token string, command Command, info TaskInfo) (*readValue, error) {
	msg, event, err := this.message(ctx, command, info, token)
	if err != nil {
		return nil, err
	}
	if event != nil {
		if info.CompletionStrategy == Optimistic {
			return nil, nil
		}
		value, err := this.lastValue(ctx, token, *event)
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

// output is what a finished task hands to the process: result as before, and results with the
// device and service of each value, so that a task on a device group tells which member
// answered (TASK-10, SNRGY-5054)
func output(version int64, subtasks []store.Subtask) map[string]any {
	return map[string]any{"result": results(version, subtasks), "results": deviceResults(subtasks)}
}

// deviceResults lists {device_id, service_id, value} in sub-task order, for the sub-tasks with a
// result like results, so that the i-th entry belongs to the i-th value of result
func deviceResults(subtasks []store.Subtask) []any {
	list := []any{}
	for _, s := range subtasks {
		if !s.HasResult {
			continue
		}
		var command Command
		_ = json.Unmarshal(s.Command, &command)
		list = append(list, map[string]any{"device_id": command.DeviceId, "service_id": command.ServiceId, "value": s.Result})
	}
	return list
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
	output, err := this.output(ctx, msg)
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
	concurrency := this.config.GroupConcurrency
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		job, err := tx.GetJob(jobId)
		if err != nil || job == nil {
			return err
		}
		retries, timeout = job.Retries, job.Timeout
		attributes, err := this.attributes(tx, *job)
		if err != nil {
			return err
		}
		concurrency = this.concurrency(attributes)
		subtasks, err := tx.ListSubtasks(jobId)
		if err != nil || len(subtasks) == 0 {
			return err
		}
		return json.Unmarshal(subtasks[0].Command, &command)
	})
	if err != nil {
		return err
	}
	token, err := this.token(msg.TaskInfo.TenantId)
	if err != nil {
		return err
	}
	done, out, err := this.advance(ctx, jobId, command.Version, token, false, retries, timeout, concurrency)
	if err != nil {
		_, failErr := this.completer.FailTask(ctx, jobId, err.Error())
		return errors.Join(err, failErr)
	}
	if done {
		_, err = this.completer.CompleteTask(ctx, jobId, out)
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
// task timeout is the answer timeout of each try and does not count as failed attempt: while
// open tries take every place, or nothing else is left to send, the task waits until the first
// of their windows ends, so that a command sent after the task was locked, e.g. on the answer of
// the previous device, gets its full timeout; otherwise the task is executed again and asks the
// next device or ends with the answers it has. In parallel, the last attempt completes with the
// results it has; without any result the timeout is a failure.
func (this *Type) OnTimeout(ctx context.Context, tx *store.Tx, task engine.Task) (*engine.TaskResult, error) {
	subtasks, err := tx.ListSubtasks(task.JobId)
	if err != nil || len(subtasks) < 2 {
		return nil, err
	}
	if this.config.GroupScheduler != Parallel {
		pending := pendingOf(subtasks)
		result := &engine.TaskResult{Async: true}
		if _, waiting := this.next(pending, task.Retries, task.Timeout, this.concurrency(task.Attributes)); waiting {
			result.WaitUntil = windowEnd(running(pending, this.now(), task.Timeout), task.Timeout)
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
	return &engine.TaskResult{Output: output(command.Version, subtasks)}, nil
}
