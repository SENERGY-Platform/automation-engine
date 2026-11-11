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
	"slices"
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
			if _, err := tx.SetModuleCheck("m1", "not running", t0, t0); err != nil {
				return err
			}
			due, err := tx.ModulesToCheck("analytics", t0, t0, 10)
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

// A module counts as checked when it is written: its pipeline may still be starting, and the
// workers checked a new module only at their next pass. A replaced resource drops the error.
func TestWrittenModuleIsDueOnlyAfterTheInterval(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		tx(t, s, func(tx *store.Tx) error {
			if err := seedInstance(tx, "d1", "i1"); err != nil {
				return err
			}
			m := model.Module{Id: "m1", Namespace: "processes", InstanceId: "i1", Owner: "alice", ModuleType: "analytics", CreatedAt: t0, UpdatedAt: t0}
			if err := tx.UpsertModule(m); err != nil {
				return err
			}
			if due, err := tx.ModulesToCheck("analytics", t0.Add(-time.Minute), t0.Add(-time.Minute), 10); err != nil || len(due) != 0 {
				return fmt.Errorf("a new module is not due before the interval: %v %v", due, err)
			}
			if due, err := tx.ModulesToCheck("analytics", t0, t0, 10); err != nil || len(due) != 1 {
				return fmt.Errorf("a new module is due after the interval: %v %v", due, err)
			}
			if err := tx.SetModuleError("m1", "not running", t0.Add(time.Hour)); err != nil {
				return err
			}
			m.UpdatedAt = t0.Add(2 * time.Hour)
			if err := tx.UpsertModule(m); err != nil {
				return err
			}
			due, err := tx.ModulesToCheck("analytics", t0.Add(2*time.Hour), t0.Add(2*time.Hour), 10)
			if err != nil || len(due) != 1 || due[0].Error != "" {
				return fmt.Errorf("a replaced resource drops the error and counts as checked when written: %+v %v", due, err)
			}
			return nil
		})
	})
}

// A finding becomes the error only at a check at least the recheck interval after the first, and
// a module with a finding is due again after that interval instead of the hourly one (SMS-11).
func TestModuleFindingIsConfirmedByALaterCheck(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		tx(t, s, func(tx *store.Tx) error {
			if err := seedInstance(tx, "d1", "i1"); err != nil {
				return err
			}
			if err := tx.UpsertModule(model.Module{Id: "m1", Namespace: "processes", InstanceId: "i1", Owner: "alice", ModuleType: "analytics", CreatedAt: t0, UpdatedAt: t0}); err != nil {
				return err
			}
			recheck := 5 * time.Minute
			hourly := t0.Add(-time.Hour) // the regular interval makes nothing due below
			var confirmations []time.Time
			check := func(finding string, at time.Time) error {
				confirmed, err := tx.SetModuleCheck("m1", finding, at, at.Add(-recheck))
				if confirmed {
					confirmations = append(confirmations, at)
				}
				return err
			}
			shown := func() string {
				errs, err := tx.ModuleErrors([]string{"i1"})
				if err != nil {
					return "read failed: " + err.Error()
				}
				return errs["i1"].Error
			}
			first := t0.Add(time.Hour)
			if err := check("not running", first); err != nil {
				return err
			}
			if shown() != "" {
				return fmt.Errorf("a first finding is only suspected: %q", shown())
			}
			if due, err := tx.ModulesToCheck("analytics", hourly, first.Add(-time.Second), 10); err != nil || len(due) != 0 {
				return fmt.Errorf("a suspected module is not due before the recheck: %v %v", due, err)
			}
			if due, err := tx.ModulesToCheck("analytics", hourly, first, 10); err != nil || len(due) != 1 {
				return fmt.Errorf("a suspected module is due at the recheck: %v %v", due, err)
			}
			// a second replica checking at the same time
			if err := check("not running", first.Add(10*time.Second)); err != nil {
				return err
			}
			if shown() != "" {
				return fmt.Errorf("a finding within the recheck interval does not confirm: %q", shown())
			}
			if err := tx.SetModuleChecked("m1", first.Add(time.Minute)); err != nil {
				return err
			}
			if err := check("not running", first.Add(recheck)); err != nil {
				return err
			}
			if shown() != "not running" {
				return fmt.Errorf("a finding at the recheck confirms the first one, also past an undecided check: %q", shown())
			}
			if due, err := tx.ModulesToCheck("analytics", hourly, first.Add(recheck), 10); err != nil || len(due) != 1 {
				return fmt.Errorf("a module with an error is due at the recheck: %v %v", due, err)
			}
			if err := check("", first.Add(2*recheck)); err != nil {
				return err
			}
			if shown() != "" {
				return fmt.Errorf("a check without finding clears the error: %q", shown())
			}
			if due, err := tx.ModulesToCheck("analytics", hourly, first.Add(time.Hour), 10); err != nil || len(due) != 0 {
				return fmt.Errorf("a healthy module is due only at the regular interval: %v %v", due, err)
			}
			if err := check("not running", first.Add(3*recheck)); err != nil {
				return err
			}
			if shown() != "" {
				return fmt.Errorf("a finding after a recovery is suspected anew: %q", shown())
			}
			if err := check("not running", first.Add(4*recheck)); err != nil {
				return err
			}
			if err := check("still not running", first.Add(5*recheck)); err != nil {
				return err
			}
			if shown() != "still not running" {
				return fmt.Errorf("a later finding replaces the text of the error: %q", shown())
			}
			// the notification of a module error goes out once per error (SNRGY-4537)
			if expected := []time.Time{first.Add(recheck), first.Add(4 * recheck)}; !slices.Equal(confirmations, expected) {
				return fmt.Errorf("confirmed at %v, want %v", confirmations, expected)
			}
			return nil
		})
	})
}
