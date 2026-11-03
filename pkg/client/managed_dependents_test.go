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
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

type recordingNotifier struct {
	mux  sync.Mutex
	sent []store.Notification
}

func (this *recordingNotifier) Notify(_ context.Context, n store.Notification) error {
	this.mux.Lock()
	defer this.mux.Unlock()
	this.sent = append(this.sent, n)
	return nil
}

func (this *recordingNotifier) to(userId string) (result []store.Notification) {
	this.mux.Lock()
	defer this.mux.Unlock()
	for _, n := range this.sent {
		if n.UserId == userId {
			result = append(result, n)
		}
	}
	return result
}

// A deployment of another owner that an instance of the deleted user manages through a module
// goes with that instance, and so do instances of others on it (SEC-13, NS-08): the deletion is
// rejected by default and deletes them with a notification of their owners with cascade.
func TestUserDeletionCountsDeploymentsManagedByTheUsersInstances(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := client.NewTestServer(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	notifier := &recordingNotifier{}
	srv.Engine.SetNotifier(notifier)
	c := srv.Controller
	release, err := c.CreateDeployment(ctx, alice, "smart-services", model.DeploymentInput{Xml: waitingModel()})
	if err != nil {
		t.Fatal(err)
	}
	smartService, err := c.StartInstance(ctx, alice, "smart-services", model.StartInstanceInput{DeploymentId: release.Id})
	if err != nil {
		t.Fatal(err)
	}
	// what a process deployment module of alice's smart service leaves when the deployment
	// belongs to someone else, e.g. the owner of the gateway it runs on (FOG-11)
	managed, err := c.CreateDeployment(ctx, bob, ns, model.DeploymentInput{Xml: waitingModel(), ManagedBy: smartService.Id})
	if err != nil {
		t.Fatal(err)
	}
	onManaged, err := c.StartInstance(ctx, bob, ns, model.StartInstanceInput{DeploymentId: managed.Id})
	if err != nil {
		t.Fatal(err)
	}
	bobsOwn, err := c.CreateDeployment(ctx, bob, ns, model.DeploymentInput{Xml: waitingModel()})
	if err != nil {
		t.Fatal(err)
	}

	deps, err := c.GetUserDependents(ctx, admin, "alice")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]model.Kind{}
	for _, d := range deps.Dependents {
		ids[d.Id] = d.Kind
	}
	if !deps.Blocked || len(deps.Dependents) != 2 || ids[managed.Id] != model.KindDeployment || ids[onManaged.Id] != model.KindInstance {
		t.Fatalf("the managed deployment and the instance on it are dependents: %+v", deps)
	}
	if err = c.DeleteUser(ctx, admin, "alice"); !errors.Is(err, model.ErrConflict) || len(model.AsError(err).Issues) != 2 {
		t.Fatalf("rejected by default with both named: %v", err)
	}

	namespace, err := c.GetNamespace(ctx, admin, ns)
	if err != nil {
		t.Fatal(err)
	}
	namespace.UserDeletion = model.UserDeletionCascade
	if _, err = c.PutNamespace(ctx, admin, ns, namespace); err != nil {
		t.Fatal(err)
	}
	if err = c.DeleteUser(ctx, admin, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.GetDeployment(ctx, admin, ns, managed.Id); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("the managed deployment goes with the instance that manages it: %v", err)
	}
	if _, err = c.GetInstance(ctx, admin, ns, onManaged.Id); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("its instance goes with it: %v", err)
	}
	if _, err = c.GetDeployment(ctx, bob, ns, bobsOwn.Id); err != nil {
		t.Errorf("bob's other deployment stays: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(notifier.to("bob")) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	sent := notifier.to("bob")
	if len(sent) != 2 {
		t.Fatalf("bob learns of both: %+v", sent)
	}
	for _, n := range sent {
		if !strings.Contains(n.Message, "the instance managing") {
			t.Errorf("the message names the cause: %v", n.Message)
		}
	}
}
