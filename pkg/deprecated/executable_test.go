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

package deprecated_test

import (
	"net/http"
	"os"
	"testing"

	"github.com/SENERGY-Platform/models/go/models"
)

// process-deployment marked a prepared deployment as not executable when a device task had no
// device to select (ctrl.SetExecutableFlag); the deploy dialog then shows that it cannot run.
func TestPreparedDeploymentWithoutDeviceOptionsIsNotExecutable(t *testing.T) {
	e := newEnvWithDevices(t)
	raw, err := os.ReadFile("testdata/dependencies/raw.bpmn")
	if err != nil {
		t.Fatal(err)
	}
	var prepared models.Deployment
	if status, _ := e.call(alice, http.MethodPost, "/deployment/v3/prepared-deployments", map[string]string{"xml": string(raw), "svg": "<svg/>"}, &prepared); status != http.StatusOK {
		t.Fatalf("prepare: %v", status)
	}
	for _, element := range prepared.Elements {
		if element.Task == nil || len(element.Task.Selection.SelectionOptions) != 0 {
			t.Fatalf("expected device tasks without options, got %+v", element)
		}
	}
	if prepared.Executable {
		t.Error("a device task without options makes the deployment not executable")
	}
}
