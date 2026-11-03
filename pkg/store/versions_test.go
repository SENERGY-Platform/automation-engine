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
	"slices"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

// seedVersions stores a deployment in the namespace with versions 1 to 5, each with an event
// description and a device group of its own, and instances:
//   - i1 runs on version 1;
//   - i2 ended on version 2;
//   - i3 runs on version 5 since a restart, its first run on version 3;
//   - version 4 has none.
func seedVersions(tx *store.Tx, namespace string) error {
	d := model.Deployment{Id: "d", Namespace: namespace, Name: "d", Owner: "alice", Version: 5, CreatedAt: t0, UpdatedAt: t0}
	if err := tx.InsertDeployment(d); err != nil {
		return err
	}
	for version := 1; version <= 5; version++ {
		if err := tx.InsertDeploymentVersion(model.DeploymentVersion{DeploymentId: "d", Version: version, Xml: "<x/>", CreatedAt: t0}); err != nil {
			return err
		}
		description := model.EventDescription{Id: fmt.Sprintf("e%v", version), DeploymentId: "d", Version: version, ElementId: "wait", Namespace: namespace, Owner: "alice",
			MessageName: "d_wait", DeviceGroupId: fmt.Sprintf("g%v", version), Topic: "t", Script: "true", ValueVariable: "v"}
		if err := tx.ReplaceEventDescriptions("d", version, []model.EventDescription{description}, []string{fmt.Sprintf("g%v", version)}, t0); err != nil {
			return err
		}
	}
	for _, i := range []struct {
		id      string
		version int
		state   model.InstanceState
	}{{"i1", 1, model.StateRunning}, {"i2", 2, model.StateCompleted}, {"i3", 5, model.StateRunning}} {
		inst := store.Instance{Instance: model.Instance{Id: i.id, Namespace: namespace, Owner: "alice", DeploymentId: "d", DeploymentVersion: i.version, Run: 1, State: i.state,
			CreatedAt: t0, StartedAt: t0, LastActivityAt: t0}, ActivityMinSeq: 1}
		if err := tx.InsertInstance(inst); err != nil {
			return err
		}
	}
	return tx.InsertRun(model.Run{InstanceId: "i3", Run: 1, DeploymentVersion: 3, Cause: model.CauseStart, State: model.StateTerminated, StartedAt: t0})
}

func described(tx *store.Tx) (versions []int, groups []string, err error) {
	if versions, err = tx.DescribedVersions("d"); err != nil {
		return nil, nil, err
	}
	groups, err = tx.DeviceGroupsOfDeployment("d", 0)
	return versions, groups, err
}

func storedVersions(tx *store.Tx) ([]int, error) {
	page, err := tx.ListDeploymentVersions("d", model.PageOptions{})
	result := []int{}
	for _, v := range page.Items {
		result = append(result, v.Version)
	}
	slices.Sort(result)
	return result, err
}

// An older version keeps its event descriptions while instances run on it and itself while an
// instance or a run refers to it; the current version stays (DEP-04).
func TestUnusedVersionsGo(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		err := s.Tx(context.Background(), func(tx *store.Tx) error {
			if err := seedVersions(tx, "processes"); err != nil {
				return err
			}
			_, _, before, err := tx.EventDescriptionsSince(0)
			if err != nil {
				return err
			}
			if err = tx.RemoveUnusedVersions("d", nil, t0); err != nil {
				return err
			}
			versions, groups, err := described(tx)
			if err != nil || !slices.Equal(versions, []int{1, 5}) || !slices.Equal(groups, []string{"g1", "g5"}) {
				return fmt.Errorf("descriptions and groups of the versions with running instances and the current one: %v %v %v", versions, groups, err)
			}
			_, removed, _, err := tx.EventDescriptionsSince(before)
			if slices.Sort(removed); err != nil || !slices.Equal(removed, []string{"e2", "e3", "e4"}) {
				return fmt.Errorf("the event workers learn of the removed descriptions: %v %v", removed, err)
			}
			stored, err := storedVersions(tx)
			if err != nil || !slices.Equal(stored, []int{1, 2, 3, 5}) {
				return fmt.Errorf("versions an instance or a run refers to and the current one: %v %v", stored, err)
			}
			if ids, err := tx.DeploymentsOfDeviceGroup("g1"); err != nil || !slices.Equal(ids, []string{"d"}) {
				return fmt.Errorf("a group of an older version with running instances still counts: %v %v", ids, err)
			}

			// the retention removes the ended instance, the running one ends
			if err = tx.DeleteInstance("i2"); err != nil {
				return err
			}
			i1, err := tx.GetInstance("i1", true)
			if err != nil {
				return err
			}
			i1.State = model.StateCompleted
			if err = tx.UpdateInstance(i1); err != nil {
				return err
			}
			if err = tx.RemoveUnusedVersions("", nil, t0); err != nil {
				return err
			}
			if versions, groups, err = described(tx); err != nil || !slices.Equal(versions, []int{5}) || !slices.Equal(groups, []string{"g5"}) {
				return fmt.Errorf("only the current version stays described: %v %v %v", versions, groups, err)
			}
			if stored, err = storedVersions(tx); err != nil || !slices.Equal(stored, []int{1, 3, 5}) {
				return fmt.Errorf("the ended instance keeps its version for its history: %v %v", stored, err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}

// In a namespace with keep_versions, versions are releases that users delete themselves; the
// descriptions of a version without running instances go all the same (DEP-04, SMS-03).
func TestKeptVersionsStay(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		err := s.Tx(context.Background(), func(tx *store.Tx) error {
			if err := seedVersions(tx, "smart-services"); err != nil {
				return err
			}
			if err := tx.RemoveUnusedVersions("", []string{"smart-services"}, t0); err != nil {
				return err
			}
			versions, _, err := described(tx)
			if err != nil || !slices.Equal(versions, []int{1, 5}) {
				return fmt.Errorf("descriptions as in other namespaces: %v %v", versions, err)
			}
			stored, err := storedVersions(tx)
			if err != nil || !slices.Equal(stored, []int{1, 2, 3, 4, 5}) {
				return fmt.Errorf("every version stays: %v %v", stored, err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}
