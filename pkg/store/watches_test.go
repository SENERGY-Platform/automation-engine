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

// The test of the watcher database (TestDb of smart-service-module-worker-watcher): due watches
// are handed out in batches, each once per interval.

func seedWatches(t *testing.T, s *store.Store, count int) {
	t.Helper()
	tx(t, s, func(tx *store.Tx) error {
		if err := seedInstance(tx, "d1", "i1"); err != nil {
			return err
		}
		for i := 0; i < count; i++ {
			err := tx.SaveWatch(store.Watch{Id: fmt.Sprint("w", i), InstanceId: "i1", Namespace: "smart-services", Owner: "alice", Procedure: "update",
				Request: model.WatchRequest{Method: "GET", Endpoint: "http://example.invalid"}, HashType: "md5", Interval: time.Hour, NextAt: t0, CreatedAt: t0})
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func claim(t *testing.T, s *store.Store, now time.Time, limit int) (result []store.Watch) {
	t.Helper()
	tx(t, s, func(tx *store.Tx) (err error) {
		result, err = tx.ClaimDueWatches(now, limit)
		return err
	})
	return result
}

func TestDueWatchesAreClaimedInBatchesEachOnce(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		seedWatches(t, s, 50)
		claimed := map[string]bool{}
		for batch := 0; batch < 5; batch++ {
			watches := claim(t, s, t0, 10)
			if len(watches) != 10 {
				t.Fatalf("batch %v: %v watches", batch, len(watches))
			}
			for _, w := range watches {
				if claimed[w.Id] {
					t.Fatalf("batch %v: %v claimed twice", batch, w.Id)
				}
				claimed[w.Id] = true
			}
		}
		if rest := claim(t, s, t0, 10); len(rest) != 0 {
			t.Errorf("all watches were claimed, still due: %v", len(rest))
		}
	})
}

func TestClaimedWatchIsDueAgainAfterItsInterval(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		seedWatches(t, s, 1)
		claim(t, s, t0, 10)
		if early := claim(t, s, t0.Add(time.Hour-time.Millisecond), 10); len(early) != 0 {
			t.Fatalf("due before its interval: %+v", early)
		}
		if due := claim(t, s, t0.Add(time.Hour), 10); len(due) != 1 {
			t.Errorf("not due after its interval: %+v", due)
		}
	})
}
