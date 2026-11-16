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

// Package deviceconnection is the task type "device_connection": it reads the connection state
// of the device selected for the task (SNRGY-5151). The output "result" is "online", "offline" or
// "unknown". The cloud reads it from device-repository, a gateway from the device-manager of the
// gateway. The state at the moment of the read is all it gives; a change between this read and a
// later wait for the connection event is not seen.
package deviceconnection

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	devicerepomodel "github.com/SENERGY-Platform/device-repository/v3/lib/model"
	"github.com/SENERGY-Platform/models/go/models"
)

const TaskType = "device_connection"

const (
	Online  = "online"
	Offline = "offline"
	Unknown = "unknown"
)

// Devices is the part of the device-repository client the task needs. The reads are not cached:
// the connection state is what changes.
type Devices interface {
	ReadDevice(ctx context.Context, id string, token string, action devicerepomodel.AuthAction) (models.Device, error, int)
	ReadExtendedDevice(ctx context.Context, id string, token string, action devicerepomodel.AuthAction, fullDt bool) (models.ExtendedDevice, error, int)
}

// Gateway reads the state at the device-manager of a gateway by local device id: online, offline
// or unknown.
type Gateway interface {
	State(ctx context.Context, localId string) (string, error)
}

type Type struct {
	devices Devices
	tokens  auth.UserTokenSource
	// local: on a gateway; gateway may be nil there, which fails the task
	local   bool
	gateway Gateway
}

// New reads the state from device-repository with the token of the owner of the instance.
func New(devices Devices, tokens auth.UserTokenSource) *Type {
	return &Type{devices: devices, tokens: tokens}
}

// NewLocal reads the device from the mirror of device-repository on the gateway and its state at
// the device-manager; gateway is nil without a url of the device-manager.
func NewLocal(devices Devices, tokens auth.UserTokenSource, gateway Gateway) *Type {
	return &Type{devices: devices, tokens: tokens, local: true, gateway: gateway}
}

func permanent(format string, args ...any) error {
	return &engine.PermanentError{Message: fmt.Sprintf(format, args...)}
}

// failure is permanent for an answer that no retry changes, the device does not exist or the
// owner may not read it; 401 is retried, the token may have changed between issuing and the call
func failure(what string, err error, code int) error {
	if code == http.StatusNotFound || code == http.StatusForbidden {
		return permanent("%v: %v", what, err)
	}
	return fmt.Errorf("%v: %w", what, err)
}

func (this *Type) Execute(ctx context.Context, task engine.Task) (engine.TaskResult, error) {
	if task.Selection == nil || task.Selection.ConnectionDeviceId == "" {
		return engine.TaskResult{}, permanent("device connection needs a selected device")
	}
	deviceId := task.Selection.ConnectionDeviceId
	if this.tokens == nil {
		return engine.TaskResult{}, errors.New("no token source for the owner")
	}
	token, err := this.tokens(task.Owner)
	if err != nil {
		return engine.TaskResult{}, fmt.Errorf("unable to get a token of the owner: %w", err)
	}
	state, err := this.read(ctx, token, deviceId)
	if err != nil {
		return engine.TaskResult{}, err
	}
	return engine.TaskResult{Output: map[string]any{"result": state}}, nil
}

func (this *Type) read(ctx context.Context, token string, deviceId string) (string, error) {
	if !this.local {
		device, err, code := this.devices.ReadExtendedDevice(ctx, deviceId, token, devicerepomodel.READ, false)
		if err != nil {
			return "", failure("device "+deviceId, err, code)
		}
		switch device.ConnectionState {
		case models.ConnectionStateOnline:
			return Online, nil
		case models.ConnectionStateOffline:
			return Offline, nil
		}
		return Unknown, nil
	}
	if this.gateway == nil {
		return "", permanent("the connection state of a device is read from the device-manager of the gateway, which is not configured (DEVICE_MANAGER_URL)")
	}
	device, err, code := this.devices.ReadDevice(ctx, deviceId, token, devicerepomodel.READ)
	if err != nil {
		return "", failure("device "+deviceId, err, code)
	}
	if device.LocalId == "" {
		return "", permanent("device %v has no local id", deviceId)
	}
	state, err := this.gateway.State(ctx, device.LocalId)
	if err != nil {
		return "", fmt.Errorf("device-manager: %w", err)
	}
	switch state {
	case Online, Offline:
		return state, nil
	}
	return Unknown, nil
}
