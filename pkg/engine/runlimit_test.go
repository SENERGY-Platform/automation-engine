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

package engine

import (
	"slices"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// Runs of an instance are a ring buffer: beyond max_runs the oldest go with their incidents and
// element statistics, within the retention, and the debugger tells that older runs were evicted
// (LONG-03).
func TestOlderRunsAreEvictedBeyondTheLimit(t *testing.T) {
	storeRun(t, func(c *Config) { c.MaxRuns = 2; c.HousekeepingInterval = 0 }, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(b.Start("start"), b.Script("boom", "x", "missing + 1"), b.Flow("start", "boom")))
		inst := h.start(d, nil)
		for range 4 {
			if _, err := h.e.RestartInstance(ctx, inst.Id, 0, model.CauseRestart); err != nil {
				t.Fatal(err)
			}
		}
		if got := len(h.incidents(inst.Id)); got != 5 {
			t.Fatalf("a run with an incident each before the retention pass: %v", got)
		}
		if h.live(inst.Id).RunsTruncated {
			t.Fatalf("no run evicted yet")
		}
		h.advance(time.Second)

		var runs, statsRuns []int
		err := h.s.Tx(ctx, func(tx *store.Tx) error {
			list, err := tx.ListRuns(inst.Id)
			for _, r := range list {
				runs = append(runs, r.Run)
			}
			if err != nil {
				return err
			}
			for _, run := range []int{1, 2, 3, 4, 5} {
				stats, err := tx.ListElementStats(inst.Id, run)
				if err != nil {
					return err
				}
				if len(stats) > 0 {
					statsRuns = append(statsRuns, run)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(runs, []int{4, 5}) || !slices.Equal(statsRuns, []int{4, 5}) {
			t.Fatalf("the newest two runs stay with their element statistics: %v %v", runs, statsRuns)
		}
		var incidentRuns []int
		for _, i := range h.incidents(inst.Id) {
			incidentRuns = append(incidentRuns, i.Run)
		}
		slices.Sort(incidentRuns)
		if !slices.Equal(incidentRuns, []int{4, 5}) {
			t.Fatalf("the incidents of the newest two runs stay: %v", incidentRuns)
		}
		if !h.live(inst.Id).RunsTruncated {
			t.Fatalf("the debugger tells that older runs were evicted")
		}
	})
}
