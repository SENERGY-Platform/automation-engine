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
	"errors"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

func (this *env) gatewayInstance(id string) (store.Instance, error) {
	var inst store.Instance
	err := this.gateway.Store.Tx(ctx, func(tx *store.Tx) (err error) { inst, err = tx.GetInstance(id, false); return err })
	return inst, err
}

func (this *env) gatewayDeploy(token string) model.Deployment {
	this.t.Helper()
	d, err := this.cloud.Controller.CreateDeployment(ctx, token, "processes", model.DeploymentInput{Xml: waitProcess(), NetworkId: network})
	if err != nil {
		this.t.Fatal(err)
	}
	this.waitFor("the deployment on the gateway", func() bool { _, err := this.gatewayDeployment(d.Id); return err == nil })
	return d
}

func (this *env) gatewayStart(token string, deploymentId string) model.Instance {
	this.t.Helper()
	inst, err := this.cloud.Controller.StartInstance(ctx, token, "processes", model.StartInstanceInput{DeploymentId: deploymentId})
	if err != nil {
		this.t.Fatal(err)
	}
	this.waitFor("the instance on the gateway", func() bool {
		gi, err := this.gatewayInstance(inst.Id)
		return err == nil && gi.State == model.StateRunning
	})
	return inst
}

// A module removal of a deployment or instance of a gateway reaches the gateway as a deletion
// through the API does (SMS-12, SMS-06).
func TestRemovedModulesDeleteTheirResourcesOnTheGateway(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	removed := e.gatewayDeploy(alice)
	kept := e.gatewayDeploy(alice)
	inst := e.gatewayStart(alice, kept.Id)

	host, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: waitProcess()})
	if err != nil {
		t.Fatal(err)
	}
	service, err := c.StartInstance(ctx, alice, "processes", model.StartInstanceInput{DeploymentId: host.Id})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	refs := []model.ResourceRef{{Kind: model.ResourceDeployment, Id: removed.Id}, {Kind: model.ResourceInstance, Id: inst.Id}}
	err = e.cloud.Store.Tx(ctx, func(tx *store.Tx) error {
		for i, ref := range refs {
			m := model.Module{Id: "m" + string(rune('1'+i)), Namespace: "processes", InstanceId: service.Id, Owner: "alice", ModuleType: "process_deployment",
				Data: map[string]any{}, DeleteInfo: &model.ModuleDeleteInfo{Resource: &ref}, CreatedAt: now, UpdatedAt: now}
			if err := tx.UpsertModule(m); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"m1", "m2"} {
		if err = c.DeleteModule(ctx, alice, "processes", id); err != nil {
			t.Fatal(err)
		}
	}
	if err = e.cloud.ModuleRemover.Process(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = c.GetDeployment(ctx, alice, "processes", removed.Id); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("the cloud removes the deployment: %v", err)
	}
	e.waitFor("the removed deployment gone from the gateway", func() bool {
		_, err := e.gatewayDeployment(removed.Id)
		return errors.Is(err, model.ErrNotFound)
	})
	e.waitFor("the removed instance gone from the gateway", func() bool {
		_, err := e.gatewayInstance(inst.Id)
		return errors.Is(err, model.ErrNotFound)
	})
	if _, err = e.gatewayDeployment(kept.Id); err != nil {
		t.Errorf("the deployment of the removed instance stays on the gateway: %v", err)
	}
}

// Deleting a user deletes the user's deployments and instances on the gateways as well (SEC-08).
func TestDeletingAUserDeletesTheirResourcesOnTheGateway(t *testing.T) {
	e := newEnv(t)
	operator := client.TestToken("operator", "admin")
	carol := client.TestToken("carol", "admin")
	d := e.gatewayDeploy(alice)
	alices := e.gatewayStart(alice, d.Id)
	carols := e.gatewayStart(carol, d.Id)

	if err := e.cloud.Controller.DeleteUser(ctx, operator, "carol"); err != nil {
		t.Fatal(err)
	}
	e.waitFor("the instance of the deleted user gone from the gateway", func() bool {
		_, err := e.gatewayInstance(carols.Id)
		return errors.Is(err, model.ErrNotFound)
	})
	if gi, err := e.gatewayInstance(alices.Id); err != nil || gi.State != model.StateRunning {
		t.Fatalf("the instance of another user keeps running: %+v %v", gi, err)
	}

	if err := e.cloud.Controller.DeleteUser(ctx, operator, "alice"); err != nil {
		t.Fatal(err)
	}
	e.waitFor("the deployment of the deleted user gone from the gateway", func() bool {
		_, err := e.gatewayDeployment(d.Id)
		return errors.Is(err, model.ErrNotFound)
	})
	e.waitFor("its instance gone from the gateway", func() bool {
		_, err := e.gatewayInstance(alices.Id)
		return errors.Is(err, model.ErrNotFound)
	})
}
