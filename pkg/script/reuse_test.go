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

package script

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// A reused VM gives what a fresh one gives.
func TestRunReusedEvaluatesAsRun(t *testing.T) {
	s := NewSandbox()
	vars := map[string]any{"x": float64(5), "payload": `{"value": 42}`, "distance_km": nil, "result": []any{float64(7)}, "ip": "10.0.0.1", "event": map[string]any{"Latitude": 51.3}}
	for _, src := range []string{"1 + 1", "x > 3", "JSON.parse(payload).value", "({a: [1, 'b', null]})", "distance_km == null", "result[0]",
		"Math.log(0)", "undefined", "`http://${ip}/ta`", "JSON.parse(event).Latitude", "var v = x * 2; v + 1", "function f(a) { return a + x } f(1)", "this === globalThis"} {
		expected, err := s.Run(src, vars)
		if err != nil {
			t.Fatal(src, err)
		}
		for i := 0; i < 2; i++ {
			got, err := s.RunReused("k", src, vars)
			if err != nil || !reflect.DeepEqual(got.Value, expected.Value) {
				t.Errorf("%v: %#v %v, want %#v", src, got.Value, err, expected.Value)
			}
		}
	}
}

// What a script declares, assigns or deletes globally is gone at the next evaluation; a changed
// built-in object, as JSON.parse or String.prototype.trim, stays, but only for the same key.
func TestRunReusedResetsTheGlobals(t *testing.T) {
	s := NewSandbox()
	src := `var r = [typeof declared, typeof implicit, typeof fn, typeof Math, JSON.parse("1"), typeof log, x, " a ".trim()];
		var declared = 1; implicit = 2; function fn() {}; delete Math; JSON.parse = function () { return "changed" };
		log = 3; String.prototype.trim = function () { return "changed" }; r`
	first, err := s.RunReused("k", src, map[string]any{"x": 1.0, "log": "variable"})
	if err != nil || !reflect.DeepEqual(first.Value, []any{"undefined", "undefined", "function", "object", float64(1), "function", float64(1), "a"}) {
		t.Fatalf("%#v %v", first.Value, err)
	}
	second, err := s.RunReused("k", src, map[string]any{"x": 2.0})
	if err != nil || !reflect.DeepEqual(second.Value, []any{"undefined", "undefined", "function", "object", "changed", "function", float64(2), "changed"}) {
		t.Fatalf("%#v %v", second.Value, err)
	}
	other, err := s.RunReused("other", src, map[string]any{"x": 3.0})
	if err != nil || other.Value.([]any)[7] != "a" {
		t.Fatalf("another key has a VM of its own: %#v %v", other.Value, err)
	}
}

func TestRunReusedKeepsLogsAndAssignments(t *testing.T) {
	got, err := NewSandbox().RunReused("k", `log("count", n); setVariable("n", n + 1); console.log(n); n`, map[string]any{"n": float64(1)})
	if err != nil || got.Value != float64(2) || !reflect.DeepEqual(got.Logs, []string{"count 1", "2"}) || len(got.Assignments) != 1 {
		t.Fatalf("%#v %v", got, err)
	}
}

// After a failure, and for a changed script, the key gets a fresh VM.
func TestRunReusedStartsFreshAfterAnErrorOrAnotherScript(t *testing.T) {
	s := NewSandbox()
	s.Timeout = 100 * time.Millisecond
	if _, err := s.RunReused("k", `String.prototype.mark = 1; throw new Error("boom")`, nil); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatal(err)
	}
	if got, err := s.RunReused("k", `String.prototype.mark = 1; typeof "".mark`, nil); err != nil || got.Value != "number" {
		t.Fatalf("%#v %v", got.Value, err)
	}
	if got, err := s.RunReused("k", `typeof "".mark`, nil); err != nil || got.Value != "undefined" {
		t.Fatalf("a changed script gets a fresh VM: %#v %v", got.Value, err)
	}
	for _, src := range []string{`var o = {}; o.toString = String.prototype.trim; String(o)`, `while (true) {}`} {
		if _, err := s.RunReused("k", src, nil); err == nil {
			t.Fatalf("%v: expected an error", src)
		}
		if got, err := s.RunReused("k", src+`; 1`, nil); err == nil {
			t.Fatalf("%v: expected an error again, got %v", src, got.Value)
		}
	}
	if got, err := s.RunReused("k", `1`, nil); err != nil || got.Value != float64(1) {
		t.Fatalf("%#v %v", got.Value, err)
	}
}

func TestRunReusedKeepsAtMostMaxReusedVMs(t *testing.T) {
	s := NewSandbox()
	s.MaxReused = 2
	for i := 0; i < 5; i++ {
		if _, err := s.RunReused(fmt.Sprint(i), "1", nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.reused) != 2 || s.reused["3"] == nil || s.reused["4"] == nil {
		t.Fatalf("the most recently used stay: %v", s.reused)
	}
	s.MaxReused = 0
	if got, err := s.RunReused("5", "2", nil); err != nil || got.Value != float64(2) || s.reused["5"] != nil {
		t.Fatalf("without reuse a fresh VM: %v %v", got.Value, err)
	}
}

func TestRunReusedConcurrently(t *testing.T) {
	s := NewSandbox()
	s.MaxReused = 3
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				key := fmt.Sprint(i % 5)
				got, err := s.RunReused(key, "x * 2", map[string]any{"x": float64(i)})
				if err != nil || got.Value != float64(2*i) {
					t.Errorf("%v %v", got.Value, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
