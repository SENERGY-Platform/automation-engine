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

	"github.com/SENERGY-Platform/automation-engine/pkg/fogsync"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// Changes the cloud makes while a gateway is offline reach it when it is back, as the
// reconciler of process-sync did for the commands it had queued (SYNC-01, SYNC-08). The checks
// read the gateway: what the gateway reports back may wait for its own resend time.

func instanceOn(srv *server.Server, id string) (model.Instance, error) {
	var result model.Instance
	err := srv.Store.Tx(ctx, func(tx *store.Tx) error {
		inst, err := tx.GetInstance(id, false)
		result = inst.Instance
		return err
	})
	return result, err
}

func deploymentOn(srv *server.Server, id string) (model.Deployment, error) {
	var result model.Deployment
	err := srv.Store.Tx(ctx, func(tx *store.Tx) (err error) { result, err = tx.GetDeployment(id, false); return err })
	return result, err
}

// desired reads the desired state of a resource of the gateway in the cloud
func (this *env) desired(kind string, id string) (result store.Desired, found bool) {
	this.t.Helper()
	err := this.cloud.Store.Tx(ctx, func(tx *store.Tx) (err error) {
		result, found, err = tx.GetDesired(network, kind, id)
		return err
	})
	if err != nil {
		this.t.Fatal(err)
	}
	return result, found
}

// waitSent waits until the cloud has sent the current desired version of a resource; while
// offline, it is lost on the way
func (this *env) waitSent(kind string, id string) {
	this.t.Helper()
	this.waitFor("the "+kind+" "+id+" sent to the gateway", func() bool {
		d, found := this.desired(kind, id)
		return found && !d.SentAt.IsZero()
	})
}

// runningOnTheGateway deploys the waiting process to the gateway and starts it there
func (this *env) runningOnTheGateway(instanceId string) model.Deployment {
	this.t.Helper()
	c := this.cloud.Controller
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: waitProcess(), NetworkId: network})
	if err != nil {
		this.t.Fatal(err)
	}
	if _, err = c.PutInstance(ctx, alice, "processes", instanceId, model.StartInstanceInput{DeploymentId: d.Id}); err != nil {
		this.t.Fatal(err)
	}
	this.waitFor("the running instance on the gateway", func() bool {
		inst, err := instanceOn(this.gateway, instanceId)
		return err == nil && inst.State == model.StateRunning
	})
	return d
}

func TestADeploymentAndAStartMadeWhileTheGatewayIsOfflineRunWhenItIsBack(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	e.online.Store(false)
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: waitProcess(), NetworkId: network})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.PutInstance(ctx, alice, "processes", "i-1", model.StartInstanceInput{DeploymentId: d.Id, Variables: map[string]any{"level": 42.0}}); err != nil {
		t.Fatal(err)
	}
	e.waitSent(fogsync.ResourceInstance, "i-1")
	e.online.Store(true)
	e.waitFor("the running instance on the gateway", func() bool {
		inst, err := instanceOn(e.gateway, "i-1")
		return err == nil && inst.State == model.StateRunning
	})
	if inst, _ := instanceOn(e.gateway, "i-1"); inst.DeploymentId != d.Id || inst.StartVariables["level"] != 42.0 {
		t.Errorf("the instance runs the deployment with the parameters of the start: %+v", inst)
	}
}

func TestADeploymentDeletedWhileTheGatewayIsOfflineIsRemovedWhenItIsBack(t *testing.T) {
	e := newEnv(t)
	d := e.runningOnTheGateway("i-1")
	e.online.Store(false)
	if err := e.cloud.Controller.DeleteDeployment(ctx, alice, "processes", d.Id); err != nil {
		t.Fatal(err)
	}
	e.waitSent(fogsync.ResourceDeployment, d.Id)
	e.online.Store(true)
	e.waitFor("the deletion on the gateway", func() bool { _, err := deploymentOn(e.gateway, d.Id); return err != nil })
}

// process-sync kept a deployment deleted before the mgw reported it until the mgw had removed
// it; the engine sends only the deletion and forgets it once the gateway applied it
func TestADeploymentDeletedBeforeTheGatewayGotItIsForgottenWhenTheGatewayIsBack(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	e.online.Store(false)
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: waitProcess(), NetworkId: network})
	if err != nil {
		t.Fatal(err)
	}
	e.waitSent(fogsync.ResourceDeployment, d.Id)
	if err = c.DeleteDeployment(ctx, alice, "processes", d.Id); err != nil {
		t.Fatal(err)
	}
	e.waitSent(fogsync.ResourceDeployment, d.Id)
	e.online.Store(true)
	e.waitFor("the applied deletion forgotten in the cloud", func() bool { _, found := e.desired(fogsync.ResourceDeployment, d.Id); return !found })
	if _, err = deploymentOn(e.gateway, d.Id); err == nil {
		t.Error("the gateway never gets the deployment")
	}
}

func TestAnInstanceStoppedWhileTheGatewayIsOfflineIsStoppedWhenItIsBack(t *testing.T) {
	e := newEnv(t)
	e.runningOnTheGateway("i-1")
	e.online.Store(false)
	if err := e.cloud.Controller.StopInstances(ctx, alice, "processes", []string{"i-1"}); err != nil {
		t.Fatal(err)
	}
	e.waitSent(fogsync.ResourceInstance, "i-1")
	e.online.Store(true)
	e.waitFor("the stopped instance on the gateway", func() bool {
		inst, err := instanceOn(e.gateway, "i-1")
		return err == nil && inst.State == model.StateTerminated
	})
}

func TestAnInstanceDeletedWhileTheGatewayIsOfflineIsRemovedWhenItIsBack(t *testing.T) {
	e := newEnv(t)
	e.runningOnTheGateway("i-1")
	e.online.Store(false)
	if err := e.cloud.Controller.DeleteInstances(ctx, alice, "processes", []string{"i-1"}); err != nil {
		t.Fatal(err)
	}
	e.waitSent(fogsync.ResourceInstance, "i-1")
	e.online.Store(true)
	e.waitFor("the deletion on the gateway", func() bool { _, err := instanceOn(e.gateway, "i-1"); return err != nil })
}
