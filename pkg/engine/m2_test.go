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
	"fmt"
	"strings"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

// fails after a message, so that every run first waits and then becomes an incident
func failingProcess() string {
	return b.Process(
		b.Message("m", "fail"),
		b.Start("start"), b.CatchMessage("wait", "m"), b.Script("boom", "x", "missing + 1"),
		b.Flow("start", "wait"), b.Flow("wait", "boom"),
	)
}

func (this *harness) fail(instanceId string) {
	this.t.Helper()
	this.correlate(model.MessageInput{Name: "fail", InstanceId: instanceId})
	this.expectState(instanceId, model.StateIncident)
}

func (this *harness) runs(id string) []model.Run {
	this.t.Helper()
	var runs []model.Run
	if err := this.s.Tx(ctx, func(tx *store.Tx) (err error) {
		runs, err = tx.ListRuns(id)
		return err
	}); err != nil {
		this.t.Fatal(err)
	}
	return runs
}

func TestIncidentWithRestartStartsNewRunAfterGrowingBackoff(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		h.process()
		d := h.deployWith("processes", failingProcess(), model.IncidentHandling{Restart: true, Notify: true})
		inst := h.start(d, map[string]any{"p": 1})
		h.fail(inst.Id)
		if got := h.instance(inst.Id); got.RestartAt == nil || !got.RestartAt.Equal(h.now().Add(time.Minute)) {
			t.Fatalf("first restart after 1m, got %v", got.RestartAt)
		}
		h.advance(59 * time.Second)
		h.expectState(inst.Id, model.StateIncident)
		h.advance(time.Second)
		restarted := h.expectState(inst.Id, model.StateRunning)
		if restarted.Run != 2 || restarted.RestartAt != nil {
			t.Fatalf("unexpected %+v", restarted)
		}
		if runs := h.runs(inst.Id); runs[1].Cause != model.CauseIncident {
			t.Errorf("unexpected cause %v", runs[1].Cause)
		}
		h.expectVars(inst.Id, map[string]any{"p": 1.0})
		h.fail(inst.Id)
		if got := h.instance(inst.Id); !got.RestartAt.Equal(h.now().Add(2 * time.Minute)) {
			t.Fatalf("second consecutive incident doubles the backoff, got %v", got.RestartAt)
		}
		h.process()
		sent := h.notifier.messages()
		if len(sent) != 2 || sent[0].UserId != "user" || sent[0].Title != "Process-Incident in test" || !strings.Contains(sent[0].Message, "process will be restarted") {
			t.Fatalf("unexpected notifications %+v", sent)
		}
	})
}

func TestRestartLoopMarksDeploymentFaulty(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		h.namespaces["processes"] = model.Namespace{Name: "processes", RestartPolicy: model.RestartPolicy{MaxRestarts: 2, Window: "1h", InitialBackoff: "1s", MaxBackoff: "1s"}}
		d := h.deployWith("processes", failingProcess(), model.IncidentHandling{Restart: true})
		inst := h.start(d, nil)
		for range 2 {
			h.fail(inst.Id)
			h.advance(time.Second)
			h.expectState(inst.Id, model.StateRunning)
		}
		h.fail(inst.Id)
		h.advance(time.Second)
		stopped := h.expectState(inst.Id, model.StateIncident)
		if stopped.RestartAt != nil {
			t.Fatalf("no further restart is scheduled")
		}
		var stats store.RestartStats
		_ = h.s.Tx(ctx, func(tx *store.Tx) (err error) {
			stats, err = tx.GetRestartStats(d)
			return err
		})
		if stats.FaultySince == nil {
			t.Fatalf("the deployment must be marked faulty (INC-04)")
		}
		sent := h.notifier.messages()
		if len(sent) != 1 || sent[0].UserId != "owner" || !strings.Contains(sent[0].Title, "stopped restarting") {
			t.Fatalf("the deployment owner is told: %+v", sent)
		}
		if err := h.s.Tx(ctx, func(tx *store.Tx) error { return h.e.ResetRestarts(tx, d) }); err != nil {
			t.Fatal(err)
		}
		if _, err := h.e.RestartInstance(ctx, inst.Id, 0, model.CauseRestart); err != nil {
			t.Fatal(err)
		}
		h.fail(inst.Id)
		h.advance(time.Second)
		h.expectState(inst.Id, model.StateRunning)
	})
}

func TestStopCancelsPendingRestart(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		inst := h.start(h.deployWith("processes", failingProcess(), model.IncidentHandling{Restart: true}), nil)
		h.fail(inst.Id)
		if err := h.e.StopInstance(ctx, inst.Id, "stopped"); err != nil {
			t.Fatal(err)
		}
		h.advance(time.Hour)
		if got := h.expectState(inst.Id, model.StateIncident); got.Run != 1 || got.RestartAt != nil {
			t.Fatalf("unexpected %+v", got)
		}
	})
}

func TestNotificationsAreRetriedWhenTheNotifierFails(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		h.notifier.fail = true
		inst := h.start(h.deployWith("processes", failingProcess(), model.IncidentHandling{Notify: true}), nil)
		h.fail(inst.Id)
		h.mux.Lock()
		h.clock = h.clock.Add(time.Second)
		h.mux.Unlock()
		if err := h.e.ProcessDue(ctx); err != nil {
			t.Fatal(err)
		}
		if err := h.e.ProcessNotifications(ctx); err == nil {
			t.Fatal("expected the failed notification to be reported")
		}
		h.notifier.fail = false
		h.advance(2 * time.Second)
		if sent := h.notifier.messages(); len(sent) != 1 {
			t.Fatalf("the notification must be sent after the outage, got %v", len(sent))
		}
	})
}

// queueNotifications queues n notifications due now
func (this *harness) queueNotifications(n int) {
	this.t.Helper()
	for i := range n {
		if err := this.e.QueueNotification(ctx, "alice", "processes", fmt.Sprintf("title %v", i), "message"); err != nil {
			this.t.Fatal(err)
		}
	}
}

// A notifier without answer would make each due notification wait for its timeout; after the
// first, the others wait for the next attempt without counting one (SNRGY-5064).
func TestNotificationsWaitTogetherWhileTheNotifierDoesNotAnswer(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		h.notifier.unreachable = true
		h.queueNotifications(3)
		if err := h.e.ProcessNotifications(ctx); err == nil {
			t.Fatal("expected the failed notification to be reported")
		}
		if n := h.notifier.attempted(); n != 1 {
			t.Fatalf("one attempt while the notifier does not answer, got %v", n)
		}
		// all three wait for the backoff of the first attempt, one second
		h.mux.Lock()
		h.clock = h.clock.Add(500 * time.Millisecond)
		h.mux.Unlock()
		if err := h.e.ProcessNotifications(ctx); err != nil || h.notifier.attempted() != 1 {
			t.Fatalf("nothing is due before the backoff: %v %v", err, h.notifier.attempted())
		}
		h.notifier.unreachable = false
		h.advance(time.Second)
		if sent := h.notifier.messages(); len(sent) != 3 {
			t.Fatalf("all notifications are sent once the notifier answers, got %v", len(sent))
		}
		if n := h.notifier.attempted(); n != 4 {
			t.Fatalf("the postponed ones were not attempted before, got %v attempts", n)
		}
	})
}

// A notifier that refuses one message may accept the next; every due notification is tried.
func TestNotificationsAreAllTriedWhenTheNotifierRefusesThem(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		h.notifier.fail = true
		h.queueNotifications(3)
		if err := h.e.ProcessNotifications(ctx); err == nil {
			t.Fatal("expected the refused notifications to be reported")
		}
		if n := h.notifier.attempted(); n != 3 {
			t.Fatalf("every notification is tried, got %v attempts", n)
		}
	})
}

// Timers and jobs do not wait for the notifier (SNRGY-5064).
func TestProcessDueSendsNoNotifications(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		h.queueNotifications(1)
		if err := h.e.ProcessDue(ctx); err != nil {
			t.Fatal(err)
		}
		if n := h.notifier.attempted(); n != 0 {
			t.Fatalf("ProcessDue called the notifier %v times", n)
		}
	})
}

// Run fires a due timer while the notifier hangs in a call (SNRGY-5064).
func TestRunFiresTimersWhileTheNotifierHangs(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		h := newHarness(t, s, func(c *Config) { c.PollInterval = 10 * time.Millisecond })
		h.notifier.hang, h.notifier.hanging = true, make(chan struct{}, 1)
		d := h.deploy(b.Process(b.Start("start"), b.CatchTimer("wait", "timeDuration", "PT1M"), b.End("end"),
			b.Flow("start", "wait"), b.Flow("wait", "end")))
		inst := h.start(d, nil)
		h.queueNotifications(1)
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			h.e.Run(runCtx)
			close(done)
		}()
		defer func() {
			cancel()
			<-done
		}()
		select {
		case <-h.notifier.hanging:
		case <-time.After(5 * time.Second):
			t.Fatal("the notifier was not called")
		}
		h.mux.Lock()
		h.clock = h.clock.Add(time.Minute)
		h.mux.Unlock()
		for deadline := time.Now().Add(5 * time.Second); h.instance(inst.Id).State != model.StateCompleted; time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("the timer did not fire while the notifier hangs: %v", h.instance(inst.Id).State)
			}
		}
	})
}

func waitProcess() string {
	return b.Process(b.Message("m", "go"), b.Start("start"), b.Script("s", "x", "1"), b.CatchMessage("wait", "m"), b.Flow("start", "s"), b.Flow("s", "wait"))
}

func TestRetentionRemovesFinishedInstancesAndOldHistory(t *testing.T) {
	storeRun(t, func(c *Config) { c.RetentionDefaultDays = 7; c.HousekeepingInterval = 0 }, func(t *testing.T, h *harness) {
		d := h.deploy(waitProcess())
		finished := h.start(d, nil)
		h.correlate(model.MessageInput{Name: "go", InstanceId: finished.Id})
		running := h.start(d, nil)
		h.advance(6 * 24 * time.Hour)
		h.instance(finished.Id)
		h.advance(2 * 24 * time.Hour)
		if err := h.s.Tx(ctx, func(tx *store.Tx) error { _, err := tx.GetInstance(finished.Id, false); return err }); err == nil {
			t.Fatalf("the finished instance must be deleted after 7 days")
		}
		h.expectState(running.Id, model.StateRunning)
		if old := h.activities(running.Id, ""); len(old) != 0 {
			t.Fatalf("history of a running instance older than the retention must go (HIST-06), got %v entries", len(old))
		}
		h.correlate(model.MessageInput{Name: "go", InstanceId: running.Id})
		if a := h.activities(running.Id, ""); len(a) == 0 || a[0].Kind != model.ActivityMessageReceived {
			t.Fatalf("new history stays: %+v", a)
		}
	})
}

func TestUserRetentionLimitApplies(t *testing.T) {
	storeRun(t, func(c *Config) { c.HousekeepingInterval = 0 }, func(t *testing.T, h *harness) {
		d := h.deploy(waitProcess())
		inst := h.start(d, nil)
		h.correlate(model.MessageInput{Name: "go", InstanceId: inst.Id})
		if err := h.s.Tx(ctx, func(tx *store.Tx) error {
			return tx.SetUserSettings(model.UserSettings{UserId: "user", MaxRetentionDays: 1})
		}); err != nil {
			t.Fatal(err)
		}
		h.advance(25 * time.Hour)
		if err := h.s.Tx(ctx, func(tx *store.Tx) error { _, err := tx.GetInstance(inst.Id, false); return err }); err == nil {
			t.Fatalf("the user limit of one day must apply")
		}
	})
}

func TestRetentionIsTheSmallestOfValueAndLimits(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		h.namespaces["capped"] = model.Namespace{Name: "capped", Retention: model.Retention{DefaultDays: 3, MaxDays: 5}}
		cases := []struct {
			namespace  string
			deployment int
			expected   int
		}{
			{"processes", 0, 7}, {"processes", 1, 1}, {"processes", 30, 30},
			{"capped", 0, 3}, {"capped", 4, 4}, {"capped", 30, 5},
		}
		for _, c := range cases {
			if got := h.e.Retention(c.namespace, c.deployment); got != c.expected {
				t.Errorf("%+v: got %v", c, got)
			}
		}
	})
}

func TestNamespaceLimitsOverrideEngineLimits(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		h.namespaces["tight"] = model.Namespace{Name: "tight", Limits: model.Limits{MaxVariableBytes: 10}}
		inst := h.start(h.deployIn("tight", b.Process(b.Start("s"), b.Script("big", "x", "'0123456789abc'"), b.Flow("s", "big"))), nil)
		h.expectState(inst.Id, model.StateIncident)
	})
}

func TestOldModelVersionsAreTrimmedUnlessDeployed(t *testing.T) {
	storeRun(t, func(c *Config) { c.ModelVersionsKept = 2; c.HousekeepingInterval = 0 }, func(t *testing.T, h *harness) {
		now := h.now()
		err := h.s.Tx(ctx, func(tx *store.Tx) error {
			if err := tx.InsertModel(model.Model{Id: "m", Namespace: "processes", Name: "m", Owner: "u", Version: 5, CreatedAt: now, UpdatedAt: now}); err != nil {
				return err
			}
			for v := 1; v <= 5; v++ {
				if err := tx.InsertModelVersion(model.ModelVersion{ModelId: "m", Version: v, Xml: "x", CreatedAt: now}); err != nil {
					return err
				}
			}
			return tx.InsertDeployment(model.Deployment{Id: "d", Namespace: "processes", Name: "d", Owner: "u", ModelId: "m", ModelVersion: 1, Version: 1, CreatedAt: now, UpdatedAt: now})
		})
		if err != nil {
			t.Fatal(err)
		}
		h.advance(time.Second)
		var versions model.Page[model.ModelVersion]
		_ = h.s.Tx(ctx, func(tx *store.Tx) (err error) {
			versions, err = tx.ListModelVersions("m", model.PageOptions{})
			return err
		})
		kept := []int{}
		for _, v := range versions.Items {
			kept = append(kept, v.Version)
		}
		if len(kept) != 3 || kept[0] != 5 || kept[1] != 4 || kept[2] != 1 {
			t.Fatalf("expected the newest two versions and the deployed 1, got %v", kept)
		}
	})
}
