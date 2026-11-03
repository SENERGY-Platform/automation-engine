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
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRunReturnsLastExpressionAsJsonValue(t *testing.T) {
	s := NewSandbox()
	cases := map[string]any{
		"1 + 1":                     float64(2),
		"0;":                        float64(0),
		"'a' + 'b'":                 "ab",
		"x > 3":                     true,
		"JSON.parse(payload).value": float64(42),
		"({a: [1, 'b', null]})":     map[string]any{"a": []any{float64(1), "b", nil}},
		"distance_km == null":       true,
		"result[0]":                 float64(7),
		"Math.max(Number(x), 10)":   float64(10),
		"Math.log(0)":               "-Inf",
		"undefined":                 nil,
		"`http://${ip}/ta`":         "http://10.0.0.1/ta",
	}
	vars := map[string]any{"x": float64(5), "payload": `{"value": 42}`, "distance_km": nil, "result": []any{float64(7)}, "ip": "10.0.0.1"}
	for src, expected := range cases {
		got, err := s.Run(src, vars)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		if !reflect.DeepEqual(got.Value, expected) {
			t.Errorf("%s: expected %#v, got %#v", src, expected, got.Value)
		}
	}
}

func TestRunCollectsLogsAndAssignments(t *testing.T) {
	got, err := NewSandbox().Run(`log("count", n, {a: 1}); setVariable("n", n + 1); console.log(n); n`, map[string]any{"n": float64(1)})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Logs, []string{`count 1 {"a":1}`, "2"}) {
		t.Errorf("unexpected logs %#v", got.Logs)
	}
	if len(got.Assignments) != 1 || got.Assignments[0].Name != "n" || got.Assignments[0].Value != float64(2) {
		t.Errorf("unexpected assignments %#v", got.Assignments)
	}
	if got.Value != float64(2) {
		t.Errorf("setVariable must update the global, got %v", got.Value)
	}
}

func TestRunInterruptsEndlessLoopAfterTimeout(t *testing.T) {
	s := NewSandbox()
	s.Timeout = 50 * time.Millisecond
	start := time.Now()
	_, err := s.Run("while(true){}", nil)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("expected timeout, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("timeout took too long")
	}
}

func TestRunRejectsResultsOverSizeLimit(t *testing.T) {
	s := NewSandbox()
	s.MaxResultBytes = 100
	_, err := s.Run("'x'.repeat(200)", nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size error, got %v", err)
	}
}

func TestRunHasNoEngineOrHostObjects(t *testing.T) {
	for _, src := range []string{"execution.setVariable('a', 1)", "java.lang.System", "require('fs')", "process.env"} {
		if _, err := NewSandbox().Run(src, nil); err == nil {
			t.Errorf("%s: expected ReferenceError", src)
		}
	}
}

func TestRunKeepsLogsOfFailedScript(t *testing.T) {
	_, err := NewSandbox().Run("log('before'); throw new Error('boom')", nil)
	var scriptErr *Error
	if !errors.As(err, &scriptErr) || len(scriptErr.Logs) != 1 || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("unexpected %#v", err)
	}
}

func TestRunRejectsDeepRecursion(t *testing.T) {
	if _, err := NewSandbox().Run("function f(){return f()} f()", nil); err == nil {
		t.Fatal("expected stack overflow error")
	}
}

func TestCheckReportsSyntaxErrors(t *testing.T) {
	if err := Check("a &&"); err == nil {
		t.Fatal("expected syntax error")
	}
	if err := Check("a && b"); err != nil {
		t.Fatal(err)
	}
}

func TestTruthyAcceptsOnlyBooleans(t *testing.T) {
	if v, err := Truthy(true); err != nil || !v {
		t.Fatal("true")
	}
	if _, err := Truthy("true"); err == nil {
		t.Fatal("expected error for string")
	}
}

// Camunda handed some variables over as JSON text that the engine keeps as objects, e.g. the
// value of a device event; models parse them, so JSON.parse returns what is no string as it is.
func TestJsonParseReturnsAValueThatIsNoStringAsItIs(t *testing.T) {
	s := NewSandbox()
	vars := map[string]any{"event": map[string]any{"Latitude": 51.3, "Longitude": 12.4}, "list": []any{"a"}, "text": `{"Latitude": 51.3}`, "n": float64(3)}
	cases := map[string]any{
		"JSON.parse(event).Latitude": 51.3,
		"JSON.parse(list)[0]":        "a",
		"JSON.parse(text).Latitude":  51.3,
		"JSON.parse(n)":              float64(3),
		`JSON.parse('{"a":1}', (k, v) => k == "a" ? v + 1 : v).a`: float64(2),
	}
	for src, expected := range cases {
		got, err := s.Run(src, vars)
		if err != nil || !reflect.DeepEqual(got.Value, expected) {
			t.Errorf("%v: %#v %v", src, got.Value, err)
		}
	}
	if _, err := s.Run(`JSON.parse("{")`, nil); err == nil {
		t.Error("invalid JSON text still fails")
	}
}

// Scripts that recurse natively killed the process on goja with "fatal error: stack overflow",
// which no recover catches; on QuickJS they fail alone (EXPR-03).
func TestRunContainsWhatKilledTheProcess(t *testing.T) {
	for name, src := range map[string]string{
		"built-in as toString":    `var o = {}; o.toString = String.prototype.trim; String(o)`,
		"built-in as toPrimitive": `var o = {}; o[Symbol.toPrimitive] = String.prototype.trim; String(o)`,
		"computed key":            `var o = {}; o["to" + "String"] = String.prototype.trim; String(o)`,
		"deep array as text":      `var a = []; for (var i = 0; i < 1000000; i++) { a = [a] }; String(a).length`,
		"proxy chain":             `var q = new Proxy({}, {}); for (var i = 0; i < 100000; i++) { q = new Proxy(q, {}) }; q.x`,
		"map containing itself":   `var m = new Map(); m.set("a", m); m`,
		"recursion caught in try": `function f() { return f() } try { f() } catch (e) { throw e }`,
	} {
		if _, err := NewSandbox().Run(src, nil); err == nil && name != "map containing itself" {
			t.Errorf("%v: expected an error", name)
		}
	}
}

func TestRunLimitsMemory(t *testing.T) {
	s := NewSandbox()
	s.MaxMemory = 16 << 20
	_, err := s.Run(`var a = []; while (true) { a.push(new Array(100000).fill(1)) }`, nil)
	if err == nil || !strings.Contains(err.Error(), "out of memory") {
		t.Fatalf("expected out of memory, got %v", err)
	}
	if got, err := s.Run(`1 + 1`, nil); err != nil || got.Value != float64(2) {
		t.Fatalf("the next evaluation is not affected: %v %v", got.Value, err)
	}
}

// Nothing a script changes reaches the next evaluation, a built-in included.
func TestRunIsolatesEvaluations(t *testing.T) {
	s := NewSandbox()
	if _, err := s.Run(`String.prototype.trim = function () { return "changed" }; var leaked = 1; implicit = 2`, nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.Run(`[" a ".trim(), typeof leaked, typeof implicit]`, nil)
	if err != nil || !reflect.DeepEqual(got.Value, []any{"a", "undefined", "undefined"}) {
		t.Fatalf("%#v %v", got.Value, err)
	}
}

func TestRunNamesWhereTheScriptFailed(t *testing.T) {
	_, err := NewSandbox().Run("var a = 1;\n\nmissing()", nil)
	if err == nil || !strings.Contains(err.Error(), "'missing' is not defined at <eval>:3:1") {
		t.Fatalf("unexpected %v", err)
	}
	if err = Check("a &&"); err == nil || !strings.Contains(err.Error(), "<eval>:1:5") {
		t.Fatalf("unexpected %v", err)
	}
}

func TestRunEncodesValuesAsGojaDid(t *testing.T) {
	s := NewSandbox()
	cases := map[string]any{
		"({a: undefined, b: [undefined]})": map[string]any{"a": nil, "b": []any{nil}},
		"NaN":                              "NaN",
		"Infinity":                         "+Inf",
	}
	for src, expected := range cases {
		got, err := s.Run(src, nil)
		if err != nil || !reflect.DeepEqual(got.Value, expected) {
			t.Errorf("%v: %#v %v", src, got.Value, err)
		}
	}
	for _, src := range []string{"({a: NaN})", "(function () {})", "({f: function () {}})"} {
		if _, err := s.Run(src, nil); err == nil {
			t.Errorf("%v: expected an error", src)
		}
	}
	if _, err := s.Run(`setVariable("x", {a: NaN})`, nil); err == nil || !strings.Contains(err.Error(), `setVariable("x")`) {
		t.Errorf("setVariable names the variable: %v", err)
	}
}
