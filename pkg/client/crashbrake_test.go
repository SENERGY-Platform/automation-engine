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

package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/crashbrake"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	enginescript "github.com/SENERGY-Platform/automation-engine/pkg/script"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// With SCRIPT_CRASH_DIR the server applies, before it runs anything, what the crash brake decided
// for the previous process, also a decision an earlier start could not apply (EXPR-03).
func TestServerStopsWhatEndedThePreviousProcess(t *testing.T) {
	dir := t.TempDir()
	configure := crashConfig(dir)
	first, stop := context.WithCancel(context.Background())
	srv, err := client.NewTestServer(first, configure)
	if err != nil {
		t.Fatal(err)
	}
	d, err := srv.Controller.CreateDeployment(first, alice, ns, model.DeploymentInput{Xml: waitingModel()})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := srv.Controller.StartInstance(first, alice, ns, model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	stop()
	time.Sleep(300 * time.Millisecond)

	pending, _ := json.Marshal([]crashbrake.Decision{{Unit: crashbrake.Unit{Kind: crashbrake.KindInstance, Id: inst.Id, Element: "wait"}, Error: "fatal error: stack overflow"}})
	if err = os.WriteFile(filepath.Join(dir, "brake", "pending"), pending, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err = client.NewTestServer(ctx, configure)
	if err != nil {
		t.Fatal(err)
	}
	got, err := srv.Controller.GetInstance(ctx, alice, ns, inst.Id)
	if err != nil || got.State != model.StateIncident {
		t.Fatalf("the instance stopped with an incident: %v %v", got.State, err)
	}
	if _, err = os.Stat(filepath.Join(dir, "brake", "pending")); !os.IsNotExist(err) {
		t.Fatalf("applied, the decision is gone: %v", err)
	}
}

func crashConfig(dir string) func(*configuration.Config, *server.Dependencies) {
	return func(config *configuration.Config, _ *server.Dependencies) {
		config.SqlitePath = "file:" + filepath.Join(dir, "engine.db")
		config.ScriptCrashDir = filepath.Join(dir, "brake")
	}
}

// TestServerCrashChild is the server that dies; it runs only when started by the test below.
func TestServerCrashChild(t *testing.T) {
	dir := os.Getenv("SERVER_CRASH_DIR")
	if dir == "" {
		t.Skip("started by TestServerStopsTheScriptThatEndedIt")
	}
	// without goja's bound on native recursion, the script ends the process as it is meant to
	enginescript.MaxNativeDepth = math.MaxInt32
	ctx := context.Background()
	srv, err := client.NewTestServer(ctx, crashConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	xml := b.Process(b.Message("m", "go"), b.Start("start"), b.CatchMessage("wait", "m"),
		b.Script("boom", "x", `var o = {}; o.toString = String.prototype.trim; String(o)`), b.End("end"),
		b.Flow("start", "wait"), b.Flow("wait", "boom"), b.Flow("boom", "end"))
	d, err := srv.Controller.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: xml})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := srv.Controller.StartInstance(ctx, alice, ns, model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "instance"), []byte(inst.Id), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = srv.Controller.CorrelateMessage(ctx, alice, model.MessageInput{Name: "go", InstanceId: inst.Id})
	t.Fatal("goja returned instead of ending the process")
}

// A server with SCRIPT_CRASH_DIR records its scripts in the brake: the next server on the same
// directory stops the instance whose script ended the first one.
func TestServerStopsTheScriptThatEndedIt(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the crash brake needs mmap on linux")
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run", "^TestServerCrashChild$", "-test.v")
	cmd.Env = append(os.Environ(), "SERVER_CRASH_DIR="+dir)
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "stack overflow") {
		t.Fatalf("the child did not end with a stack overflow: %v\n%s", err, out)
	}
	instanceId, err := os.ReadFile(filepath.Join(dir, "instance"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := client.NewTestServer(ctx, crashConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	got, err := srv.Controller.GetInstance(ctx, alice, ns, string(instanceId))
	if err != nil || got.State != model.StateIncident {
		t.Fatalf("the instance stopped with an incident: %v %v", got.State, err)
	}
}

// Enabling or disabling a deployment leaves its start timers as they are; enabling it out of a
// quarantine of the crash brake, or resetting its restarts, computes them again, because a
// quarantined deployment keeps none.
func TestLiftingAQuarantineComputesTheStartTimers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := client.NewTestServer(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := srv.Controller
	// a cycle from now: computing it again moves the due time
	xml := b.Process(b.StartTimer("start", "timeCycle", "R/PT1H"), b.End("end"), b.Flow("start", "end"))
	due := func(id string) *time.Time {
		var event store.StartEvent
		err := srv.Store.Tx(ctx, func(tx *store.Tx) (err error) {
			event, err = tx.GetStartEvent(id, "start")
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return event.DueAt
	}
	quarantine := func(id string) {
		srv.Engine.ApplyCrashDecisions(ctx, []crashbrake.Decision{{Unit: crashbrake.Unit{Kind: crashbrake.KindDeployment, Id: id, Element: "start", Version: 1}, Error: "fatal error: stack overflow"}})
		// activated again, e.g. by a gateway, the quarantined version keeps no due start timer
		err := srv.Store.Tx(ctx, func(tx *store.Tx) error {
			return srv.Engine.ActivateVersion(tx, id, 1)
		})
		if err != nil || due(id) != nil {
			t.Fatalf("quarantined without due start: %v %v", due(id), err)
		}
	}

	d, err := c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: xml})
	if err != nil {
		t.Fatal(err)
	}
	before := due(d.Id)
	for _, disabled := range []bool{false, true, false} {
		if _, err = c.SetDeploymentDisabled(ctx, alice, ns, d.Id, model.DisabledInput{Disabled: disabled}); err != nil {
			t.Fatal(err)
		}
	}
	if after := due(d.Id); after == nil || !after.Equal(*before) {
		t.Fatalf("a user's enable and disable keep the start timer: %v %v", before, after)
	}

	quarantine(d.Id)
	if _, err = c.SetDeploymentDisabled(ctx, alice, ns, d.Id, model.DisabledInput{Disabled: false}); err != nil {
		t.Fatal(err)
	}
	if due(d.Id) == nil {
		t.Fatal("enabled out of the quarantine, the start timer is due again")
	}

	other, err := c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: xml})
	if err != nil {
		t.Fatal(err)
	}
	quarantine(other.Id)
	reset, err := c.ResetRestarts(ctx, alice, ns, other.Id)
	if err != nil || reset.Disabled || due(other.Id) == nil {
		t.Fatalf("a reset lifts the quarantine and computes the start timer: %+v %v", reset, err)
	}
}

// A server that fails to start frees the directory of the crash brake for the next attempt.
func TestFailedStartFreesTheCrashDirectory(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := client.NewTestServer(ctx, func(config *configuration.Config, deps *server.Dependencies) {
		crashConfig(dir)(config, deps)
		config.DeviceCacheTimeout = "no duration"
	})
	if err == nil {
		t.Fatal("expected the start to fail")
	}
	brake, _, err := crashbrake.Open(filepath.Join(dir, "brake"), nil)
	if err != nil {
		t.Fatalf("the directory is free: %v", err)
	}
	brake.Close()
}

// Admins read the blocklist and remove an entry; a deployment with a copy of a blocked script is
// refused until then (EXPR-03).
func TestBlocklistThroughTheApi(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := client.NewTestServer(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := srv.Controller
	crashing := `var o = {}; o.toString = String.prototype.trim; String(o)`
	fingerprint := enginescript.Fingerprint(crashing)
	err = srv.Store.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.BlockScript(model.BlockedScript{Fingerprint: fingerprint, Source: crashing, Reason: "fatal error: stack overflow", Origin: "test", CreatedAt: time.Now()})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = srv.Engine.LoadBlocklist(ctx); err != nil {
		t.Fatal(err)
	}
	xml := b.Process(b.Start("start"), b.Script("boom", "x", "// copy\nvar o = {}; o.toString = String.prototype.trim; String(o) // copy"), b.End("end"), b.Flow("start", "boom"), b.Flow("boom", "end"))
	if _, err = c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: xml}); !errors.Is(err, model.ErrValidation) || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("a copy is refused: %v", err)
	}
	// a parameter replaces the harmless prescript of a module task with a copy
	withPrescript := b.Process(b.Start("start"), b.Task("module", b.InText("prescript", "1;")), b.End("end"), b.Flow("start", "module"), b.Flow("module", "end"))
	parameters := map[string]model.ElementParameters{"module": {Inputs: map[string]string{"prescript": crashing + " // copy"}}}
	if _, err = c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: withPrescript, Parameters: parameters}); !errors.Is(err, model.ErrValidation) || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("a copy given as parameter is refused: %v", err)
	}
	if _, err = c.ListBlockedScripts(ctx, alice); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("admins only: %v", err)
	}
	list, err := c.ListBlockedScripts(ctx, admin)
	if err != nil || len(list) != 1 || list[0].Fingerprint != fingerprint || list[0].Source != crashing {
		t.Fatalf("%+v %v", list, err)
	}
	if err = c.UnblockScript(ctx, alice, fingerprint); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("admins only: %v", err)
	}
	if err = c.UnblockScript(ctx, admin, fingerprint); err != nil {
		t.Fatal(err)
	}
	if err = c.UnblockScript(ctx, admin, fingerprint); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("gone: %v", err)
	}
	if _, err = c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: xml}); err != nil {
		t.Fatalf("unblocked, it deploys: %v", err)
	}
}
