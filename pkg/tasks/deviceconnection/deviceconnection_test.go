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

package deviceconnection_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/tasks/deviceconnection"
	devicerepomodel "github.com/SENERGY-Platform/device-repository/v3/lib/model"
	"github.com/SENERGY-Platform/models/go/models"
)

// repo answers the two reads of the task; tokens records the token of the last one
type repo struct {
	extended map[string]models.ExtendedDevice
	devices  map[string]models.Device
	token    string
}

func (this *repo) ReadDevice(_ context.Context, id string, token string, _ devicerepomodel.AuthAction) (models.Device, error, int) {
	this.token = token
	d, ok := this.devices[id]
	if !ok {
		return d, errors.New("not found"), http.StatusNotFound
	}
	return d, nil, http.StatusOK
}

func (this *repo) ReadExtendedDevice(_ context.Context, id string, token string, _ devicerepomodel.AuthAction, _ bool) (models.ExtendedDevice, error, int) {
	this.token = token
	d, ok := this.extended[id]
	switch {
	case id == "forbidden":
		return d, errors.New("forbidden"), http.StatusForbidden
	case id == "expired":
		return d, errors.New("unauthorized"), http.StatusUnauthorized
	case id == "broken":
		return d, errors.New("unavailable"), http.StatusServiceUnavailable
	case !ok:
		return d, errors.New("not found"), http.StatusNotFound
	}
	return d, nil, http.StatusOK
}

type gateway map[string]string

func (this gateway) State(_ context.Context, localId string) (string, error) {
	if localId == "unreachable" {
		return "", errors.New("connection refused")
	}
	if state, ok := this[localId]; ok {
		return state, nil
	}
	return deviceconnection.Unknown, nil
}

func tokens(user string) (string, error) { return "Bearer " + user, nil }

func taskOf(deviceId string) engine.Task {
	selection := &model.Selection{ConnectionDeviceId: deviceId}
	if deviceId == "" {
		selection = nil
	}
	return engine.Task{Owner: "alice", Selection: selection}
}

func extended(state string) models.ExtendedDevice {
	return models.ExtendedDevice{Device: models.Device{Id: "d"}, ConnectionState: state}
}

func TestCloudReadsTheStateFromDeviceRepositoryWithTheTokenOfTheOwner(t *testing.T) {
	r := &repo{extended: map[string]models.ExtendedDevice{"on": extended("online"), "off": extended("offline"), "new": extended("")}}
	connection := deviceconnection.New(r, tokens)
	for id, expected := range map[string]string{"on": "online", "off": "offline", "new": "unknown"} {
		result, err := connection.Execute(context.Background(), taskOf(id))
		if err != nil || result.Output["result"] != expected {
			t.Errorf("%v: %+v %v", id, result, err)
		}
	}
	if r.token != "Bearer alice" {
		t.Errorf("the owner reads: %v", r.token)
	}
}

func TestCloudFailures(t *testing.T) {
	connection := deviceconnection.New(&repo{}, tokens)
	for name, id := range map[string]string{"no selection": "", "unknown device": "missing", "not readable": "forbidden"} {
		_, err := connection.Execute(context.Background(), taskOf(id))
		if !engine.IsPermanent(err) {
			t.Errorf("%v: a retry changes nothing: %v", name, err)
		}
	}
	if _, err := connection.Execute(context.Background(), taskOf("broken")); err == nil || engine.IsPermanent(err) {
		t.Errorf("an unavailable device-repository is retried: %v", err)
	}
	if _, err := connection.Execute(context.Background(), taskOf("expired")); err == nil || engine.IsPermanent(err) {
		t.Errorf("a token that changed between issuing and the call is retried: %v", err)
	}
	if _, err := deviceconnection.New(&repo{}, nil).Execute(context.Background(), taskOf("d")); err == nil {
		t.Error("without a token source the owner cannot read")
	}
}

func TestGatewayReadsTheStateAtTheDeviceManagerByLocalId(t *testing.T) {
	r := &repo{devices: map[string]models.Device{
		"d1": {Id: "d1", LocalId: "l1"}, "d2": {Id: "d2", LocalId: "l2"}, "d3": {Id: "d3", LocalId: "l3"}, "nolocal": {Id: "nolocal"}, "down": {Id: "down", LocalId: "unreachable"},
	}}
	connection := deviceconnection.NewLocal(r, tokens, gateway{"l1": "online", "l2": "offline"})
	for id, expected := range map[string]string{"d1": "online", "d2": "offline", "d3": "unknown"} {
		result, err := connection.Execute(context.Background(), taskOf(id))
		if err != nil || result.Output["result"] != expected {
			t.Errorf("%v: %+v %v", id, result, err)
		}
	}
	for name, id := range map[string]string{"unknown device": "missing", "device without local id": "nolocal"} {
		if _, err := connection.Execute(context.Background(), taskOf(id)); !engine.IsPermanent(err) {
			t.Errorf("%v: %v", name, err)
		}
	}
	if _, err := connection.Execute(context.Background(), taskOf("down")); err == nil || engine.IsPermanent(err) {
		t.Errorf("an unreachable device-manager is retried: %v", err)
	}
}

func TestGatewayWithoutADeviceManagerFailsWithAMessage(t *testing.T) {
	r := &repo{devices: map[string]models.Device{"d1": {Id: "d1", LocalId: "l1"}}}
	_, err := deviceconnection.NewLocal(r, tokens, nil).Execute(context.Background(), taskOf("d1"))
	var permanent *engine.PermanentError
	if !errors.As(err, &permanent) || permanent.Message == "" {
		t.Fatalf("%v", err)
	}
}
