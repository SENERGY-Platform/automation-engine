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

package events

import (
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/crashbrake"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/script"
)

type valueUnmarshaller struct{}

func (valueUnmarshaller) Unmarshal(desc MessageDesc) (any, error) {
	return desc.Message["value"], nil
}

// A condition that is a boolean literal, as describe makes of an empty one, is decided without a
// script runtime; the evaluator here has none, so evaluating a script would panic.
func TestConstantConditionsNeedNoScript(t *testing.T) {
	e := &Evaluator{unmarshaller: valueUnmarshaller{}}
	for script, expected := range map[string]bool{"true": true, " true\n": true, "false": false} {
		result, err := e.Evaluate(model.EventDescription{Script: script, ValueVariable: "x"}, map[string]any{"value": 42.0})
		if err != nil || result.Triggered != expected || result.Value != 42.0 {
			t.Errorf("%q: %+v %v", script, result, err)
		}
	}
}

// TestConditionCrashChild is the worker that dies; it runs only when started by the test below.
func TestConditionCrashChild(t *testing.T) {
	dir := os.Getenv("EVENTS_CRASH_DIR")
	if dir == "" {
		t.Skip("started by TestConditionThatEndsTheProcessIsNamed")
	}
	// without goja's bound on native recursion, the script ends the process as it is meant to
	script.MaxNativeDepth = math.MaxInt32
	brake, _, err := crashbrake.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := (&Evaluator{unmarshaller: valueUnmarshaller{}, sandbox: script.NewSandbox()}).WithCrashBrake(brake)
	_, _ = e.Evaluate(model.EventDescription{Id: "e1", MessageName: "d_wait", Script: `var o = {}; o.toString = String.prototype.trim; String(o)`, ValueVariable: "x"}, map[string]any{"value": 1.0})
	t.Fatal("goja returned instead of ending the process")
}

// A condition that ends the process is named by its description at the next start.
func TestConditionThatEndsTheProcessIsNamed(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the crash brake needs mmap on linux")
	}
	dir := filepath.Join(t.TempDir(), "brake")
	cmd := exec.Command(os.Args[0], "-test.run", "^TestConditionCrashChild$", "-test.v")
	cmd.Env = append(os.Environ(), "EVENTS_CRASH_DIR="+dir)
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "stack overflow") {
		t.Fatalf("the child did not end with a stack overflow: %v\n%s", err, out)
	}
	brake, decisions, err := crashbrake.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer brake.Close()
	if len(decisions) != 1 || decisions[0].Unit != (crashbrake.Unit{Kind: crashbrake.KindEvent, Id: "e1", Script: script.Fingerprint(`var o = {}; o.toString = String.prototype.trim; String(o)`)}) {
		t.Fatalf("%+v", decisions)
	}
}

// A condition on the blocklist is not evaluated (EXPR-03).
func TestBlockedConditionIsNotEvaluated(t *testing.T) {
	blocklist := script.NewBlocklist()
	blocklist.Add(script.Fingerprint("x > 20"), "fatal error: stack overflow")
	e := NewEvaluator(valueUnmarshaller{}).WithBlocklist(blocklist)
	if _, err := e.Evaluate(model.EventDescription{Id: "e1", Script: "// copy\nx > 20 // copy", ValueVariable: "x"}, map[string]any{"value": 25.0}); !errors.Is(err, script.ErrBlocked) {
		t.Fatalf("%v", err)
	}
}
