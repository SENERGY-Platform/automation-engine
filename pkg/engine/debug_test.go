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
	"reflect"
	"sort"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/debug"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// essence reduces a debug state to what both the runtime state and the activity log know
func essence(s model.DebugState) map[string]any {
	active := map[string]int{}
	for _, a := range s.Active {
		active[a.ElementId] = a.Tokens
	}
	waits := []string{}
	for _, w := range s.Waits {
		waits = append(waits, fmt.Sprintf("%v/%v/%v", w.ElementId, w.Kind, w.GatewayId))
	}
	sort.Strings(waits)
	variables := map[string]any{}
	for name, v := range s.Variables {
		variables[name] = v.Value
	}
	elements := map[string]int64{}
	for _, e := range s.Elements {
		elements[e.ElementId] = e.Count
	}
	incidents := []string{}
	for _, i := range s.Incidents {
		incidents = append(incidents, i.Id)
	}
	return map[string]any{"state": s.Instance.State, "run": s.Instance.Run, "version": s.Instance.DeploymentVersion, "active": active,
		"waits": waits, "variables": variables, "elements": elements, "incidents": incidents}
}

func (this *harness) live(id string) (result model.DebugState) {
	this.t.Helper()
	err := this.s.Tx(ctx, func(tx *store.Tx) error {
		inst, err := tx.GetInstance(id, false)
		if err != nil {
			return err
		}
		proc, err := this.e.process(tx, inst.DeploymentId, inst.DeploymentVersion)
		if err != nil {
			return err
		}
		result, err = debug.Live(tx, inst, proc)
		return err
	})
	if err != nil {
		this.t.Fatal(err)
	}
	return result
}

func (this *harness) at(id string, seq int64) (result model.DebugState, err error) {
	err = this.s.Tx(ctx, func(tx *store.Tx) error {
		inst, err := tx.GetInstance(id, false)
		if err != nil {
			return err
		}
		result, err = debug.At(tx, inst, seq)
		return err
	})
	return result, err
}

// expectReplay checks that the history reconstructs the current state (DBG-13)
func (this *harness) expectReplay(id string, step string) {
	this.t.Helper()
	live := this.live(id)
	replayed, err := this.at(id, 0)
	if err != nil {
		this.t.Fatalf("%v: %v", step, err)
	}
	if want, got := essence(live), essence(replayed); !reflect.DeepEqual(want, got) {
		this.t.Fatalf("%v: replay differs from the live state\nlive:   %v\nreplay: %v", step, want, got)
	}
	if replayed.Seq != live.Seq || !replayed.Reconstructed {
		this.t.Fatalf("%v: seq %v, want %v", step, replayed.Seq, live.Seq)
	}
}

func TestHistoryReconstructsTheLiveState(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		h.e.RegisterTaskType("echo", TaskTypeFunc(func(ctx context.Context, task Task) (TaskResult, error) {
			return TaskResult{Output: map[string]any{"result": []any{task.Input["in"]}}}, nil
		}))
		parallel := h.deploy(b.Process(
			b.Message("m", "door"),
			b.Start("start"), b.Gateway("parallel", "fork", ""), b.Gateway("parallel", "join", ""),
			b.Service("task", "echo", b.InScript("in", "x"), b.OutScript("out", "result[0]")),
			b.CatchMessage("wait", "m", b.OutScript("opened", "event")), b.End("end"),
			b.Flow("start", "fork"), b.Flow("fork", "task"), b.Flow("fork", "wait"), b.Flow("task", "join"), b.Flow("wait", "join"), b.Flow("join", "end"),
		))
		inst := h.start(parallel, map[string]any{"x": 7})
		h.expectReplay(inst.Id, "task pending, message waiting")
		h.process()
		h.expectReplay(inst.Id, "task done, join waiting")
		h.correlate(model.MessageInput{Name: "door", All: true, LocalVariables: map[string]any{"event": "open"}})
		h.expectState(inst.Id, model.StateCompleted)
		h.expectReplay(inst.Id, "completed")

		gateway := h.deploy(b.Process(
			b.Message("m", "bell"),
			b.Start("start"), b.Gateway("eventBased", "ebg", ""),
			b.CatchMessage("msg", "m"), b.CatchTimer("timeout", "timeDuration", "PT1M"), b.End("end"),
			b.Flow("start", "ebg"), b.Flow("ebg", "msg"), b.Flow("ebg", "timeout"), b.Flow("msg", "end"), b.Flow("timeout", "end"),
		))
		waiting := h.start(gateway, nil)
		h.expectReplay(waiting.Id, "waiting at the event-based gateway")
		if waits := h.live(waiting.Id).Waits; len(waits) != 2 || waits[0].GatewayId != "ebg" {
			t.Fatalf("both events wait for the gateway: %+v", waits)
		}
		h.advance(time.Minute)
		h.expectState(waiting.Id, model.StateCompleted)
		h.expectReplay(waiting.Id, "timer fired, message cancelled")

		failing := h.deploy(b.Process(b.Start("start"), b.Script("broken", "y", "undefinedFunction()"), b.End("end"), b.Flow("start", "broken"), b.Flow("broken", "end")))
		broken := h.start(failing, nil)
		h.expectState(broken.Id, model.StateIncident)
		h.expectReplay(broken.Id, "incident")
		if _, err := h.e.RestartInstance(ctx, broken.Id, 0, model.CauseRestart); err != nil {
			t.Fatal(err)
		}
		h.expectReplay(broken.Id, "restarted")
	})
}

func TestHistoryStepsSurviveTheEvictionOfTheirBeginning(t *testing.T) {
	storeRun(t, func(c *Config) { c.MaxActivities = 120; c.SnapshotsPerLog = 20 }, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(
			b.Message("m", "tick"),
			b.Start("start"), b.Gateway("exclusive", "loop", ""), b.CatchMessage("wait", "m", b.OutScript("value", "event")),
			b.Script("count", "n", "typeof n == 'undefined' ? 1 : n + 1"),
			b.Flow("start", "loop"), b.Flow("loop", "wait"), b.Flow("wait", "count"), b.Flow("count", "loop"),
		))
		inst := h.start(d, nil)
		steps := map[int64]map[string]any{}
		for i := range 60 {
			h.correlate(model.MessageInput{Name: "tick", All: true, LocalVariables: map[string]any{"event": float64(i)}})
			live := h.live(inst.Id)
			steps[live.Seq] = essence(live)
		}
		current := h.instance(inst.Id)
		if current.ActivityMinSeq <= 1 {
			t.Fatalf("the test needs evicted entries: %+v", current)
		}
		first, err := h.at(inst.Id, 0)
		if err != nil {
			t.Fatal(err)
		}
		if first.FirstSeq < current.ActivityMinSeq-1 || !first.Truncated {
			t.Fatalf("the earliest step is the oldest usable snapshot: first %v, min seq %v", first.FirstSeq, current.ActivityMinSeq)
		}
		checked := 0
		for seq, want := range steps {
			got, err := h.at(inst.Id, seq)
			if seq < first.FirstSeq {
				if !errors.Is(err, model.ErrNotFound) {
					t.Fatalf("step %v before the earliest %v must be unavailable, got %v", seq, first.FirstSeq, err)
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want, essence(got)) {
				t.Fatalf("step %v:\nlive:   %v\nreplay: %v", seq, want, essence(got))
			}
			checked++
		}
		// a ring of 120 entries holds about 11 iterations; snapshots come every 50 entries at the
		// least, so the first usable one costs up to 50 of them
		if checked < 5 {
			t.Fatalf("only %v steps were reconstructable", checked)
		}
	})
}
