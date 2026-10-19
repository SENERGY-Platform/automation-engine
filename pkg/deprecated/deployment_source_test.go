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

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// The deployment list of the web-ui asks with source=sepl and so hides the deployments that
// widgets create with source=generated. A deployment without source counts as sepl, as the
// wrapper stored it in Camunda.
func TestDeploymentListOfTheWebUiHidesGeneratedDeployments(t *testing.T) {
	e := newEnv(t)
	own := e.deploy(waitingProcess())
	if _, err := e.srv.Controller.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Name: "Widget", Xml: waitingProcess(), Source: "generated"}); err != nil {
		t.Fatal(err)
	}
	var list []struct{ Id string }
	if status, _ := e.call(alice, http.MethodGet, "/engine/v2/deployments?sortBy=deploymentTime&sortOrder=desc&maxResults=10&firstResult=0&source=sepl", nil, &list); status != http.StatusOK {
		t.Fatal(status)
	}
	if len(list) != 1 || list[0].Id != own.Id {
		t.Errorf("only the deployment of the user, without the one of the widget: %+v", list)
	}
}

// Asking for source=generated lists only the deployments of widgets.
func TestDeploymentListFiltersGeneratedDeployments(t *testing.T) {
	e := newEnv(t)
	e.deploy(waitingProcess())
	generated, err := e.srv.Controller.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Name: "Widget", Xml: waitingProcess(), Source: "generated"})
	if err != nil {
		t.Fatal(err)
	}
	var list []struct{ Id string }
	if status, _ := e.call(alice, http.MethodGet, "/engine/v2/deployments?source=generated", nil, &list); status != http.StatusOK {
		t.Fatal(status)
	}
	if len(list) != 1 || list[0].Id != generated.Id {
		t.Errorf("%+v", list)
	}
}
