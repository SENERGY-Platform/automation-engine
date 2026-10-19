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
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// The instances of a deployment are its running ones: finished instances and those of other
// deployments are not listed.
func TestInstancesOfADeploymentAreItsRunningOnes(t *testing.T) {
	e := newEnv(t)
	first := e.deploy(waitingProcess())
	second := e.deploy(waitingProcess())
	for _, key := range []string{"a", "b", "c"} {
		if status, _ := e.call(alice, http.MethodGet, "/engine/v2/deployments/"+first.Id+"/start?business_key="+key, nil, nil); status != http.StatusOK {
			t.Fatal(status)
		}
	}
	if status, _ := e.call(alice, http.MethodGet, "/engine/v2/deployments/"+second.Id+"/start?business_key=other", nil, nil); status != http.StatusOK {
		t.Fatal(status)
	}
	if _, err := e.srv.Engine.Correlate(ctx, engine.CorrelationRequest{MessageInput: model.MessageInput{Name: "go", InstanceId: "a"}}); err != nil {
		t.Fatal(err)
	}
	e.waitFor("the completed instance", func() bool { return e.instance("a").State == model.StateCompleted })
	var instances []struct{ Id string }
	if status, _ := e.call(alice, http.MethodGet, "/engine/v2/deployments/"+first.Id+"/instances", nil, &instances); status != http.StatusOK {
		t.Fatal(status)
	}
	ids := map[string]bool{}
	for _, i := range instances {
		ids[i.Id] = true
	}
	if len(instances) != 2 || !ids["b"] || !ids["c"] {
		t.Errorf("%+v", instances)
	}
}

// Stopping an instance that does not exist is answered as not found or as done, not as a
// failure of the server.
func TestStoppingAnUnknownInstanceIsNoServerError(t *testing.T) {
	e := newEnv(t)
	status, _ := e.call(alice, http.MethodDelete, "/engine/v2/process-instances/unknown", nil, nil)
	if status != http.StatusOK && status != http.StatusNotFound {
		t.Errorf("%v", status)
	}
}
