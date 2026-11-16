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

package events

// The connection state of devices on a gateway (SNRGY-5151): device connectors report it to the
// device-manager of the gateway at device-manager/device/{connector} and leave a last will at
// device-manager/device/{connector}/lw. The source turns the changes into messages for the event
// worker; the device-manager answers the state at start and for the task device_connection.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
)

const (
	// LocalConnectionTopic is the topic the messages of the source carry; it names no MQTT topic
	// the worker reads but ties the descriptions of a gateway to the source
	LocalConnectionTopic = "device-manager/device"

	deviceManagerTopicFilter = "device-manager/device/+"
	deviceManagerWillFilter  = "device-manager/device/+/lw"

	ConnectionOnline  = "online"
	ConnectionOffline = "offline"
	// ConnectionUnknown is any other state, e.g. "n/a", or none
	ConnectionUnknown = "unknown"
)

// ManagedDevice is what the device-manager of a gateway knows of a device. The python
// device-management-service names the connector module_id, the go mgw-device-manager ref.
type ManagedDevice struct {
	State    string `json:"state"`
	ModuleId string `json:"module_id"`
	Ref      string `json:"ref"`
}

// Connector is the connector that reports the device, whichever of the two names is set.
func (this ManagedDevice) Connector() string {
	if this.ModuleId != "" {
		return this.ModuleId
	}
	return this.Ref
}

// normalState maps the states of the device-manager to online, offline or "" for any other
func normalState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case ConnectionOnline:
		return ConnectionOnline
	case ConnectionOffline:
		return ConnectionOffline
	}
	return ""
}

// DeviceManager reads the devices of the device-manager of a gateway, e.g. http://device-manager:8080.
// It needs no token.
type DeviceManager struct {
	url  string
	http *http.Client
}

func NewDeviceManager(deviceManagerUrl string) *DeviceManager {
	return &DeviceManager{url: strings.TrimRight(deviceManagerUrl, "/"), http: &http.Client{Timeout: 10 * time.Second}}
}

// maxDeviceManagerBody bounds what a device-manager can make the engine read
const maxDeviceManagerBody = 16 << 20

// Devices returns all devices by local id.
func (this *DeviceManager) Devices(ctx context.Context) (result map[string]ManagedDevice, err error) {
	err = this.get(ctx, this.url+"/devices", &result)
	return result, err
}

// State returns online, offline or unknown, unknown also for a device the device-manager does
// not know.
func (this *DeviceManager) State(ctx context.Context, localId string) (string, error) {
	var device ManagedDevice
	err := this.get(ctx, this.url+"/devices/"+url.PathEscape(localId), &device)
	var status *statusError
	if errors.As(err, &status) && status.code == http.StatusNotFound {
		return ConnectionUnknown, nil
	}
	if err != nil {
		return "", err
	}
	if state := normalState(device.State); state != "" {
		return state, nil
	}
	return ConnectionUnknown, nil
}

func (this *DeviceManager) get(ctx context.Context, resourceUrl string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceUrl, nil)
	if err != nil {
		return err
	}
	resp, err := this.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return &statusError{code: resp.StatusCode, err: fmt.Errorf("GET %v: %v %v", req.URL.Path, resp.StatusCode, strings.TrimSpace(string(body)))}
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxDeviceManagerBody)).Decode(result)
}

// deviceManagerMessage is a message of a connector at device-manager/device/{connector}
type deviceManagerMessage struct {
	Method   string `json:"method"`
	DeviceId string `json:"device_id"`
	Data     struct {
		State string `json:"state"`
	} `json:"data"`
}

// DeviceManagerSource reports the changes of the connection state of the devices of a gateway to
// the event worker. It keeps the last state of each device and passes on only a change: a
// connector that reports the same state again causes no event. A message that arrives with the
// retain flag is the broker replaying what a connector once left there, at start and after every
// reconnect; it updates what the source knows and is never a change. The state at start is read
// from the device-manager and causes none either, and replaces a state that is only known from
// retained messages; without it the first live message of a device counts as change.
type DeviceManagerSource struct {
	broker  mqtt.Broker
	manager *DeviceManager
	logger  *slog.Logger
	now     func() time.Time

	mux sync.Mutex
	// last known online or offline state by local device id
	states map[string]string
	// devices whose state a live message has set, which the snapshot at start does not replace;
	// the state of any other device comes from retained messages only
	live map[string]bool
	// the devices of a connector by connector, for its last will
	connectors map[string]map[string]bool
	// local device id to connector, to move a device that another connector reports
	owners   map[string]string
	handle   func(Message) error
	sequence atomic.Int64
}

// NewDeviceManagerSource reads from the broker of the gateway; manager is nil without a
// device-manager url, then the state is known from the messages only.
func NewDeviceManagerSource(broker mqtt.Broker, manager *DeviceManager, logger *slog.Logger) *DeviceManagerSource {
	return &DeviceManagerSource{broker: broker, manager: manager, logger: logger, now: time.Now,
		states: map[string]string{}, live: map[string]bool{}, connectors: map[string]map[string]bool{}, owners: map[string]string{}}
}

// Start subscribes, then reads the devices of the device-manager: a message that arrives in
// between is newer than the answer and wins. A device-manager that does not answer is logged;
// the source works without its answer.
func (this *DeviceManagerSource) Start(ctx context.Context, handle func(Message) error) error {
	this.mux.Lock()
	this.handle = handle
	this.mux.Unlock()
	if err := this.broker.SubscribeRetain(deviceManagerTopicFilter, 1, this.onDevice); err != nil {
		return err
	}
	if err := this.broker.SubscribeRetain(deviceManagerWillFilter, 1, this.onWill); err != nil {
		return err
	}
	if this.manager == nil {
		return nil
	}
	devices, err := this.manager.Devices(ctx)
	if err != nil {
		this.logger.Warn("unable to read the devices of the device-manager; the first message of each device counts as a change", "error", err)
		return nil
	}
	this.mux.Lock()
	defer this.mux.Unlock()
	for id, device := range devices {
		if this.live[id] {
			continue
		}
		// the answer of the device-manager is newer than what a connector left retained; a state
		// it does not give is not known
		if state := normalState(device.State); state != "" {
			this.states[id] = state
		} else {
			delete(this.states, id)
		}
		this.attach(device.Connector(), id)
	}
	return nil
}

// attach records that the connector reports the device; a connector is not empty for a device
// that has none
func (this *DeviceManagerSource) attach(connector string, deviceId string) {
	if connector == "" {
		return
	}
	if previous, ok := this.owners[deviceId]; ok && previous != connector {
		delete(this.connectors[previous], deviceId)
	}
	if this.connectors[connector] == nil {
		this.connectors[connector] = map[string]bool{}
	}
	this.connectors[connector][deviceId] = true
	this.owners[deviceId] = connector
}

// connectorOf is the segment after device-manager/device/, a topic of any other shape has none
func connectorOf(topic string) string {
	rest, ok := strings.CutPrefix(topic, "device-manager/device/")
	if !ok {
		return ""
	}
	if rest == "" || strings.Contains(rest, "/") {
		return ""
	}
	return rest
}

func (this *DeviceManagerSource) onDevice(topic string, payload []byte, retained bool) error {
	connector := connectorOf(topic)
	var m deviceManagerMessage
	// retained messages with an empty payload clear a retained message; there is nothing to read
	if connector == "" || json.Unmarshal(payload, &m) != nil || m.DeviceId == "" {
		return nil
	}
	this.mux.Lock()
	defer this.mux.Unlock()
	switch m.Method {
	case "set":
		this.attach(connector, m.DeviceId)
		if state := normalState(m.Data.State); state != "" {
			this.apply(m.DeviceId, state, retained)
		}
	case "delete":
		if retained && this.live[m.DeviceId] {
			// a replay is older than what a live message told
			return nil
		}
		delete(this.states, m.DeviceId)
		delete(this.live, m.DeviceId)
		delete(this.connectors[connector], m.DeviceId)
		delete(this.owners, m.DeviceId)
	}
	return nil
}

// onWill is the last will of a connector: all its devices that were online are offline now
func (this *DeviceManagerSource) onWill(topic string, payload []byte, retained bool) error {
	connector := connectorOf(strings.TrimSuffix(topic, "/lw"))
	if connector == "" || normalState(strings.Trim(strings.TrimSpace(string(payload)), `"`)) != ConnectionOffline {
		return nil
	}
	this.mux.Lock()
	defer this.mux.Unlock()
	// in the order of the ids, for messages that do not differ from run to run
	for _, id := range slices.Sorted(maps.Keys(this.connectors[connector])) {
		if this.states[id] == ConnectionOnline {
			this.apply(id, ConnectionOffline, retained)
		}
	}
	return nil
}

// apply takes over a state: a retained one is only remembered, and only for a device no live
// message has named yet, because a replay can be older than what a live message told (a client
// that left a retained "online" and went offline through its will); a live one is passed on
// when it differs. The caller holds the lock.
func (this *DeviceManagerSource) apply(deviceId string, state string, retained bool) {
	if retained {
		if !this.live[deviceId] {
			this.states[deviceId] = state
		}
		return
	}
	this.live[deviceId] = true
	this.change(deviceId, state)
}

// change passes on a state that differs from the last one; the caller holds the lock, so that the
// messages of a device reach the worker in the order of the states
func (this *DeviceManagerSource) change(deviceId string, state string) {
	if this.states[deviceId] == state {
		return
	}
	this.states[deviceId] = state
	if this.handle == nil {
		return
	}
	now := this.now()
	connected := state == ConnectionOnline
	value, err := json.Marshal(ConnectionMessage{Id: deviceId, Connected: &connected, Time: now})
	if err != nil {
		return
	}
	m := Message{Topic: LocalConnectionTopic, Value: value, Time: now, Offset: now.UnixNano() + this.sequence.Add(1)}
	if err := this.handle(m); err != nil {
		this.logger.Warn("connection state not processed", "device", deviceId, "error", err)
	}
}
