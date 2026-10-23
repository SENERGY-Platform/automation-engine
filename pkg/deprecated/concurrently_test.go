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

package deprecated

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// smart-service-repository computed the usable flags of a release list in parallel; one after
// another a list of 46 releases took longer than the mobile app waits
func TestConcurrentlyRunsEveryCallWithinTheLimit(t *testing.T) {
	var running, peak atomic.Int32
	var mux sync.Mutex
	seen := map[int]int{}
	concurrently(4, 20, func(i int) {
		now := running.Add(1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		running.Add(-1)
		mux.Lock()
		seen[i]++
		mux.Unlock()
	})
	if len(seen) != 20 {
		t.Fatalf("every index once: %v", seen)
	}
	for i, n := range seen {
		if n != 1 {
			t.Errorf("%v ran %v times", i, n)
		}
	}
	if p := peak.Load(); p < 2 || p > 4 {
		t.Errorf("at most 4 at a time and more than one: %v", p)
	}
}
