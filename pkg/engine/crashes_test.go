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
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/crashbrake"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

const crash = "fatal error: stack overflow"

func (this *harness) faultySince(deploymentId string) *time.Time {
	this.t.Helper()
	var stats store.RestartStats
	err := this.s.Tx(ctx, func(tx *store.Tx) (err error) {
		stats, err = tx.GetRestartStats(deploymentId)
		return err
	})
	if err != nil {
		this.t.Fatal(err)
	}
	return stats.FaultySince
}

func (this *harness) crashNotified() bool {
	this.t.Helper()
	for _, n := range this.notifier.messages() {
		if strings.HasPrefix(n.Title, "Script stopped after it ended the engine") && strings.Contains(n.Message, crash) {
			return true
		}
	}
	return false
}

// A script of an instance that ended the process stops the instance with an incident instead of
// running again; automatic restarts after incidents stay paused until the deployment is reset.
func TestCrashOfAnInstanceScriptBecomesAnIncident(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deployWith("processes", b.Process(b.Message("m", "go"), b.Start("start"), b.CatchMessage("wait", "m"), b.Script("boom", "x", "1"), b.End("end"),
			b.Flow("start", "wait"), b.Flow("wait", "boom"), b.Flow("boom", "end")), model.IncidentHandling{Restart: true, Notify: true})
		inst := h.start(d, nil)
		remaining := h.e.ApplyCrashDecisions(ctx, []crashbrake.Decision{{Unit: crashbrake.Unit{Kind: crashbrake.KindInstance, Id: inst.Id, Element: "boom"}, Error: crash}})
		if len(remaining) != 0 {
			t.Fatalf("not applied: %+v", remaining)
		}
		h.expectState(inst.Id, model.StateIncident)
		incidents := h.incidents(inst.Id)
		if len(incidents) != 1 || incidents[0].ElementId != "boom" || !strings.Contains(incidents[0].Message, crash) {
			t.Fatalf("an incident at the element names the crash: %+v", incidents)
		}
		if h.faultySince(d) == nil {
			t.Fatal("the deployment is marked faulty")
		}
		h.advance(time.Hour)
		h.expectState(inst.Id, model.StateIncident)
		if !h.crashNotified() {
			t.Errorf("the owner learns of it: %+v", h.notifier.messages())
		}
		// decided twice, e.g. retried after a failed start: nothing more happens
		if remaining = h.e.ApplyCrashDecisions(ctx, []crashbrake.Decision{{Unit: crashbrake.Unit{Kind: crashbrake.KindInstance, Id: inst.Id, Element: "boom"}, Error: crash}}); len(remaining) != 0 {
			t.Fatalf("%+v", remaining)
		}
		if len(h.incidents(inst.Id)) != 1 {
			t.Error("an ended instance gets no second incident")
		}
	})
}

// A condition that ended the process is suspended, and the instances waiting for it get an
// incident with the crash; a reset of the deployment evaluates it again.
func TestCrashOfAnEventConditionSuspendsIt(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(b.Start("start"), b.CatchDeviceEvent("wait"), b.End("end"), b.Flow("start", "wait"), b.Flow("wait", "end")))
		description := model.EventDescription{Id: "e1", DeploymentId: d, Version: 1, ElementId: "wait", Namespace: "processes", Owner: "owner",
			MessageName: EventMessageName(d, "wait"), DeviceId: "dev", ServiceId: "svc", Topic: "t", Script: "x == 42", ValueVariable: "x"}
		err := h.s.Tx(ctx, func(tx *store.Tx) error {
			return tx.ReplaceEventDescriptions(d, 1, []model.EventDescription{description}, nil, h.now())
		})
		if err != nil {
			t.Fatal(err)
		}
		first, second := h.start(d, nil), h.start(d, nil)
		if remaining := h.e.ApplyCrashDecisions(ctx, []crashbrake.Decision{{Unit: crashbrake.Unit{Kind: crashbrake.KindEvent, Id: "e1"}, Error: crash}}); len(remaining) != 0 {
			t.Fatalf("not applied: %+v", remaining)
		}
		for _, inst := range []model.Instance{first, second} {
			h.expectState(inst.Id, model.StateIncident)
			if incidents := h.incidents(inst.Id); len(incidents) != 1 || incidents[0].ElementId != "wait" || !strings.Contains(incidents[0].Message, crash) {
				t.Fatalf("the waiting instance has an incident with the crash: %+v", incidents)
			}
		}
		suspended := func() string {
			var list []model.EventDescription
			err := h.s.Tx(ctx, func(tx *store.Tx) (err error) {
				list, err = tx.ListEventDescriptions(d, 0)
				return err
			})
			if err != nil || len(list) != 1 {
				t.Fatal(list, err)
			}
			return list[0].Suspended
		}
		if suspended() != crash || h.faultySince(d) == nil {
			t.Fatalf("suspended %q, faulty %v", suspended(), h.faultySince(d))
		}
		h.process()
		if !h.crashNotified() {
			t.Errorf("the owner learns of it: %+v", h.notifier.messages())
		}
		// an instance that reaches the condition later would wait for ever
		third := h.start(d, nil)
		h.expectState(third.Id, model.StateIncident)
		if incidents := h.incidents(third.Id); len(incidents) != 1 || incidents[0].ElementId != "wait" || !strings.Contains(incidents[0].Message, crash) {
			t.Fatalf("a new instance at the suspended condition has an incident: %+v", incidents)
		}
		err = h.s.Tx(ctx, func(tx *store.Tx) error {
			return h.e.ResetRestarts(tx, d)
		})
		if err != nil || suspended() != "" || h.faultySince(d) != nil {
			t.Fatalf("a reset evaluates the condition again: %q %v", suspended(), err)
		}
		h.expectState(h.start(d, nil).Id, model.StateRunning)
	})
}

// deployAs stores a version of a deployment with a known id, as the API or a gateway would, in one
// transaction with its activation
func (this *harness) deployAs(id string, version int, xml string) error {
	return this.s.Tx(ctx, func(tx *store.Tx) error {
		now := this.now()
		d := model.Deployment{Id: id, Namespace: "processes", Name: "test", Owner: "owner", Version: version, CreatedAt: now, UpdatedAt: now}
		if version == 1 {
			if err := tx.InsertDeployment(d); err != nil {
				return err
			}
		} else if err := tx.UpdateDeployment(d); err != nil {
			return err
		}
		if err := tx.InsertDeploymentVersion(model.DeploymentVersion{DeploymentId: id, Version: version, Xml: xml, CreatedAt: now}); err != nil {
			return err
		}
		return this.e.ActivateVersion(tx, id, version)
	})
}

func (this *harness) deployment(id string) model.Deployment {
	this.t.Helper()
	var result model.Deployment
	err := this.s.Tx(ctx, func(tx *store.Tx) (err error) {
		result, err = tx.GetDeployment(id, false)
		return err
	})
	if err != nil {
		this.t.Fatal(err)
	}
	return result
}

func (this *harness) startTimerDue(deploymentId string, elementId string) *time.Time {
	this.t.Helper()
	var event store.StartEvent
	err := this.s.Tx(ctx, func(tx *store.Tx) (err error) {
		event, err = tx.GetStartEvent(deploymentId, elementId)
		return err
	})
	if err != nil {
		this.t.Fatal(err)
	}
	return event.DueAt
}

func timerModel(expression string) string {
	return b.Process(b.StartTimerExpression("start", "timeCycle", expression), b.End("end"), b.Flow("start", "end"))
}

// A start timer expression that ended the process quarantines its deployment version, also before
// the deployment exists, as when a gateway applied it: the deployment is disabled and does not
// evaluate the expression again, until a newer version or a reset.
func TestCrashOfAStartTimerQuarantinesTheDeployment(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		// the crash rolled back the creation of the deployment
		if remaining := h.e.ApplyCrashDecisions(ctx, []crashbrake.Decision{{Unit: crashbrake.Unit{Kind: crashbrake.KindDeployment, Id: "d1", Element: "start", Version: 1}, Error: crash}}); len(remaining) != 0 {
			t.Fatalf("not applied: %+v", remaining)
		}
		// applied again; the expression would fail if it were evaluated
		if err := h.deployAs("d1", 1, timerModel(`undefinedFunction()`)); err != nil {
			t.Fatalf("activated without evaluating the expression: %v", err)
		}
		if !h.deployment("d1").Disabled || h.faultySince("d1") == nil || h.startTimerDue("d1", "start") != nil {
			t.Fatalf("disabled, faulty, no due start: %+v", h.deployment("d1"))
		}
		h.process()
		if !h.crashNotified() {
			t.Errorf("the owner learns of it: %+v", h.notifier.messages())
		}
		if _, _, err := h.e.StartInstance(ctx, StartRequest{DeploymentId: "d1", Owner: "user"}); err == nil {
			t.Fatal("a quarantined deployment starts nothing")
		}
		// a newer version may have fixed the expression
		if err := h.deployAs("d1", 2, timerModel(`"0 0 * * * ?"`)); err != nil {
			t.Fatal(err)
		}
		if h.deployment("d1").Disabled || h.startTimerDue("d1", "start") == nil {
			t.Fatalf("a newer version lifts the quarantine: %+v", h.deployment("d1"))
		}

		// a deployment that exists is disabled at once, and a reset lifts it
		d := h.deploy(timerModel(`"0 0 * * * ?"`))
		h.e.ApplyCrashDecisions(ctx, []crashbrake.Decision{{Unit: crashbrake.Unit{Kind: crashbrake.KindDeployment, Id: d, Element: "start", Version: 1}, Error: crash}})
		if !h.deployment(d).Disabled || h.faultySince(d) == nil {
			t.Fatal("the deployment is disabled and faulty")
		}
		// a reset of the restarts alone, as an update makes it, keeps the quarantine; lifting it
		// enables the deployment
		err := h.s.Tx(ctx, func(tx *store.Tx) error {
			return h.e.ResetRestarts(tx, d)
		})
		if err != nil || !h.deployment(d).Disabled || h.faultySince(d) != nil {
			t.Fatalf("the restarts are reset, the quarantine stays: %v", err)
		}
		err = h.s.Tx(ctx, func(tx *store.Tx) error {
			lifted, err := h.e.LiftQuarantine(tx, d)
			if err == nil && !lifted {
				err = errors.New("nothing lifted")
			}
			return err
		})
		if err != nil || h.deployment(d).Disabled {
			t.Fatalf("lifted, the deployment is enabled: %v", err)
		}
		// units that are gone are applied as nothing to do; a module script of a deleted instance,
		// which has no version, does not quarantine its deployment
		gone := h.e.ApplyCrashDecisions(ctx, []crashbrake.Decision{
			{Unit: crashbrake.Unit{Kind: crashbrake.KindInstance, Id: "gone", Element: "x", Deployment: d}},
			{Unit: crashbrake.Unit{Kind: crashbrake.KindEvent, Id: "gone"}},
		})
		if len(gone) != 0 || h.deployment(d).Disabled {
			t.Fatalf("%+v disabled %v", gone, h.deployment(d).Disabled)
		}
	})
}

// A gateway applies the update to the version whose timer expression ended the process again,
// and resets the restarts before, as for every update: the quarantine of that version holds.
func TestQuarantineHoldsWhenTheSameVersionIsAppliedAgain(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		if err := h.deployAs("d1", 1, timerModel(`"0 0 * * * ?"`)); err != nil {
			t.Fatal(err)
		}
		// the crash rolled back the activation of version 2
		h.e.ApplyCrashDecisions(ctx, []crashbrake.Decision{{Unit: crashbrake.Unit{Kind: crashbrake.KindDeployment, Id: "d1", Element: "start", Version: 2}, Error: crash}})
		if !h.deployment("d1").Disabled {
			t.Fatal("the deployment is disabled")
		}
		err := h.s.Tx(ctx, func(tx *store.Tx) error {
			return h.e.ResetRestarts(tx, "d1")
		})
		if err != nil {
			t.Fatal(err)
		}
		// the expression would fail if it were evaluated
		if err = h.deployAs("d1", 2, timerModel(`undefinedFunction()`)); err != nil {
			t.Fatalf("version 2 applied again without evaluating the expression: %v", err)
		}
		if !h.deployment("d1").Disabled || h.startTimerDue("d1", "start") != nil {
			t.Fatal("version 2 stays quarantined")
		}
	})
}

// A crash in the automatic restart of an instance after an incident rolled the restart back,
// which would run again; it is cancelled.
func TestCrashOfAnAutomaticRestartCancelsIt(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deployWith("processes", b.Process(b.Start("start"), b.Script("boom", "x", `throw new Error("plain")`), b.End("end"),
			b.Flow("start", "boom"), b.Flow("boom", "end")), model.IncidentHandling{Restart: true})
		inst := h.start(d, nil)
		if h.instance(inst.Id).RestartAt == nil {
			t.Fatal("a restart is scheduled after the incident")
		}
		h.e.ApplyCrashDecisions(ctx, []crashbrake.Decision{{Unit: crashbrake.Unit{Kind: crashbrake.KindInstance, Id: inst.Id, Element: "boom", Deployment: d, Version: 1}, Error: crash}})
		if h.instance(inst.Id).RestartAt != nil || h.faultySince(d) == nil {
			t.Fatal("the restart is cancelled and the deployment faulty")
		}
		h.advance(24 * time.Hour)
		h.expectState(inst.Id, model.StateIncident)
		if len(h.incidents(inst.Id)) != 1 {
			t.Errorf("no further run: %+v", h.incidents(inst.Id))
		}
		if !h.crashNotified() {
			t.Errorf("the owner learns of it: %+v", h.notifier.messages())
		}
	})
}

const crashingScript = `var o = {}; o.toString = String.prototype.trim; String(o)`

// TestCrashChild is the engine that dies; it runs only when started by the test below.
func TestCrashChild(t *testing.T) {
	dir := os.Getenv("ENGINE_CRASH_DIR")
	if dir == "" {
		t.Skip("started by TestScriptThatEndsTheEngineRunsOnlyOnce")
	}
	s, err := store.Open(ctx, store.SQLite, "file:"+filepath.Join(dir, "engine.db"))
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, s)
	brake, _, err := crashbrake.Open(filepath.Join(dir, "brake"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h.e.SetCrashBrake(brake)
	switch os.Getenv("ENGINE_CRASH_MODE") {
	case "waiting instance":
		d := h.deploy(b.Process(b.Message("m", "go"), b.Start("start"), b.CatchMessage("wait", "m"), b.Script("boom", "x", crashingScript), b.End("end"),
			b.Flow("start", "wait"), b.Flow("wait", "boom"), b.Flow("boom", "end")))
		inst := h.start(d, nil)
		if err = os.WriteFile(filepath.Join(dir, "id"), []byte(inst.Id), 0o644); err != nil {
			t.Fatal(err)
		}
		// the message, e.g. from Kafka, is not committed and comes again after the restart
		h.correlate(model.MessageInput{Name: "go", InstanceId: inst.Id})
	case "message start":
		d := h.deploy(b.Process(b.Message("m", "go"), b.StartMessage("start", "m"), b.Script("boom", "x", crashingScript), b.End("end"),
			b.Flow("start", "boom"), b.Flow("boom", "end")))
		if err = os.WriteFile(filepath.Join(dir, "id"), []byte(d), 0o644); err != nil {
			t.Fatal(err)
		}
		// the crash rolls back the instance the message started
		h.correlate(model.MessageInput{Name: "go"})
	case "timer expression":
		// the crash rolls back the deployment, as when a gateway applies it
		_ = h.deployAs("d1", 1, timerModel(crashingScript))
	}
	t.Fatal("goja returned instead of ending the process")
}

func crashEngine(t *testing.T, mode string) (h *harness, id string, decisions []crashbrake.Decision) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the crash brake needs mmap on linux")
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run", "^TestCrashChild$", "-test.v")
	cmd.Env = append(os.Environ(), "ENGINE_CRASH_DIR="+dir, "ENGINE_CRASH_MODE="+mode)
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), crash) {
		t.Fatalf("the child did not end with a stack overflow: %v\n%s", err, out)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "id"))
	s, err := store.Open(ctx, store.SQLite, "file:"+filepath.Join(dir, "engine.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	h = newHarness(t, s)
	brake, decisions, err := crashbrake.Open(filepath.Join(dir, "brake"), nil)
	if err != nil {
		t.Fatal(err)
	}
	brake.Applied(h.e.ApplyCrashDecisions(ctx, decisions))
	brake.Close()
	if _, again, err := crashbrake.Open(filepath.Join(dir, "brake"), nil); err != nil || len(again) != 0 {
		t.Fatalf("applied, the decision is gone: %+v %v", again, err)
	}
	return h, string(raw), decisions
}

// A script that ends the engine with a fatal stack overflow is stopped at the next start, so that
// what triggered it does not end the engine again; the test process would die if it did.
func TestScriptThatEndsTheEngineRunsOnlyOnce(t *testing.T) {
	t.Run("an instance waiting for a message", func(t *testing.T) {
		h, instanceId, decisions := crashEngine(t, "waiting instance")
		if len(decisions) != 1 || decisions[0].Unit.Kind != crashbrake.KindInstance || decisions[0].Unit.Id != instanceId || decisions[0].Unit.Element != "boom" {
			t.Fatalf("the crashing script is named: %+v", decisions)
		}
		h.expectState(instanceId, model.StateIncident)
		if result := h.correlate(model.MessageInput{Name: "go", All: true}); len(result.Correlations) != 0 {
			t.Fatalf("the message again finds no instance: %+v", result)
		}
	})
	t.Run("an instance started by a message", func(t *testing.T) {
		h, deploymentId, decisions := crashEngine(t, "message start")
		if len(decisions) != 1 || decisions[0].Unit.Deployment != deploymentId || decisions[0].Unit.Version != 1 {
			t.Fatalf("the deployment of the rolled back instance is named: %+v", decisions)
		}
		if !h.deployment(deploymentId).Disabled {
			t.Fatal("the deployment is disabled")
		}
		h.correlate(model.MessageInput{Name: "go"})
	})
	t.Run("a start timer expression", func(t *testing.T) {
		h, _, decisions := crashEngine(t, "timer expression")
		if len(decisions) != 1 || decisions[0].Unit != (crashbrake.Unit{Kind: crashbrake.KindDeployment, Id: "d1", Element: "start", Version: 1}) {
			t.Fatalf("the deployment version is named: %+v", decisions)
		}
		if err := h.deployAs("d1", 1, timerModel(crashingScript)); err != nil {
			t.Fatal(err)
		}
		if !h.deployment("d1").Disabled {
			t.Fatal("applied again, the deployment is disabled")
		}
	})
}
