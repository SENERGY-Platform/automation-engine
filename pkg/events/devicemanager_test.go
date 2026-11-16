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

package events_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
)

// deviceManager answers GET /devices and GET /devices/{id} as the device-manager of a gateway
// does, with the connector as module_id (python) or ref (go)
func deviceManager(t *testing.T, devices map[string]events.ManagedDevice) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/devices" {
			_ = json.NewEncoder(w).Encode(devices)
			return
		}
		device, ok := devices[strings.TrimPrefix(r.URL.Path, "/devices/")]
		if !ok || !strings.HasPrefix(r.URL.Path, "/devices/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(device)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestDeviceManagerStates(t *testing.T) {
	server := deviceManager(t, map[string]events.ManagedDevice{
		"on": {State: "online", ModuleId: "c1"}, "off": {State: "offline", Ref: "c2"}, "na": {State: "n/a"},
	})
	manager := events.NewDeviceManager(server.URL + "/")
	for id, expected := range map[string]string{"on": "online", "off": "offline", "na": "unknown", "missing": "unknown"} {
		if state, err := manager.State(ctx, id); err != nil || state != expected {
			t.Errorf("%v: %v %v", id, state, err)
		}
	}
	devices, err := manager.Devices(ctx)
	if err != nil || devices["on"].Connector() != "c1" || devices["off"].Connector() != "c2" {
		t.Errorf("the connector is module_id or ref: %+v %v", devices, err)
	}
	server.Close()
	if _, err = manager.State(ctx, "on"); err == nil {
		t.Error("an unreachable device-manager is an error, not unknown")
	}
}

// collected is what the source passes to the worker
type collected struct {
	mux      sync.Mutex
	messages []events.Message
}

func (this *collected) handle(m events.Message) error {
	this.mux.Lock()
	defer this.mux.Unlock()
	this.messages = append(this.messages, m)
	return nil
}

// states are the changes as "device:online" in the order they came
func (this *collected) states(t *testing.T) (result []string) {
	this.mux.Lock()
	defer this.mux.Unlock()
	for _, m := range this.messages {
		var c events.ConnectionMessage
		if err := json.Unmarshal(m.Value, &c); err != nil || m.Topic != events.LocalConnectionTopic || c.Connected == nil {
			t.Fatalf("unexpected message %+v", m)
		}
		state := "offline"
		if *c.Connected {
			state = "online"
		}
		result = append(result, c.Id+":"+state)
	}
	return result
}

func publishSet(t *testing.T, broker *mqtt.Memory, connector string, device string, state string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"method": "set", "device_id": device, "data": map[string]any{"name": device, "state": state, "device_type": "t"}})
	if err := broker.Publish(ctx, "device-manager/device/"+connector, 1, raw); err != nil {
		t.Fatal(err)
	}
}

func startSource(t *testing.T, manager *events.DeviceManager) (*mqtt.Memory, *collected) {
	t.Helper()
	return startSourceOn(t, mqtt.NewMemory(), manager)
}

// startSourceOn starts the source on a broker that may hold retained messages already
func startSourceOn(t *testing.T, broker *mqtt.Memory, manager *events.DeviceManager) (*mqtt.Memory, *collected) {
	t.Helper()
	got := &collected{}
	source := events.NewDeviceManagerSource(broker, manager, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := source.Start(ctx, got.handle); err != nil {
		t.Fatal(err)
	}
	return broker, got
}

func expectStates(t *testing.T, got *collected, expected ...string) {
	t.Helper()
	if actual := strings.Join(got.states(t), ","); actual != strings.Join(expected, ",") {
		t.Errorf("expected [%v], got [%v]", strings.Join(expected, ","), actual)
	}
}

// Only a change of state is passed on, the state at start counts as known, a last will puts the
// devices of its connector offline that were online, and a deleted device is forgotten
// (SNRGY-5151).
func TestDeviceManagerSourcePassesOnChangesOnly(t *testing.T) {
	server := deviceManager(t, map[string]events.ManagedDevice{
		"d1": {State: "online", ModuleId: "c1"}, "d2": {State: "online", Ref: "c1"}, "d3": {State: "offline", ModuleId: "c1"}, "d4": {State: "online", ModuleId: "c2"},
	})
	broker, got := startSource(t, events.NewDeviceManager(server.URL))
	expectStates(t, got)

	publishSet(t, broker, "c1", "d1", "online")
	expectStates(t, got)
	publishSet(t, broker, "c1", "d1", "offline")
	expectStates(t, got, "d1:offline")
	publishSet(t, broker, "c1", "d1", "offline")
	publishSet(t, broker, "c1", "d1", "n/a")
	expectStates(t, got, "d1:offline")
	publishSet(t, broker, "c1", "d1", "online")
	expectStates(t, got, "d1:offline", "d1:online")
	// a connector that leaves: d1 and d2 were online, d3 was offline, d4 belongs to another one
	if err := broker.Publish(ctx, "device-manager/device/c1/lw", 1, []byte("offline")); err != nil {
		t.Fatal(err)
	}
	expectStates(t, got, "d1:offline", "d1:online", "d1:offline", "d2:offline")
	if err := broker.Publish(ctx, "device-manager/device/c1/lw", 1, []byte("offline")); err != nil {
		t.Fatal(err)
	}
	if err := broker.Publish(ctx, "device-manager/device/c1/lw", 1, []byte("online")); err != nil {
		t.Fatal(err)
	}
	expectStates(t, got, "d1:offline", "d1:online", "d1:offline", "d2:offline")
	// a deleted device is forgotten, so that it counts as new when it returns
	raw, _ := json.Marshal(map[string]any{"method": "delete", "device_id": "d1"})
	if err := broker.Publish(ctx, "device-manager/device/c1", 1, raw); err != nil {
		t.Fatal(err)
	}
	publishSet(t, broker, "c1", "d1", "offline")
	expectStates(t, got, "d1:offline", "d1:online", "d1:offline", "d2:offline", "d1:offline")
	// what is no message of a connector is dropped
	for _, payload := range []string{"", "no json", `{"method":"set"}`, `{"method":"other","device_id":"d5"}`} {
		if err := broker.Publish(ctx, "device-manager/device/c1", 1, []byte(payload)); err != nil {
			t.Fatal(err)
		}
	}
	expectStates(t, got, "d1:offline", "d1:online", "d1:offline", "d2:offline", "d1:offline")
	// the messages of a worker are told apart by offset
	seen := map[int64]bool{}
	for _, m := range got.messages {
		if seen[m.Offset] {
			t.Errorf("message ids must differ: %v", m.Offset)
		}
		seen[m.Offset] = true
	}
}

func TestDeviceManagerSourceWithoutASnapshotCountsTheFirstMessage(t *testing.T) {
	broker, got := startSource(t, nil)
	publishSet(t, broker, "c1", "d1", "online")
	publishSet(t, broker, "c1", "d1", "online")
	expectStates(t, got, "d1:online")
	if err := broker.Publish(ctx, "device-manager/device/c1/lw", 1, []byte(`"offline"`)); err != nil {
		t.Fatal(err)
	}
	expectStates(t, got, "d1:online", "d1:offline")

	// a device-manager that does not answer is no reason to stop
	dead := deviceManager(t, nil)
	url := dead.URL
	dead.Close()
	broker, got = startSource(t, events.NewDeviceManager(url))
	publishSet(t, broker, "c1", "d1", "offline")
	expectStates(t, got, "d1:offline")
}

// A gateway correlates the connection state of a device that its connectors report at the
// device-manager topics, and not the state it read at start (SNRGY-5151).
func TestGatewayEvaluatesConnectionsOfItsDeviceManager(t *testing.T) {
	manager := deviceManager(t, map[string]events.ManagedDevice{"local-d1": {State: "online", ModuleId: "c1"}, "local-d2": {State: "online", ModuleId: "c1"}})
	srv, broker := newFogServerWith(t, func(config *configuration.Config) { config.DeviceManagerUrl = manager.URL })
	c := srv.Controller
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: connectionProcess(""),
		Selections: map[string]model.Selection{"presence": {ConnectionDeviceId: "d1"}}})
	if err != nil {
		t.Fatal(err)
	}
	descriptions, err := c.ListEventDescriptions(ctx, alice, "processes", d.Id)
	if err != nil || len(descriptions) != 1 || descriptions[0].LocalDeviceId != "local-d1" {
		t.Fatalf("a gateway describes the device by its local id: %+v %v", descriptions, err)
	}
	inst, err := c.StartInstance(ctx, alice, "processes", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = srv.Events.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	read := func(name string) any {
		vars, _ := c.GetVariables(ctx, alice, "processes", inst.Id)
		for _, v := range vars {
			if v.Name == name {
				return v.Value
			}
		}
		return nil
	}
	publishSet(t, broker, "c1", "local-d1", "online")
	if read("count") != nil {
		t.Fatalf("the state at start causes no event: %v", read("count"))
	}
	publishSet(t, broker, "c1", "local-d2", "offline")
	if read("count") != nil {
		t.Fatalf("another device causes none: %v", read("count"))
	}
	publishSet(t, broker, "c1", "local-d1", "offline")
	if read("count") != 1.0 || read("last") != "offline" || read("from") != "d1" {
		t.Fatalf("a change is correlated with the platform id of the device: %v %v %v", read("count"), read("last"), read("from"))
	}
	publishSet(t, broker, "c1", "local-d1", "offline")
	publishSet(t, broker, "c1", "local-d1", "online")
	if read("count") != 2.0 || read("last") != "online" {
		t.Fatalf("%v %v", read("count"), read("last"))
	}
	if err = broker.Publish(ctx, "device-manager/device/c1/lw", 1, []byte("offline")); err != nil {
		t.Fatal(err)
	}
	if read("count") != 3.0 || read("last") != "offline" {
		t.Fatalf("the last will of the connector: %v %v", read("count"), read("last"))
	}
}

func retainSet(t *testing.T, broker *mqtt.Memory, connector string, device string, state string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"method": "set", "device_id": device, "data": map[string]any{"state": state}})
	if err := broker.PublishRetained(ctx, "device-manager/device/"+connector, 1, raw); err != nil {
		t.Fatal(err)
	}
}

// A message with the retain flag is the broker replaying what a connector left; it is never a
// change, at start and after a reconnect, for a state, a deletion and a last will (SNRGY-5151).
func TestDeviceManagerSourceIgnoresRetainedMessagesAsChanges(t *testing.T) {
	broker := mqtt.NewMemory()
	// a broker keeps one retained message per topic, so the devices use connectors of their own
	retainSet(t, broker, "c1", "d1", "online")
	retainSet(t, broker, "c2", "d2", "offline")
	_, got := startSourceOn(t, broker, nil)
	expectStates(t, got)
	// without a snapshot the retained state counts as known: the same live state is no change
	publishSet(t, broker, "c1", "d1", "online")
	expectStates(t, got)
	publishSet(t, broker, "c2", "d2", "online")
	expectStates(t, got, "d2:online")

	// a reconnect replays the retained messages, which may be stale (d2 is retained offline):
	// no event, and the state a live message told stays, so the same live state is no change
	broker.Reconnect()
	expectStates(t, got, "d2:online")
	publishSet(t, broker, "c2", "d2", "online")
	expectStates(t, got, "d2:online")
	publishSet(t, broker, "c2", "d2", "offline")
	expectStates(t, got, "d2:online", "d2:offline")

	// a retained deletion reaches the subscriber live at once and forgets the device; its replay
	// after a reconnect changes nothing, and the device counts as new when it returns live
	raw, _ := json.Marshal(map[string]any{"method": "delete", "device_id": "d1"})
	if err := broker.PublishRetained(ctx, "device-manager/device/c1", 1, raw); err != nil {
		t.Fatal(err)
	}
	broker.Reconnect()
	expectStates(t, got, "d2:online", "d2:offline")
	publishSet(t, broker, "c1", "d1", "online")
	expectStates(t, got, "d2:online", "d2:offline", "d1:online")

	// a retained deletion from before the start does not forget a device a live message named
	// since: the replay after a reconnect is older than that message
	other := mqtt.NewMemory()
	raw, _ = json.Marshal(map[string]any{"method": "delete", "device_id": "d5"})
	if err := other.PublishRetained(ctx, "device-manager/device/c5", 1, raw); err != nil {
		t.Fatal(err)
	}
	_, got5 := startSourceOn(t, other, nil)
	publishSet(t, other, "c5", "d5", "online")
	expectStates(t, got5, "d5:online")
	other.Reconnect()
	publishSet(t, other, "c5", "d5", "online")
	expectStates(t, got5, "d5:online")
}

func TestDeviceManagerSourceRetainedLastWillIsNoChange(t *testing.T) {
	broker := mqtt.NewMemory()
	retainSet(t, broker, "c1", "d1", "online")
	if err := broker.PublishRetained(ctx, "device-manager/device/c1/lw", 1, []byte("offline")); err != nil {
		t.Fatal(err)
	}
	_, got := startSourceOn(t, broker, nil)
	expectStates(t, got)
	// the replayed will put the device offline silently, so a live online is the first change
	publishSet(t, broker, "c1", "d1", "online")
	expectStates(t, got, "d1:online")
	// a live will is a change, a replayed one after it is not
	if err := broker.Publish(ctx, "device-manager/device/c1/lw", 1, []byte("offline")); err != nil {
		t.Fatal(err)
	}
	expectStates(t, got, "d1:online", "d1:offline")
	broker.Reconnect()
	expectStates(t, got, "d1:online", "d1:offline")
}

// A connector that died leaves its retained "online" behind: the snapshot replaces it, and a
// later live "online" is a change. A state a live message set since the subscription stays.
func TestDeviceManagerSourceSnapshotOverridesRetainedStatesOnly(t *testing.T) {
	server := deviceManager(t, map[string]events.ManagedDevice{"d1": {State: "offline", ModuleId: "c1"}, "d2": {State: "online", ModuleId: "c1"}})
	broker := mqtt.NewMemory()
	retainSet(t, broker, "c1", "d1", "online")
	retainSet(t, broker, "c2", "d2", "online")
	_, got := startSourceOn(t, broker, events.NewDeviceManager(server.URL))
	expectStates(t, got)
	publishSet(t, broker, "c2", "d2", "online")
	expectStates(t, got)
	publishSet(t, broker, "c1", "d1", "online")
	expectStates(t, got, "d1:online")

	// the live message between the subscription and the snapshot wins over the snapshot
	slow := make(chan struct{})
	release := make(chan struct{})
	delayed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(slow)
		<-release
		_ = json.NewEncoder(w).Encode(map[string]events.ManagedDevice{"d3": {State: "offline", ModuleId: "c1"}})
	}))
	t.Cleanup(delayed.Close)
	broker = mqtt.NewMemory()
	retainSet(t, broker, "c1", "d3", "online")
	got = &collected{}
	source := events.NewDeviceManagerSource(broker, events.NewDeviceManager(delayed.URL), slog.New(slog.NewTextHandler(io.Discard, nil)))
	done := make(chan error)
	go func() { done <- source.Start(ctx, got.handle) }()
	<-slow
	publishSet(t, broker, "c1", "d3", "offline")
	expectStates(t, got, "d3:offline")
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// the snapshot said offline too, but even a snapshot of another state must not win
	publishSet(t, broker, "c1", "d3", "offline")
	expectStates(t, got, "d3:offline")
}
