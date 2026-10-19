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
	"strings"
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// The sync client sent the incidents of the mgw to process-sync, with the business key of the
// instance; here an incident travels with the state of its instance (SYNC-02)
func TestAnIncidentOnTheGatewayIsReportedToTheCloud(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	failing := b.Process(b.Start("start"), b.Script("bad", "x", "undefinedVar + 1"), b.End("end"), b.Flow("start", "bad"), b.Flow("bad", "end"))
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: failing, NetworkId: network})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.PutInstance(ctx, alice, "processes", "i-1", model.StartInstanceInput{DeploymentId: d.Id}); err != nil {
		t.Fatal(err)
	}
	var incidents []model.Incident
	e.waitFor("the incident in the cloud", func() bool {
		page, err := c.ListIncidents(ctx, alice, "processes", model.IncidentListOptions{InstanceId: "i-1"})
		incidents = page.Items
		return err == nil && len(incidents) > 0
	})
	if incidents[0].ElementId != "bad" || !strings.Contains(incidents[0].Message, "undefinedVar") {
		t.Errorf("the incident names element and error of the gateway: %+v", incidents[0])
	}
}
