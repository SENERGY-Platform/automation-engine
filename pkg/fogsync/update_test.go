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

package fogsync_test

import (
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// process-sync replaced the Camunda deployment of an outdated model; the engine sends the new
// version with the same id (SYNC-01, DEP-04)
func TestAnUpdatedDeploymentReachesTheGatewayAsItsNextVersion(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: waitProcess(), NetworkId: network})
	if err != nil {
		t.Fatal(err)
	}
	e.waitFor("the deployment on the gateway", func() bool { _, err := deploymentOn(e.gateway, d.Id); return err == nil })
	changed := b.Process(b.Message("m", "go"), b.Start("start"), b.CatchMessage("wait", "m"), b.CatchMessage("again", "m"), b.End("end"),
		b.Flow("start", "wait"), b.Flow("wait", "again"), b.Flow("again", "end"))
	if _, err = c.PutDeployment(ctx, alice, "processes", d.Id, model.DeploymentInput{Xml: changed, NetworkId: network, UpdateStrategy: model.UpdateDrain}); err != nil {
		t.Fatal(err)
	}
	e.waitFor("version 2 on the gateway", func() bool { gd, err := deploymentOn(e.gateway, d.Id); return err == nil && gd.Version == 2 })
}
