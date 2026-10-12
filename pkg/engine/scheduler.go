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
	"fmt"
	"sync"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/timer"
	"github.com/SENERGY-Platform/automation-engine/pkg/tracing"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
)

// ProcessDue fires due timers and start timers and executes due jobs. Every timer and job is
// handled in its own transaction that locks the instance (or the deployment for start timers)
// and checks that the timer or job still exists; a second replica finds nothing to do, so
// each fires exactly once (RUN-06, OPS-01).
func (this *Engine) ProcessDue(ctx context.Context) error {
	now := this.now()
	return errors.Join(
		this.fireStartTimers(ctx, now),
		this.fireSchedules(ctx, now),
		this.fireTimers(ctx, now),
		this.runJobs(ctx, now),
		this.processRestarts(ctx, now),
		this.processNotifications(ctx, now),
		this.housekeeping(ctx, now),
	)
}

func (this *Engine) fireTimers(ctx context.Context, now time.Time) error {
	var due []store.Subscription
	err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		due, err = tx.DueTimers(now, this.config.BatchSize)
		return err
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, candidate := range due {
		err = this.store.Tx(ctx, func(tx *store.Tx) error {
			inst, err := tx.GetInstance(candidate.InstanceId, true)
			if err != nil {
				return ignoreNotFound(err)
			}
			s, err := tx.GetSubscription(candidate.Id)
			if err != nil || s == nil || inst.State != model.StateRunning {
				return err
			}
			r, err := this.newRunner(tx, &inst)
			if err != nil {
				return err
			}
			if s.DueAt != nil {
				this.metrics.TimerDelay(now.Sub(*s.DueAt))
			}
			data := map[string]any{"due": s.DueAt, "definition": s.Definition}
			if err = r.trigger(*s, nil, nil, model.ActivityTimerFired, data); err != nil {
				if err = r.handle(err); err != nil {
					return err
				}
			}
			return r.finish()
		})
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (this *Engine) fireStartTimers(ctx context.Context, now time.Time) error {
	var due []store.StartEvent
	err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		due, err = tx.DueStartTimers(now, this.config.BatchSize)
		return err
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, candidate := range due {
		err = this.store.Tx(ctx, func(tx *store.Tx) error {
			deployment, err := tx.GetDeployment(candidate.DeploymentId, true)
			if err != nil {
				return ignoreNotFound(err)
			}
			current, err := tx.GetStartEvent(candidate.DeploymentId, candidate.ElementId)
			if err != nil {
				return ignoreNotFound(err)
			}
			if current.DueAt == nil || current.DueAt.After(now) {
				return nil
			}
			proc, err := this.process(tx, deployment.Id, current.Version)
			if err != nil {
				return err
			}
			start := proc.Element(current.ElementId)
			if start == nil || start.Timer == nil {
				return tx.SetStartEventDue(deployment.Id, current.ElementId, nil)
			}
			// missed cycles fire once, not once per missed cycle (LONG-12)
			next, err := this.startTimerDue(start, now)
			if err != nil || start.Timer.Kind != timer.KindCycle {
				next = nil
			}
			if err = tx.SetStartEventDue(deployment.Id, current.ElementId, next); err != nil {
				return err
			}
			_, _, err = this.startInstance(tx, StartRequest{DeploymentId: deployment.Id, DeploymentVersion: current.Version, Owner: deployment.Owner, StartElementId: current.ElementId}, model.CauseStart)
			var modelErr *model.Error
			if errors.As(err, &modelErr) && modelErr.Kind != model.KindInternal && modelErr.Kind != model.KindUnavailable {
				this.logger.Warn("timer start failed", "deployment_id", deployment.Id, "element_id", current.ElementId, "error", err)
				return tx.SetStartEventDue(deployment.Id, current.ElementId, next)
			}
			return err
		})
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func ignoreNotFound(err error) error {
	if errors.Is(err, model.ErrNotFound) {
		return nil
	}
	return err
}

// claimed is a job claimed by this replica for one execution
type claimed struct {
	job  store.Job
	task Task
}

func (this *Engine) runJobs(ctx context.Context, now time.Time) error {
	var candidates []store.Job
	err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		candidates, err = tx.JobCandidates(now, this.config.BatchSize)
		return err
	})
	if err != nil {
		return err
	}
	var errs []error
	var claims []claimed
	for _, candidate := range candidates {
		c, err := this.claim(ctx, candidate.Id, now)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if c != nil {
			claims = append(claims, *c)
		}
	}
	var wg sync.WaitGroup
	var mux sync.Mutex
	for _, c := range claims {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := this.execute(ctx, c); err != nil {
				mux.Lock()
				errs = append(errs, err)
				mux.Unlock()
			}
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

// claim locks a due job for one execution; an expired lock counts as failed attempt (TASK-07)
func (this *Engine) claim(ctx context.Context, jobId string, now time.Time) (result *claimed, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		job, err := tx.GetJob(jobId)
		if err != nil || job == nil {
			return err
		}
		inst, err := tx.GetInstance(job.InstanceId, true)
		if err != nil {
			return ignoreNotFound(err)
		}
		// re-read under the instance lock
		job, err = tx.GetJob(jobId)
		if err != nil || job == nil || inst.State != model.StateRunning {
			return err
		}
		switch {
		case job.State == store.JobPending && !job.DueAt.After(now):
		case job.State != store.JobPending && !job.LockUntil.After(now):
			if result, handled, err := this.timeoutResult(ctx, tx, inst, *job); err != nil || handled {
				if err != nil {
					return err
				}
				r, err := this.newRunner(tx, &inst)
				if err != nil {
					return err
				}
				if err = r.completeTask(*job, result.Output); err != nil {
					if err = r.handle(err); err != nil {
						return err
					}
				}
				return r.finish()
			}
			r, err := this.newRunner(tx, &inst)
			if err != nil {
				return err
			}
			retry, err := r.failJob(job, fmt.Sprintf("task timed out after %v", job.Timeout), false)
			if err != nil {
				return err
			}
			if err = r.finish(); err != nil || !retry {
				return err
			}
			// the retry is due after the backoff
			return nil
		default:
			return nil
		}
		version, err := this.version(tx, inst.DeploymentId, inst.DeploymentVersion)
		if err != nil {
			return err
		}
		job.State = store.JobRunning
		job.Attempt++
		job.LockOwner = this.config.ReplicaId + "/" + uuid.NewString()
		job.LockUntil = now.Add(job.Timeout)
		if err = tx.SaveJob(*job); err != nil {
			return err
		}
		result = &claimed{job: *job, task: taskOf(*job, inst, version)}
		return nil
	})
	return result, err
}

func rootOf(inst store.Instance) string {
	if inst.ParentId != "" {
		return inst.ParentId
	}
	return inst.Id
}

func taskOf(job store.Job, inst store.Instance, version *cachedVersion) Task {
	attributes := map[string]string{}
	if element := version.process.Element(job.ElementId); element != nil {
		attributes = element.Attributes
	}
	var selection *model.Selection
	if s, ok := version.selections[job.ElementId]; ok {
		selection = &s
	}
	return Task{
		JobId: job.Id, InstanceId: inst.Id, Run: inst.Run, Namespace: inst.Namespace, DeploymentId: inst.DeploymentId,
		ElementId: job.ElementId, TaskType: job.TaskType, Owner: inst.Owner, Input: job.Input, Attributes: attributes,
		Attempt: job.Attempt, Retries: job.Retries, Timeout: job.Timeout, Selection: selection, RootInstanceId: rootOf(inst),
	}
}

// timeoutResult asks the task type for a result of a timed out asynchronous task
func (this *Engine) timeoutResult(ctx context.Context, tx *store.Tx, inst store.Instance, job store.Job) (*TaskResult, bool, error) {
	taskType, ok := this.taskType(job.TaskType)
	if !ok {
		return nil, false, nil
	}
	handler, ok := taskType.(TimeoutHandler)
	if !ok || job.Attempt <= job.Retries {
		// only the last attempt ends with a partial result; earlier ones are retried
		return nil, false, nil
	}
	version, err := this.version(tx, inst.DeploymentId, inst.DeploymentVersion)
	if err != nil {
		return nil, false, err
	}
	result, err := handler.OnTimeout(ctx, tx, taskOf(job, inst, version))
	if err != nil || result == nil {
		return nil, false, err
	}
	return result, true, nil
}

func (this *Engine) execute(ctx context.Context, c claimed) (err error) {
	// the span covers the task and the commit of its result; finishJob and the store run on
	// spanCtx, so that they carry the trace but not the timeout of the task
	spanCtx, span := tracing.Start(ctx, "task "+c.job.TaskType, tracing.Instance{
		Namespace: c.task.Namespace, DeploymentId: c.task.DeploymentId, InstanceId: c.task.InstanceId, Owner: c.task.Owner,
	}, attribute.String("element_id", c.job.ElementId), attribute.Int("attempt", c.job.Attempt))
	var taskErr error
	defer func() { tracing.End(span, errors.Join(taskErr, err)) }()
	taskType, ok := this.taskType(c.job.TaskType)
	if !ok {
		taskErr = fmt.Errorf("task type %q is not available", c.job.TaskType)
		_, err = this.finishJob(spanCtx, c.job.Id, c.job.LockOwner, TaskResult{}, taskErr, false)
		return err
	}
	taskCtx, cancel := context.WithTimeout(spanCtx, c.job.Timeout)
	defer cancel()
	started := time.Now()
	result, taskErr := safeExecute(taskCtx, taskType, c.task)
	outcome := "completed"
	switch {
	case taskErr != nil:
		outcome = "failed"
	case result.Async:
		outcome = "async"
	}
	this.metrics.Task(c.job.TaskType, outcome, time.Since(started))
	if ctx.Err() != nil {
		// the engine shuts down; the lock expires and the job is retried
		return nil
	}
	if taskErr == nil && result.Async {
		return this.store.Tx(spanCtx, func(tx *store.Tx) error {
			job, err := tx.GetJob(c.job.Id)
			if err != nil || job == nil || job.LockOwner != c.job.LockOwner || job.State != store.JobRunning {
				return err
			}
			job.State = store.JobWaiting
			return tx.SaveJob(*job)
		})
	}
	applied, err := this.finishJob(spanCtx, c.job.Id, c.job.LockOwner, result, taskErr, false)
	if !applied && taskErr == nil && result.Effects != nil {
		if undoer, ok := taskType.(Undoer); ok {
			reason := err
			if reason == nil {
				reason = errors.New("the task result was not applied, the instance no longer waits for it")
			}
			undoer.Undo(context.WithoutCancel(spanCtx), c.task, result.Effects, reason)
		}
	}
	return err
}

func safeExecute(ctx context.Context, taskType TaskType, task Task) (result TaskResult, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("task type panicked: %v", p)
		}
	}()
	return taskType.Execute(ctx, task)
}

// finishJob completes or fails a job. lockOwner identifies the execution; an answer for
// another execution or for a job that no longer exists is ignored, so every task is completed
// at most once (TASK-11). external answers (CompleteTask, FailTask) skip the owner check.
func (this *Engine) finishJob(ctx context.Context, jobId string, lockOwner string, result TaskResult, taskErr error, external bool) (applied bool, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		applied = false
		job, err := tx.GetJob(jobId)
		if err != nil || job == nil {
			return err
		}
		inst, err := tx.GetInstance(job.InstanceId, true)
		if err != nil {
			return ignoreNotFound(err)
		}
		job, err = tx.GetJob(jobId)
		if err != nil || job == nil || inst.State != model.StateRunning || job.State == store.JobPending {
			return err
		}
		if !external && job.LockOwner != lockOwner {
			return nil
		}
		r, err := this.newRunner(tx, &inst)
		if err != nil {
			return err
		}
		if taskErr != nil {
			_, err = r.failJob(job, taskErr.Error(), IsPermanent(taskErr))
		} else {
			if result.Effects != nil {
				if err = r.applyEffects(*job, *result.Effects); err != nil {
					return err
				}
			}
			err = r.completeTask(*job, result.Output)
		}
		if err != nil {
			if err = r.handle(err); err != nil {
				return err
			}
		}
		applied = true
		return r.finish()
	})
	return applied && err == nil, err
}

// CompleteTask delivers the answer of an asynchronous task; false if the task is no longer
// waiting, e.g. because the answer is a duplicate or late.
func (this *Engine) CompleteTask(ctx context.Context, jobId string, output map[string]any) (bool, error) {
	return this.answer(ctx, jobId, output, nil)
}

// FailTask reports the failure of an asynchronous task; it is retried or becomes an incident.
func (this *Engine) FailTask(ctx context.Context, jobId string, message string) (bool, error) {
	return this.answer(ctx, jobId, nil, errors.New(message))
}

func (this *Engine) answer(ctx context.Context, jobId string, output map[string]any, taskErr error) (bool, error) {
	var exists bool
	err := this.store.Tx(ctx, func(tx *store.Tx) error {
		job, err := tx.GetJob(jobId)
		exists = job != nil && job.State != store.JobPending
		return err
	})
	if err != nil || !exists {
		return false, err
	}
	_, err = this.finishJob(ctx, jobId, "", TaskResult{Output: output}, taskErr, true)
	return true, err
}

// The job is retried after a backoff while retries are left, then the failure becomes an incident.
// failJob counts a failed attempt; a permanent error skips the remaining retries
func (this *runner) failJob(job *store.Job, message string, permanent bool) (retry bool, err error) {
	element := this.proc.Element(job.ElementId)
	if element == nil {
		element = &bpmn.Element{Id: job.ElementId}
	}
	if err = this.tx.RecordElementError(this.inst.Id, this.inst.Run, job.ElementId, message, this.now); err != nil {
		return false, err
	}
	if err = this.log(model.ActivityTaskFailed, element, map[string]any{"job_id": job.Id, "attempt": job.Attempt, "error": message}); err != nil {
		return false, err
	}
	if job.Attempt > job.Retries || permanent {
		return false, this.incident(job.ElementId, message)
	}
	job.State = store.JobPending
	job.DueAt = this.now.Add(this.e.config.TaskRetryBackoff * time.Duration(max(job.Attempt, 1)))
	job.LockOwner = ""
	job.LockUntil = time.Time{}
	job.LastError = message
	this.scheduled = true
	return true, this.tx.SaveJob(*job)
}

// housekeeping removes old message receipts and returns free pages to the file system in
// small steps, without a batch window (LONG-05, LONG-06)
func (this *Engine) housekeeping(ctx context.Context, now time.Time) error {
	this.housekeepingMux.Lock()
	if now.Sub(this.lastHousekeeping) < this.config.HousekeepingInterval {
		this.housekeepingMux.Unlock()
		return nil
	}
	this.lastHousekeeping = now
	this.housekeepingMux.Unlock()
	err := this.store.Tx(ctx, func(tx *store.Tx) error {
		if _, err := tx.PurgeChanges(now.Add(-this.config.ChangeRetention), 10000); err != nil {
			return err
		}
		_, err := tx.PurgeReceipts(now.Add(-this.config.ReceiptRetention))
		return err
	})
	return errors.Join(err, this.enforceRetention(ctx, now), this.enforceBudget(ctx, now), this.trimModelVersions(ctx), this.store.Vacuum(ctx, 256))
}

// BudgetWarning is the title of the warning about a nearly full storage budget.
const BudgetWarning = "Storage nearly full"

// enforceBudget trims the history beyond the retention, oldest first, when the database uses
// 80 % of the storage budget, until it uses 70 %; it warns at most once a day. On a gateway the
// budget takes precedence over the retention, and runtime state over history (LONG-07).
func (this *Engine) enforceBudget(ctx context.Context, now time.Time) error {
	budget := this.config.StorageBudget
	if budget <= 0 {
		return nil
	}
	used, err := this.usedBytes(ctx)
	if err != nil || used < budget*8/10 {
		return err
	}
	if now.Sub(this.lastBudgetWarning) >= 24*time.Hour {
		this.lastBudgetWarning = now
		message := fmt.Sprintf("The process engine uses %d of %d MiB; the oldest history of processes is removed beyond its retention.", used>>20, budget>>20)
		// on a gateway the cloud addresses the warning to the owner of the network
		if err = this.QueueNotification(ctx, "", BudgetWarning, message); err != nil {
			this.logger.Warn("unable to queue the storage warning", "error", err)
		}
	}
	for round := 0; round < 100 && used > budget*7/10; round++ {
		var removed int64
		err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
			removed, err = tx.TrimOldestHistory(this.config.BatchSize * 10)
			return err
		})
		if err != nil || removed == 0 {
			if removed == 0 && err == nil {
				this.logger.Warn("storage budget exceeded by runtime state, no history left to remove", "used", used, "budget", budget)
			}
			return err
		}
		if used, err = this.usedBytes(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (this *Engine) usedBytes(ctx context.Context) (used int64, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		used, err = tx.UsedBytes()
		return err
	})
	return used, err
}
