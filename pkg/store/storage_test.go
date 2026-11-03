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

package store_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

// The storage report counts the rows of every table of instances and sizes runtime state,
// history, outboxes and IO values with their indexes (LONG-14).
func TestStorageReportCoversEveryPart(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		err := s.Tx(context.Background(), func(tx *store.Tx) error {
			rows, err := tx.StorageRows()
			if err != nil {
				return err
			}
			for _, table := range []string{"io_values", "runs", "element_stats", "command_subtasks", "activities", "tokens", "changes"} {
				if _, ok := rows[table]; !ok {
					return fmt.Errorf("rows of %v missing: %v", table, rows)
				}
			}
			before, err := tx.StorageBytes()
			if err != nil {
				return err
			}
			for _, category := range []string{"runtime", "history", "outbox", "io"} {
				if _, ok := before[category]; !ok {
					return fmt.Errorf("size of %v missing: %v", category, before)
				}
			}
			if err = seedInstance(tx, "d1", "i1"); err != nil {
				return err
			}
			// history: an activity per step of a long-running instance
			for seq := int64(1); seq <= 2000; seq++ {
				a := model.Activity{InstanceId: "i1", Run: 1, Seq: seq, Kind: model.ActivityElementEntered, ElementId: "task", Time: t0, Data: map[string]any{"pad": strings.Repeat("x", 200)}}
				if err = tx.InsertActivity(a); err != nil {
					return err
				}
			}
			after, err := tx.StorageBytes()
			if err != nil {
				return err
			}
			if after["history"] <= before["history"] {
				return fmt.Errorf("the history grows with its entries: %v -> %v", before["history"], after["history"])
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}
