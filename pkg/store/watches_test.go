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

// Watches of a device group wait for announcements of device-repository instead of their
// interval once checked (SNRGY-5132).

func seedGroupWatches(t *testing.T, s *store.Store, groups map[string]string, members map[string][]string) {
	t.Helper()
	tx(t, s, func(tx *store.Tx) error {
		if err := seedInstance(tx, "d1", "i1"); err != nil {
			return err
		}
		for id, group := range groups {
			err := tx.SaveWatch(store.Watch{Id: id, InstanceId: "i1", Namespace: "smart-services", Owner: "alice", Procedure: "update",
				HashType: "deviceids", Interval: time.Hour, LastHash: "baseline-" + id, NextAt: t0, CreatedAt: t0, DeviceGroupId: group})
			if err != nil {
				return err
			}
			if err = tx.SetWatchDevices(id, members[id]); err != nil {
				return err
			}
		}
		return nil
	})
}

// claimAndPark checks every due watch once and lets it wait for an announcement, or a day
func claimAndPark(t *testing.T, s *store.Store, now time.Time) []store.Watch {
	t.Helper()
	watches := claim(t, s, now, 100)
	tx(t, s, func(tx *store.Tx) error {
		for _, w := range watches {
			if err := tx.ParkWatch(w.Id, w.NextAt, now.Add(24*time.Hour)); err != nil {
				return err
			}
		}
		return nil
	})
	return watches
}

func TestAWatchOfADeviceGroupKeepsGroupAndBaseline(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		seedGroupWatches(t, s, map[string]string{"w1": "g1"}, nil)
		watches := claim(t, s, t0, 10)
		if len(watches) != 1 || watches[0].DeviceGroupId != "g1" || watches[0].LastHash != "baseline-w1" || !watches[0].NextAt.Equal(t0.Add(time.Hour)) {
			t.Errorf("%+v", watches)
		}
	})
}

func TestAParkedWatchIsDueOnlyWhenItsDeviceGroupIsAnnounced(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		seedGroupWatches(t, s, map[string]string{"w1": "g1", "w2": "g2"}, nil)
		claimAndPark(t, s, t0)
		if due := claim(t, s, t0.Add(2*time.Hour), 10); len(due) != 0 {
			t.Fatalf("a parked watch is not due by its interval: %+v", due)
		}
		t1 := t0.Add(time.Minute)
		var changed int64
		tx(t, s, func(tx *store.Tx) (err error) {
			changed, err = tx.DueWatchesOfDeviceGroup("g1", t1)
			return err
		})
		if changed != 1 {
			t.Errorf("changed: %v", changed)
		}
		if due := claim(t, s, t1, 10); len(due) != 1 || due[0].Id != "w1" {
			t.Errorf("only the watch of the announced group is due: %+v", due)
		}
	})
}

func TestAnAnnouncementKeepsTheTimeOfAWatchAlreadyDue(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		seedGroupWatches(t, s, map[string]string{"w1": "g1"}, nil)
		tx(t, s, func(tx *store.Tx) error {
			_, err := tx.DueWatchesOfDeviceGroup("g1", t0.Add(time.Minute))
			return err
		})
		if due := claim(t, s, t0, 10); len(due) != 1 {
			t.Errorf("the watch stays due at its earlier time: %+v", due)
		}
	})
}

// An announcement during the check of a watch is not lost by the parking that ends the check.
func TestAWatchAnnouncedDuringItsCheckStaysDue(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		seedGroupWatches(t, s, map[string]string{"w1": "g1"}, nil)
		watches := claim(t, s, t0, 10)
		t1 := t0.Add(time.Second)
		tx(t, s, func(tx *store.Tx) error {
			if _, err := tx.DueWatchesOfDeviceGroup("g1", t1); err != nil {
				return err
			}
			return tx.ParkWatch(watches[0].Id, watches[0].NextAt, t0.Add(24*time.Hour))
		})
		if due := claim(t, s, t1, 10); len(due) != 1 {
			t.Errorf("the announced watch is still due: %+v", due)
		}
	})
}

// in case an announcement was missed
func TestAParkedWatchIsDueAgainAtTheEndOfItsParking(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		seedGroupWatches(t, s, map[string]string{"w1": "g1"}, nil)
		claimAndPark(t, s, t0)
		if due := claim(t, s, t0.Add(24*time.Hour-time.Millisecond), 10); len(due) != 0 {
			t.Fatalf("due before the end of its parking: %+v", due)
		}
		if due := claim(t, s, t0.Add(24*time.Hour), 10); len(due) != 1 {
			t.Errorf("due at the end of its parking: %+v", due)
		}
	})
}

func TestADeletedDeviceMakesTheWatchesOfItsGroupsDue(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		seedGroupWatches(t, s, map[string]string{"w1": "g1", "w2": "g2", "w3": "g3"},
			map[string][]string{"w1": {"dev1", "dev2"}, "w2": {"dev2"}, "w3": {"dev3"}})
		claimAndPark(t, s, t0)
		t1 := t0.Add(time.Minute)
		tx(t, s, func(tx *store.Tx) error {
			_, err := tx.DueWatchesOfDevice("dev2", t1)
			return err
		})
		due := claim(t, s, t1, 10)
		ids := map[string]bool{}
		for _, w := range due {
			ids[w.Id] = true
		}
		if len(due) != 2 || !ids["w1"] || !ids["w2"] {
			t.Errorf("the watches whose group had the device are due: %+v", due)
		}
	})
}

func TestTheDevicesOfAWatchAreReplacedAndGoWithIt(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		seedGroupWatches(t, s, map[string]string{"w1": "g1"}, map[string][]string{"w1": {"dev1"}})
		claimAndPark(t, s, t0)
		t1 := t0.Add(time.Minute)
		var changed int64
		tx(t, s, func(tx *store.Tx) (err error) {
			if err = tx.SetWatchDevices("w1", []string{"dev2", "dev2"}); err != nil {
				return err
			}
			changed, err = tx.DueWatchesOfDevice("dev1", t1)
			return err
		})
		if changed != 0 {
			t.Errorf("a device the group no longer had makes nothing due: %v", changed)
		}
		var rows map[string]int
		tx(t, s, func(tx *store.Tx) (err error) {
			if err = tx.DeleteWatch("w1"); err != nil {
				return err
			}
			rows, err = tx.StorageRows()
			return err
		})
		if rows["watch_devices"] != 0 {
			t.Errorf("the devices go with the watch: %v", rows["watch_devices"])
		}
	})
}
