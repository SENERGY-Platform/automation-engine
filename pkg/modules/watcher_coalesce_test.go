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

package modules

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// SNRGY-5143: a device in several watched groups changes several hashes at once; the instance
// gets one run of the procedure per change, not one per watch.

// watchGroup runs a watcher task of the fixture's instance for a device group
func (this *groupWatchFixture) watchGroup(task string, group string, procedure string, level string) {
	this.t.Helper()
	_, _, err := this.runtime.watcher.Do(context.Background(), Task{Id: task, InstanceId: this.instance, RootInstanceId: this.instance, Namespace: "smart-services", Owner: "alice",
		Inputs: map[string]any{
			"watcher.maintenance_procedure":              procedure,
			"watcher.watch_device_group":                 group,
			"watcher.maintenance_procedure_inputs.level": level,
		}})
	if err != nil {
		this.t.Fatal(err)
	}
}

// all reads every stored watch without changing it
func (this *groupWatchFixture) all() []store.Watch {
	this.t.Helper()
	var result []store.Watch
	err := this.store.Tx(context.Background(), func(tx *store.Tx) (err error) {
		if result, err = tx.ClaimDueWatches(time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), 100); err != nil {
			return err
		}
		return errors.New("rollback")
	})
	if err != nil && err.Error() != "rollback" {
		this.t.Fatal(err)
	}
	return result
}

// announce changes the members of the groups and announces each
func (this *groupWatchFixture) announce(members []string, groups ...string) {
	this.t.Helper()
	for _, group := range groups {
		this.repository.set(group, members...)
		if err := this.runtime.DeviceGroupChanged(context.Background(), group); err != nil {
			this.t.Fatal(err)
		}
	}
}

func TestWatchesThatFireTogetherStartTheProcedureOncePerInputs(t *testing.T) {
	f := newGroupWatchFixture(t, true)
	groups := []string{"g1", "g2", "g3", "g4", "g5"}
	for _, group := range groups {
		f.repository.set(group, "dev1")
	}
	f.watchGroup("task1", "g1", "refresh_devices", "3")
	f.watchGroup("task2", "g2", "refresh_devices", "3")
	f.watchGroup("task3", "g3", "refresh_devices", "3")
	f.watchGroup("task4", "g4", "refresh_devices", "4")
	f.watchGroup("task5", "g5", "other", "3")
	f.check()
	if f.maintenance.count() != 0 {
		t.Fatal("the groups as the tasks read them are the baseline")
	}
	before := map[string]string{}
	for _, w := range f.all() {
		before[w.Id] = w.LastHash
	}

	// dev2 joins every group, one change for all watches
	f.maintenance.failing = true
	f.announce([]string{"dev1", "dev2"}, groups...)
	f.check()
	for _, w := range f.all() {
		if w.LastHash != before[w.Id] || !strings.HasPrefix(w.LastError, "trigger: ") {
			t.Errorf("a failed start keeps the hash of every watch it stood for: %+v", w)
		}
	}

	f.maintenance.failing = false
	f.now = f.now.Add(time.Hour)
	f.check()
	started := append([]string(nil), f.maintenance.started...)
	slices.Sort(started)
	expected := []string{
		f.instance + ` other {"level":3}`,
		f.instance + ` refresh_devices {"level":3}`,
		f.instance + ` refresh_devices {"level":4}`,
	}
	if !slices.Equal(started, expected) {
		t.Fatalf("one start per procedure and inputs: %v", started)
	}
	for _, w := range f.all() {
		if w.LastHash == before[w.Id] || w.LastError != "" {
			t.Errorf("the start advances the hash of every watch it stood for: %+v", w)
		}
	}
}
