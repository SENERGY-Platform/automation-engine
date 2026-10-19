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

	"github.com/SENERGY-Platform/automation-engine/pkg/fogsync"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// process-sync removed a placeholder start that the user stopped before the mgw had it; here
// the start is withdrawn by the stop, and the instance must not stay pending in the cloud
func TestAStartStoppedBeforeTheGatewayGotItDoesNotStayPending(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: waitProcess(), NetworkId: network})
	if err != nil {
		t.Fatal(err)
	}
	e.waitFor("the deployment on the gateway", func() bool { _, err := deploymentOn(e.gateway, d.Id); return err == nil })
	e.online.Store(false)
	if _, err = c.PutInstance(ctx, alice, "processes", "i-1", model.StartInstanceInput{DeploymentId: d.Id}); err != nil {
		t.Fatal(err)
	}
	e.waitSent(fogsync.ResourceInstance, "i-1")
	if err = c.StopInstances(ctx, alice, "processes", []string{"i-1"}); err != nil {
		t.Fatal(err)
	}
	e.waitSent(fogsync.ResourceInstance, "i-1")
	e.online.Store(true)
	e.waitFor("the stop applied on the gateway", func() bool {
		desired, found := e.desired(fogsync.ResourceInstance, "i-1")
		return !found || desired.AppliedVersion == desired.Version
	})
	var state model.InstanceState
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		inst, err := c.GetInstance(ctx, alice, "processes", "i-1")
		if err != nil {
			return
		}
		if state = inst.State; state != model.StatePending {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("the stopped start is still %v in the cloud, though the gateway will never run it", state)
}

// failedResources lists the resources of the gateway that it could not apply (SYNC-13)
func (this *env) failedResources() (result []model.NetworkResource) {
	this.t.Helper()
	detail, err := this.cloud.Controller.GetNetwork(ctx, alice, network)
	if err != nil {
		this.t.Fatal(err)
	}
	for _, r := range detail.Resources {
		if r.State == model.ResourceFailed {
			result = append(result, r)
		}
	}
	return result
}

// waitForTheStartToSettle waits until the start of an instance is forgotten or failed
func (this *env) waitForTheStartToSettle(id string) {
	this.t.Helper()
	this.waitFor("the start of "+id+" forgotten or failed", func() bool {
		desired, found := this.desired(fogsync.ResourceInstance, id)
		return !found || desired.Error != ""
	})
}

// process-sync removed the placeholders of a deleted deployment with their starts; the
// engine must not keep a start whose deployment is gone and fail it on the gateway
func TestDeletingADeploymentWithAStartTheGatewayNeverGotLeavesNoFailedResource(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	e.online.Store(false)
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: waitProcess(), NetworkId: network})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.PutInstance(ctx, alice, "processes", "i-1", model.StartInstanceInput{DeploymentId: d.Id}); err != nil {
		t.Fatal(err)
	}
	e.waitSent(fogsync.ResourceInstance, "i-1")
	if err = c.DeleteDeployment(ctx, alice, "processes", d.Id); err != nil {
		t.Fatal(err)
	}
	e.waitSent(fogsync.ResourceDeployment, d.Id)
	e.online.Store(true)
	e.waitFor("the applied deletion forgotten in the cloud", func() bool { _, found := e.desired(fogsync.ResourceDeployment, d.Id); return !found })
	e.waitForTheStartToSettle("i-1")
	if failed := e.failedResources(); len(failed) != 0 {
		t.Errorf("the gateway is shown with a failed resource of a deleted deployment: %+v", failed)
	}
}

// A deployment deleted while its instance runs on the gateway takes the instance with it on
// both sides; its start is done then, as for an ended instance, and must not stay in the
// desired state of the gateway
func TestDeletingADeploymentForgetsTheStartOfItsRunningInstance(t *testing.T) {
	e := newEnv(t)
	d := e.runningOnTheGateway("i-1")
	if err := e.cloud.Controller.DeleteDeployment(ctx, alice, "processes", d.Id); err != nil {
		t.Fatal(err)
	}
	e.waitFor("the instance removed on the gateway", func() bool { _, err := instanceOn(e.gateway, "i-1"); return err != nil })
	// the next status shows the cloud what the gateway still has
	if err := e.gateway.Sync.SendStatus(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, found := e.desired(fogsync.ResourceInstance, "i-1"); !found {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	detail, _ := e.cloud.Controller.GetNetwork(ctx, alice, network)
	t.Errorf("the start of the removed instance stays in the desired state: %+v", detail.Resources)
}
