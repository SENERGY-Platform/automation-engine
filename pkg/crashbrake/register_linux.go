//go:build linux

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
	"fmt"
	"os"
	"syscall"
)

// mmapRegister is the register in a file mapped MAP_SHARED: a store into the byte slice lands in
// the page cache, which the kernel writes back after the process died, so the next start reads
// what was in flight without a system call per evaluation.
type mmapRegister struct {
	file *os.File
	data []byte
}

// openRegister maps path, creating and sizing it on first use; it holds the previous process's
// entries until reset.
func openRegister(path string) (register, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("crash brake register: %w", err)
	}
	// one process per directory: a second one would reset the register of the first and blame a
	// crash on a unit of its own whose goroutine has the same id; the kernel releases the lock with
	// the process
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("crash brake register %v is used by another process: %w", path, err)
	}
	if err = file.Truncate(registerBytes); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("crash brake register size: %w", err)
	}
	data, err := syscall.Mmap(int(file.Fd()), 0, registerBytes, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("crash brake register mmap: %w", err)
	}
	return &mmapRegister{file: file, data: data}, nil
}

func (this *mmapRegister) slot(index int) []byte {
	start := headerSize + index*slotSize
	return this.data[start : start+slotSize]
}

func (this *mmapRegister) recover() ([]inFlight, bool) {
	var result []inFlight
	for i := 0; i < SlotCount; i++ {
		if slot := this.slot(i); slot[0] == 1 {
			if entry, ok := decodeEntry(slot); ok {
				result = append(result, entry)
			}
		}
	}
	return result, this.data[0] == 1
}

func (this *mmapRegister) reset() {
	clear(this.data)
}

func (this *mmapRegister) mark(index int, goroutine uint64, payload []byte) {
	slot := this.slot(index)
	// the entry before the occupied flag, so that a reader that sees the flag sees the entry
	encodeEntry(slot, goroutine, payload)
	slot[0] = 1
}

func (this *mmapRegister) clear(index int) {
	this.slot(index)[0] = 0
}

func (this *mmapRegister) markClean() {
	for i := 0; i < SlotCount; i++ {
		this.slot(i)[0] = 0
	}
	this.data[0] = 1
}

// close releases the file but keeps the mapping: a release after close writing to unmapped
// memory would be a SIGSEGV
func (this *mmapRegister) close() {
	// the mapping keeps the file open, so the lock is released here, not by the close
	_ = syscall.Flock(int(this.file.Fd()), syscall.LOCK_UN)
	_ = this.file.Close()
}
