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

package devices_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/devices"
)

// mgw-external-task-worker v1.2.2 TestNotFoundErrorNamesTheMissingEntity: the incident of a
// task names what device-repository does not know.
func TestRepositoryErrorNamesTheMissingResource(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	repo, err := devices.NewRepository(server.URL, func(user string) (string, error) { return "Bearer " + user, nil }, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token := devices.Token("Bearer alice")
	cases := map[string]func() error{
		"/devices/id":       func() error { _, err := repo.GetDevice(token, "id"); return err },
		"/device-types/id":  func() error { _, err := repo.GetDeviceType(token, "id"); return err },
		"/protocols/id":     func() error { _, err := repo.GetProtocol(token, "id"); return err },
		"/device-groups/id": func() error { _, err := repo.GetDeviceGroup(token, "id"); return err },
	}
	for path, get := range cases {
		if err := get(); err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "404") {
			t.Errorf("expected an error naming %v and the status, got %v", path, err)
		}
	}
}
