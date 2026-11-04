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
	"fmt"
	"maps"
	"sync/atomic"
)

// ErrBlocked is the error of a script on the blocklist.
var ErrBlocked = errors.New("the script is blocked")

// Blocklist holds the fingerprints of blocked scripts with the reason (EXPR-03). It is safe for
// concurrent use; a nil Blocklist blocks nothing, and an empty one computes no fingerprint.
type Blocklist struct {
	entries atomic.Pointer[map[string]string]
}

func NewBlocklist() *Blocklist {
	result := &Blocklist{}
	result.entries.Store(&map[string]string{})
	return result
}

// Replace sets the entries, fingerprint to reason.
func (this *Blocklist) Replace(entries map[string]string) {
	copied := maps.Clone(entries)
	this.entries.Store(&copied)
}

// Add blocks one more fingerprint.
func (this *Blocklist) Add(fingerprint string, reason string) {
	for {
		current := this.entries.Load()
		next := maps.Clone(*current)
		next[fingerprint] = reason
		if this.entries.CompareAndSwap(current, &next) {
			return
		}
	}
}

// Blocked returns the reason a script is blocked for, if it is.
func (this *Blocklist) Blocked(src string) (string, bool) {
	if this == nil {
		return "", false
	}
	entries := *this.entries.Load()
	if len(entries) == 0 {
		return "", false
	}
	reason, ok := entries[Fingerprint(src)]
	return reason, ok
}

// Check returns ErrBlocked for a blocked script.
func (this *Blocklist) Check(src string) error {
	if reason, ok := this.Blocked(src); ok {
		return fmt.Errorf("%w, it ended the engine with %v", ErrBlocked, reason)
	}
	return nil
}
