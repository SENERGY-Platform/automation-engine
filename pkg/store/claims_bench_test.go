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
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

// BenchmarkClaims measures how the replicas of the engine share the due work of a Postgres
// database (SNRGY-5134): each replica is a store with its own connection pool, as a pod has, and
// drains the same queue the way the engine's loop for it does. One operation drains the whole
// queue; besides the time it reports
//
//   - items/s: drained items per second,
//   - conflicts/item: claims that found the item taken by another replica,
//   - lockwait-max: the most backends waiting for a lock at once, sampled every millisecond,
//   - lockwait-%: the share of samples with at least one backend waiting for a lock.
//
// An item handled twice fails the benchmark. The work done for an item (send, execute, check)
// is a sleep of workDelay. Run it with e.g.
//
//	go test ./pkg/store -run '^$' -bench BenchmarkClaims -benchtime 3x
func BenchmarkClaims(b *testing.B) {
	const workDelay = time.Millisecond
	scenarios := []struct {
		name  string
		items int
		seed  func(tx *store.Tx, items int) error
		drain func(s *store.Store, handled *handledItems, conflicts *atomic.Int64) error
	}{
		{"notifications", 1000, seedNotifications, func(s *store.Store, h *handledItems, c *atomic.Int64) error {
			return drainNotifications(s, h, c, workDelay)
		}},
		{"notifications-skip-locked", 1000, seedNotifications, func(s *store.Store, h *handledItems, c *atomic.Int64) error {
			return drainNotificationsSkipLocked(s, h, workDelay)
		}},
		{"jobs", 1000, func(tx *store.Tx, items int) error { return seedJobs(tx, items, 1) }, func(s *store.Store, h *handledItems, c *atomic.Int64) error {
			return drainJobs(s, h, c, workDelay)
		}},
		{"jobs-10-per-instance", 1000, func(tx *store.Tx, items int) error { return seedJobs(tx, items, 10) }, func(s *store.Store, h *handledItems, c *atomic.Int64) error {
			return drainJobs(s, h, c, workDelay)
		}},
		{"watches", 500, seedDueWatches, func(s *store.Store, h *handledItems, c *atomic.Int64) error {
			return drainWatches(s, h, workDelay)
		}},
		{"module-removals", 500, seedRemovals, func(s *store.Store, h *handledItems, c *atomic.Int64) error {
			return drainRemovals(s, h, workDelay)
		}},
	}
	for _, scenario := range scenarios {
		for _, replicas := range []int{1, 2, 4} {
			b.Run(fmt.Sprintf("%v/replicas=%v", scenario.name, replicas), func(b *testing.B) {
				first, dsn := storetest.OpenPostgresDsn(b)
				stores := []*store.Store{first}
				for len(stores) < replicas {
					s, err := store.Open(context.Background(), store.Postgres, dsn)
					if err != nil {
						b.Fatal(err)
					}
					b.Cleanup(func() { s.Close() })
					stores = append(stores, s)
				}
				for _, s := range stores {
					// as POSTGRES_MAX_CONNS of the cloud deployment
					s.SetMaxOpenConns(8)
				}
				admin, err := sql.Open("pgx", dsn)
				if err != nil {
					b.Fatal(err)
				}
				b.Cleanup(func() { admin.Close() })

				var conflicts atomic.Int64
				var waits lockWaits
				b.ResetTimer()
				for range b.N {
					b.StopTimer()
					if err := first.Tx(context.Background(), func(tx *store.Tx) error {
						for _, table := range []string{"notifications", "jobs", "watches", "module_removals", "instances", "deployments"} {
							if err := store.ExecForTest(tx, "DELETE FROM "+table); err != nil {
								return err
							}
						}
						return scenario.seed(tx, scenario.items)
					}); err != nil {
						b.Fatal(err)
					}
					handled := &handledItems{counts: map[string]int{}}
					stop := waits.sample(admin)
					b.StartTimer()
					var wg sync.WaitGroup
					errs := make([]error, len(stores))
					for i, s := range stores {
						wg.Add(1)
						go func() {
							defer wg.Done()
							errs[i] = scenario.drain(s, handled, &conflicts)
						}()
					}
					wg.Wait()
					b.StopTimer()
					stop()
					for _, err := range errs {
						if err != nil {
							b.Fatal(err)
						}
					}
					if n, twice := handled.check(); n != scenario.items || twice != "" {
						b.Fatalf("%v of %v items handled, %q more than once", n, scenario.items, twice)
					}
					b.StartTimer()
				}
				b.StopTimer()
				total := float64(scenario.items * b.N)
				b.ReportMetric(total/b.Elapsed().Seconds(), "items/s")
				b.ReportMetric(float64(conflicts.Load())/total, "conflicts/item")
				b.ReportMetric(float64(waits.max.Load()), "lockwait-max")
				b.ReportMetric(waits.share(), "lockwait-%")
			})
		}
	}
}

// the time the benchmark drains a queue at; every seeded item is due
var claimNow = t0.Add(time.Minute)

type handledItems struct {
	mux    sync.Mutex
	counts map[string]int
}

func (this *handledItems) add(id string) {
	this.mux.Lock()
	defer this.mux.Unlock()
	this.counts[id]++
}

// check returns the number of items handled and one handled more than once
func (this *handledItems) check() (int, string) {
	this.mux.Lock()
	defer this.mux.Unlock()
	for id, n := range this.counts {
		if n > 1 {
			return len(this.counts), id
		}
	}
	return len(this.counts), ""
}

type lockWaits struct {
	max     atomic.Int64
	samples atomic.Int64
	waiting atomic.Int64
}

// sample counts the backends of the database waiting for a lock until the returned stop
func (this *lockWaits) sample(admin *sql.DB) (stop func()) {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
			}
			var n int64
			if admin.QueryRow("SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'").Scan(&n) != nil {
				continue
			}
			this.samples.Add(1)
			if n > 0 {
				this.waiting.Add(1)
			}
			for {
				current := this.max.Load()
				if n <= current || this.max.CompareAndSwap(current, n) {
					break
				}
			}
		}
	}()
	return func() {
		close(done)
		<-finished
	}
}

func (this *lockWaits) share() float64 {
	if this.samples.Load() == 0 {
		return 0
	}
	return 100 * float64(this.waiting.Load()) / float64(this.samples.Load())
}

func seedNotifications(tx *store.Tx, items int) error {
	for i := range items {
		if err := tx.InsertNotification(store.Notification{Id: fmt.Sprint("n-", i), UserId: "alice", Title: "t", Message: "m", Topic: "incident", CreatedAt: t0}); err != nil {
			return err
		}
	}
	return nil
}

// drainNotifications is the loop of Engine.ProcessNotifications: read a batch, then claim, send
// and delete each in transactions of their own
func drainNotifications(s *store.Store, handled *handledItems, conflicts *atomic.Int64, work time.Duration) error {
	ctx := context.Background()
	for {
		var due []store.Notification
		if err := s.Tx(ctx, func(tx *store.Tx) (err error) {
			due, err = tx.DueNotifications(claimNow, 100)
			return err
		}); err != nil || len(due) == 0 {
			return err
		}
		for _, n := range due {
			var claimed bool
			if err := s.Tx(ctx, func(tx *store.Tx) (err error) {
				claimed, err = tx.ClaimNotification(n.Id, n.DueAt, claimNow.Add(2*time.Minute))
				return err
			}); err != nil {
				return err
			}
			if !claimed {
				conflicts.Add(1)
				continue
			}
			time.Sleep(work)
			handled.add(n.Id)
			if err := s.Tx(ctx, func(tx *store.Tx) error { return tx.DeleteNotification(n.Id) }); err != nil {
				return err
			}
		}
	}
}

// drainNotificationsSkipLocked reads and claims a batch in one statement instead
func drainNotificationsSkipLocked(s *store.Store, handled *handledItems, work time.Duration) error {
	ctx := context.Background()
	for {
		var ids []string
		if err := s.Tx(ctx, func(tx *store.Tx) (err error) {
			ids, err = store.ClaimNotificationsSkipLockedForTest(tx, claimNow, claimNow.Add(2*time.Minute), 100)
			return err
		}); err != nil || len(ids) == 0 {
			return err
		}
		for _, id := range ids {
			time.Sleep(work)
			handled.add(id)
			if err := s.Tx(ctx, func(tx *store.Tx) error { return tx.DeleteNotification(id) }); err != nil {
				return err
			}
		}
	}
}

func seedInstances(tx *store.Tx, instances int) error {
	if err := tx.InsertDeployment(model.Deployment{Id: "d-1", Namespace: "processes", Name: "d", Owner: "alice", Version: 1, CreatedAt: t0, UpdatedAt: t0}); err != nil {
		return err
	}
	for i := range instances {
		if err := tx.InsertInstance(store.Instance{Instance: model.Instance{Id: fmt.Sprint("i-", i), Namespace: "processes", Owner: "alice", DeploymentId: "d-1",
			DeploymentName: "d", DeploymentVersion: 1, Run: 1, State: model.StateRunning, CreatedAt: t0, StartedAt: t0, LastActivityAt: t0}, ActivityMinSeq: 1}); err != nil {
			return err
		}
	}
	return nil
}

func seedJobs(tx *store.Tx, items int, perInstance int) error {
	if err := seedInstances(tx, items/perInstance); err != nil {
		return err
	}
	for i := range items {
		if err := tx.SaveJob(store.Job{Id: fmt.Sprint("j-", i), InstanceId: fmt.Sprint("i-", i/perInstance), Run: 1, TokenId: fmt.Sprint("t-", i), ElementId: "task",
			TaskType: "work", Input: map[string]any{}, State: store.JobPending, Retries: 3, Timeout: time.Minute, DueAt: t0, CreatedAt: t0}); err != nil {
			return err
		}
	}
	return nil
}

// drainJobs is the loop of Engine.runJobs and Engine.claim: read the candidates, then claim each
// under the lock of its instance, execute it and complete it under that lock again
func drainJobs(s *store.Store, handled *handledItems, conflicts *atomic.Int64, work time.Duration) error {
	ctx := context.Background()
	for {
		var candidates []store.Job
		if err := s.Tx(ctx, func(tx *store.Tx) (err error) {
			candidates, err = tx.JobCandidates(claimNow, 100)
			return err
		}); err != nil || len(candidates) == 0 {
			return err
		}
		for _, candidate := range candidates {
			var claimed *store.Job
			if err := s.Tx(ctx, func(tx *store.Tx) error {
				job, err := tx.GetJob(candidate.Id)
				if err != nil || job == nil {
					return err
				}
				if _, err = tx.GetInstance(job.InstanceId, true); err != nil {
					return err
				}
				job, err = tx.GetJob(candidate.Id)
				if err != nil || job == nil || job.State != store.JobPending || job.DueAt.After(claimNow) {
					return err
				}
				job.State, job.Attempt = store.JobRunning, job.Attempt+1
				job.LockOwner, job.LockUntil = "replica", claimNow.Add(job.Timeout)
				claimed = job
				return tx.SaveJob(*job)
			}); err != nil {
				return err
			}
			if claimed == nil {
				conflicts.Add(1)
				continue
			}
			time.Sleep(work)
			handled.add(claimed.Id)
			if err := s.Tx(ctx, func(tx *store.Tx) error {
				if _, err := tx.GetInstance(claimed.InstanceId, true); err != nil {
					return err
				}
				return tx.DeleteJob(claimed.Id)
			}); err != nil {
				return err
			}
		}
	}
}

func seedDueWatches(tx *store.Tx, items int) error {
	if err := seedInstances(tx, items); err != nil {
		return err
	}
	for i := range items {
		if err := tx.SaveWatch(store.Watch{Id: fmt.Sprint("w-", i), InstanceId: fmt.Sprint("i-", i), Namespace: "smart-services", Owner: "alice",
			Procedure: "p", Parameters: map[string]any{}, Interval: time.Hour, NextAt: t0, CreatedAt: t0}); err != nil {
			return err
		}
	}
	return nil
}

// drainWatches is the loop of Runtime.CheckWatches: claim a batch, check each, store the result
func drainWatches(s *store.Store, handled *handledItems, work time.Duration) error {
	ctx := context.Background()
	for {
		var due []store.Watch
		if err := s.Tx(ctx, func(tx *store.Tx) (err error) {
			due, err = tx.ClaimDueWatches(claimNow, 20)
			return err
		}); err != nil || len(due) == 0 {
			return err
		}
		for _, w := range due {
			time.Sleep(work)
			handled.add(w.Id)
			if err := s.Tx(ctx, func(tx *store.Tx) error { return tx.UpdateWatch(w.Id, "hash", "") }); err != nil {
				return err
			}
		}
	}
}

func seedRemovals(tx *store.Tx, items int) error {
	var ids []string
	for i := range items {
		ids = append(ids, fmt.Sprint("m-", i))
	}
	return queueRemovals(tx, ids...)
}

// drainRemovals is the loop of Remover.Process: claim a batch, delete each resource, finish it
func drainRemovals(s *store.Store, handled *handledItems, work time.Duration) error {
	ctx := context.Background()
	for {
		var due []store.ModuleRemoval
		if err := s.Tx(ctx, func(tx *store.Tx) (err error) {
			due, err = tx.ClaimModuleRemovals(claimNow, claimNow.Add(6*time.Minute), 10)
			return err
		}); err != nil || len(due) == 0 {
			return err
		}
		for _, r := range due {
			time.Sleep(work)
			handled.add(r.ModuleId)
			if err := s.Tx(ctx, func(tx *store.Tx) error { return tx.FinishModuleRemoval(r.ModuleId) }); err != nil {
				return err
			}
		}
	}
}
