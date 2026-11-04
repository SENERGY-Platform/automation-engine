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
	"errors"
	"fmt"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

// A suspended condition reaches the event workers with a new revision, and so does its resumption
// (EXPR-03, crash brake).
func TestSuspendedEventDescriptionsReachTheWorkers(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		err := s.Tx(context.Background(), func(tx *store.Tx) error {
			if err := seedVersions(tx, "processes"); err != nil {
				return err
			}
			_, _, before, err := tx.EventDescriptionsSince(0)
			if err != nil {
				return err
			}
			d, suspended, err := tx.SuspendEventDescription("e5", "fatal error: stack overflow")
			if err != nil || !suspended || d.Suspended != "fatal error: stack overflow" || d.DeploymentId != "d" || d.Version != 5 {
				return fmt.Errorf("the suspended description: %+v %v", d, err)
			}
			if _, suspended, err = tx.SuspendEventDescription("e5", "again"); err != nil || suspended {
				return fmt.Errorf("suspended once: %v %v", suspended, err)
			}
			changed, _, latest, err := tx.EventDescriptionsSince(before)
			if err != nil || len(changed) != 1 || changed[0].Id != "e5" || changed[0].Suspended == "" {
				return fmt.Errorf("the workers learn of the suspension: %+v %v", changed, err)
			}
			// described again, e.g. after a change of a device group, the condition stays suspended
			again := changed[0]
			again.Id, again.Suspended = "e5b", ""
			if err = tx.ReplaceEventDescriptions("d", 5, []model.EventDescription{again}, nil, t0); err != nil {
				return err
			}
			changed, _, latest, err = tx.EventDescriptionsSince(latest)
			if err != nil || len(changed) != 1 || changed[0].Id != "e5b" || changed[0].Suspended != "fatal error: stack overflow" {
				return fmt.Errorf("described again, still suspended: %+v %v", changed, err)
			}
			if _, _, err = tx.SuspendEventDescription("gone", "x"); !errors.Is(err, model.ErrNotFound) {
				return fmt.Errorf("a removed description: %v", err)
			}
			if err = tx.ResumeEventDescriptions("d"); err != nil {
				return err
			}
			changed, _, after, err := tx.EventDescriptionsSince(latest)
			if err != nil || len(changed) != 1 || changed[0].Id != "e5b" || changed[0].Suspended != "" {
				return fmt.Errorf("the workers learn of the resumption: %+v %v", changed, err)
			}
			if err = tx.ResumeEventDescriptions("d"); err != nil {
				return err
			}
			if _, _, again, err := tx.EventDescriptionsSince(after); err != nil || again != after {
				return fmt.Errorf("nothing suspended, no new revision: %v %v", again, err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}

// A deployment can be quarantined before it exists, once, and the quarantine goes with it.
func TestQuarantinedDeployments(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		err := s.Tx(context.Background(), func(tx *store.Tx) error {
			q := store.Quarantine{DeploymentId: "d", Version: 2, ElementId: "start", Error: "fatal error: stack overflow", Since: t0}
			if added, err := tx.QuarantineDeployment(q); err != nil || !added {
				return fmt.Errorf("quarantined before the deployment exists: %v %v", added, err)
			}
			if added, err := tx.QuarantineDeployment(q); err != nil || added {
				return fmt.Errorf("once: %v %v", added, err)
			}
			if got, err := tx.GetQuarantine("d"); err != nil || got == nil || *got != q {
				return fmt.Errorf("%+v %v", got, err)
			}
			if got, err := tx.GetQuarantine("other"); err != nil || got != nil {
				return fmt.Errorf("none: %+v %v", got, err)
			}
			if lifted, err := tx.LiftQuarantine("d"); err != nil || !lifted {
				return fmt.Errorf("lifted: %v %v", lifted, err)
			}
			if lifted, err := tx.LiftQuarantine("d"); err != nil || lifted {
				return fmt.Errorf("nothing to lift: %v %v", lifted, err)
			}
			if err := tx.InsertDeployment(model.Deployment{Id: "d", Namespace: "processes", Name: "d", Owner: "alice", Version: 1, CreatedAt: t0, UpdatedAt: t0}); err != nil {
				return err
			}
			if _, err := tx.QuarantineDeployment(q); err != nil {
				return err
			}
			if err := tx.DeleteDeployment("d"); err != nil {
				return err
			}
			if got, err := tx.GetQuarantine("d"); err != nil || got != nil {
				return fmt.Errorf("the quarantine goes with the deployment: %+v %v", got, err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}
