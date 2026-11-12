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
	"fmt"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

// A notification is claimed once at the due time it was read with, and a postponement after a
// notifier without answer leaves one that another replica claimed as it is.
func TestANotificationIsClaimedOnceAndKeepsItsClaim(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		tx(t, s, func(tx *store.Tx) error {
			for _, id := range []string{"n1", "n2"} {
				if err := tx.InsertNotification(store.Notification{Id: id, UserId: "alice", Title: id, CreatedAt: t0}); err != nil {
					return err
				}
			}
			due, err := tx.DueNotifications(t0, 10)
			if err != nil || len(due) != 2 || !due[0].DueAt.Equal(t0) {
				return fmt.Errorf("due with their due time: %+v %v", due, err)
			}
			claimUntil := t0.Add(2 * time.Minute)
			if claimed, err := tx.ClaimNotification("n2", t0, claimUntil); err != nil || !claimed {
				return fmt.Errorf("claim: %v %v", claimed, err)
			}
			if claimed, err := tx.ClaimNotification("n2", t0, claimUntil); err != nil || claimed {
				return fmt.Errorf("a second replica does not claim it: %v %v", claimed, err)
			}
			for _, id := range []string{"n1", "n2"} {
				if err := tx.PostponeNotification(id, t0.Add(time.Second), t0); err != nil {
					return err
				}
			}
			if due, err = tx.DueNotifications(t0.Add(time.Second), 10); err != nil || len(due) != 1 || due[0].Id != "n1" {
				return fmt.Errorf("only the unclaimed one is postponed, the claimed one keeps its claim: %+v %v", due, err)
			}
			if due, err = tx.DueNotifications(claimUntil, 10); err != nil || len(due) != 2 {
				return fmt.Errorf("both are due once the claim runs out: %+v %v", due, err)
			}
			return nil
		})
	})
}

// While engines with and without the column run side by side, a notification an engine without it
// queued keeps the hour every notification had before (SNRGY-3911).
func TestANotificationQueuedWithoutTheWindowColumnKeepsAnHour(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		tx(t, s, func(tx *store.Tx) error {
			// the column list of migration 23
			if err := store.ExecForTest(tx, "INSERT INTO notifications (id, user_id, title, message, topic, namespace, due_at, created_at) VALUES ('old', 'alice', 't', 'm', 'processes', 'processes', 0, 0)"); err != nil {
				return err
			}
			if err := tx.InsertNotification(store.Notification{Id: "new", UserId: "alice", Title: "t", CreatedAt: t0}); err != nil {
				return err
			}
			due, err := tx.DueNotifications(t0, 10)
			if err != nil || len(due) != 2 {
				return fmt.Errorf("both due: %+v %v", due, err)
			}
			windows := map[string]int64{}
			for _, n := range due {
				windows[n.Id] = n.DuplicateWindow
			}
			if windows["old"] != store.DefaultDuplicateWindow || windows["new"] != 0 {
				return fmt.Errorf("the old one keeps an hour, the new one its own window: %v", windows)
			}
			return nil
		})
	})
}
