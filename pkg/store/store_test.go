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
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

var ctx = context.Background()
var t0 = time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)

func tx(t *testing.T, s *store.Store, fn func(tx *store.Tx) error) {
	t.Helper()
	if err := s.Tx(ctx, fn); err != nil {
		t.Fatal(err)
	}
}

func TestModelsArePagedSearchedAndFilteredByAccess(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		tx(t, s, func(tx *store.Tx) error {
			for i, name := range []string{"Heating", "heat pump", "Light", "100%_sure"} {
				owner := "alice"
				if i == 2 {
					owner = "bob"
				}
				m := model.Model{Id: fmt.Sprint("m", i), Namespace: "processes", Name: name, Owner: owner, Version: 1, CreatedAt: t0, UpdatedAt: t0}
				if err := tx.InsertModel(m); err != nil {
					return err
				}
			}
			return tx.InsertModel(model.Model{Id: "other_ns", Namespace: "smart-services", Name: "Heat", Owner: "alice", Version: 1, CreatedAt: t0, UpdatedAt: t0})
		})
		tx(t, s, func(tx *store.Tx) error {
			page, err := tx.ListModels("processes", model.ModelListOptions{Search: "HEAT", PageOptions: model.PageOptions{Limit: 1}}, store.Access{Owner: "alice"})
			if err != nil {
				return err
			}
			if page.Total != 2 || len(page.Items) != 1 || page.Items[0].Name != "heat pump" {
				return fmt.Errorf("unexpected page %+v", page)
			}
			page, err = tx.ListModels("processes", model.ModelListOptions{Search: "%_"}, store.Access{Owner: "alice"})
			if err != nil || page.Total != 1 || page.Items[0].Id != "m3" {
				return fmt.Errorf("search must escape wildcards: %+v %v", page, err)
			}
			page, err = tx.ListModels("processes", model.ModelListOptions{Sort: "name.desc"}, store.Access{Owner: "carol", Ids: []string{"m2"}})
			if err != nil || page.Total != 1 || page.Items[0].Id != "m2" {
				return fmt.Errorf("granted ids must be visible: %+v %v", page, err)
			}
			page, err = tx.ListModels("processes", model.ModelListOptions{}, store.Access{Admin: true})
			if err != nil || page.Total != 4 {
				return fmt.Errorf("admin sees all of the namespace: %+v %v", page, err)
			}
			_, err = tx.ListModels("processes", model.ModelListOptions{Sort: "owner"}, store.Access{Admin: true})
			if !errors.Is(err, model.ErrBadRequest) {
				return fmt.Errorf("unknown sort field must be rejected, got %v", err)
			}
			return nil
		})
	})
}

func seedInstance(tx *store.Tx, deploymentId string, instanceId string) error {
	if _, err := tx.GetDeployment(deploymentId, false); errors.Is(err, model.ErrNotFound) {
		d := model.Deployment{Id: deploymentId, Namespace: "processes", Name: "d", Owner: "alice", Version: 1, CreatedAt: t0, UpdatedAt: t0}
		if err := tx.InsertDeployment(d); err != nil {
			return err
		}
		if err := tx.InsertDeploymentVersion(model.DeploymentVersion{DeploymentId: deploymentId, Version: 1, Xml: "<x/>", CreatedAt: t0}); err != nil {
			return err
		}
	}
	i := store.Instance{Instance: model.Instance{Id: instanceId, Namespace: "processes", Owner: "alice", DeploymentId: deploymentId, DeploymentVersion: 1, Run: 1, State: model.StateRunning, CreatedAt: t0, StartedAt: t0, LastActivityAt: t0}, ActivityMinSeq: 1}
	return tx.InsertInstance(i)
}

func TestDeletingDeploymentCascadesToAllInstanceData(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		tx(t, s, func(tx *store.Tx) error {
			if err := seedInstance(tx, "d1", "i1"); err != nil {
				return err
			}
			for _, err := range []error{
				tx.InsertRun(model.Run{InstanceId: "i1", Run: 1, DeploymentVersion: 1, Cause: model.CauseStart, State: model.StateRunning, StartedAt: t0}),
				tx.SaveToken(store.Token{Id: "tok", InstanceId: "i1", Run: 1, ElementId: "e", State: store.TokenWaiting, CreatedAt: t0}),
				tx.SetVariable("i1", "x", 1.0, "e", t0),
				tx.InsertSubscription(store.Subscription{Id: "sub", InstanceId: "i1", Run: 1, TokenId: "tok", ElementId: "e", Kind: store.SubscriptionTimer, DueAt: &t0, CreatedAt: t0}),
				tx.SaveJob(store.Job{Id: "job", InstanceId: "i1", Run: 1, TokenId: "tok", ElementId: "e", TaskType: "x", State: store.JobPending, DueAt: t0, CreatedAt: t0}),
				tx.InsertActivity(model.Activity{InstanceId: "i1", Seq: 1, Run: 1, Time: t0, Kind: model.ActivityRunStarted}),
				tx.CountElementPass("i1", 1, "e", t0),
				tx.InsertIncident(model.Incident{Id: "inc", Namespace: "processes", Owner: "alice", InstanceId: "i1", Run: 1, DeploymentId: "d1", Message: "boom", Time: t0}),
				tx.ReplaceStartEvents("d1", []store.StartEvent{{Version: 1, ElementId: "s", Kind: "message", MessageName: "m"}}),
			} {
				if err != nil {
					return err
				}
			}
			return tx.DeleteDeployment("d1")
		})
		tx(t, s, func(tx *store.Tx) error {
			for _, table := range []string{"instances", "runs", "tokens", "variables", "subscriptions", "jobs", "activities", "element_stats", "incidents", "start_events", "deployment_versions"} {
				n, err := countRows(tx, table)
				if err != nil {
					return err
				}
				if n != 0 {
					return fmt.Errorf("%v still has %v rows", table, n)
				}
			}
			return nil
		})
	})
}

func countRows(tx *store.Tx, table string) (int, error) {
	return store.CountForTest(tx, table)
}

func TestVariablesAreUpsertedAndSized(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		tx(t, s, func(tx *store.Tx) error {
			if err := seedInstance(tx, "d1", "i1"); err != nil {
				return err
			}
			if err := tx.SetVariable("i1", "x", map[string]any{"a": 1.0}, "e1", t0); err != nil {
				return err
			}
			if err := tx.SetVariable("i1", "x", "abc", "e2", t0.Add(time.Second)); err != nil {
				return err
			}
			vars, err := tx.GetVariables("i1")
			if err != nil {
				return err
			}
			if len(vars) != 1 || vars[0].Value != "abc" || vars[0].Type != "string" || vars[0].ElementId != "e2" {
				return fmt.Errorf("unexpected %+v", vars)
			}
			size, err := tx.VariablesSize("i1")
			if err != nil || size != len(`"abc"`) {
				return fmt.Errorf("unexpected size %v %v", size, err)
			}
			return nil
		})
	})
}

func TestReceiptsAreInsertedOncePerTarget(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		tx(t, s, func(tx *store.Tx) error {
			first, err := tx.InsertReceipt("msg", "i1", t0)
			if err != nil || !first {
				return fmt.Errorf("first insert: %v %v", first, err)
			}
			second, err := tx.InsertReceipt("msg", "i1", t0)
			if err != nil || second {
				return fmt.Errorf("second insert must be a duplicate: %v %v", second, err)
			}
			other, err := tx.InsertReceipt("msg", "i2", t0)
			if err != nil || !other {
				return fmt.Errorf("other target: %v %v", other, err)
			}
			n, err := tx.PurgeReceipts(t0.Add(time.Second))
			if err != nil || n != 2 {
				return fmt.Errorf("purge: %v %v", n, err)
			}
			return nil
		})
	})
}

func TestJobCandidatesIncludeExpiredLocks(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		tx(t, s, func(tx *store.Tx) error {
			if err := seedInstance(tx, "d1", "i1"); err != nil {
				return err
			}
			jobs := []store.Job{
				{Id: "due", State: store.JobPending, DueAt: t0},
				{Id: "later", State: store.JobPending, DueAt: t0.Add(time.Hour)},
				{Id: "locked", State: store.JobRunning, DueAt: t0, LockUntil: t0.Add(time.Hour)},
				{Id: "expired", State: store.JobWaiting, DueAt: t0, LockUntil: t0.Add(-time.Second)},
			}
			for _, j := range jobs {
				j.InstanceId, j.Run, j.TokenId, j.ElementId, j.TaskType, j.CreatedAt = "i1", 1, "tok", "e", "x", t0
				if err := tx.SaveJob(j); err != nil {
					return err
				}
			}
			candidates, err := tx.JobCandidates(t0, 10)
			if err != nil {
				return err
			}
			ids := map[string]bool{}
			for _, c := range candidates {
				ids[c.Id] = true
			}
			if len(ids) != 2 || !ids["due"] || !ids["expired"] {
				return fmt.Errorf("unexpected candidates %v", ids)
			}
			return nil
		})
	})
}

func TestMigrationsAreAppliedOnce(t *testing.T) {
	dsn := storetest.MemoryDsn()
	for range 2 {
		s, err := store.Open(ctx, store.SQLite, dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
	}
}

func TestEventDescriptionsAreReadByRevision(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		var first int64
		tx(t, s, func(tx *store.Tx) error {
			if err := seedInstance(tx, "d1", "i1"); err != nil {
				return err
			}
			descriptions := []model.EventDescription{
				{Id: "e1", DeploymentId: "d1", Version: 1, ElementId: "a", Namespace: "processes", Owner: "alice", MessageName: "d1_a", DeviceId: "x", ServiceId: "s", Topic: "t1", Script: "true", ValueVariable: "v", Variables: map[string]string{"k": "1"}, AspectIds: []string{"air"}, Service: []byte(`{"id":"s"}`)},
				{Id: "e2", DeploymentId: "d1", Version: 1, ElementId: "a", Namespace: "processes", Owner: "alice", MessageName: "d1_a", DeviceGroupId: "g", Topic: "t2", Script: "true", ValueVariable: "v"},
			}
			if err := tx.ReplaceEventDescriptions("d1", descriptions, []string{"g"}, t0); err != nil {
				return err
			}
			all, removed, latest, err := tx.EventDescriptionsSince(0)
			if err != nil || len(all) != 2 || len(removed) != 0 || all[0].Variables["k"] != "1" || all[0].AspectIds[0] != "air" || string(all[0].Service) != `{"id":"s"}` {
				return fmt.Errorf("unexpected %+v %v %v", all, removed, err)
			}
			first = latest
			deployments, err := tx.DeploymentsOfDeviceGroup("g")
			if err != nil || len(deployments) != 1 {
				return fmt.Errorf("groups: %v %v", deployments, err)
			}
			return tx.ReplaceEventDescriptions("d1", descriptions[:1], nil, t0)
		})
		tx(t, s, func(tx *store.Tx) error {
			changed, removed, latest, err := tx.EventDescriptionsSince(first)
			if err != nil || latest <= first || len(changed) != 1 || len(removed) != 2 {
				return fmt.Errorf("a replacement removes all and adds the new ones: %+v %v %v %v", changed, removed, latest, err)
			}
			if err = tx.DeleteDeployment("d1"); err != nil {
				return err
			}
			_, removed, _, err = tx.EventDescriptionsSince(latest)
			if err != nil || len(removed) != 1 {
				return fmt.Errorf("deleting the deployment leaves tombstones: %v %v", removed, err)
			}
			return nil
		})
	})
}

func TestSubtasksKeepResults(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		tx(t, s, func(tx *store.Tx) error {
			if err := seedInstance(tx, "d1", "i1"); err != nil {
				return err
			}
			if err := tx.SaveJob(store.Job{Id: "job", InstanceId: "i1", Run: 1, TokenId: "t", ElementId: "e", TaskType: "x", State: store.JobRunning, DueAt: t0, CreatedAt: t0}); err != nil {
				return err
			}
			subtasks := []store.Subtask{{JobId: "job", SubId: "job_0_0", Position: 0, Command: []byte(`{}`), TaskInfo: []byte(`{}`)}, {JobId: "job", SubId: "job_1_0", Position: 1, Command: []byte(`{}`), TaskInfo: []byte(`{}`)}}
			if err := tx.EnsureSubtasks(subtasks); err != nil {
				return err
			}
			if ok, err := tx.SetSubtaskResult("job", "job_1_0", 2.5); err != nil || !ok {
				return fmt.Errorf("set result: %v %v", ok, err)
			}
			if err := tx.EnsureSubtasks(subtasks); err != nil {
				return err
			}
			list, err := tx.ListSubtasks("job")
			if err != nil || len(list) != 2 || list[0].HasResult || !list[1].HasResult || list[1].Result != 2.5 {
				return fmt.Errorf("a retry keeps the results: %+v %v", list, err)
			}
			if ok, _ := tx.SetSubtaskResult("job", "unknown", 1); ok {
				return fmt.Errorf("unknown sub-tasks are reported")
			}
			if err = tx.DeleteJob("job"); err != nil {
				return err
			}
			list, err = tx.ListSubtasks("job")
			if err != nil || len(list) != 0 {
				return fmt.Errorf("sub-tasks go with the job: %+v %v", list, err)
			}
			return nil
		})
	})
}

func TestIoValuesAndSchedules(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		tx(t, s, func(tx *store.Tx) error {
			if err := seedInstance(tx, "d1", "i1"); err != nil {
				return err
			}
			for _, v := range []model.IoValue{{Key: "free", Value: 1.0, UpdatedAt: t0}, {Key: "bound", Value: "x", InstanceId: "i1", DeploymentId: "d1", UpdatedAt: t0}} {
				if err := tx.SetIoValue("alice", v); err != nil {
					return err
				}
			}
			if n, err := tx.CountIoValues("alice"); err != nil || n != 1 {
				return fmt.Errorf("only unbound values count: %v %v", n, err)
			}
			bound, err := tx.ListIoValues("alice", "", "i1", "key.desc")
			if err != nil || len(bound) != 1 || bound[0].DeploymentId != "d1" {
				return fmt.Errorf("bound: %+v %v", bound, err)
			}
			due := t0.Add(time.Minute)
			if err = tx.SaveSchedule(model.Schedule{Id: "s1", Namespace: "processes", DeploymentId: "d1", Owner: "alice", Cron: "* * * * *", NextDue: &due, CreatedAt: t0, UpdatedAt: t0}); err != nil {
				return err
			}
			list, err := tx.DueSchedules(t0.Add(2*time.Minute), 10)
			if err != nil || len(list) != 1 {
				return fmt.Errorf("due: %+v %v", list, err)
			}
			if err = tx.DeleteInstance("i1"); err != nil {
				return err
			}
			if _, ok, _ := tx.GetIoValue("alice", "bound"); ok {
				return fmt.Errorf("a bound value goes with its instance")
			}
			if err = tx.DeleteDeployment("d1"); err != nil {
				return err
			}
			if _, err = tx.GetSchedule("s1"); !errors.Is(err, model.ErrNotFound) {
				return fmt.Errorf("schedules go with the deployment: %v", err)
			}
			_, ok, err := tx.GetIoValue("alice", "free")
			if err != nil || !ok {
				return fmt.Errorf("unbound values stay: %v %v", ok, err)
			}
			return nil
		})
	})
}
