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

// SNRGY-5143: a watch that fires while its procedure runs queues a rerun instead of a second run

// seedProcedures inserts the instance p with the children of state per procedure, as running
// procedure "<procedure>-<n>"
func seedProcedures(tx *store.Tx, children map[string]model.InstanceState) error {
	if err := tx.InsertDeployment(model.Deployment{Id: "d", Namespace: "smart-services", Name: "d", Owner: "alice", Version: 1, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		return err
	}
	insert := func(id string, parent string, procedure string, state model.InstanceState) error {
		return tx.InsertInstance(store.Instance{Instance: model.Instance{Id: id, Namespace: "smart-services", Owner: "alice", DeploymentId: "d",
			DeploymentName: "d", DeploymentVersion: 1, Run: 1, State: state, CreatedAt: t0, StartedAt: t0, LastActivityAt: t0, ParentId: parent}, ActivityMinSeq: 1, Procedure: procedure})
	}
	if err := insert("p", "", "", model.StateCompleted); err != nil {
		return err
	}
	if err := insert("other", "", "", model.StateCompleted); err != nil {
		return err
	}
	n := 0
	for procedure, state := range children {
		n++
		if err := insert(fmt.Sprint("c-", n), "p", procedure, state); err != nil {
			return err
		}
	}
	return nil
}

func rerun(instance string, procedure string, variables map[string]any, at time.Time) store.MaintenanceRerun {
	return store.MaintenanceRerun{InstanceId: instance, Procedure: procedure, Variables: variables, VariablesHash: store.RerunHash(variables), RequestedAt: at}
}

func TestOnlyARunningChildCountsAsARunningProcedure(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		tx(t, s, func(tx *store.Tx) error {
			if err := seedProcedures(tx, map[string]model.InstanceState{"running": model.StateRunning, "incident": model.StateIncident,
				"completed": model.StateCompleted, "terminated": model.StateTerminated}); err != nil {
				return err
			}
			for procedure, expected := range map[string]bool{"running": true, "incident": false, "completed": false, "terminated": false, "unknown": false} {
				if running, err := tx.MaintenanceRunning("p", procedure); err != nil || running != expected {
					return fmt.Errorf("%v: running %v %v", procedure, running, err)
				}
			}
			if running, err := tx.MaintenanceRunning("other", "running"); err != nil || running {
				return fmt.Errorf("the run of another instance counts: %v %v", running, err)
			}
			inst, err := tx.GetInstance("c-1", false)
			if err != nil || inst.Procedure == "" {
				return fmt.Errorf("a child reads with its procedure: %+v %v", inst, err)
			}
			return nil
		})
	})
}

func TestARerunIsClaimedOnceItsProcedureNoLongerRuns(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		now, until := t0.Add(time.Minute), t0.Add(2*time.Minute)
		tx(t, s, func(tx *store.Tx) error {
			if err := seedProcedures(tx, map[string]model.InstanceState{"refresh": model.StateRunning}); err != nil {
				return err
			}
			if err := tx.QueueMaintenanceRerun(rerun("p", "refresh", map[string]any{"level": 3.0}, t0)); err != nil {
				return err
			}
			if err := tx.QueueMaintenanceRerun(rerun("p", "refresh", map[string]any{"level": 3.0}, t0.Add(time.Second))); err != nil {
				return err
			}
			claimed, err := tx.ClaimMaintenanceReruns(now, until, 10)
			if err != nil || len(claimed) != 0 {
				return fmt.Errorf("nothing is claimed while the procedure runs: %+v %v", claimed, err)
			}
			if err = store.ExecForTest(tx, "UPDATE instances SET state = 'incident' WHERE id = 'c-1'"); err != nil {
				return err
			}
			claimed, err = tx.ClaimMaintenanceReruns(now, until, 10)
			if err != nil || len(claimed) != 1 || claimed[0].Procedure != "refresh" || claimed[0].Variables["level"] != 3.0 ||
				claimed[0].VariablesHash != store.RerunHash(map[string]any{"level": 3.0}) || !claimed[0].RequestedAt.Equal(t0.Add(time.Second)) {
				return fmt.Errorf("a second request of the same rerun is one rerun, claimed once its run is in an incident: %+v %v", claimed, err)
			}
			if again, err := tx.ClaimMaintenanceReruns(until.Add(-time.Millisecond), until, 10); err != nil || len(again) != 0 {
				return fmt.Errorf("nothing is due while claimed: %+v %v", again, err)
			}
			if err = tx.QueueMaintenanceRerun(rerun("p", "refresh", map[string]any{"level": 3.0}, t0.Add(time.Minute))); err != nil {
				return err
			}
			if again, err := tx.ClaimMaintenanceReruns(now, until, 10); err != nil || len(again) != 1 {
				return fmt.Errorf("a new request is due at once: %+v %v", again, err)
			}
			if queued, err := tx.MaintenanceRerunQueued("p", "refresh", claimed[0].VariablesHash); err != nil || !queued {
				return fmt.Errorf("a claimed rerun is still queued: %v %v", queued, err)
			}
			if err = tx.DeleteMaintenanceRerun("p", "refresh", claimed[0].VariablesHash); err != nil {
				return err
			}
			if queued, err := tx.MaintenanceRerunQueued("p", "refresh", claimed[0].VariablesHash); err != nil || queued {
				return fmt.Errorf("a started rerun is gone: %v %v", queued, err)
			}
			if again, err := tx.ClaimMaintenanceReruns(until, until.Add(time.Minute), 10); err != nil || len(again) != 0 {
				return fmt.Errorf("a started rerun is not claimed again: %+v %v", again, err)
			}
			return nil
		})
	})
}

func TestReRunsWithOtherVariablesAreKeptApart(t *testing.T) {
	if store.RerunHash(nil) != store.RerunHash(map[string]any{}) ||
		store.RerunHash(map[string]any{"a": 1.0, "b": "x"}) != store.RerunHash(map[string]any{"b": "x", "a": 1.0}) ||
		store.RerunHash(map[string]any{"a": 1.0}) == store.RerunHash(map[string]any{"a": 2.0}) {
		t.Fatal("the hash is that of the variables, without order")
	}
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		tx(t, s, func(tx *store.Tx) error {
			if err := seedProcedures(tx, nil); err != nil {
				return err
			}
			for _, variables := range []map[string]any{{"level": 3.0}, {"level": 4.0}, nil} {
				if err := tx.QueueMaintenanceRerun(rerun("p", "refresh", variables, t0)); err != nil {
					return err
				}
			}
			if claimed, err := tx.ClaimMaintenanceReruns(t0, t0.Add(time.Minute), 10); err != nil || len(claimed) != 3 {
				return fmt.Errorf("one rerun per variables: %+v %v", claimed, err)
			}
			return nil
		})
	})
}

func TestReRunsGoWithTheRunTheyWereOwedTo(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		tx(t, s, func(tx *store.Tx) error {
			if err := seedProcedures(tx, nil); err != nil {
				return err
			}
			if err := tx.QueueMaintenanceRerun(rerun("p", "refresh", nil, t0)); err != nil {
				return err
			}
			if err := tx.QueueMaintenanceRerun(rerun("other", "refresh", nil, t0)); err != nil {
				return err
			}
			if _, err := tx.RemoveModulesOfInstance("p"); err != nil {
				return err
			}
			if queued, err := tx.MaintenanceRerunQueued("p", "refresh", store.RerunHash(nil)); err != nil || queued {
				return fmt.Errorf("a new run of the instance drops its reruns: %v %v", queued, err)
			}
			if err := tx.DeleteInstance("other"); err != nil {
				return err
			}
			n, err := store.CountForTest(tx, "maintenance_reruns")
			if err != nil || n != 0 {
				return fmt.Errorf("deleting the instance deletes its reruns: %v %v", n, err)
			}
			return nil
		})
	})
}
