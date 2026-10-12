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
	"errors"
	"sync"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

func TestIntermediateTimerFiresOnceAcrossReplicas(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(
			b.Start("start"), b.CatchTimer("wait", "timeDuration", "PT5M"), b.Script("count", "fired", "typeof fired == 'undefined' ? 1 : fired + 1"), b.End("end"),
			b.Flow("start", "wait"), b.Flow("wait", "count"), b.Flow("count", "end"),
		))
		inst := h.start(d, nil)
		h.advance(4 * time.Minute)
		h.expectState(inst.Id, model.StateRunning)
		h.mux.Lock()
		h.clock = h.clock.Add(time.Minute)
		h.mux.Unlock()
		replicas := []*Engine{h.e, h.replica(), h.replica()}
		var wg sync.WaitGroup
		for _, e := range replicas {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := e.ProcessDue(ctx); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		h.expectState(inst.Id, model.StateCompleted)
		h.expectVars(inst.Id, map[string]any{"fired": 1.0})
	})
}

func TestTimerValuesFromExpressionsAndCron(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(
			b.Start("start"), b.CatchTimerExpression("delay", "timeDuration", "`PT${minutes}M`"), b.CatchTimer("cron", "timeCycle", "0 0 11 * * ?"), b.End("end"),
			b.Flow("start", "delay"), b.Flow("delay", "cron"), b.Flow("cron", "end"),
		))
		inst := h.start(d, map[string]any{"minutes": 10})
		h.advance(10 * time.Minute) // 10:40
		scheduled := h.activities(inst.Id, model.ActivityTimerScheduled)
		if len(scheduled) != 2 || scheduled[1].Data["definition"] != "0 0 11 * * ?" {
			t.Fatalf("unexpected %+v", scheduled)
		}
		h.advance(19 * time.Minute) // 10:59
		h.expectState(inst.Id, model.StateRunning)
		h.advance(time.Minute) // 11:00
		h.expectState(inst.Id, model.StateCompleted)
	})
}

func TestEventBasedGatewayTakesFirstEventAndCancelsTheOthers(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(
			b.Message("m", "door"),
			b.Start("start"), b.Gateway("eventBased", "ebg", ""),
			b.CatchMessage("msg", "m", b.OutScript("opened", "event")), b.CatchTimer("timeout", "timeDuration", "PT1M"),
			b.Script("viaMessage", "path", "'message'"), b.Script("viaTimer", "path", "'timer'"),
			b.Flow("start", "ebg"), b.Flow("ebg", "msg"), b.Flow("ebg", "timeout"), b.Flow("msg", "viaMessage"), b.Flow("timeout", "viaTimer"),
		))
		byMessage := h.start(d, nil)
		result := h.correlate(model.MessageInput{Name: "door", All: true, LocalVariables: map[string]any{"event": "open"}})
		if len(result.Correlations) != 1 {
			t.Fatalf("unexpected %+v", result)
		}
		h.expectState(byMessage.Id, model.StateCompleted)
		h.expectVars(byMessage.Id, map[string]any{"path": "message", "opened": "open"})

		byTimer := h.start(d, nil)
		h.advance(time.Minute)
		h.expectState(byTimer.Id, model.StateCompleted)
		h.expectVars(byTimer.Id, map[string]any{"path": "timer"})
		if result = h.correlate(model.MessageInput{Name: "door", All: true}); len(result.Correlations) != 0 {
			t.Fatalf("the message subscription must be cancelled: %+v", result)
		}
	})
}

func TestDeviceEventsLoopWithoutGrowingRuntimeOrHistory(t *testing.T) {
	storeRun(t, func(c *Config) { c.MaxActivities = 50 }, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(
			b.Start("start"), b.Gateway("exclusive", "loop", ""),
			b.CatchDeviceEvent("reading", b.OutScript("value", "event")),
			b.Script("count", "n", "typeof n == 'undefined' ? 1 : n + 1"),
			b.Flow("start", "loop"), b.Flow("loop", "reading"), b.Flow("reading", "count"), b.Flow("count", "loop"),
		))
		inst := h.start(d, nil)
		name := EventMessageName(d, "reading")
		for i := range 200 {
			result := h.correlate(model.MessageInput{Name: name, All: true, MessageId: "msg-" + string(rune('a'+i%26)) + string(rune('a'+i/26)), LocalVariables: map[string]any{"event": float64(i)}})
			if len(result.Correlations) != 1 {
				t.Fatalf("iteration %v: %+v", i, result)
			}
		}
		h.expectVars(inst.Id, map[string]any{"n": 200.0, "value": 199.0})
		if rt := h.runtime(inst.Id); rt.tokens != 1 || rt.subscriptions != 1 {
			t.Errorf("runtime state must not grow with iterations (LONG-01): %+v", rt)
		}
		stored := h.activities(inst.Id, "")
		if len(stored) > 50 {
			t.Errorf("activity log must be bounded (LONG-03), got %v", len(stored))
		}
		if stored[0].Seq <= 1 {
			t.Errorf("oldest entries must be evicted")
		}
	})
}

func storeRun(t *testing.T, configure func(*Config), fn func(t *testing.T, h *harness)) {
	run(t, func(t *testing.T, base *harness) {
		fn(t, newHarness(t, base.s, configure))
	})
}

func TestCorrelationIsIdempotentPerMessageId(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(
			b.Message("m", "tick"),
			b.Start("start"), b.Gateway("exclusive", "loop", ""), b.CatchMessage("wait", "m"), b.Script("count", "n", "typeof n == 'undefined' ? 1 : n + 1"),
			b.Flow("start", "loop"), b.Flow("loop", "wait"), b.Flow("wait", "count"), b.Flow("count", "loop"),
		))
		inst := h.start(d, nil)
		h.correlate(model.MessageInput{Name: "tick", All: true, MessageId: "topic:0:1"})
		again := h.correlate(model.MessageInput{Name: "tick", All: true, MessageId: "topic:0:1"})
		if len(again.Correlations) != 1 || !again.Correlations[0].Duplicate {
			t.Fatalf("redelivery must be reported as duplicate: %+v", again)
		}
		h.correlate(model.MessageInput{Name: "tick", All: true, MessageId: "topic:0:2"})
		h.expectVars(inst.Id, map[string]any{"n": 2.0})
		if none := h.correlate(model.MessageInput{Name: "unknown", All: true}); len(none.Correlations) != 0 {
			t.Fatalf("a message without target is not an error: %+v", none)
		}
	})
}

func TestCorrelationWithoutAllRejectsMultipleTargets(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(b.Message("m", "go"), b.Start("start"), b.CatchMessage("wait", "m"), b.Flow("start", "wait")))
		first, second := h.start(d, nil), h.start(d, nil)
		_, err := h.e.Correlate(ctx, CorrelationRequest{MessageInput: model.MessageInput{Name: "go"}})
		if !errors.Is(err, model.ErrConflict) {
			t.Fatalf("expected conflict, got %v", err)
		}
		h.correlate(model.MessageInput{Name: "go", InstanceId: second.Id})
		h.expectState(second.Id, model.StateCompleted)
		h.expectState(first.Id, model.StateRunning)
		other, err := h.e.Correlate(ctx, CorrelationRequest{MessageInput: model.MessageInput{Name: "go", All: true}, OwnerFilter: "someone else"})
		if err != nil || len(other.Correlations) != 0 {
			t.Fatalf("the owner filter must restrict correlation: %+v %v", other, err)
		}
	})
}

func TestMessageStartCreatesInstanceWithGivenId(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		h.deploy(b.Process(
			b.Message("m", "maintenance"),
			b.Start("start"), b.StartMessage("byMessage", "m"), b.Script("s", "started", "true"), b.End("end"),
			b.Flow("start", "end"), b.Flow("byMessage", "s"), b.Flow("s", "end"),
		))
		result := h.correlate(model.MessageInput{Name: "maintenance", InstanceId: "maintenance-1", Variables: map[string]any{"p": 1}, MessageId: "m1"})
		if len(result.Correlations) != 1 || !result.Correlations[0].Started || result.Correlations[0].InstanceId != "maintenance-1" {
			t.Fatalf("unexpected %+v", result)
		}
		inst := h.expectState("maintenance-1", model.StateCompleted)
		if inst.Owner != "owner" {
			t.Errorf("a message start runs as the deployment owner (SEC-12), got %v", inst.Owner)
		}
		h.expectVars(inst.Id, map[string]any{"p": 1.0, "started": true})
		again := h.correlate(model.MessageInput{Name: "maintenance", InstanceId: "maintenance-1", Variables: map[string]any{"p": 1}, MessageId: "m1"})
		if len(again.Correlations) != 1 || !again.Correlations[0].Duplicate {
			t.Fatalf("unexpected %+v", again)
		}
	})
}

func TestTimerStartFiresOncePerCycleAndSkipsMissedCycles(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(b.StartTimer("hourly", "timeCycle", "0 0 * * * ?"), b.End("end"), b.Flow("hourly", "end")))
		count := func() int {
			var page model.Page[model.Instance]
			if err := h.s.Tx(ctx, func(tx *storeTx) (err error) {
				page, err = tx.ListInstances("processes", model.InstanceListOptions{DeploymentId: d}, adminAccess)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			for _, i := range page.Items {
				if i.Owner != "owner" {
					t.Errorf("timer starts run as the deployment owner, got %v", i.Owner)
				}
			}
			return page.Total
		}
		h.advance(29 * time.Minute) // 10:59
		if n := count(); n != 0 {
			t.Fatalf("started too early: %v", n)
		}
		h.advance(time.Minute) // 11:00
		h.advance(0)
		if n := count(); n != 1 {
			t.Fatalf("expected one start, got %v", n)
		}
		h.advance(5 * time.Hour) // 16:00, four cycles missed
		h.advance(0)
		if n := count(); n != 2 {
			t.Fatalf("missed cycles must fire once (LONG-12), got %v starts", n)
		}
	})
}
