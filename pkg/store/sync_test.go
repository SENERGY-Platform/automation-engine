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

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

func seedEnded(tx *store.Tx, instanceId string) error {
	if err := seedInstance(tx, "d1", instanceId); err != nil {
		return err
	}
	inst, err := tx.GetInstance(instanceId, true)
	if err != nil {
		return err
	}
	ended := t0
	inst.State, inst.EndedAt = model.StateCompleted, &ended
	return tx.UpdateInstance(inst)
}

func exists(t *testing.T, s *store.Store, id string) bool {
	t.Helper()
	var err error
	tx(t, s, func(tx *store.Tx) error { _, err = tx.GetInstance(id, true); return nil })
	return err == nil
}

// On a gateway, retention and storage budget keep an ended instance until the cloud has taken
// its end; otherwise the cloud would never learn how it ended (LONG-07, LONG-10). Without a
// position of the synchronisation, in the cloud, nothing changes.
func TestTrimmingWaitsForTheCloudToTakeTheEnd(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		later := t0.Add(1000 * time.Hour)
		tx(t, s, func(tx *store.Tx) error { return seedEnded(tx, "cloud") })
		tx(t, s, func(tx *store.Tx) error { _, err := tx.TrimOldestHistory(100); return err })
		if exists(t, s, "cloud") {
			t.Fatal("without synchronisation the budget removes ended instances")
		}

		tx(t, s, func(tx *store.Tx) error {
			if err := tx.InitSyncPosition(store.PositionUpAcked); err != nil {
				return err
			}
			if err := seedEnded(tx, "kept"); err != nil {
				return err
			}
			if _, err := tx.TrimOldestHistory(100); err != nil {
				return err
			}
			_, err := tx.DeleteFinishedInstances("deployment_id", "d1", later, 100)
			return err
		})
		if !exists(t, s, "kept") {
			t.Fatal("an instance with changes the cloud has not taken stays")
		}
		tx(t, s, func(tx *store.Tx) error {
			latest, err := tx.LatestChangeSeq()
			if err != nil {
				return err
			}
			if err = tx.SetSyncPosition(store.PositionUpAcked, latest); err != nil {
				return err
			}
			n, err := tx.DeleteFinishedInstances("deployment_id", "d1", later, 100)
			if n != 1 {
				return fmt.Errorf("the retention removes it once the cloud took it: %v %v", n, err)
			}
			return err
		})
	})
}

// The cloud forgets applied deletions and ended instances; a later resource with the same id
// still gets a newer version than the gateway applied, or the gateway would skip it.
func TestDesiredVersionsGrowAfterTheCloudForgotAResource(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		var first, second, third int64
		tx(t, s, func(tx *store.Tx) (err error) {
			if first, err = tx.Desire("hub-1", "instance", "i1", false, map[string]any{}, t0); err != nil {
				return err
			}
			if second, err = tx.Desire("hub-1", "instance", "i1", false, map[string]any{"stopped": true}, t0); err != nil {
				return err
			}
			if err = tx.ForgetEndedInstance("hub-1", "i1"); err != nil {
				return err
			}
			if _, found, err := tx.GetDesired("hub-1", "instance", "i1"); err != nil || found {
				return fmt.Errorf("an ended instance is forgotten: %v %v", found, err)
			}
			third, err = tx.Desire("hub-1", "instance", "i1", true, nil, t0.Add(time.Second))
			return err
		})
		if !(first < second && second < third) {
			t.Errorf("versions grow within a resource and across forgetting: %v %v %v", first, second, third)
		}
	})
}

// Without permissions-v2, as on a gateway, nothing removes queued rights; the queue stays empty
// instead of growing with every removed instance.
func TestRemovalsAreNotQueuedWithoutPermissions(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		removals := func() (n int) {
			tx(t, s, func(tx *store.Tx) (err error) { n, err = countRows(tx, "permission_removals"); return err })
			return n
		}
		tx(t, s, func(tx *store.Tx) error {
			if err := seedInstance(tx, "d1", "i1"); err != nil {
				return err
			}
			return tx.DeleteInstance("i1")
		})
		if removals() != 1 {
			t.Fatalf("with permissions-v2 the rights are queued for removal")
		}
		s.DisablePermissionRemovals()
		tx(t, s, func(tx *store.Tx) error {
			if err := seedInstance(tx, "d1", "i2"); err != nil {
				return err
			}
			return tx.DeleteInstance("i2")
		})
		if removals() != 1 {
			t.Errorf("without permissions-v2 nothing more is queued")
		}
	})
}

// A health check with interval 0 checks every module again, also one checked in the same
// millisecond; otherwise a fast second check skips it.
func TestModuleCheckedAtTheCutoffIsDue(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		tx(t, s, func(tx *store.Tx) error {
			if err := seedInstance(tx, "d1", "i1"); err != nil {
				return err
			}
			if err := tx.UpsertModule(model.Module{Id: "m1", Namespace: "processes", InstanceId: "i1", Owner: "alice", ModuleType: "analytics", CreatedAt: t0, UpdatedAt: t0}); err != nil {
				return err
			}
			if err := tx.SetModuleCheck("m1", "not running", t0); err != nil {
				return err
			}
			due, err := tx.ModulesToCheck("analytics", t0, 10)
			if err != nil {
				return err
			}
			if len(due) != 1 {
				return fmt.Errorf("a module checked at the cutoff is due: %v", due)
			}
			return nil
		})
	})
}
