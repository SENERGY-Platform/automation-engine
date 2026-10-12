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
	"strings"
	"time"

	"github.com/dop251/goja"
)

// Sandbox evaluates JavaScript, the only expression and script language of the engine (EXPR-01).
// Scripts see the variables as globals plus log(...) and setVariable(name, value) (EXPR-04).
// They have no access to engine internals, the file system or the network, because goja
// provides none of these by default and nothing else is registered (EXPR-03).
//
// goja has no memory limit. The timeout and the result size limit bound what a script can do,
// but a script can allocate memory until the timeout interrupts it.
type Sandbox struct {
	Timeout        time.Duration
	MaxResultBytes int
	MaxLogEntries  int
	MaxCallStack   int
}

func NewSandbox() *Sandbox {
	return &Sandbox{
		Timeout:        2 * time.Second,
		MaxResultBytes: 1 << 20,
		MaxLogEntries:  100,
		MaxCallStack:   1000,
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
	_, err := goja.Compile("", src, false)
	return err
}

// Run evaluates src; the value of the last expression statement is the result.
func (this *Sandbox) Run(src string, variables map[string]any) (result Result, err error) {
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	if this.MaxCallStack > 0 {
		vm.SetMaxCallStackSize(this.MaxCallStack)
	}
	fail := func(err error) (Result, error) {
		return Result{}, &Error{Err: err, Logs: result.Logs}
	}
	for name, value := range variables {
		if err = vm.Set(name, value); err != nil {
			return fail(fmt.Errorf("unable to set variable %v: %w", name, err))
		}
	}
	logFn := func(call goja.FunctionCall) goja.Value {
		if this.MaxLogEntries > 0 && len(result.Logs) >= this.MaxLogEntries {
			return goja.Undefined()
		}
		parts := make([]string, 0, len(call.Arguments))
		for _, arg := range call.Arguments {
			parts = append(parts, stringify(arg))
		}
		result.Logs = append(result.Logs, strings.Join(parts, " "))
		return goja.Undefined()
	}
	if err = vm.Set("log", logFn); err != nil {
		return fail(err)
	}
	console := vm.NewObject()
	_ = console.Set("log", logFn)
	if err = vm.Set("console", console); err != nil {
		return fail(err)
	}
	err = vm.Set("setVariable", func(name string, value goja.Value) goja.Value {
		normalized, err := this.normalize(value)
		if err != nil {
			panic(vm.NewTypeError(fmt.Sprintf("setVariable(%q): %v", name, err)))
		}
		result.Assignments = append(result.Assignments, Assignment{Name: name, Value: normalized})
		_ = vm.Set(name, normalized)
		return goja.Undefined()
	})
	if err != nil {
		return fail(err)
	}

	timer := time.AfterFunc(this.Timeout, func() {
		vm.Interrupt(ErrTimeout)
	})
	defer timer.Stop()

	value, err := vm.RunString(src)
	if err != nil {
		var interrupted *goja.InterruptedError
		if errors.As(err, &interrupted) {
			return fail(fmt.Errorf("%w after %v", ErrTimeout, this.Timeout))
		}
		return fail(err)
	}
	result.Value, err = this.normalize(value)
	if err != nil {
		return fail(err)
	}
	return result, nil
}

// normalize converts a JS value into its JSON form, so that numbers are float64 and objects
// are maps, independent of how goja exports them.
func (this *Sandbox) normalize(value goja.Value) (any, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, nil
	}
	exported := value.Export()
	if _, isFunc := goja.AssertFunction(value); isFunc {
		return nil, errors.New("a function is not a valid value")
	}
	return Normalize(exported, this.MaxResultBytes)
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

func stringify(value goja.Value) string {
	if value == nil || goja.IsUndefined(value) {
		return "undefined"
	}
	if s, ok := value.Export().(string); ok {
		return s
	}
	raw, err := json.Marshal(value.Export())
	if err != nil {
		return value.String()
	}
	return string(raw)
}

// Truthy reports whether a condition result selects a sequence flow; only booleans are accepted.
func Truthy(value any) (bool, error) {
	b, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("condition must evaluate to a boolean, got %v (%T)", value, value)
	}
	return b, nil
}
