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
	"sync"
	"sync/atomic"
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
	// VMs RunReused keeps, about 175 KiB each; the least recently used goes first
	MaxReused int

	reuseMux sync.Mutex
	reused   map[string]*reusedVM
}

func NewSandbox() *Sandbox {
	return &Sandbox{
		Timeout:        2 * time.Second,
		MaxResultBytes: 1 << 20,
		MaxLogEntries:  100,
		MaxMemory:      64 << 20,
		MaxReused:      200,
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
	vm, err := this.newVM(variables)
	if err != nil {
		return result, &Error{Err: err}
	}
	defer vm.Close()
	this.collect(vm, &result)
	value, err := vm.Eval(src, this.MaxResultBytes)
	return this.finish(result, value, err)
}

// newVM returns a VM with the variables and the functions of the sandbox as globals; a function
// replaces a variable of the same name
func (this *Sandbox) newVM(variables map[string]any) (*VM, error) {
	vm, err := NewVM(this.Timeout, this.MaxMemory)
	if err != nil {
		return nil, err
	}
	if err = vm.SetGlobals(variables); err != nil {
		vm.Close()
		return nil, fmt.Errorf("unable to set variables: %w", err)
	}
	if _, err = vm.call("sandbox"); err != nil {
		vm.Close()
		return nil, err
	}
	return vm, nil
}

// collect makes the logs and assignments of the next evaluation on vm go into result
func (this *Sandbox) collect(vm *VM, result *Result) {
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
}

func (this *Sandbox) finish(result Result, value any, err error) (Result, error) {
	if err == nil {
		result.Value, err = Normalize(value, this.MaxResultBytes)
	}
	if err != nil {
		return Result{}, &Error{Err: err, Logs: result.Logs}
	}
	return result, nil
}

// reusedVM is a VM RunReused keeps for one key
type reusedVM struct {
	mux sync.Mutex
	vm  *VM
	src string
	// guarded by Sandbox.reuseMux
	used time.Time
	// set once the entry left the map; the holder of mux closes the VM
	dropped atomic.Bool
}

// RunReused is Run in a VM kept for key and reused while src stays the same, which saves creating
// a VM for each evaluation of a script that runs again and again, like the condition of an event
// (EVENT_SCRIPT_REUSE). The variables are visible only to the script, and what it declares or
// assigns globally is removed afterwards; a change of a built-in object such as
// String.prototype, however, stays for the next evaluation in that VM, which is why the key must
// not be shared by scripts of different owners. A VM is dropped after an error, so that a script
// that ran out of time or memory starts fresh. While the VM of key is busy, a fresh one is used.
func (this *Sandbox) RunReused(key string, src string, variables map[string]any) (result Result, err error) {
	entry := this.reusedEntry(key, src)
	if entry == nil || !entry.mux.TryLock() {
		return this.Run(src, variables)
	}
	defer entry.mux.Unlock()
	if entry.dropped.Load() {
		return this.Run(src, variables)
	}
	if entry.vm == nil {
		if entry.vm, err = this.newVM(nil); err == nil {
			if err = entry.vm.reusable(); err != nil {
				entry.vm.Close()
				entry.vm = nil
			}
		}
		if err != nil {
			this.drop(key, entry)
			return result, &Error{Err: err}
		}
	}
	this.collect(entry.vm, &result)
	value, err := entry.vm.EvalReused(src, variables, this.MaxResultBytes)
	if err != nil {
		this.drop(key, entry)
	}
	if entry.dropped.Load() {
		entry.vm.Close()
		entry.vm = nil
	}
	return this.finish(result, value, err)
}

// reusedEntry returns the entry of key, a new one if there is none or its script differs
func (this *Sandbox) reusedEntry(key string, src string) *reusedVM {
	if this.MaxReused <= 0 {
		return nil
	}
	this.reuseMux.Lock()
	defer this.reuseMux.Unlock()
	if this.reused == nil {
		this.reused = map[string]*reusedVM{}
	}
	entry, ok := this.reused[key]
	if ok && entry.src != src {
		this.dropLocked(key, entry)
		ok = false
	}
	if !ok {
		if len(this.reused) >= this.MaxReused {
			oldestKey := ""
			var oldest *reusedVM
			for k, e := range this.reused {
				if oldest == nil || e.used.Before(oldest.used) {
					oldestKey, oldest = k, e
				}
			}
			this.dropLocked(oldestKey, oldest)
		}
		entry = &reusedVM{src: src}
		this.reused[key] = entry
	}
	entry.used = time.Now()
	return entry
}

func (this *Sandbox) drop(key string, entry *reusedVM) {
	this.reuseMux.Lock()
	defer this.reuseMux.Unlock()
	this.dropLocked(key, entry)
}

// dropLocked removes the entry; its VM is closed now if it is idle, otherwise by its holder
func (this *Sandbox) dropLocked(key string, entry *reusedVM) {
	if this.reused[key] == entry {
		delete(this.reused, key)
	}
	entry.dropped.Store(true)
	if entry.mux.TryLock() {
		if entry.vm != nil {
			entry.vm.Close()
			entry.vm = nil
		}
		entry.mux.Unlock()
	}
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
