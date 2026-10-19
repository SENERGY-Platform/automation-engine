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
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// More expired instances than one batch are all removed in one pass, while an instance that
// finished within the retention stays.
func TestRetentionRemovesExpiredInstancesBeyondOneBatchAndKeepsYoungerOnes(t *testing.T) {
	storeRun(t, func(c *Config) { c.RetentionDefaultDays = 7; c.HousekeepingInterval = 0; c.BatchSize = 2 }, func(t *testing.T, h *harness) {
		d := h.deploy(waitProcess())
		var expired []string
		for range 5 {
			inst := h.start(d, nil)
			h.correlate(model.MessageInput{Name: "go", InstanceId: inst.Id})
			expired = append(expired, inst.Id)
		}
		h.advance(6 * 24 * time.Hour)
		younger := h.start(d, nil)
		h.correlate(model.MessageInput{Name: "go", InstanceId: younger.Id})
		h.advance(2 * 24 * time.Hour)
		for _, id := range expired {
			if err := h.s.Tx(ctx, func(tx *store.Tx) error { _, err := tx.GetInstance(id, false); return err }); err == nil {
				t.Errorf("instance %v finished 8 days ago and must be deleted", id)
			}
		}
		h.expectState(younger.Id, model.StateCompleted)
	})
}
