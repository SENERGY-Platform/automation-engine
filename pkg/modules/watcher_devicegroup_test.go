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
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
	devicerepository "github.com/SENERGY-Platform/device-repository/v3/lib/client"
	"github.com/SENERGY-Platform/models/go/models"
)

// Watches of a device group (SNRGY-5132): the engine reads the group itself and checks it when
// device-repository announces a change, instead of a generic request by interval.

// groupRepository stands in for device-repository: it answers the reads of device groups and
// records the token of each
type groupRepository struct {
	mux    sync.Mutex
	groups map[string][]string
	reads  []string
}

func (this *groupRepository) set(id string, devices ...string) {
	this.mux.Lock()
	defer this.mux.Unlock()
	if devices == nil {
		delete(this.groups, id)
		return
	}
	this.groups[id] = devices
}

func (this *groupRepository) readCount() int {
	this.mux.Lock()
	defer this.mux.Unlock()
	return len(this.reads)
}

func (this *groupRepository) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	this.mux.Lock()
	defer this.mux.Unlock()
	id, ok := strings.CutPrefix(r.URL.Path, "/device-groups/")
	if r.Method != http.MethodGet || !ok {
		http.Error(w, "unexpected", http.StatusBadRequest)
		return
	}
	this.reads = append(this.reads, r.Header.Get("Authorization"))
	devices, ok := this.groups[id]
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(models.DeviceGroup{Id: id, Name: "group", DeviceIds: devices})
}

// maintenanceRecorder records the procedures a watch starts and fails them while failing is set
type maintenanceRecorder struct {
	mux     sync.Mutex
	started []string
	failing bool
}

func (this *maintenanceRecorder) StartMaintenanceInternal(_ context.Context, id string, procedure string, variables map[string]any) (model.Instance, error) {
	this.mux.Lock()
	defer this.mux.Unlock()
	if this.failing {
		return model.Instance{}, errors.New("procedure not startable")
	}
	raw, _ := json.Marshal(variables)
	this.started = append(this.started, id+" "+procedure+" "+string(raw))
	return model.Instance{}, nil
}

func (this *maintenanceRecorder) StartMaintenanceRerun(ctx context.Context, rerun store.MaintenanceRerun) (model.Instance, error) {
	return this.StartMaintenanceInternal(ctx, rerun.InstanceId, rerun.Procedure, rerun.Variables)
}

func (this *maintenanceRecorder) count() int {
	this.mux.Lock()
	defer this.mux.Unlock()
	return len(this.started)
}

type groupWatchFixture struct {
	t           *testing.T
	store       *store.Store
	runtime     *Runtime
	repository  *groupRepository
	maintenance *maintenanceRecorder
	now         time.Time
	instance    string
}

func newGroupWatchFixture(t *testing.T, events bool) *groupWatchFixture {
	t.Helper()
	repository := &groupRepository{groups: map[string][]string{}}
	server := httptest.NewServer(repository)
	t.Cleanup(server.Close)
	s := storetest.OpenSQLite(t)
	f := &groupWatchFixture{t: t, store: s, repository: repository, maintenance: &maintenanceRecorder{}, now: time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)}
	f.instance = instanceWith(t, s, "smart-services")
	f.runtime = NewRuntime(s, Settings{DeviceRepository: devicerepository.NewClient(server.URL, devicerepository.Options{})},
		func(user string) (string, error) { return "Bearer token-of-" + user, nil }, logger)
	f.runtime.now = func() time.Time { return f.now }
	f.runtime.AddWatcher(f.maintenance, nil, WatcherSettings{DefaultInterval: time.Hour, DeviceGroupEvents: events})
	return f
}

// watch runs a watcher task of the instance with the device group variable
func (this *groupWatchFixture) watch(value any) error {
	this.t.Helper()
	_, _, err := this.runtime.watcher.Do(context.Background(), Task{Id: "task1", InstanceId: this.instance, RootInstanceId: this.instance, Namespace: "smart-services", Owner: "alice",
		Inputs: map[string]any{
			"watcher.maintenance_procedure":              "members_changed",
			"watcher.watch_device_group":                 value,
			"watcher.maintenance_procedure_inputs.level": "3",
		}})
	return err
}

// check runs one pass of the watches at the current time of the fixture
func (this *groupWatchFixture) check() {
	this.t.Helper()
	if err := this.runtime.CheckWatches(context.Background()); err != nil {
		this.t.Fatal(err)
	}
}

func (this *groupWatchFixture) stored() store.Watch {
	this.t.Helper()
	var result store.Watch
	err := this.store.Tx(context.Background(), func(tx *store.Tx) error {
		watches, err := tx.ClaimDueWatches(time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), 10)
		if err != nil {
			return err
		}
		if len(watches) != 1 {
			return errors.New("not one watch")
		}
		result = watches[0]
		return errors.New("rollback")
	})
	if err != nil && err.Error() != "rollback" {
		this.t.Fatal(err)
	}
	return result
}

func TestWatchOfADeviceGroupTakesTheIdTheJsonStringAndTheIotOption(t *testing.T) {
	for name, value := range map[string]any{
		"id":          "urn:infai:ses:device-group:g1",
		"json string": `"urn:infai:ses:device-group:g1"`,
		"iot option":  `{"device_group_selection":{"id":"urn:infai:ses:device-group:g1"},"label":"group"}`,
		"iot object":  map[string]any{"device_group_selection": map[string]any{"id": "urn:infai:ses:device-group:g1"}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newGroupWatchFixture(t, true)
			f.repository.set("urn:infai:ses:device-group:g1", "dev2", "dev1")
			if err := f.watch(value); err != nil {
				t.Fatal(err)
			}
			w := f.stored()
			if w.DeviceGroupId != "urn:infai:ses:device-group:g1" || w.Request.Endpoint != "" || w.LastHash == "" || w.Procedure != "members_changed" ||
				!reflect.DeepEqual(w.Parameters, map[string]any{"level": 3.0}) {
				t.Errorf("%+v", w)
			}
		})
	}
}

func TestWatchOfADeviceGroupFailsTheTaskForAGroupTheOwnerCannotRead(t *testing.T) {
	for name, value := range map[string]string{
		"missing group":       "urn:infai:ses:device-group:unknown",
		"empty id":            " ",
		"device option":       `{"device_selection":{"device_id":"dev1"}}`,
		"malformed iot value": `{"device_group_selection":`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newGroupWatchFixture(t, true)
			if err := f.watch(value); err == nil {
				t.Error("no error")
			}
		})
	}
}

// Criteria come first, as the worker took the first variable present.
func TestCriteriaWinOverADeviceGroup(t *testing.T) {
	f := newGroupWatchFixture(t, true)
	_, _, err := f.runtime.watcher.Do(context.Background(), Task{Id: "task1", InstanceId: f.instance, RootInstanceId: f.instance, Namespace: "smart-services", Owner: "alice",
		Inputs: map[string]any{
			"watcher.maintenance_procedure":     "members_changed",
			"watcher.watch_devices_by_criteria": `[{"function_id":"fid"}]`,
			"watcher.watch_device_group":        "urn:infai:ses:device-group:unknown",
		}})
	if err != nil {
		t.Fatal(err)
	}
	if w := f.stored(); w.DeviceGroupId != "" || w.Request.Endpoint == "" {
		t.Errorf("%+v", w)
	}
}

func TestWatchOfADeviceGroupStartsTheProcedureOnAnAnnouncedChangeOnly(t *testing.T) {
	f := newGroupWatchFixture(t, true)
	f.repository.set("g1", "dev1", "dev2")
	if err := f.watch("g1"); err != nil {
		t.Fatal(err)
	}
	f.repository.mux.Lock()
	reads := append([]string(nil), f.repository.reads...)
	f.repository.mux.Unlock()
	if len(reads) != 1 || reads[0] != "Bearer token-of-alice" {
		t.Fatalf("the task reads the group with the owner's token: %v", reads)
	}
	// the check right after the task, for a change announced before the watch was stored
	f.check()
	if f.maintenance.count() != 0 {
		t.Fatal("the group as the task read it is the baseline")
	}
	f.repository.set("g1", "dev1", "dev2", "dev3")
	f.now = f.now.Add(23 * time.Hour)
	f.check()
	if reads := f.repository.readCount(); reads != 2 || f.maintenance.count() != 0 {
		t.Fatalf("without an announcement nothing is read before the daily recheck: %v reads, %v starts", reads, f.maintenance.count())
	}
	if err := f.runtime.DeviceGroupChanged(context.Background(), "g1"); err != nil {
		t.Fatal(err)
	}
	f.check()
	if f.maintenance.count() != 1 || f.maintenance.started[0] != f.instance+` members_changed {"level":3}` {
		t.Fatalf("the announced change starts the procedure with the inputs of the watch: %v", f.maintenance.started)
	}
	if err := f.runtime.DeviceGroupChanged(context.Background(), "g1"); err != nil {
		t.Fatal(err)
	}
	f.repository.set("g1", "dev3", "dev2", "dev1", "dev1")
	f.check()
	if f.maintenance.count() != 1 {
		t.Fatal("a write that keeps the members, in another order, starts nothing")
	}
	if err := f.runtime.DeviceGroupChanged(context.Background(), "other"); err != nil {
		t.Fatal(err)
	}
	count := f.repository.readCount()
	f.check()
	if f.repository.readCount() != count {
		t.Fatal("the announcement of another group checks nothing")
	}
}

// device-repository keeps a deleted device in its groups without announcing them, but no longer
// returns it.
func TestADeletedDeviceIsAChangeOfTheGroupsItWasIn(t *testing.T) {
	f := newGroupWatchFixture(t, true)
	f.repository.set("g1", "dev1", "dev2")
	if err := f.watch("g1"); err != nil {
		t.Fatal(err)
	}
	f.check()
	if err := f.runtime.DeviceDeleted(context.Background(), "dev9"); err != nil {
		t.Fatal(err)
	}
	reads := f.repository.readCount()
	f.check()
	if f.repository.readCount() != reads {
		t.Fatal("a device of no watched group checks nothing")
	}
	f.repository.set("g1", "dev1")
	if err := f.runtime.DeviceDeleted(context.Background(), "dev2"); err != nil {
		t.Fatal(err)
	}
	f.check()
	if f.maintenance.count() != 1 {
		t.Fatal("the deletion of a member starts the procedure")
	}
}

// A failed start keeps the change for the retry after the interval; the change is not lost
// although no further announcement comes.
func TestAFailedStartOfTheProcedureIsRetriedAfterTheInterval(t *testing.T) {
	f := newGroupWatchFixture(t, true)
	f.repository.set("g1", "dev1")
	if err := f.watch("g1"); err != nil {
		t.Fatal(err)
	}
	f.check()
	f.repository.set("g1", "dev1", "dev2")
	f.maintenance.failing = true
	if err := f.runtime.DeviceGroupChanged(context.Background(), "g1"); err != nil {
		t.Fatal(err)
	}
	f.check()
	if w := f.stored(); !strings.HasPrefix(w.LastError, "trigger: ") {
		t.Fatalf("the failure is recorded: %+v", w)
	}
	f.maintenance.failing = false
	f.now = f.now.Add(time.Hour)
	f.check()
	if f.maintenance.count() != 1 {
		t.Fatal("the retry starts the procedure")
	}
	f.now = f.now.Add(time.Hour)
	reads := f.repository.readCount()
	f.check()
	if f.repository.readCount() != reads {
		t.Fatal("after the retry the watch waits for announcements again")
	}
}

// A deleted group starts nothing and waits: its recreation would be announced.
func TestAWatchOfADeletedGroupRecordsItAndWaits(t *testing.T) {
	f := newGroupWatchFixture(t, true)
	f.repository.set("g1", "dev1")
	if err := f.watch("g1"); err != nil {
		t.Fatal(err)
	}
	f.check()
	f.repository.set("g1")
	if err := f.runtime.DeviceGroupChanged(context.Background(), "g1"); err != nil {
		t.Fatal(err)
	}
	f.check()
	if w := f.stored(); f.maintenance.count() != 0 || !strings.Contains(w.LastError, "not found") {
		t.Fatalf("no start, the failure is recorded: %+v", w)
	}
	reads := f.repository.readCount()
	f.now = f.now.Add(23 * time.Hour)
	f.check()
	if f.repository.readCount() != reads {
		t.Fatal("the deleted group is not read by interval")
	}
	f.repository.set("g1", "dev2")
	if err := f.runtime.DeviceGroupChanged(context.Background(), "g1"); err != nil {
		t.Fatal(err)
	}
	f.check()
	if f.maintenance.count() != 1 {
		t.Fatal("the recreated group with other members starts the procedure")
	}
}

// A missed announcement, e.g. of a change in device-repository while Kafka was unavailable to
// it, is found by the daily recheck.
func TestAMissedAnnouncementIsFoundByTheDailyRecheck(t *testing.T) {
	f := newGroupWatchFixture(t, true)
	f.repository.set("g1", "dev1")
	if err := f.watch("g1"); err != nil {
		t.Fatal(err)
	}
	f.check()
	f.repository.set("g1", "dev1", "dev2")
	f.now = f.now.Add(24*time.Hour - time.Second)
	f.check()
	if f.maintenance.count() != 0 {
		t.Fatal("before the recheck nothing is read")
	}
	f.now = f.now.Add(time.Second)
	f.check()
	if f.maintenance.count() != 1 {
		t.Fatal("the recheck finds the change")
	}
	reads := f.repository.readCount()
	f.now = f.now.Add(time.Hour)
	f.check()
	if f.repository.readCount() != reads {
		t.Fatal("after the recheck the watch waits again")
	}
}

// Without announcements, e.g. without Kafka, the watch reads the group by interval.
func TestWithoutAnnouncementsAWatchOfADeviceGroupIsCheckedByInterval(t *testing.T) {
	f := newGroupWatchFixture(t, false)
	f.repository.set("g1", "dev1")
	if err := f.watch("g1"); err != nil {
		t.Fatal(err)
	}
	f.check()
	f.repository.set("g1", "dev1", "dev2")
	f.now = f.now.Add(time.Hour)
	f.check()
	if f.maintenance.count() != 1 {
		t.Fatal("the change is found after the interval")
	}
}
