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

// Package crashbrake turns a fatal crash in a script evaluation from a crash loop into one crash
// (EXPR-03). goja recurses natively over values a script builds, and a Go stack overflow is a
// fatal error that no recover catches; the trigger that ran the script, a due timer, a job whose
// lock ran out or an uncommitted Kafka message, would run it again after every restart.
//
// A memory-mapped register records which unit and goroutine is inside a script, Go writes its
// crash report next to it, and the next start returns the unit whose goroutine the report names.
// A death without report (SIGKILL, OOM, os.Exit) names nothing. Off linux, or without a
// directory, the brake does nothing. Ported from lib/crashbrake of moses.
package crashbrake

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/SENERGY-Platform/automation-engine/pkg/script"
)

// SlotCount is how many evaluations can be protected at once, far more than run concurrently;
// one beyond runs unprotected.
const SlotCount = 1024

// payloadCapacity is the most a unit may take in a slot as JSON; a larger one goes without its
// element or else runs unprotected, rather than truncated, so that a slot always names exactly one
// unit.
const payloadCapacity = 500

// MaxStack caps the stack of every goroutine once the brake is on: with the default of 1 GB the
// container's memory limit kills the process before the stack overflows, and that death writes
// no report. Legitimate work needs a few megabytes.
const MaxStack = 256 << 20

type Kind string

const (
	// a script of an instance at an element: script tasks, mappings, conditions, timers, message
	// names, and the pre- and postscripts of module tasks
	KindInstance Kind = "instance"
	// the condition of an event description
	KindEvent Kind = "event"
	// a script of a deployment without instance: the expression of a start timer
	KindDeployment Kind = "deployment"
)

// Unit is what a script runs for, and what is stopped when it crashed the process. Deployment and
// Version name the deployment version a script of an instance runs on, which is stopped when the
// crash rolled back the creation of the instance; a unit of a deployment has its id in Id.
type Unit struct {
	Kind       Kind   `json:"kind"`
	Id         string `json:"id"`
	Element    string `json:"element,omitempty"`
	Deployment string `json:"deployment,omitempty"`
	Version    int    `json:"version,omitempty"`
	// fingerprint of the running script, which goes on the blocklist (script.Fingerprint)
	Script string `json:"script,omitempty"`
	// the smart service instance that deploys a unit of a deployment, which a crash at its
	// activation rolls back together with the deployment
	ManagedBy string `json:"managed_by,omitempty"`
}

// Decision is a unit the start check found to have crashed the previous process, with the error
// in one line and the first lines of the crash report.
type Decision struct {
	Unit   Unit   `json:"unit"`
	Error  string `json:"error"`
	Report string `json:"report"`
}

// Brake owns the register and the crash report file of one process. A nil Brake is off; its
// methods are safe for concurrent use.
type Brake struct {
	dir    string
	reg    register
	logger *slog.Logger

	// slot state, 0 free and 1 taken, claimed by compare-and-swap
	taken   []int32
	nextHit atomic.Uint64
	noSlot  sync.Once
	tooLong sync.Once

	crashLog *os.File
}

// Open maps the register in dir, sends crash reports to a file beside it, caps the goroutine
// stack and returns the brake with the units that crashed the previous process, including those
// an earlier start could not apply. An empty dir returns a nil brake, which is off.
func Open(dir string, logger *slog.Logger) (*Brake, []Decision, error) {
	if dir == "" {
		return nil, nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("crash brake dir: %w", err)
	}
	reg, err := openRegister(filepath.Join(dir, "register"))
	if err != nil {
		return nil, nil, err
	}
	brake := &Brake{dir: dir, reg: reg, logger: logger, taken: make([]int32, SlotCount)}
	inflight, clean := reg.recover()
	// the crash log is truncated below, so what it holds now is from the process that just ended
	crashed, report, goroutine := readCrashReport(filepath.Join(dir, "crash.log"))
	decisions := mergeDecisions(readPending(filepath.Join(dir, "pending")), decide(inflight, clean, crashed, report, goroutine))
	// keep them until Applied, so that a start that cannot reach the database retries them
	writePending(filepath.Join(dir, "pending"), decisions)
	reg.reset()
	crashLog, err := os.OpenFile(filepath.Join(dir, "crash.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		// the decisions of the previous process still have to be applied
		return brake, decisions, fmt.Errorf("crash brake crash log: %w", err)
	}
	brake.crashLog = crashLog
	if err = debug.SetCrashOutput(crashLog, debug.CrashOptions{}); err != nil {
		return brake, decisions, fmt.Errorf("crash brake crash output: %w", err)
	}
	debug.SetMaxStack(MaxStack)
	return brake, decisions, nil
}

// decide returns exactly the units whose goroutine is the crashing one of a Go fatal report. A
// clean shutdown, no report, or a report matching no slot names nothing: stopping every unit in
// flight would stop innocent ones that merely ran at the same time.
func decide(inflight []inFlight, clean bool, crashed bool, report string, goroutine uint64) []Decision {
	if clean || !crashed || goroutine == 0 {
		return nil
	}
	var result []Decision
	for _, entry := range inflight {
		if entry.goroutine == goroutine {
			result = append(result, Decision{Unit: entry.unit, Error: errorOf(report), Report: report})
		}
	}
	return result
}

// Enter marks a script of unit as running on the current goroutine and returns the release, which
// has to be deferred: a slot not released after a panic stays taken, and enough of them switch the
// brake off. With every slot taken, or a unit too long for a slot, the evaluation runs unprotected.
func (this *Brake) Enter(unit Unit) func() {
	if this == nil {
		return func() {}
	}
	return this.enter(goroutineId(), unit)
}

// EnterScript is Enter with the fingerprint of the script in the unit; it computes nothing while
// the brake is off.
func (this *Brake) EnterScript(unit Unit, src string) func() {
	return this.EnterScriptOn(this.Goroutine(), unit, src)
}

// EnterScriptOn is EnterScript on a goroutine whose id the caller determined before with
// Goroutine, e.g. once for all scripts of a transaction: the id costs a few microseconds.
func (this *Brake) EnterScriptOn(goroutine uint64, unit Unit, src string) func() {
	if this == nil {
		return func() {}
	}
	if fingerprint, ok := script.CachedFingerprint(src); ok {
		unit.Script = fingerprint
		return this.release(this.claim(goroutine, unit))
	}
	// the slot names the unit before its fingerprint is computed, which parses the script: a
	// crash in there stops the unit, if without blocking the script
	slot := this.claim(goroutine, unit)
	unit.Script = script.Fingerprint(src)
	if slot >= 0 {
		if payload, ok := this.payload(unit); ok {
			this.reg.mark(slot, goroutine, payload)
		}
	}
	return this.release(slot)
}

// Goroutine returns the id of the current goroutine, zero while the brake is off.
func (this *Brake) Goroutine() uint64 {
	if this == nil {
		return 0
	}
	return goroutineId()
}

func (this *Brake) enter(goroutine uint64, unit Unit) func() {
	return this.release(this.claim(goroutine, unit))
}

// claim marks unit in a free slot and returns the slot, or -1 when it runs unprotected
func (this *Brake) claim(goroutine uint64, unit Unit) int {
	payload, ok := this.payload(unit)
	if !ok {
		this.tooLong.Do(func() {
			this.logger.Warn("the crash brake cannot record a unit this long, its scripts run unprotected", "kind", unit.Kind, "id", unit.Id)
		})
		return -1
	}
	start := int(this.nextHit.Add(1))
	for probe := 0; probe < SlotCount; probe++ {
		i := (start + probe) % SlotCount
		if atomic.CompareAndSwapInt32(&this.taken[i], 0, 1) {
			this.reg.mark(i, goroutine, payload)
			return i
		}
	}
	this.noSlot.Do(func() {
		this.logger.Warn("the crash brake ran out of slots, some scripts run unprotected", "slots", SlotCount)
	})
	return -1
}

func (this *Brake) release(slot int) func() {
	if slot < 0 {
		return func() {}
	}
	return func() {
		this.reg.clear(slot)
		atomic.StoreInt32(&this.taken[slot], 0)
	}
}

// payload returns the unit as JSON for a slot. The element id comes from the model and has no
// limit; a unit too long with it goes without, which leaves its incident without an element but
// stops it, rather than letting a long id switch the brake off.
func (this *Brake) payload(unit Unit) ([]byte, bool) {
	payload, err := json.Marshal(unit)
	if err == nil && len(payload) > payloadCapacity && unit.Element != "" {
		unit.Element = ""
		payload, err = json.Marshal(unit)
	}
	return payload, err == nil && len(payload) <= payloadCapacity
}

// Applied records which decisions are still to apply; the next start retries those.
func (this *Brake) Applied(remaining []Decision) {
	if this == nil {
		return
	}
	writePending(filepath.Join(this.dir, "pending"), remaining)
}

// Shutdown records a deliberate stop, so that the next start does not read the register as a
// death.
func (this *Brake) Shutdown() {
	if this != nil {
		this.reg.markClean()
	}
}

// Close closes the register file and the crash log. The mapping stays, because a release still
// running after Close must write to valid memory.
func (this *Brake) Close() {
	if this == nil {
		return
	}
	this.reg.close()
	if this.crashLog != nil {
		_ = this.crashLog.Close()
	}
}

// goroutineId reads the id of the current goroutine from the first line of its stack; it runs
// before the script, while the stack is shallow, and formats only the header line.
func goroutineId() uint64 {
	var buf [64]byte
	line := buf[:runtime.Stack(buf[:], false)]
	const prefix = "goroutine "
	if !bytes.HasPrefix(line, []byte(prefix)) {
		return 0
	}
	line = line[len(prefix):]
	if i := bytes.IndexByte(line, ' '); i >= 0 {
		id, _ := strconv.ParseUint(string(line[:i]), 10, 64)
		return id
	}
	return 0
}
