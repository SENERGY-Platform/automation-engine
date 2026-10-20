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
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// The restart after an incident of a gateway instance is the gateway's alone; the cloud shows
// when it is planned but never runs the instance itself (FOG-07, INC-04).
func TestTheCloudDoesNotRestartAnInstanceOfAGateway(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	failing := b.Process(b.Start("start"), b.Script("bad", "x", "undefinedVar + 1"), b.End("end"), b.Flow("start", "bad"), b.Flow("bad", "end"))
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: failing, NetworkId: network, IncidentHandling: &model.IncidentHandling{Restart: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.PutInstance(ctx, alice, "processes", "i-1", model.StartInstanceInput{DeploymentId: d.Id}); err != nil {
		t.Fatal(err)
	}
	e.waitFor("the incident with its planned restart in the cloud", func() bool {
		inst := e.cloudInstance("i-1")
		return inst.State == model.StateIncident && inst.RestartAt != nil
	})
	// the restart is due for the cloud; the gateway, on its own clock, has not restarted yet
	later := time.Now().Add(time.Hour)
	e.cloud.Engine.SetClock(func() time.Time { return later })
	if err = e.cloud.Engine.ProcessDue(ctx); err != nil {
		t.Fatal(err)
	}
	inst := e.cloudInstance("i-1")
	if inst.Run != 1 || inst.State != model.StateIncident {
		t.Errorf("the cloud does not run the instance of a gateway: %+v", inst)
	}
	if inst.RestartAt == nil {
		t.Errorf("the planned restart of the gateway stays visible: %+v", inst)
	}
	var rows map[string]int
	_ = e.cloud.Store.Tx(ctx, func(tx *store.Tx) (err error) {
		rows, err = tx.StorageRows()
		return err
	})
	if rows["tokens"] != 0 || rows["activities"] != 0 || rows["jobs"] != 0 || rows["subscriptions"] != 0 {
		t.Errorf("no runtime state of a gateway instance in the cloud: %v", rows)
	}
}
