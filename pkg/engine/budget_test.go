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
	"strings"
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

// Near the storage budget the oldest history goes, beyond any retention, while running
// instances and their state stay; the owner of the network is warned once a day (LONG-07).
func TestStorageBudgetTrimsTheOldestHistoryAndWarns(t *testing.T) {
	h := newHarness(t, storetest.OpenSQLite(t), func(c *Config) { c.HousekeepingInterval = 0; c.RetentionDefaultDays = 365 })
	big := strings.Repeat("x", 4<<10)
	finished := h.deploy(b.Process(b.Start("start"), b.Script("s", "x", "'"+big+"'"), b.End("end"), b.Flow("start", "s"), b.Flow("s", "end")))
	for range 200 {
		h.start(finished, nil)
	}
	running := h.start(h.deploy(waitProcess()), map[string]any{"keep": big})
	h.process()
	used := func() (result int64) {
		_ = h.s.Tx(ctx, func(tx *store.Tx) (err error) { result, err = tx.UsedBytes(); return err })
		return result
	}
	before := used()
	h.e.config.StorageBudget = before
	h.advance(0)
	after := used()
	if after > before*7/10 {
		t.Errorf("history is trimmed to 70 %% of the budget: %v of %v", after, before)
	}
	h.expectState(running.Id, model.StateRunning)
	if h.vars(running.Id)["keep"] != big {
		t.Errorf("runtime state takes precedence over history")
	}
	// notifications go out in the next round; a second housekeeping warns no further
	h.advance(0)
	h.advance(0)
	warnings := 0
	for _, n := range h.notifier.messages() {
		if n.Title == BudgetWarning {
			warnings++
		}
	}
	if warnings != 1 {
		t.Errorf("one warning, not one per housekeeping: %+v", h.notifier.messages())
	}
}
