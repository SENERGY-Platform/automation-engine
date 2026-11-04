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

package crashbrake

import (
	"io"
	"log/slog"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/script"
)

var (
	crashing = Unit{Kind: KindInstance, Id: "a3f1", Element: "Task_1", Deployment: "d9", Version: 2}
	innocent = Unit{Kind: KindEvent, Id: "e7c2"}
	// ran on the crashing goroutine before, and was released
	released = Unit{Kind: KindDeployment, Id: "d4", Element: "start", Version: 1}
)

// TestChild is the process that dies; it runs only when started by the tests below.
func TestChild(t *testing.T) {
	mode := os.Getenv("CRASHBRAKE_CHILD")
	if mode == "" {
		t.Skip("started by the tests of this package")
	}
	brake, _, err := Open(os.Getenv("CRASHBRAKE_DIR"), nil)
	if err != nil {
		t.Fatal(err)
	}
	// an evaluation that merely runs at the time of the crash
	running := make(chan struct{})
	go func() {
		defer brake.Enter(innocent)()
		close(running)
		select {}
	}()
	<-running
	switch mode {
	case "crash":
		brake.Enter(released)()
		defer brake.Enter(crashing)()
		// a native as own toString recurses on the Go stack until the fatal stack overflow
		_, _ = script.NewSandbox().Run(`var o = {}; o.toString = String.prototype.trim; String(o)`, nil)
		t.Fatal("goja returned instead of crashing")
	case "kill":
		defer brake.Enter(crashing)()
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		time.Sleep(time.Minute)
	case "shutdown":
		brake.Enter(crashing)()
		brake.Shutdown()
		brake.Close()
	case "crash after shutdown":
		// a script still running when the process stops deliberately is no cause
		defer brake.Enter(crashing)()
		brake.Shutdown()
		_, _ = script.NewSandbox().Run(`var o = {}; o.toString = String.prototype.trim; String(o)`, nil)
	}
}

func runChild(t *testing.T, dir string, mode string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", "^TestChild$", "-test.v")
	cmd.Env = append(os.Environ(), "CRASHBRAKE_CHILD="+mode, "CRASHBRAKE_DIR="+dir)
	out, err := cmd.CombinedOutput()
	if mode != "shutdown" && err == nil {
		t.Fatalf("the child of %v did not die: %s", mode, out)
	}
}

// Of the units in flight when goja crashed the process, the next start names exactly the one
// whose goroutine crashed, until the decision is applied.
func TestFatalCrashNamesTheCrashingUnit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the register needs mmap on linux")
	}
	dir := t.TempDir()
	runChild(t, dir, "crash")
	brake, decisions, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || decisions[0].Unit != crashing || decisions[0].Error != "fatal error: stack overflow" || !strings.Contains(decisions[0].Report, "runtime.newstack") {
		t.Fatalf("%+v", decisions)
	}
	brake.Close()
	// not applied: the next start still has it
	brake, again, err := Open(dir, nil)
	if err != nil || !reflect.DeepEqual(again, decisions) {
		t.Fatalf("an unapplied decision is kept: %+v %v", again, err)
	}
	brake.Applied(nil)
	brake.Close()
	if _, after, err := Open(dir, nil); err != nil || len(after) != 0 {
		t.Fatalf("an applied decision is gone: %+v %v", after, err)
	}
}

// A death without report, such as an OOM kill, and a deliberate stop name nothing: the unit in
// flight is not shown to be the cause.
func TestDeathWithoutReportNamesNothing(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the register needs mmap on linux")
	}
	for _, mode := range []string{"kill", "shutdown", "crash after shutdown"} {
		dir := t.TempDir()
		runChild(t, dir, mode)
		if _, decisions, err := Open(dir, nil); err != nil || len(decisions) != 0 {
			t.Errorf("%v: %+v %v", mode, decisions, err)
		}
	}
}

// A second process on the same directory would reset the register of the first and could blame a
// crash on its own unit; it gets no brake.
func TestTheDirectoryBelongsToOneProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the register needs mmap on linux")
	}
	dir := t.TempDir()
	first, _, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second, _, err := Open(dir, nil); second != nil || err == nil {
		t.Fatalf("a second brake on the directory: %v %v", second, err)
	}
	first.Close()
	if again, _, err := Open(dir, nil); again == nil || err != nil {
		t.Fatalf("closed, the directory is free: %v", err)
	}
}

func TestWithoutDirTheBrakeIsOff(t *testing.T) {
	brake, decisions, err := Open("", nil)
	if brake != nil || decisions != nil || err != nil {
		t.Fatal(brake, decisions, err)
	}
	brake.Enter(crashing)()
	brake.Applied(nil)
	brake.Shutdown()
	brake.Close()
}

func TestReportParsing(t *testing.T) {
	fatal := "runtime: goroutine stack exceeds 268435456-byte limit\nfatal error: stack overflow\n\nruntime stack:\nruntime.throw(...)\n\ngoroutine 7 gp=0x1 m=4 [running]:\nmain.f()\n\ngoroutine 1 [chan receive]:\n"
	if !isFatalReport([]byte(fatal)) || crashingGoroutine([]byte(fatal)) != 7 {
		t.Error("a stack overflow names the running goroutine")
	}
	if crashingGoroutine([]byte("fatal error: x\n\ngoroutine 3 [chan receive]:\n\ngoroutine 7 [running]:\n")) != 7 {
		t.Error("the running goroutine is the crashing one, even when another comes first")
	}
	if isFatalReport([]byte("SIGQUIT: quit\nPC=0x0\n\ngoroutine 1 [running]:\n")) {
		t.Error("a signal dump is no crash")
	}
	inflight := []inFlight{{goroutine: 7, unit: crashing}, {goroutine: 9, unit: innocent}}
	if got := decide(inflight, false, true, "r", 7); len(got) != 1 || got[0].Unit != crashing {
		t.Errorf("%+v", got)
	}
	if decide(inflight, true, true, "r", 7) != nil || decide(inflight, false, false, "", 0) != nil || decide(inflight, false, true, "r", 8) != nil {
		t.Error("a clean stop, no report or a report of another goroutine names nothing")
	}
}

// A slot names exactly one unit: a unit too long for it runs unprotected instead of truncated,
// and a torn entry is dropped.
func TestEntriesDecodeExactly(t *testing.T) {
	slot := make([]byte, slotSize)
	encodeEntry(slot, 42, []byte(`{"kind":"instance","id":"a","element":"b"}`))
	if entry, ok := decodeEntry(slot); !ok || entry.goroutine != 42 || entry.unit != (Unit{Kind: KindInstance, Id: "a", Element: "b"}) {
		t.Fatalf("%+v %v", entry, ok)
	}
	encodeEntry(slot, 42, []byte(`{"kind":"instance","id":"a","elem`))
	if _, ok := decodeEntry(slot); ok {
		t.Error("a torn entry decodes")
	}
	brake := &Brake{reg: &countingRegister{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), taken: make([]int32, SlotCount)}
	brake.Enter(Unit{Kind: KindInstance, Id: strings.Repeat("x", payloadCapacity)})()
	if brake.reg.(*countingRegister).marks != 0 {
		t.Error("a unit too long for a slot was recorded")
	}
}

type countingRegister struct {
	marks int
}

func (this *countingRegister) recover() ([]inFlight, bool) { return nil, true }
func (this *countingRegister) reset()                      {}
func (this *countingRegister) mark(int, uint64, []byte)    { this.marks++ }
func (this *countingRegister) clear(int)                   {}
func (this *countingRegister) markClean()                  {}
func (this *countingRegister) close()                      {}
