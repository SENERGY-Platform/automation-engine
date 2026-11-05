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
	"bytes"
	"context"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/crashbrake"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/script"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/google/uuid"
)

// a copy of crashingScript that differs in // comments, which both fingerprints ignore
const crashingCopy = "// a copy\n" + crashingScript + " // harmless"

// waitThenRun waits for message go, optionally for a timer before, and then runs src
func waitThenRun(src string, delay bool) string {
	parts := []string{b.Message("m", "go"), b.Start("start"), b.CatchMessage("wait", "m"), b.Script("boom", "x", src), b.End("end")}
	if delay {
		parts = append(parts, b.CatchTimer("delay", "timeDuration", "PT1H"), b.Flow("start", "delay"), b.Flow("delay", "wait"))
	} else {
		parts = append(parts, b.Flow("start", "wait"))
	}
	return b.Process(append(parts, b.Flow("wait", "boom"), b.Flow("boom", "end"))...)
}

// deployManaged stores a deployment that an instance manages through a module, as a smart service
// creates it with process_deployment
func (this *harness) deployManaged(managedBy string, owner string, xml string) string {
	this.t.Helper()
	return this.insert(model.Deployment{Owner: owner, ManagedBy: managedBy}, xml)
}

// insert stores version 1 of a deployment with the fields of d that a deploy through the engine
// does not set, e.g. the gateway, and activates it
func (this *harness) insert(d model.Deployment, xml string) string {
	this.t.Helper()
	d.Id, d.Namespace, d.Name, d.Version = uuid.NewString(), "processes", "managed", 1
	if d.Owner == "" {
		d.Owner = "owner"
	}
	err := this.s.Tx(ctx, func(tx *store.Tx) error {
		d.CreatedAt, d.UpdatedAt = this.now(), this.now()
		if err := tx.InsertDeployment(d); err != nil {
			return err
		}
		if err := tx.InsertDeploymentVersion(model.DeploymentVersion{DeploymentId: d.Id, Version: 1, Xml: xml, CreatedAt: d.CreatedAt}); err != nil {
			return err
		}
		return this.e.ActivateVersion(tx, d.Id, 1)
	})
	if err != nil {
		this.t.Fatal(err)
	}
	return d.Id
}

// addVersion activates a new version of a deployment, which keeps its other fields
func (this *harness) addVersion(id string, xml string) {
	this.t.Helper()
	err := this.s.Tx(ctx, func(tx *store.Tx) error {
		d, err := tx.GetDeployment(id, true)
		if err != nil {
			return err
		}
		d.Version++
		d.UpdatedAt = this.now()
		if err = tx.UpdateDeployment(d); err != nil {
			return err
		}
		if err = tx.InsertDeploymentVersion(model.DeploymentVersion{DeploymentId: id, Version: d.Version, Xml: xml, CreatedAt: d.UpdatedAt}); err != nil {
			return err
		}
		return this.e.ActivateVersion(tx, id, d.Version)
	})
	if err != nil {
		this.t.Fatal(err)
	}
}

// describeEvent stores the description of a device event with a condition
func (this *harness) describeEvent(deploymentId string, id string, condition string) {
	this.t.Helper()
	err := this.s.Tx(ctx, func(tx *store.Tx) error {
		return tx.ReplaceEventDescriptions(deploymentId, 1, []model.EventDescription{{Id: id, DeploymentId: deploymentId, Version: 1, ElementId: "wait", Namespace: "processes", Owner: "owner",
			MessageName: EventMessageName(deploymentId, "wait"), Topic: "t", Script: condition, ValueVariable: "x"}}, nil, this.now())
	})
	if err != nil {
		this.t.Fatal(err)
	}
}

// waitForEvent waits for a device event
var waitForEvent = b.Process(b.Start("start"), b.CatchDeviceEvent("wait"), b.End("end"), b.Flow("start", "wait"), b.Flow("wait", "end"))

func (this *harness) blockedScripts() []model.BlockedScript {
	this.t.Helper()
	var result []model.BlockedScript
	err := this.s.Tx(ctx, func(tx *store.Tx) (err error) {
		result, err = tx.ListBlockedScripts()
		return err
	})
	if err != nil {
		this.t.Fatal(err)
	}
	return result
}

func (this *harness) block(src string) {
	this.t.Helper()
	err := this.s.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.BlockScript(model.BlockedScript{Fingerprint: script.Fingerprint(src), Source: src, Reason: crash, Origin: "test", CreatedAt: this.now()})
		return err
	})
	if err != nil {
		this.t.Fatal(err)
	}
	if _, err = this.e.LoadBlocklist(ctx); err != nil {
		this.t.Fatal(err)
	}
}

// An instance that reaches a blocked script, also a copy formatted differently, gets an incident
// instead of running it.
func TestBlockedScriptBecomesAnIncident(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		inst := h.start(h.deploy(waitThenRun(crashingCopy, false)), nil)
		h.block(crashingScript)
		h.correlate(model.MessageInput{Name: "go", InstanceId: inst.Id})
		h.expectState(inst.Id, model.StateIncident)
		if incidents := h.incidents(inst.Id); len(incidents) != 1 || incidents[0].ElementId != "boom" || !strings.Contains(incidents[0].Message, "blocked") {
			t.Fatalf("%+v", incidents)
		}
		if _, issues, err := bpmn.ParseAndValidate(waitThenRun(crashingCopy, false), h.e.Rules(model.Namespace{Name: "processes"})); err != nil || len(issues) != 1 || issues[0].ElementId != "boom" {
			t.Fatalf("a model with the script is refused: %+v %v", issues, err)
		}
	})
}

// A crash puts the script on the blocklist and stops the smart service instance that deployed it,
// with every deployment it manages and their running instances.
func TestCrashBlocksTheScriptAndStopsTheOriginator(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		root := h.start(h.deploy(b.Process(b.Message("r", "never"), b.Start("start"), b.CatchMessage("wait", "r"), b.Flow("start", "wait"))), nil)
		managed := []string{h.deployManaged(root.Id, "owner", waitThenRun(crashingScript, false)), h.deployManaged(root.Id, "owner", waitThenRun(crashingScript, true)),
			h.deployManaged(root.Id, "gateway-owner", b.Process(b.Start("start"), b.CatchTimer("t", "timeDuration", "PT1H"), b.Flow("start", "t")))}
		instances := []model.Instance{}
		for _, d := range managed {
			instances = append(instances, h.start(d, nil))
		}
		// an instance of an older version of a managed deployment, and a deployment of a gateway
		updated := h.deployManaged(root.Id, "owner", waitThenRun("2 + 2", false))
		onOld := h.start(updated, nil)
		h.addVersion(updated, waitThenRun("3 + 3", false))
		gateway := h.insert(model.Deployment{ManagedBy: root.Id, NetworkId: "gateway"}, waitThenRun("2 + 2", false))
		unit := crashbrake.Unit{Kind: crashbrake.KindInstance, Id: instances[0].Id, Element: "boom", Deployment: managed[0], Version: 1, Script: script.Fingerprint(crashingScript)}
		if remaining := h.e.ApplyCrashDecisions(ctx, []crashbrake.Decision{{Unit: unit, Error: crash}}); len(remaining) != 0 {
			t.Fatalf("%+v", remaining)
		}
		blocked := h.blockedScripts()
		if len(blocked) != 1 || blocked[0].Fingerprint != unit.Script || blocked[0].Source != crashingScript || blocked[0].Reason != crash {
			t.Fatalf("the script is blocked with its source: %+v", blocked)
		}
		if _, ok := h.e.Blocklist().Blocked(crashingCopy); !ok {
			t.Fatal("and this replica blocks it at once")
		}
		h.expectState(root.Id, model.StateIncident)
		for i, d := range managed {
			if !h.deployment(d).Disabled {
				t.Errorf("managed deployment %v is disabled", i)
			}
			h.expectState(instances[i].Id, model.StateIncident)
		}
		h.expectState(onOld.Id, model.StateIncident)
		if h.deployment(gateway).Disabled {
			t.Error("the deployment of a gateway is left to the gateway")
		}
	})
}

// A crash of an instance quarantines its deployment version and stops the other instances of the
// version, which would run the same script, also one assembled at runtime that no fingerprint
// catches; an instance of a newer version goes on, and the deployment with it.
func TestCrashStopsTheVersionOfTheInstance(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		single := h.deploy(waitThenRun(crashingScript, false))
		first, second := h.start(single, nil), h.start(single, nil)
		updated := h.deploy(waitThenRun(crashingScript, false))
		onOld := h.start(updated, nil)
		h.addVersion(updated, waitThenRun("1 + 1", false))
		onNew := h.start(updated, nil)
		// without a fingerprint, as for a script that differs in every instance
		decisions := []crashbrake.Decision{
			// a module script names no version; the instance does
			{Unit: crashbrake.Unit{Kind: crashbrake.KindInstance, Id: first.Id, Element: "boom"}, Error: crash},
			{Unit: crashbrake.Unit{Kind: crashbrake.KindInstance, Id: onOld.Id, Element: "boom", Deployment: updated, Version: 1}, Error: crash},
		}
		if remaining := h.e.ApplyCrashDecisions(ctx, decisions); len(remaining) != 0 {
			t.Fatalf("%+v", remaining)
		}
		for _, inst := range []model.Instance{first, second, onOld} {
			h.expectState(inst.Id, model.StateIncident)
		}
		if incidents := h.incidents(second.Id); len(incidents) != 1 || incidents[0].ElementId != "boom" || !strings.Contains(incidents[0].Message, "another instance") {
			t.Fatalf("%+v", incidents)
		}
		h.expectState(onNew.Id, model.StateRunning)
		if !h.deployment(single).Disabled || h.deployment(updated).Disabled {
			t.Fatalf("only the deployment whose current version crashed is quarantined: %v %v", h.deployment(single).Disabled, h.deployment(updated).Disabled)
		}
	})
}

// A crash of a unit that is gone since still blocks its script: an attacker could delete the
// deployment while the engine restarts and deploy a copy.
func TestCrashOfAGoneUnitBlocksTheScript(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		stored := h.deploy(waitThenRun("3 + 3", false))
		decisions := []crashbrake.Decision{
			{Unit: crashbrake.Unit{Kind: crashbrake.KindEvent, Id: "gone", Script: script.Fingerprint(crashingScript)}, Error: crash},
			{Unit: crashbrake.Unit{Kind: crashbrake.KindInstance, Id: "gone", Element: "boom", Script: script.Fingerprint("1 + 1")}, Error: crash},
			// an instance deleted since, of a deployment still stored: the source comes from there
			{Unit: crashbrake.Unit{Kind: crashbrake.KindInstance, Id: "gone", Element: "boom", Deployment: stored, Version: 1, Script: script.Fingerprint("3 + 3")}, Error: crash},
		}
		if remaining := h.e.ApplyCrashDecisions(ctx, decisions); len(remaining) != 0 {
			t.Fatalf("%+v", remaining)
		}
		blocked := h.blockedScripts()
		sources := map[string]string{}
		for _, entry := range blocked {
			sources[entry.Fingerprint] = entry.Source
		}
		if len(blocked) != 3 || sources[script.Fingerprint("3 + 3")] != "3 + 3" {
			t.Fatalf("%+v", blocked)
		}
		for _, src := range []string{crashingCopy, "1 + 1 // copy"} {
			if _, ok := h.e.Blocklist().Blocked(src); !ok {
				t.Errorf("%q is blocked", src)
			}
		}
	})
}

// A start timer expression that crashes while a smart service deploys rolls the deployment back,
// with its reference to the smart service; the unit names the smart service, which is stopped.
func TestCrashAtActivationStopsTheOriginator(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		root := h.start(h.deploy(b.Process(b.Message("r", "never"), b.Start("start"), b.CatchMessage("wait", "r"), b.Flow("start", "wait"))), nil)
		earlier := h.deployManaged(root.Id, "owner", waitThenRun("2 + 2", false))
		unit := crashbrake.Unit{Kind: crashbrake.KindDeployment, Id: "rolled-back", Element: "start", Version: 1, ManagedBy: root.Id, Script: script.Fingerprint(crashingScript)}
		if remaining := h.e.ApplyCrashDecisions(ctx, []crashbrake.Decision{{Unit: unit, Error: crash}}); len(remaining) != 0 {
			t.Fatalf("%+v", remaining)
		}
		h.expectState(root.Id, model.StateIncident)
		if !h.deployment(earlier).Disabled {
			t.Error("the deployments the smart service manages are quarantined")
		}
	})
}

// A smart service instance waiting for its automatic restart after an incident does not restart
// once a deployment it manages crashed: it would deploy the next variant.
func TestCrashCancelsTheRestartOfTheOriginator(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		rootDeployment := h.deploy(b.Process(b.Message("r", "never"), b.Start("start"), b.CatchMessage("wait", "r"), b.Flow("start", "wait")))
		root := h.start(rootDeployment, nil)
		managed := h.deployManaged(root.Id, "owner", waitThenRun(crashingScript, false))
		inst := h.start(managed, nil)
		err := h.s.Tx(ctx, func(tx *store.Tx) error {
			i, err := tx.GetInstance(root.Id, true)
			if err != nil {
				return err
			}
			restartAt := h.now().Add(time.Minute)
			i.State, i.RestartAt = model.StateIncident, &restartAt
			return tx.UpdateInstance(i)
		})
		if err != nil {
			t.Fatal(err)
		}
		unit := crashbrake.Unit{Kind: crashbrake.KindInstance, Id: inst.Id, Element: "boom", Deployment: managed, Version: 1, Script: script.Fingerprint(crashingScript)}
		if remaining := h.e.ApplyCrashDecisions(ctx, []crashbrake.Decision{{Unit: unit, Error: crash}}); len(remaining) != 0 {
			t.Fatalf("%+v", remaining)
		}
		if after := h.expectState(root.Id, model.StateIncident); after.RestartAt != nil {
			t.Fatalf("the restart is cancelled: %v", after.RestartAt)
		}
		if h.faultySince(rootDeployment) == nil {
			t.Error("the deployment of the smart service is marked faulty")
		}
		h.advance(time.Hour)
		h.expectState(root.Id, model.StateIncident)
	})
}

// A new entry is logged at level error, which reaches the admins, with the process, the task and
// the owner it came from; once, by the replica that added it.
func TestCrashLogsTheBlockForTheAdmins(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		var logged bytes.Buffer
		h.e.logger = slog.New(slog.NewTextHandler(&logged, nil))
		xml := b.Process(b.Message("m", "go"), b.Start("start"), b.CatchMessage("wait", "m"), `<bpmn:scriptTask id="boom" name="Compute level" scriptFormat="javascript"><bpmn:script>`+crashingScript+`</bpmn:script></bpmn:scriptTask>`, b.End("end"),
			b.Flow("start", "wait"), b.Flow("wait", "boom"), b.Flow("boom", "end"))
		d := h.deploy(xml)
		inst := h.start(d, nil)
		unit := crashbrake.Unit{Kind: crashbrake.KindInstance, Id: inst.Id, Element: "boom", Deployment: d, Version: 1, Script: script.Fingerprint(crashingScript)}
		for i := 0; i < 2; i++ {
			if remaining := h.e.ApplyCrashDecisions(ctx, []crashbrake.Decision{{Unit: unit, Error: crash}}); len(remaining) != 0 {
				t.Fatalf("%+v", remaining)
			}
		}
		var lines []string
		for _, line := range strings.Split(logged.String(), "\n") {
			if strings.Contains(line, "on the blocklist now") {
				lines = append(lines, line)
			}
		}
		if len(lines) != 1 {
			t.Fatalf("logged once: %q", logged.String())
		}
		for _, expected := range []string{"level=ERROR", "process=test", "deployment_id=" + d, "task=\"Compute level\"", "element_id=boom", "owner=owner", "fingerprint=" + unit.Script} {
			if !strings.Contains(lines[0], expected) {
				t.Errorf("%v missing in %v", expected, lines[0])
			}
		}
	})
}

// Entries stored in one fingerprint mode block in the other through their source.
func TestEntriesMatchAfterTheModeChanged(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		defer func() { _ = script.SetFingerprintMode(script.FingerprintText) }()
		if err := script.SetFingerprintMode(script.FingerprintTree); err != nil {
			t.Fatal(err)
		}
		h.block(crashingScript)
		if err := script.SetFingerprintMode(script.FingerprintText); err != nil {
			t.Fatal(err)
		}
		if _, err := h.e.LoadBlocklist(ctx); err != nil {
			t.Fatal(err)
		}
		if _, ok := h.e.Blocklist().Blocked(crashingCopy); !ok {
			t.Fatal("a copy is blocked")
		}
		copied := h.deploy(waitThenRun(crashingCopy, false))
		if err := h.e.ScanBlockedScripts(ctx); err != nil {
			t.Fatal(err)
		}
		if !h.deployment(copied).Disabled {
			t.Error("the search finds the copy")
		}
	})
}

// The crash of an event condition in a deployment of a smart service stops the smart service.
func TestCrashOfAConditionStopsTheOriginator(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		root := h.start(h.deploy(b.Process(b.Message("r", "never"), b.Start("start"), b.CatchMessage("wait", "r"), b.Flow("start", "wait"))), nil)
		managed := h.deployManaged(root.Id, "owner", waitForEvent)
		h.describeEvent(managed, "e1", crashingScript)
		unit := crashbrake.Unit{Kind: crashbrake.KindEvent, Id: "e1", Deployment: managed, Version: 1, Script: script.Fingerprint(crashingScript)}
		if remaining := h.e.ApplyCrashDecisions(ctx, []crashbrake.Decision{{Unit: unit, Error: crash}}); len(remaining) != 0 {
			t.Fatalf("%+v", remaining)
		}
		h.expectState(root.Id, model.StateIncident)
		if blocked := h.blockedScripts(); len(blocked) != 1 || blocked[0].Source != crashingScript {
			t.Fatalf("%+v", blocked)
		}
	})
}

// The search for a newly blocked script quarantines a deployment whose current version contains a
// copy and stops the instances of every version with one; an event condition with it is
// suspended; the rest stays.
func TestScanFindsCopiesInStoredDeployments(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		current := h.deploy(waitThenRun(crashingCopy, false))
		onCurrent := h.start(current, nil)
		old := uuid.NewString()
		if err := h.deployAs(old, 1, waitThenRun(crashingCopy, true)); err != nil {
			t.Fatal(err)
		}
		onOld := h.start(old, nil)
		if err := h.deployAs(old, 2, waitThenRun("1 + 1", false)); err != nil {
			t.Fatal(err)
		}
		onNew := h.start(old, nil)
		clean := h.deploy(waitThenRun("2 + 2", false))
		onClean := h.start(clean, nil)
		event := h.deploy(b.Process(b.Start("start"), b.CatchDeviceEvent("wait"), b.End("end"), b.Flow("start", "wait"), b.Flow("wait", "end")))
		err := h.s.Tx(ctx, func(tx *store.Tx) error {
			return tx.ReplaceEventDescriptions(event, 1, []model.EventDescription{{Id: "e1", DeploymentId: event, Version: 1, ElementId: "wait", Namespace: "processes", Owner: "owner",
				MessageName: EventMessageName(event, "wait"), Topic: "t", Script: crashingCopy, ValueVariable: "x"}}, nil, h.now())
		})
		if err != nil {
			t.Fatal(err)
		}

		h.block(crashingScript)
		if err = h.e.ScanBlockedScripts(ctx); err != nil {
			t.Fatal(err)
		}
		if !h.deployment(current).Disabled || h.deployment(old).Disabled || h.deployment(clean).Disabled {
			t.Fatalf("only the deployment whose current version has the script is quarantined: %v %v %v", h.deployment(current).Disabled, h.deployment(old).Disabled, h.deployment(clean).Disabled)
		}
		for _, inst := range []model.Instance{onCurrent, onOld} {
			h.expectState(inst.Id, model.StateIncident)
		}
		for _, inst := range []model.Instance{onNew, onClean} {
			h.expectState(inst.Id, model.StateRunning)
		}
		var descriptions []model.EventDescription
		err = h.s.Tx(ctx, func(tx *store.Tx) (err error) {
			descriptions, err = tx.ListEventDescriptions(event, 0)
			return err
		})
		if err != nil || len(descriptions) != 1 || descriptions[0].Suspended == "" {
			t.Fatalf("the condition is suspended: %+v %v", descriptions, err)
		}
		if blocked := h.blockedScripts(); len(blocked) != 1 || blocked[0].ScannedAt == nil {
			t.Fatalf("searched once: %+v", blocked)
		}
	})
}

// The search stops the smart service that deployed a copy, once, with every deployment it manages,
// and leaves deployments of gateways to the gateway.
func TestScanStopsTheOriginatorAndSkipsGateways(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		root := h.start(h.deploy(b.Process(b.Message("r", "never"), b.Start("start"), b.CatchMessage("wait", "r"), b.Flow("start", "wait"))), nil)
		copies := []string{h.deployManaged(root.Id, "owner", waitThenRun(crashingCopy, false)), h.deployManaged(root.Id, "owner", waitThenRun(crashingCopy, true))}
		clean := h.deployManaged(root.Id, "owner", waitThenRun("2 + 2", false))
		onClean := h.start(clean, nil)
		gateway := h.insert(model.Deployment{NetworkId: "gateway"}, waitThenRun(crashingCopy, false))
		h.block(crashingScript)
		if err := h.e.ScanBlockedScripts(ctx); err != nil {
			t.Fatal(err)
		}
		h.expectState(root.Id, model.StateIncident)
		if incidents := h.incidents(root.Id); len(incidents) != 1 {
			t.Fatalf("stopped once: %+v", incidents)
		}
		for _, d := range append(copies, clean) {
			if !h.deployment(d).Disabled {
				t.Errorf("managed deployment %v is disabled", d)
			}
		}
		h.expectState(onClean.Id, model.StateIncident)
		if h.deployment(gateway).Disabled {
			t.Error("the deployment of a gateway is left to the gateway")
		}
	})
}

// The search goes through all deployments, beyond the first page.
func TestScanPagesThroughAllDeployments(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		xml := waitThenRun(crashingCopy, false)
		ids := []string{}
		for i := 0; i < 205; i++ {
			ids = append(ids, h.insert(model.Deployment{}, xml))
		}
		h.block(crashingScript)
		if err := h.e.ScanBlockedScripts(ctx); err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if !h.deployment(id).Disabled {
				t.Fatalf("deployment %v is quarantined", id)
			}
		}
	})
}

// TestAttackChild is the engine that a smart service attacks; it runs only when started by the
// test below.
func TestAttackChild(t *testing.T) {
	dir := os.Getenv("ENGINE_ATTACK_DIR")
	if dir == "" {
		t.Skip("started by TestAttackOfASmartServiceEndsTheEngineOnce")
	}
	// without goja's bound on native recursion, the script ends the process as it is meant to
	script.MaxNativeDepth = math.MaxInt32
	s, err := store.Open(ctx, store.SQLite, "file:"+filepath.Join(dir, "engine.db"))
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, s)
	brake, _, err := crashbrake.Open(filepath.Join(dir, "brake"), nil)
	if err != nil {
		t.Fatal(err)
	}
	h.e.SetCrashBrake(brake)
	// a smart service deploys the process many times and starts it, some with a delay before the
	// script; someone else deploys a copy formatted differently
	root := h.start(h.deploy(b.Process(b.Message("r", "never"), b.Start("start"), b.CatchMessage("wait", "r"), b.Flow("start", "wait"))), nil)
	for i := 0; i < 20; i++ {
		d := h.deployManaged(root.Id, "attacker", waitThenRun(crashingScript, i%3 == 0))
		for j := 0; j < 3; j++ {
			h.start(d, nil)
		}
	}
	for i := 0; i < 3; i++ {
		h.start(h.deploy(waitThenRun(crashingCopy, false)), nil)
	}
	if err = os.WriteFile(filepath.Join(dir, "root"), []byte(root.Id), 0o644); err != nil {
		t.Fatal(err)
	}
	h.correlate(model.MessageInput{Name: "go", All: true})
	t.Fatal("goja returned instead of ending the process")
}

// The attack ends the engine once: the next start blocks the script, stops the smart service and
// everything it deployed, and finds the copies; the messages and timers that would run the script
// again find no instance waiting, and a new copy is refused. The test process dies if any runs.
func TestAttackOfASmartServiceEndsTheEngineOnce(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the crash brake needs mmap on linux")
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run", "^TestAttackChild$", "-test.v")
	cmd.Env = append(os.Environ(), "ENGINE_ATTACK_DIR="+dir)
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), crash) {
		t.Fatalf("the child did not end with a stack overflow: %v\n%s", err, out)
	}
	root, err := os.ReadFile(filepath.Join(dir, "root"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(ctx, store.SQLite, "file:"+filepath.Join(dir, "engine.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := newHarness(t, s)
	brake, decisions, err := crashbrake.Open(filepath.Join(dir, "brake"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer brake.Close()
	h.e.SetCrashBrake(brake)
	if len(decisions) != 1 {
		t.Fatalf("one script crashed: %+v", decisions)
	}
	brake.Applied(h.e.ApplyCrashDecisions(ctx, decisions))
	if err = h.e.ScanBlockedScripts(ctx); err != nil {
		t.Fatal(err)
	}
	h.expectState(string(root), model.StateIncident)
	running := 0
	err = h.s.Tx(ctx, func(tx *store.Tx) error {
		page, err := tx.ListInstances("processes", model.InstanceListOptions{State: "running", PageOptions: model.PageOptions{Limit: 1000}}, store.Access{Admin: true})
		running = page.Total
		return err
	})
	if err != nil || running != 0 {
		t.Fatalf("every instance that would run the script is stopped: %v running, %v", running, err)
	}
	h.correlate(model.MessageInput{Name: "go", All: true})
	h.advance(2 * time.Hour)
	if _, issues, err := bpmn.ParseAndValidate(waitThenRun(crashingCopy, false), h.e.Rules(model.Namespace{Name: "processes"})); err != nil || len(issues) == 0 {
		t.Fatalf("a new copy is refused: %+v %v", issues, err)
	}
}

// Another replica takes up a script blocked by this one through the revision, and searches the
// deployments for it.
func TestReplicasTakeUpTheBlocklist(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		other := h.replica()
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		copied := h.deploy(waitThenRun(crashingCopy, false))
		go other.WatchBlocklist(ctx, 10*time.Millisecond)
		h.block(crashingScript)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, ok := other.Blocklist().Blocked(crashingCopy); ok && h.deployment(copied).Disabled {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("the other replica blocks the script and searches the deployments for copies")
	})
}
