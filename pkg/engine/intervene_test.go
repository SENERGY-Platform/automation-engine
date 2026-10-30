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
	"strings"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

func TestTriggerWaitFiresATimerBeforeItIsDue(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(b.Start("start"), b.CatchTimer("wait", "timeDuration", "PT1H"), b.End("end"), b.Flow("start", "wait"), b.Flow("wait", "end")))
		inst := h.start(d, nil)
		if err := h.e.TriggerWait(ctx, inst.Id, "wait", map[string]any{"ignored": 1}); err != nil {
			t.Fatal(err)
		}
		h.expectState(inst.Id, model.StateCompleted)
		fired := h.activities(inst.Id, model.ActivityTimerFired)
		if len(fired) != 1 || fired[0].ElementId != "wait" || fired[0].Data["manual"] != true || fired[0].Data["definition"] == "" {
			t.Fatalf("the history marks the timer as fired by hand: %+v", fired)
		}
		h.advance(2 * time.Hour)
		if fired = h.activities(inst.Id, model.ActivityTimerFired); len(fired) != 1 {
			t.Fatalf("the timer must not fire again when it would have been due: %+v", fired)
		}
	})
}

func TestTriggerWaitCorrelatesADeviceEventWithoutItsCondition(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(b.Start("start"), b.CatchDeviceEvent("wait", b.OutScript("received", "event")), b.End("end"), b.Flow("start", "wait"), b.Flow("wait", "end")))
		inst := h.start(d, nil)
		if err := h.e.TriggerWait(ctx, inst.Id, "wait", map[string]any{"event": 41}); err != nil {
			t.Fatal(err)
		}
		h.expectState(inst.Id, model.StateCompleted)
		h.expectVars(inst.Id, map[string]any{"received": 41})
		received := h.activities(inst.Id, model.ActivityMessageReceived)
		if len(received) != 1 || received[0].Data["manual"] != true || received[0].Data["message_name"] == "" || received[0].Data["local_variables"] == nil {
			t.Fatalf("the history marks the correlation as done by hand: %+v", received)
		}
	})
}

func TestTriggerWaitTakesOneEventOfAnEventBasedGateway(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(
			b.Message("m", "go"), b.Start("start"), b.Gateway("eventBased", "choice", ""),
			b.CatchMessage("message", "m"), b.CatchTimer("timer", "timeDuration", "PT1H"),
			b.Script("viaMessage", "path", `"message"`), b.Script("viaTimer", "path", `"timer"`), b.End("end"),
			b.Flow("start", "choice"), b.Flow("choice", "message"), b.Flow("choice", "timer"),
			b.Flow("message", "viaMessage"), b.Flow("timer", "viaTimer"), b.Flow("viaMessage", "end"), b.Flow("viaTimer", "end"),
		))
		inst := h.start(d, nil)
		if err := h.e.TriggerWait(ctx, inst.Id, "timer", nil); err != nil {
			t.Fatal(err)
		}
		h.expectState(inst.Id, model.StateCompleted)
		h.expectVars(inst.Id, map[string]any{"path": "timer"})
		if counts := h.runtime(inst.Id); counts.subscriptions != 0 {
			t.Fatalf("the other event of the gateway is cancelled: %+v", counts)
		}
	})
}

func TestTriggerWaitRefusesWhatDoesNotWait(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(b.Message("m", "go"), b.Start("start"), b.CatchMessage("wait", "m"), b.End("end"), b.Flow("start", "wait"), b.Flow("wait", "end")))
		inst := h.start(d, nil)
		if err := h.e.TriggerWait(ctx, inst.Id, "end", nil); !errors.Is(err, model.ErrConflict) || !strings.Contains(err.Error(), "end") {
			t.Fatalf("an element without a waiting event: %v", err)
		}
		if err := h.e.TriggerWait(ctx, inst.Id, "wait", nil); err != nil {
			t.Fatal(err)
		}
		if err := h.e.TriggerWait(ctx, inst.Id, "wait", nil); !errors.Is(err, model.ErrConflict) {
			t.Fatalf("a finished instance: %v", err)
		}
		if err := h.e.TriggerWait(ctx, "unknown", "wait", nil); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("an unknown instance: %v", err)
		}
	})
}

func TestActivitySearchFindsVariableValues(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(b.Message("m", "go"), b.Start("start"), b.CatchMessage("wait", "m"), b.End("end"), b.Flow("start", "wait"), b.Flow("wait", "end")))
		inst := h.start(d, map[string]any{"mode": "Heating"})
		for _, v := range []struct {
			name  string
			value any
		}{{"level", 21.5}, {"mode", "cooling"}, {"level", 19}, {"settings", map[string]any{"target": 21}}, {"long", strings.Repeat("x", 5000) + "needle"}} {
			if err := h.e.SetVariable(ctx, inst.Id, v.name, v.value); err != nil {
				t.Fatal(err)
			}
		}
		if err := h.e.DeleteVariable(ctx, inst.Id, "mode"); err != nil {
			t.Fatal(err)
		}
		search := func(options model.ActivityListOptions) (result []string) {
			t.Helper()
			var found []model.Activity
			err := h.s.Tx(ctx, func(tx *store.Tx) (err error) {
				found, err = tx.ListActivities(inst.Id, options)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, a := range found {
				result = append(result, string(a.Kind)+" "+a.Data["name"].(string))
				if !options.Matches(a) {
					t.Errorf("the store returned an entry the options do not match: %+v", a)
				}
			}
			return result
		}
		expect := func(what string, got []string, want ...string) {
			t.Helper()
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("%v: expected %v, got %v", what, want, got)
			}
		}
		expect("all entries of a variable", search(model.ActivityListOptions{Variable: "mode"}), "variable_set mode", "variable_set mode", "variable_deleted mode")
		expect("text ignoring case", search(model.ActivityListOptions{Value: "heat"}), "variable_set mode")
		expect("numbers as JSON", search(model.ActivityListOptions{Value: "21"}), "variable_set level", "variable_set settings")
		expect("objects as compact JSON", search(model.ActivityListOptions{Value: `"target":21`}), "variable_set settings")
		expect("variable and value", search(model.ActivityListOptions{Variable: "level", Value: "19"}), "variable_set level")
		expect("a value cut in the log is searched in its preview", search(model.ActivityListOptions{Value: "xxxx"}), "variable_set long")
		expect("the limit counts matches", search(model.ActivityListOptions{Variable: "level", Limit: 1}), "variable_set level")
		expect("other filters still apply", search(model.ActivityListOptions{Variable: "level", Kind: string(model.ActivityVariableDeleted)}))
	})
}
