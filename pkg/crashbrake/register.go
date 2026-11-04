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
	"encoding/binary"
	"encoding/json"
)

// register is the store of what is in flight: a memory-mapped file on linux
// (register_linux.go), nothing elsewhere (register_other.go).
type register interface {
	recover() (inflight []inFlight, clean bool)
	reset()
	mark(index int, goroutine uint64, payload []byte)
	clear(index int)
	markClean()
	close()
}

type inFlight struct {
	goroutine uint64
	unit      Unit
}

// A slot is [0] occupied, [1:9] goroutine id, [9:11] payload length, then the unit as JSON.
const (
	goroutineOffset = 1
	lengthOffset    = 9
	payloadOffset   = 11
	slotSize        = payloadOffset + payloadCapacity
)

// headerSize is the bytes before the first slot; byte 0 is the clean-shutdown flag.
const headerSize = 8

const registerBytes = headerSize + SlotCount*slotSize

func encodeEntry(slot []byte, goroutine uint64, payload []byte) {
	binary.LittleEndian.PutUint64(slot[goroutineOffset:lengthOffset], goroutine)
	binary.LittleEndian.PutUint16(slot[lengthOffset:payloadOffset], uint16(len(payload)))
	copy(slot[payloadOffset:], payload)
}

// decodeEntry reports false for an entry that does not decode exactly, e.g. a torn write, which is
// then never blamed on a unit
func decodeEntry(slot []byte) (inFlight, bool) {
	n := int(binary.LittleEndian.Uint16(slot[lengthOffset:payloadOffset]))
	if n == 0 || n > payloadCapacity {
		return inFlight{}, false
	}
	var unit Unit
	if json.Unmarshal(slot[payloadOffset:payloadOffset+n], &unit) != nil || unit.Kind == "" || unit.Id == "" {
		return inFlight{}, false
	}
	return inFlight{goroutine: binary.LittleEndian.Uint64(slot[goroutineOffset:lengthOffset]), unit: unit}, true
}
