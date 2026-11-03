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
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"modernc.org/quickjs"
)

// Sandbox evaluates JavaScript, the only expression and script language of the engine (EXPR-01).
// Scripts see the variables as globals plus log(...) and setVariable(name, value) (EXPR-04).
// Each evaluation runs in a QuickJS VM of its own (VM), which provides no access to engine
// internals, the file system or the network, and which bounds time, memory and stack: a script
// that exceeds one of them fails alone instead of the process (EXPR-03).
type Sandbox struct {
	Timeout        time.Duration
	MaxResultBytes int
	MaxLogEntries  int
	// bytes the JavaScript heap of one evaluation may take
	MaxMemory int
}

func NewSandbox() *Sandbox {
	return &Sandbox{
		Timeout:        2 * time.Second,
		MaxResultBytes: 1 << 20,
		MaxLogEntries:  100,
		MaxMemory:      64 << 20,
	}
}

type Assignment struct {
	Name  string
	Value any
}

type Result struct {
	// JSON compatible value: nil, bool, float64, string, []any or map[string]any (EXPR-05)
	Value       any
	Logs        []string
	Assignments []Assignment
}

var ErrTimeout = errors.New("script execution timeout")

// Error is a script failure; Logs holds what the script wrote before it failed.
type Error struct {
	Err  error
	Logs []string
}

func (this *Error) Error() string {
	return this.Err.Error()
}

func (this *Error) Unwrap() error {
	return this.Err
}

// Check compiles the source without running it.
func Check(src string) error {
	vm, err := quickjs.NewVM()
	if err != nil {
		return err
	}
	defer func() { _ = vm.Close() }()
	_, err = vm.Compile(src, quickjs.EvalGlobal)
	return scriptError(err)
}

// Run evaluates src; the value of the last expression statement is the result.
func (this *Sandbox) Run(src string, variables map[string]any) (result Result, err error) {
	fail := func(err error) (Result, error) {
		return Result{}, &Error{Err: err, Logs: result.Logs}
	}
	vm, err := NewVM(this.Timeout, this.MaxMemory)
	if err != nil {
		return fail(err)
	}
	defer vm.Close()
	vm.onLog = func(line string) {
		if this.MaxLogEntries <= 0 || len(result.Logs) < this.MaxLogEntries {
			result.Logs = append(result.Logs, line)
		}
	}
	vm.onAssign = func(name string, raw string) (string, error) {
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return "", err
		}
		normalized, err := Normalize(value, this.MaxResultBytes)
		if err != nil {
			return "", err
		}
		result.Assignments = append(result.Assignments, Assignment{Name: name, Value: normalized})
		out, err := json.Marshal(normalized)
		return string(out), err
	}
	if err = vm.SetGlobals(variables); err != nil {
		return fail(fmt.Errorf("unable to set variables: %w", err))
	}
	if _, err = vm.call("sandbox"); err != nil {
		return fail(err)
	}
	value, err := vm.Eval(src, this.MaxResultBytes)
	if err != nil {
		return fail(err)
	}
	result.Value, err = Normalize(value, this.MaxResultBytes)
	if err != nil {
		return fail(err)
	}
	return result, nil
}

// Normalize converts a Go value into its JSON form and enforces maxBytes if positive.
func Normalize(value any, maxBytes int) (any, error) {
	switch v := value.(type) {
	case nil, bool:
		return v, nil
	case string:
		if maxBytes > 0 && len(v) > maxBytes {
			return nil, fmt.Errorf("value exceeds %v bytes", maxBytes)
		}
		return v, nil
	case float64:
		// JSON has no NaN and Infinity; as strings they survive Number(x) in later scripts
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return strconv.FormatFloat(v, 'f', -1, 64), nil
		}
		return v, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("value is not JSON serializable: %w", err)
	}
	if maxBytes > 0 && len(raw) > maxBytes {
		return nil, fmt.Errorf("value exceeds %v bytes", maxBytes)
	}
	var result any
	err = json.Unmarshal(raw, &result)
	return result, err
}

// Truthy reports whether a condition result selects a sequence flow; only booleans are accepted.
func Truthy(value any) (bool, error) {
	b, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("condition must evaluate to a boolean, got %v (%T)", value, value)
	}
	return b, nil
}
