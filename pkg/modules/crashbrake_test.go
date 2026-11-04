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

package modules

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/crashbrake"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

// TestPrescriptCrashChild is the process that dies; it runs only when started by the test below.
func TestPrescriptCrashChild(t *testing.T) {
	dir := os.Getenv("MODULES_CRASH_DIR")
	if dir == "" {
		t.Skip("started by TestPrescriptThatEndsTheProcessIsNamed")
	}
	s := storetest.OpenSQLite(t)
	instance := instanceWith(t, s, "smart-services")
	if err := os.WriteFile(filepath.Join(dir, "instance"), []byte(instance), 0o644); err != nil {
		t.Fatal(err)
	}
	brake, _, err := crashbrake.Open(filepath.Join(dir, "brake"), nil)
	if err != nil {
		t.Fatal(err)
	}
	rt := NewRuntime(s, Settings{}, func(user string) (string, error) { return "Bearer token-of-" + user, nil }, logger)
	rt.SetCrashBrake(brake)
	rt.add("recording", &recordingHandler{})
	_, _ = rt.types["recording"].Execute(context.Background(), engine.Task{JobId: "task1", InstanceId: instance, RootInstanceId: instance, Namespace: "smart-services",
		DeploymentId: "d-smart-services", ElementId: "task1", TaskType: "recording", Owner: "alice",
		Input: map[string]any{"prescript": `var o = {}; o.toString = String.prototype.trim; String(o)`}})
	t.Fatal("goja returned instead of ending the process")
}

// A prescript of a module task that ends the process is named by its instance and task.
func TestPrescriptThatEndsTheProcessIsNamed(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the crash brake needs mmap on linux")
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run", "^TestPrescriptCrashChild$", "-test.v")
	cmd.Env = append(os.Environ(), "MODULES_CRASH_DIR="+dir)
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "stack overflow") {
		t.Fatalf("the child did not end with a stack overflow: %v\n%s", err, out)
	}
	instance, err := os.ReadFile(filepath.Join(dir, "instance"))
	if err != nil {
		t.Fatal(err)
	}
	brake, decisions, err := crashbrake.Open(filepath.Join(dir, "brake"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer brake.Close()
	expected := crashbrake.Unit{Kind: crashbrake.KindInstance, Id: string(instance), Element: "task1", Deployment: "d-smart-services"}
	if len(decisions) != 1 || decisions[0].Unit != expected {
		t.Fatalf("%+v", decisions)
	}
}
