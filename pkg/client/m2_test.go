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

package client_test

import (
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
)

var carol = client.TestToken("carol")

// share grants user rights on a resource through permissions-v2, as the web-ui does with the owner's token
func (this env) share(t *testing.T, token string, topic string, id string, user string, rights permmodel.PermissionsMap) {
	t.Helper()
	resource, err, _ := this.permissions.GetResource(token, topic, id)
	if err != nil {
		t.Fatalf("get resource %v %v: %v", topic, id, err)
	}
	resource.UserPermissions[user] = rights
	if _, err, _ = this.permissions.SetPermission(token, topic, id, resource.ResourcePermissions); err != nil {
		t.Fatal(err)
	}
}

var readExecute = permmodel.PermissionsMap{Read: true, Execute: true}

func TestSharedModelsAreVisibleNotSharedDeploymentsAreNot(t *testing.T) {
	eachEnv(t, func(t *testing.T, e env) {
		m, err := e.CreateModel(ctx, alice, ns, model.ModelInput{Xml: waitingModel()})
		if err != nil {
			t.Fatal(err)
		}
		d, err := e.CreateDeployment(ctx, alice, ns, model.DeploymentInput{ModelId: m.Id})
		if err != nil {
			t.Fatal(err)
		}
		e.sync(t)
		e.share(t, alice, "processes_models", m.Id, "bob", readExecute)
		e.share(t, alice, "processes_deployments", d.Id, "bob", readExecute)

		if _, err = e.GetModel(ctx, bob, ns, m.Id); err != nil {
			t.Fatalf("a shared model is readable (MOD-02): %v", err)
		}
		list, err := e.ListModels(ctx, bob, ns, model.ModelListOptions{})
		if err != nil || list.Total != 1 {
			t.Fatalf("a shared model is listed: %+v %v", list, err)
		}
		if _, err = e.CreateDeployment(ctx, bob, ns, model.DeploymentInput{ModelId: m.Id}); err != nil {
			t.Fatalf("execute on the model allows deploying it: %v", err)
		}
		if _, err = e.UpdateModel(ctx, bob, ns, m.Id, model.ModelInput{Xml: waitingModel()}); !errors.Is(err, model.ErrForbidden) {
			t.Fatalf("read and execute do not allow writing, got %v", err)
		}
		if _, err = e.GetDeployment(ctx, bob, ns, d.Id); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("deployments in processes are not shareable, a share in permissions-v2 does not count: %v", err)
		}
	})
}

func TestOwnerChangeAcceptsAdministrateThroughRole(t *testing.T) {
	eachEnv(t, func(t *testing.T, e env) {
		roles := model.Namespace{Models: model.LevelSettings{Shareable: true, InitialRights: model.Principals{Roles: map[string]model.Rights{"user": {Read: true, Administrate: true}}}}}
		if _, err := e.PutNamespace(ctx, admin, "roles", roles); err != nil {
			t.Fatal(err)
		}
		m, err := e.CreateModel(ctx, alice, "roles", model.ModelInput{Xml: waitingModel()})
		if err != nil {
			t.Fatal(err)
		}
		e.sync(t)
		if err = e.SetOwner(ctx, alice, "roles", model.KindModel, m.Id, model.OwnerInput{Owner: "bob"}); err != nil {
			t.Fatalf("bob administrates the model through the role user (SEC-11): %v", err)
		}
		if got, _ := e.GetModel(ctx, bob, "roles", m.Id); got.Owner != "bob" {
			t.Fatalf("owner not changed: %+v", got)
		}
	})
}

func TestInitialRightsAndOwnerChange(t *testing.T) {
	eachEnv(t, func(t *testing.T, e env) {
		custom := model.Namespace{Models: model.LevelSettings{Shareable: true, InitialRights: model.Principals{Roles: map[string]model.Rights{"user": {Read: true}}}}}
		if _, err := e.PutNamespace(ctx, alice, "custom", custom); !errors.Is(err, model.ErrForbidden) {
			t.Fatalf("only admins manage namespaces, got %v", err)
		}
		created, err := e.PutNamespace(ctx, admin, "custom", custom)
		if err != nil || created.UserDeletion != model.UserDeletionReject || created.RestartPolicy.MaxRestarts != 10 {
			t.Fatalf("namespace with defaults: %+v %v", created, err)
		}
		if _, err, _ := e.permissions.GetTopic(client.TestToken("x", "admin"), "custom_instances"); err != nil {
			t.Fatalf("topics are created with the namespace (NS-05): %v", err)
		}
		m, err := e.CreateModel(ctx, alice, "custom", model.ModelInput{Xml: waitingModel()})
		if err != nil {
			t.Fatal(err)
		}
		e.sync(t)
		if _, err = e.GetModel(ctx, bob, "custom", m.Id); err != nil {
			t.Fatalf("initial rights give every user read: %v", err)
		}
		if err = e.SetOwner(ctx, alice, "custom", model.KindModel, m.Id, model.OwnerInput{Owner: "bob"}); !errors.Is(err, model.ErrBadRequest) {
			t.Fatalf("the new owner must administrate the model already, got %v", err)
		}
		e.share(t, alice, "custom_models", m.Id, "bob", permmodel.PermissionsMap{Read: true, Write: true, Execute: true, Administrate: true})
		if err = e.SetOwner(ctx, alice, "custom", model.KindModel, m.Id, model.OwnerInput{Owner: "bob"}); err != nil {
			t.Fatal(err)
		}
		if got, _ := e.GetModel(ctx, bob, "custom", m.Id); got.Owner != "bob" {
			t.Fatalf("owner not changed: %+v", got)
		}
		if err = e.DeleteNamespace(ctx, admin, "custom"); !errors.Is(err, model.ErrConflict) {
			t.Fatalf("a namespace with resources cannot be deleted, got %v", err)
		}
		if err = e.DeleteModel(ctx, bob, "custom", m.Id, false); err != nil {
			t.Fatal(err)
		}
		if err = e.DeleteNamespace(ctx, admin, "custom"); err != nil {
			t.Fatal(err)
		}
		if _, err = e.GetNamespace(ctx, alice, "custom"); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("namespace still there: %v", err)
		}
	})
}

func TestDeletedResourcesLeavePermissions(t *testing.T) {
	eachEnv(t, func(t *testing.T, e env) {
		m, err := e.CreateModel(ctx, alice, ns, model.ModelInput{Xml: waitingModel()})
		if err != nil {
			t.Fatal(err)
		}
		e.sync(t)
		adminToken := client.TestToken("x", "admin")
		if _, err, _ = e.permissions.GetResource(adminToken, "processes_models", m.Id); err != nil {
			t.Fatalf("the model is registered: %v", err)
		}
		if err = e.DeleteModel(ctx, alice, ns, m.Id, false); err != nil {
			t.Fatal(err)
		}
		e.sync(t)
		if _, err, code := e.permissions.GetResource(adminToken, "processes_models", m.Id); err == nil || code != 404 {
			t.Fatalf("the rights must be removed with the model, got %v %v", code, err)
		}
	})
}

func TestUserDeletionRejectsOrCascades(t *testing.T) {
	eachEnv(t, func(t *testing.T, e env) {
		const sms = "smart-services"
		release, err := e.CreateDeployment(ctx, alice, sms, model.DeploymentInput{Xml: waitingModel()})
		if err != nil {
			t.Fatal(err)
		}
		own, err := e.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: waitingModel()})
		if err != nil {
			t.Fatal(err)
		}
		carolsModel, err := e.CreateModel(ctx, carol, ns, model.ModelInput{Xml: waitingModel()})
		if err != nil {
			t.Fatal(err)
		}
		e.sync(t)
		e.share(t, alice, "smart-services_deployments", release.Id, "bob", readExecute)
		e.share(t, carol, "processes_models", carolsModel.Id, "alice", readExecute)
		bobsInstance, err := e.StartInstance(ctx, bob, sms, model.StartInstanceInput{DeploymentId: release.Id})
		if err != nil {
			t.Fatalf("bob starts an instance of the shared release (SMS-07): %v", err)
		}
		if bobsInstance.Owner != "bob" {
			t.Fatalf("the starting user owns the instance (SEC-12): %v", bobsInstance.Owner)
		}

		deps, err := e.GetUserDependents(ctx, admin, "alice")
		if err != nil {
			t.Fatal(err)
		}
		if !deps.Blocked || len(deps.Dependents) != 1 || deps.Dependents[0].Id != bobsInstance.Id || deps.Owned["smart-services_deployments"] != 1 || deps.Owned["processes_deployments"] != 1 {
			t.Fatalf("unexpected dry run %+v", deps)
		}
		err = e.DeleteUser(ctx, admin, "alice")
		if !errors.Is(err, model.ErrConflict) || len(model.AsError(err).Issues) != 1 {
			t.Fatalf("reject is the default (SEC-13), got %v", err)
		}
		if _, err = e.GetDeployment(ctx, alice, ns, own.Id); err != nil {
			t.Fatalf("a rejected deletion deletes nothing: %v", err)
		}
		if err = e.DeleteUser(ctx, alice, "alice"); !errors.Is(err, model.ErrForbidden) {
			t.Fatalf("only admins delete users, got %v", err)
		}

		namespace, _ := e.GetNamespace(ctx, admin, sms)
		namespace.UserDeletion = model.UserDeletionCascade
		if _, err = e.PutNamespace(ctx, admin, sms, namespace); err != nil {
			t.Fatal(err)
		}
		if err = e.DeleteUser(ctx, admin, "alice"); err != nil {
			t.Fatal(err)
		}
		for _, check := range []func() error{
			func() error { _, err := e.GetDeployment(ctx, admin, ns, own.Id); return err },
			func() error { _, err := e.GetDeployment(ctx, admin, sms, release.Id); return err },
			func() error { _, err := e.GetInstance(ctx, admin, sms, bobsInstance.Id); return err },
		} {
			if err := check(); !errors.Is(err, model.ErrNotFound) {
				t.Fatalf("expected deletion, got %v", err)
			}
		}
		resource, err, _ := e.permissions.GetResource(client.TestToken("x", "admin"), "processes_models", carolsModel.Id)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := resource.UserPermissions["alice"]; ok || !resource.UserPermissions["carol"].Administrate {
			t.Fatalf("alice's rights on carol's model must be gone (SEC-08): %+v", resource.UserPermissions)
		}
	})
}

func TestRetentionAboveUserLimitIsRejected(t *testing.T) {
	eachEnv(t, func(t *testing.T, e env) {
		if _, err := e.SetUserSettings(ctx, alice, "alice", model.UserSettings{MaxRetentionDays: 3}); !errors.Is(err, model.ErrForbidden) {
			t.Fatalf("users cannot raise their own limits, got %v", err)
		}
		if _, err := e.SetUserSettings(ctx, admin, "alice", model.UserSettings{MaxRetentionDays: 3}); err != nil {
			t.Fatal(err)
		}
		settings, err := e.GetUserSettings(ctx, alice, "alice")
		if err != nil || settings.MaxRetentionDays != 3 {
			t.Fatalf("users read their own limits: %+v %v", settings, err)
		}
		if _, err = e.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: waitingModel(), RetentionDays: 5}); !errors.Is(err, model.ErrBadRequest) {
			t.Fatalf("a value above the limit is rejected (HIST-04), got %v", err)
		}
		d, err := e.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: waitingModel(), RetentionDays: 2, IncidentHandling: &model.IncidentHandling{Restart: true}})
		if err != nil || d.RetentionDays != 2 {
			t.Fatalf("deploy: %+v %v", d, err)
		}
		v, err := e.GetDeploymentVersion(ctx, alice, ns, d.Id, 1)
		if err != nil || !v.IncidentHandling.Restart || v.IncidentHandling.Notify {
			t.Fatalf("incident handling is stored at the version: %+v %v", v.IncidentHandling, err)
		}
		if reset, err := e.ResetRestarts(ctx, alice, ns, d.Id); err != nil || reset.FaultySince != nil {
			t.Fatalf("reset: %+v %v", reset, err)
		}
	})
}

func TestMetricsAreExposed(t *testing.T) {
	envs := envs(t)
	e := envs["test"]
	d, err := e.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: waitingModel()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.StartInstance(ctx, alice, ns, model.StartInstanceInput{DeploymentId: d.Id}); err != nil {
		t.Fatal(err)
	}
	if err = e.server.Metrics.Collect(ctx, e.server.Store); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	e.server.Metrics.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body, _ := io.ReadAll(recorder.Body)
	for _, expected := range []string{
		`automation_engine_runs_started_total{deployment_id="` + d.Id + `",namespace="processes",user_id="alice"} 1`,
		`automation_engine_storage_rows{table="tokens"} 1`,
		`automation_engine_database_bytes`,
	} {
		if !strings.Contains(string(body), expected) {
			t.Errorf("missing %v", expected)
		}
	}
}
