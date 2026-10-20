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
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

// On a gateway, old entries of the change stream after the acknowledged position are folded to
// the latest per instance, not removed; in the cloud they go by age (LONG-07, LONG-10).
func TestTrimmingTheChangeStreamKeepsWhatTheCloudHasNotTaken(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		future := time.Now().Add(2 * time.Hour)
		touch := func(tx *store.Tx, id string) error {
			inst, err := tx.GetInstance(id, true)
			if err != nil {
				return err
			}
			return tx.UpdateInstance(inst)
		}
		var acked int64
		tx(t, s, func(tx *store.Tx) (err error) {
			if err = seedInstance(tx, "d1", "taken"); err != nil {
				return err
			}
			if err = seedInstance(tx, "d1", "open"); err != nil {
				return err
			}
			acked, err = tx.LatestChangeSeq()
			if err != nil {
				return err
			}
			if err = tx.SetSyncPosition(store.PositionUpAcked, acked); err != nil {
				return err
			}
			for range 3 {
				if err = touch(tx, "open"); err != nil {
					return err
				}
			}
			_, err = tx.TrimChanges(future, 1000)
			return err
		})
		var changes []store.Change
		tx(t, s, func(tx *store.Tx) (err error) { changes, err = tx.ChangesAfter(0, 100); return err })
		if len(changes) != 1 || changes[0].InstanceId != "open" || changes[0].Seq <= acked {
			t.Fatalf("one entry of the instance the cloud has not taken stays: %+v", changes)
		}

		tx(t, s, func(tx *store.Tx) (err error) {
			if err = tx.SetSyncPosition(store.PositionUpAcked, changes[0].Seq); err != nil {
				return err
			}
			_, err = tx.TrimChanges(future, 1000)
			return err
		})
		tx(t, s, func(tx *store.Tx) (err error) { changes, err = tx.ChangesAfter(0, 100); return err })
		if len(changes) != 0 {
			t.Fatalf("once the cloud has taken it, the entry goes by age: %+v", changes)
		}
	})
}

func TestTrimmingTheChangeStreamWithoutSynchronisationGoesByAge(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		tx(t, s, func(tx *store.Tx) error {
			if err := seedInstance(tx, "d1", "i1"); err != nil {
				return err
			}
			if _, err := tx.TrimChanges(time.Now().Add(-time.Hour), 1000); err != nil {
				return err
			}
			if n, err := tx.LatestChangeSeq(); err != nil || n == 0 {
				t.Errorf("recent entries stay: %v %v", n, err)
			}
			_, err := tx.TrimChanges(time.Now().Add(2*time.Hour), 1000)
			return err
		})
		var changes []store.Change
		tx(t, s, func(tx *store.Tx) (err error) { changes, err = tx.ChangesAfter(0, 100); return err })
		if len(changes) != 0 {
			t.Errorf("in the cloud old entries go by age: %+v", changes)
		}
	})
}
