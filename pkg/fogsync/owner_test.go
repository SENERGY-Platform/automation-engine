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
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/fogsync"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
)

// Deployments and instances of a gateway belong to the owner of its network (FOG-11).

func staticOwner(owner string, err error) fogsync.NetworkOwner {
	return func(context.Context) (string, error) { return owner, err }
}

func (this *gatewayEnv) gatewayOwners(deploymentId string, instanceId string) (deployment string, instance string) {
	this.t.Helper()
	err := this.srv.Store.Tx(ctx, func(tx *store.Tx) error {
		d, err := tx.GetDeployment(deploymentId, false)
		if err != nil {
			return err
		}
		i, err := tx.GetInstance(instanceId, false)
		deployment, instance = d.Owner, i.Owner
		return err
	})
	if err != nil {
		this.t.Fatal(err)
	}
	return deployment, instance
}

func TestTheGatewayGivesWhatTheCloudSendsTheOwnerOfItsNetwork(t *testing.T) {
	e := newGatewayEnv(t)
	e.agent.SetNetworkOwner(staticOwner("olga", nil))
	if err := e.agent.TakeOverOwner(ctx); err != nil {
		t.Fatal(err)
	}
	e.send(fogsync.KindDesired, fogsync.Desired{Envelope: envelope(), Resources: []fogsync.Resource{deployment("d1", 1, waitProcess()), start("i1", "d1", 1)}})
	if d, i := e.gatewayOwners("d1", "i1"); d != "olga" || i != "olga" {
		t.Errorf("deployment and instance belong to the owner of the network, not to alice the cloud named: %v %v", d, i)
	}
}

func TestTheGatewayGivesExistingDeploymentsAndInstancesTheOwnerOfItsNetwork(t *testing.T) {
	e := newGatewayEnv(t)
	e.send(fogsync.KindDesired, fogsync.Desired{Envelope: envelope(), Resources: []fogsync.Resource{deployment("d1", 1, waitProcess()), start("i1", "d1", 1)}})
	if d, i := e.gatewayOwners("d1", "i1"); d != "alice" || i != "alice" {
		t.Fatalf("without a source the gateway keeps the owners of the cloud: %v %v", d, i)
	}
	e.agent.SetNetworkOwner(staticOwner("olga", nil))
	if err := e.agent.TakeOverOwner(ctx); err != nil {
		t.Fatal(err)
	}
	if d, i := e.gatewayOwners("d1", "i1"); d != "olga" || i != "olga" {
		t.Fatalf("what the gateway received before belongs to the owner of the network: %v %v", d, i)
	}
	e.tick()
	var reported []model.Instance
	for _, batch := range received[fogsync.Changes](e, fogsync.KindChanges) {
		for _, state := range batch.Instances {
			reported = append(reported, state.Instance)
		}
	}
	if len(reported) == 0 || reported[len(reported)-1].Id != "i1" || reported[len(reported)-1].Owner != "olga" {
		t.Errorf("the change stream reports the instance with its new owner, so that the cloud takes it: %+v", reported)
	}
}

func TestTheGatewayKeepsTheOwnersWhileItsNetworkIsUnknown(t *testing.T) {
	e := newGatewayEnv(t)
	e.send(fogsync.KindDesired, fogsync.Desired{Envelope: envelope(), Resources: []fogsync.Resource{deployment("d1", 1, waitProcess()), start("i1", "d1", 1)}})

	e.agent.SetNetworkOwner(staticOwner("", nil))
	if err := e.agent.TakeOverOwner(ctx); err != nil {
		t.Errorf("a gateway without network is no error: %v", err)
	}
	if d, i := e.gatewayOwners("d1", "i1"); d != "alice" || i != "alice" {
		t.Errorf("an unpaired gateway keeps the owners: %v %v", d, i)
	}

	e.agent.SetNetworkOwner(staticOwner("", errors.New("connection refused")))
	if err := e.agent.TakeOverOwner(ctx); err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("an unreachable cloud proxy is reported: %v", err)
	}
	if d, i := e.gatewayOwners("d1", "i1"); d != "alice" || i != "alice" {
		t.Errorf("an unreachable cloud proxy keeps the owners: %v %v", d, i)
	}
}

func TestTheGatewayReadsTheOwnerOfItsNetworkAgainAfterTheInterval(t *testing.T) {
	e := newGatewayEnv(t)
	owner := "olga"
	e.agent.SetNetworkOwner(func(context.Context) (string, error) { return owner, nil })
	e.send(fogsync.KindDesired, fogsync.Desired{Envelope: envelope(), Resources: []fogsync.Resource{deployment("d1", 1, waitProcess()), start("i1", "d1", 1)}})
	e.tick()
	if d, _ := e.gatewayOwners("d1", "i1"); d != "olga" {
		t.Fatalf("the first tick reads the owner: %v", d)
	}
	owner = "oskar"
	e.tick()
	if d, _ := e.gatewayOwners("d1", "i1"); d != "olga" {
		t.Errorf("within the interval the owner is not read again: %v", d)
	}
	e.advance(fogsync.DefaultGatewaySettings().OwnerInterval)
	e.tick()
	if d, i := e.gatewayOwners("d1", "i1"); d != "oskar" || i != "oskar" {
		t.Errorf("after the interval a new owner of the network takes over: %v %v", d, i)
	}
}

func TestNetworkInfoReadsTheUserOfTheNetworkFromTheCertManager(t *testing.T) {
	answer := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/network" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(answer)
		if answer == http.StatusOK {
			_, _ = w.Write([]byte(`{"id":"hub-1","user_id":"olga","added":"2026-10-01T00:00:00Z","cloud_status":{"code":0,"error":""}}`))
		} else {
			_, _ = w.Write([]byte("no network"))
		}
	}))
	t.Cleanup(srv.Close)
	read := fogsync.NetworkInfo(srv.URL + "/api/network")
	if owner, err := read(ctx); err != nil || owner != "olga" {
		t.Errorf("user_id is the owner: %q %v", owner, err)
	}
	answer = http.StatusInternalServerError
	if owner, err := read(ctx); err == nil || owner != "" || !strings.Contains(err.Error(), "500 no network") {
		t.Errorf("another status is an error with status and body: %q %v", owner, err)
	}
}

func TestDeploymentsAndInstancesOfAGatewayBelongToTheOwnerOfTheNetwork(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	e.owners.set("olga", false)
	// alice administrates the hub, olga owns it
	d := e.gatewayDeploy(alice)
	if d.Owner != "olga" {
		t.Errorf("the deployment belongs to the owner of the network: %v", d.Owner)
	}
	admin := auth.UnsignedToken("admin", "admin")
	inst, err := c.StartInstance(ctx, admin, "processes", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	if inst.Owner != "olga" {
		t.Errorf("the instance belongs to the owner of the network, not to the starting user (SEC-12): %v", inst.Owner)
	}
	e.waitFor("the deployment and the instance of olga on the gateway", func() bool {
		gd, err := e.gatewayDeployment(d.Id)
		gi, errI := e.gatewayInstance(inst.Id)
		return err == nil && errI == nil && gd.Owner == "olga" && gi.Owner == "olga"
	})

	_, err = c.CreateDeployment(ctx, admin, "processes", model.DeploymentInput{Xml: waitProcess(), NetworkId: network, Owner: "alice"})
	if !errors.Is(err, model.ErrBadRequest) || !strings.Contains(err.Error(), "owner of the network") {
		t.Errorf("another owner is rejected, also for admins: %v", err)
	}
	_, err = c.StartInstance(ctx, admin, "processes", model.StartInstanceInput{DeploymentId: d.Id, Owner: "alice"})
	if !errors.Is(err, model.ErrBadRequest) {
		t.Errorf("an instance for another owner is rejected: %v", err)
	}
	err = c.SetOwner(ctx, admin, "processes", model.KindDeployment, d.Id, model.OwnerInput{Owner: "alice"})
	if !errors.Is(err, model.ErrBadRequest) {
		t.Errorf("the owner of a gateway deployment cannot change to another user (SEC-11): %v", err)
	}
	err = c.SetOwner(ctx, admin, "processes", model.KindInstance, inst.Id, model.OwnerInput{Owner: "alice"})
	if !errors.Is(err, model.ErrBadRequest) {
		t.Errorf("the owner of a gateway instance cannot change to another user (SEC-11): %v", err)
	}

	e.owners.mux.Lock()
	delete(e.owners.hubs, network)
	e.owners.mux.Unlock()
	_, err = c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: waitProcess(), NetworkId: network})
	if !errors.Is(err, model.ErrUnavailable) {
		t.Errorf("without the owner of the network nothing is deployed: %v", err)
	}
}

func TestTheCloudGivesExistingResourcesOfAGatewayTheOwnerOfTheNetwork(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	d := e.gatewayDeploy(alice)
	inst := e.gatewayStart(alice, d.Id)

	// the hub of alice has a new owner; the gateway has not learnt it yet
	e.owners.set("olga", false)
	if err := c.TakeOverNetworkOwners(ctx); err != nil {
		t.Fatal(err)
	}
	olga := auth.UnsignedToken("olga")
	if got, err := c.GetDeployment(ctx, olga, "processes", d.Id); err != nil || got.Owner != "olga" {
		t.Errorf("the deployment belongs to the owner of the network: %+v %v", got, err)
	}
	if got, err := c.GetInstance(ctx, olga, "processes", inst.Id); err != nil || got.Owner != "olga" {
		t.Errorf("the instance belongs to the owner of the network: %+v %v", got, err)
	}

	e.owners.mux.Lock()
	delete(e.owners.hubs, network)
	e.owners.mux.Unlock()
	if err := c.TakeOverNetworkOwners(ctx); err == nil || !strings.Contains(err.Error(), network) {
		t.Errorf("a network without readable owner is named in the error: %v", err)
	}
}

func TestTheCloudKeepsTheOwnerOfTheNetworkWhateverAGatewayReports(t *testing.T) {
	e := newEnv(t)
	d := e.gatewayDeploy(alice)
	inst := e.gatewayStart(alice, d.Id)

	// the hub has a new owner, which the gateway with an older process module does not learn
	e.owners.set("olga", false)
	if err := e.cloud.Controller.TakeOverNetworkOwners(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.gateway.Engine.Correlate(ctx, engine.CorrelationRequest{MessageInput: model.MessageInput{Name: "go", InstanceId: inst.Id}}); err != nil {
		t.Fatal(err)
	}
	var reported store.Instance
	e.waitFor("the completed instance in the cloud", func() bool {
		_ = e.cloud.Store.Tx(ctx, func(tx *store.Tx) (err error) { reported, err = tx.GetInstance(inst.Id, false); return err })
		return reported.State == model.StateCompleted
	})
	if gi, _ := e.gatewayInstance(inst.Id); gi.Owner != "alice" {
		t.Fatalf("the gateway reported its own owner: %v", gi.Owner)
	}
	if reported.Owner != "olga" {
		t.Errorf("the cloud keeps the owner of the network: %v", reported.Owner)
	}
	// the owner administrates the instance in permissions-v2 (SEC-10)
	e.waitUpTo("the rights of olga on the instance", 5*time.Second, func() bool {
		resource, err, _ := e.permissions.GetResource(auth.UnsignedToken("admin", "admin"), model.Topic("processes", model.KindInstance), inst.Id)
		return err == nil && resource.UserPermissions["olga"].Administrate
	})
}

func TestTheCloudTakesTheOwnerOfTheNetworkForInstancesStartedOnTheGateway(t *testing.T) {
	e := newEnv(t)
	d := e.gatewayDeploy(alice)
	e.owners.set("olga", false)
	if err := e.cloud.Controller.TakeOverNetworkOwners(ctx); err != nil {
		t.Fatal(err)
	}
	// a start on the gateway itself, e.g. by a device event, with the owner the gateway knows
	inst, _, err := e.gateway.Engine.StartInstance(ctx, engine.StartRequest{DeploymentId: d.Id, Owner: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	e.waitFor("the instance of the network owner in the cloud", func() bool {
		var owner string
		_ = e.cloud.Store.Tx(ctx, func(tx *store.Tx) error {
			i, err := tx.GetInstance(inst.Id, false)
			owner = i.Owner
			return err
		})
		return owner == "olga"
	})
}

// whoever deploys or starts on a gateway of another owner gets r, w and a on the result (FOG-11)

func TestWhoDeploysOnTheGatewayOfAnotherOwnerSeesAndDeletesTheDeployment(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	e.owners.set("olga", false)
	d := e.gatewayDeploy(alice)
	if d.Owner != "olga" {
		t.Fatalf("the deployment belongs to the owner of the network: %v", d.Owner)
	}
	if got, err := c.GetDeployment(ctx, alice, "processes", d.Id); err != nil || got.Id != d.Id {
		t.Errorf("alice sees the deployment she made: %v", err)
	}
	page, err := c.ListDeployments(ctx, alice, "processes", model.DeploymentListOptions{})
	if err != nil || len(page.Items) != 1 || page.Items[0].Id != d.Id {
		t.Errorf("alice lists the deployment she made: %+v %v", page.Items, err)
	}
	if _, err = c.GetDeployment(ctx, bob, "processes", d.Id); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("bob, who only reads the hub, does not see it: %v", err)
	}
	if page, _ = c.ListDeployments(ctx, bob, "processes", model.DeploymentListOptions{}); len(page.Items) != 0 {
		t.Errorf("nor lists it: %+v", page.Items)
	}
	if _, err = c.StartInstance(ctx, alice, "processes", model.StartInstanceInput{DeploymentId: d.Id}); !errors.Is(err, model.ErrForbidden) {
		t.Errorf("starting needs execute, which alice did not get: %v", err)
	}
	if err = c.DeleteDeployment(ctx, alice, "processes", d.Id); err != nil {
		t.Fatalf("alice deletes the deployment she made: %v", err)
	}
	e.waitFor("the deleted deployment gone from the gateway", func() bool {
		_, err := e.gatewayDeployment(d.Id)
		return errors.Is(err, model.ErrNotFound)
	})
}

func TestWhoStartsOnTheGatewayOfAnotherOwnerSeesStopsAndDeletesTheInstance(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	e.owners.set("olga", false)
	d := e.gatewayDeploy(alice)
	// olga lets bob, who may start on the hub, start her deployment
	admin := auth.UnsignedToken("admin", "admin")
	topic := model.Topic("processes", model.KindDeployment)
	resource, err, _ := e.permissions.GetResource(admin, topic, d.Id)
	if err != nil {
		t.Fatal(err)
	}
	resource.UserPermissions["bob"] = permmodel.PermissionsMap{Read: true, Execute: true}
	if _, err, _ = e.permissions.SetPermission(admin, topic, d.Id, resource.ResourcePermissions); err != nil {
		t.Fatal(err)
	}

	inst := e.gatewayStart(bob, d.Id)
	if inst.Owner != "olga" {
		t.Fatalf("the instance belongs to the owner of the network: %v", inst.Owner)
	}
	if got, err := c.GetInstance(ctx, bob, "processes", inst.Id); err != nil || got.Id != inst.Id {
		t.Errorf("bob sees the instance he started: %v", err)
	}
	page, err := c.ListInstances(ctx, bob, "processes", model.InstanceListOptions{})
	if err != nil || len(page.Items) != 1 || page.Items[0].Id != inst.Id {
		t.Errorf("bob lists the instance he started: %+v %v", page.Items, err)
	}
	if _, err = c.GetInstance(ctx, alice, "processes", inst.Id); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("alice, who did not start it, does not see it: %v", err)
	}
	if err = c.StopInstances(ctx, bob, "processes", []string{inst.Id}); err != nil {
		t.Fatalf("bob stops the instance he started: %v", err)
	}
	if err = c.DeleteInstances(ctx, bob, "processes", []string{inst.Id}); err != nil {
		t.Fatalf("bob deletes the instance he started: %v", err)
	}
}

func TestAFailedGrantCreatesNothing(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	e.owners.set("olga", false)
	e.adminTokenFails.Store(true)
	_, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: waitProcess(), NetworkId: network})
	if !errors.Is(err, model.ErrUnavailable) || !strings.Contains(err.Error(), "keycloak unreachable") {
		t.Fatalf("the deployment fails with the reason: %v", err)
	}
	page, _ := c.ListDeployments(ctx, auth.UnsignedToken("admin", "admin"), "processes", model.DeploymentListOptions{})
	if len(page.Items) != 0 {
		t.Errorf("nothing is created that alice could not see: %+v", page.Items)
	}
}

func TestTheNamespaceDecidesTheRightsOfWhoDeploysOnTheGatewayOfAnotherOwner(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	e.owners.set("olga", false)
	ns, _ := e.cloud.Namespaces.Get("processes")
	if rights := *ns.GatewayCreatorRights; rights != (model.Rights{Read: true, Write: true, Administrate: true}) {
		t.Errorf("by default read, write and administrate: %+v", rights)
	}

	ns.GatewayCreatorRights = &model.Rights{Read: true}
	if _, _, err := e.cloud.Namespaces.Put(ctx, ns); err != nil {
		t.Fatal(err)
	}
	d := e.gatewayDeploy(alice)
	if _, err := c.GetDeployment(ctx, alice, "processes", d.Id); err != nil {
		t.Errorf("read lets alice see the deployment: %v", err)
	}
	if err := c.DeleteDeployment(ctx, alice, "processes", d.Id); !errors.Is(err, model.ErrForbidden) {
		t.Errorf("without administrate alice does not delete it: %v", err)
	}

	ns.GatewayCreatorRights = &model.Rights{}
	if _, _, err := e.cloud.Namespaces.Put(ctx, ns); err != nil {
		t.Fatal(err)
	}
	d = e.gatewayDeploy(alice)
	if _, err := c.GetDeployment(ctx, alice, "processes", d.Id); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("no rights leave the deployment to the owner of the network: %v", err)
	}
}
