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
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

// A script is blocked once; every change moves the revision the replicas watch (EXPR-03).
func TestBlockedScripts(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		err := s.Tx(context.Background(), func(tx *store.Tx) error {
			before, err := tx.BlocklistRevision()
			if err != nil {
				return err
			}
			b := model.BlockedScript{Fingerprint: "f1", Source: "String(o)", Reason: "fatal error: stack overflow", Origin: "element boom of deployment d", CreatedAt: t0}
			if added, err := tx.BlockScript(b); err != nil || !added {
				return fmt.Errorf("blocked: %v %v", added, err)
			}
			if added, err := tx.BlockScript(b); err != nil || added {
				return fmt.Errorf("once: %v %v", added, err)
			}
			revision, err := tx.BlocklistRevision()
			if err != nil || revision != before+1 {
				return fmt.Errorf("revision %v after %v: %v", revision, before, err)
			}
			if err = tx.SetBlockedScriptScanned("f1", t0); err != nil {
				return err
			}
			list, err := tx.ListBlockedScripts()
			if err != nil || len(list) != 1 || list[0].Fingerprint != "f1" || list[0].Source != b.Source || list[0].ScannedAt == nil || !list[0].CreatedAt.Equal(t0) {
				return fmt.Errorf("%+v %v", list, err)
			}
			if removed, err := tx.UnblockScript("f1"); err != nil || !removed {
				return fmt.Errorf("unblocked: %v %v", removed, err)
			}
			if removed, err := tx.UnblockScript("f1"); err != nil || removed {
				return fmt.Errorf("nothing to unblock: %v %v", removed, err)
			}
			if revision, err = tx.BlocklistRevision(); err != nil || revision != before+2 {
				return fmt.Errorf("revision %v: %v", revision, err)
			}
			// the deployments an instance manages and the running instances of a version
			for _, d := range []model.Deployment{{Id: "m1", ManagedBy: "root"}, {Id: "m2", ManagedBy: "root"}, {Id: "own"}} {
				d.Namespace, d.Name, d.Owner, d.Version, d.CreatedAt, d.UpdatedAt = "processes", d.Id, "alice", 1, t0, t0
				if err = tx.InsertDeployment(d); err != nil {
					return err
				}
			}
			if ids, err := tx.ManagedDeployments("root"); err != nil || fmt.Sprint(ids) != "[m1 m2]" {
				return fmt.Errorf("managed: %v %v", ids, err)
			}
			for _, i := range []struct {
				id      string
				version int
				state   model.InstanceState
			}{{"i1", 1, model.StateRunning}, {"i2", 2, model.StateRunning}, {"i3", 1, model.StateCompleted}} {
				inst := store.Instance{Instance: model.Instance{Id: i.id, Namespace: "processes", Owner: "alice", DeploymentId: "m1", DeploymentVersion: i.version, Run: 1, State: i.state,
					CreatedAt: t0, StartedAt: t0, LastActivityAt: t0}, ActivityMinSeq: 1}
				if err = tx.InsertInstance(inst); err != nil {
					return err
				}
			}
			if ids, err := tx.RunningInstancesOf("m1", 1); err != nil || fmt.Sprint(ids) != "[i1]" {
				return fmt.Errorf("running on version 1: %v %v", ids, err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}

// Replicas that crashed on the same script add it at the same time: both succeed, one adds it.
func TestConcurrentBlocksOfOneScript(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		ctx := context.Background()
		var before int64
		if err := s.Tx(ctx, func(tx *store.Tx) (err error) { before, err = tx.BlocklistRevision(); return err }); err != nil {
			t.Fatal(err)
		}
		const replicas = 4
		added := make(chan bool, replicas)
		failed := make(chan error, replicas)
		start := make(chan struct{})
		// inside their transactions, the replicas wait for each other, so that they overlap;
		// SQLite runs one transaction at a time, there the wait times out
		var begun sync.WaitGroup
		begun.Add(replicas)
		allBegun := make(chan struct{})
		go func() { begun.Wait(); close(allBegun) }()
		for i := 0; i < replicas; i++ {
			go func() {
				<-start
				err := s.Tx(ctx, func(tx *store.Tx) error {
					if _, err := tx.BlocklistRevision(); err != nil {
						return err
					}
					begun.Done()
					select {
					case <-allBegun:
					case <-time.After(200 * time.Millisecond):
					}
					ok, err := tx.BlockScript(model.BlockedScript{Fingerprint: "f1", Source: "String(o)", Reason: "fatal error: stack overflow", Origin: "o", CreatedAt: t0})
					added <- ok && err == nil
					return err
				})
				failed <- err
			}()
		}
		close(start)
		for i := 0; i < replicas; i++ {
			if err := <-failed; err != nil {
				t.Fatal(err)
			}
		}
		close(added)
		count := 0
		for ok := range added {
			if ok {
				count++
			}
		}
		var revision int64
		if err := s.Tx(ctx, func(tx *store.Tx) (err error) { revision, err = tx.BlocklistRevision(); return err }); err != nil {
			t.Fatal(err)
		}
		if count != 1 || revision != before+1 {
			t.Fatalf("added %v times, revision %v after %v", count, revision, before)
		}
	})
}
