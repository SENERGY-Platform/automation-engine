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
	"reflect"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

// seedIncidents stores four incidents of alice on two instances of two deployments and one of
// bob; a and b are on i1, c and d on i2, at the times 0h, 3h, 1h and 2h after t0
func seedIncidents(t *testing.T, s *store.Store) {
	t.Helper()
	tx(t, s, func(tx *store.Tx) error {
		for _, seed := range [][2]string{{"d1", "i1"}, {"d2", "i2"}, {"d3", "i3"}} {
			if err := seedInstance(tx, seed[0], seed[1]); err != nil {
				return err
			}
		}
		for _, i := range []model.Incident{
			{Id: "a", Owner: "alice", InstanceId: "i1", Run: 1, DeploymentId: "d1", ElementId: "task", Time: t0},
			{Id: "b", Owner: "alice", InstanceId: "i1", Run: 2, DeploymentId: "d1", ElementId: "script", Time: t0.Add(3 * time.Hour)},
			{Id: "c", Owner: "alice", InstanceId: "i2", Run: 1, DeploymentId: "d2", ElementId: "task", Time: t0.Add(time.Hour)},
			{Id: "d", Owner: "alice", InstanceId: "i2", Run: 1, DeploymentId: "d2", ElementId: "script", Time: t0.Add(2 * time.Hour)},
			{Id: "e", Owner: "bob", InstanceId: "i3", Run: 1, DeploymentId: "d3", ElementId: "task", Time: t0},
		} {
			i.Namespace, i.DeploymentVersion, i.Message = "processes", 1, "error message"
			if err := tx.InsertIncident(i); err != nil {
				return err
			}
		}
		return nil
	})
}

func incidentIds(page model.Page[model.Incident]) []string {
	ids := []string{}
	for _, i := range page.Items {
		ids = append(ids, i.Id)
	}
	return ids
}

func listIncidents(t *testing.T, s *store.Store, options model.IncidentListOptions, access store.Access) (page model.Page[model.Incident]) {
	t.Helper()
	tx(t, s, func(tx *store.Tx) (err error) {
		page, err = tx.ListIncidents("processes", options, access)
		return err
	})
	return page
}

var alicesAccess = store.Access{Owner: "alice"}

func TestIncidentsAreFilteredByInstanceRunDeploymentAndElement(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		seedIncidents(t, s)
		cases := map[string]struct {
			options  model.IncidentListOptions
			expected []string
		}{
			"instance":         {model.IncidentListOptions{InstanceId: "i1", Sort: "time.asc"}, []string{"a", "b"}},
			"unknown instance": {model.IncidentListOptions{InstanceId: "unknown"}, []string{}},
			"run":              {model.IncidentListOptions{InstanceId: "i1", Run: 2}, []string{"b"}},
			"deployment":       {model.IncidentListOptions{DeploymentId: "d2", Sort: "time.asc"}, []string{"c", "d"}},
			"element":          {model.IncidentListOptions{ElementId: "task", Sort: "time.asc"}, []string{"a", "c"}},
		}
		for name, c := range cases {
			if got := incidentIds(listIncidents(t, s, c.options, alicesAccess)); !reflect.DeepEqual(got, c.expected) {
				t.Errorf("%v: expected %v, got %v", name, c.expected, got)
			}
		}
	})
}

func TestIncidentsAreNewestFirstUnlessSortedByTimeAscending(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		seedIncidents(t, s)
		cases := map[string][]string{
			"":          {"b", "d", "c", "a"},
			"time.desc": {"b", "d", "c", "a"},
			"time.asc":  {"a", "c", "d", "b"},
			"time":      {"a", "c", "d", "b"},
		}
		for sort, expected := range cases {
			if got := incidentIds(listIncidents(t, s, model.IncidentListOptions{Sort: sort}, alicesAccess)); !reflect.DeepEqual(got, expected) {
				t.Errorf("sort %q: expected %v, got %v", sort, expected, got)
			}
		}
	})
}

func TestIncidentsArePagedWithTheTotalOfAllMatches(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		seedIncidents(t, s)
		page := listIncidents(t, s, model.IncidentListOptions{Sort: "time.asc", PageOptions: model.PageOptions{Limit: 2, Offset: 1}}, alicesAccess)
		if got := incidentIds(page); page.Total != 4 || !reflect.DeepEqual(got, []string{"c", "d"}) {
			t.Fatalf("expected c, d of 4, got %v of %v", got, page.Total)
		}
	})
}

func TestIncidentsOfOtherOwnersAreNotListed(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		seedIncidents(t, s)
		if got := incidentIds(listIncidents(t, s, model.IncidentListOptions{}, store.Access{Owner: "bob"})); !reflect.DeepEqual(got, []string{"e"}) {
			t.Fatalf("expected only bob's incident, got %v", got)
		}
	})
}

func TestDeletingAnInstanceDeletesItsIncidentsOnly(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		seedIncidents(t, s)
		tx(t, s, func(tx *store.Tx) error {
			if err := tx.DeleteInstance("i1"); err != nil {
				return fmt.Errorf("delete: %w", err)
			}
			return nil
		})
		if got := incidentIds(listIncidents(t, s, model.IncidentListOptions{Sort: "time.asc"}, alicesAccess)); !reflect.DeepEqual(got, []string{"c", "d"}) {
			t.Fatalf("expected the incidents of i2 to remain, got %v", got)
		}
	})
}
