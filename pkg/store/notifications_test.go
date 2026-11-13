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

// A notification is claimed by one replica until the end of its claim (SNRGY-5140), and a
// postponement after a notifier without answer moves only what this replica still holds.
func TestANotificationIsClaimedOnceAndKeepsItsClaim(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		tx(t, s, func(tx *store.Tx) error {
			for i, id := range []string{"n1", "n2", "n3"} {
				if err := tx.InsertNotification(store.Notification{Id: id, UserId: "alice", Title: id, CreatedAt: t0.Add(time.Duration(i) * time.Millisecond)}); err != nil {
					return err
				}
			}
			claimUntil := t0.Add(2 * time.Minute)
			first, err := tx.ClaimNotifications(t0.Add(time.Second), claimUntil, 2)
			if err != nil || len(first) != 2 || first[0].Id != "n1" || first[1].Id != "n2" || !first[0].DueAt.Equal(claimUntil) {
				return fmt.Errorf("the oldest two, in the order they were created, with the claim as due time: %+v %v", first, err)
			}
			second, err := tx.ClaimNotifications(t0.Add(time.Second), claimUntil, 2)
			if err != nil || len(second) != 1 || second[0].Id != "n3" {
				return fmt.Errorf("a second replica gets only the one left: %+v %v", second, err)
			}
			// n2 waits for the next attempt; n3 was claimed by the other replica and keeps its claim
			for _, id := range []string{"n2", "n3"} {
				if err := tx.PostponeNotification(id, t0.Add(time.Minute), t0.Add(3*time.Minute)); err != nil {
					return err
				}
			}
			if err := tx.PostponeNotification("n2", t0.Add(time.Minute), claimUntil); err != nil {
				return err
			}
			if due, err := tx.DueNotifications(t0.Add(time.Minute), 10); err != nil || len(due) != 1 || due[0].Id != "n2" {
				return fmt.Errorf("only the notification held with that claim is postponed: %+v %v", due, err)
			}
			if again, err := tx.ClaimNotifications(claimUntil, claimUntil.Add(2*time.Minute), 10); err != nil || len(again) != 3 {
				return fmt.Errorf("all are due again once the claim runs out: %+v %v", again, err)
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
