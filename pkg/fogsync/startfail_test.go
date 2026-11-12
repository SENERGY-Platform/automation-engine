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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/fogsync"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// SNRGY-3689: a smart service that starts a process on a gateway learns of a failed start only
// through the state of the instance in the cloud; a start the gateway refuses must not stay
// pending there, or nothing ever reports it
func TestAStartTheGatewayRefusesEndsTheInstanceInTheCloud(t *testing.T) {
	e := newEnv(t)
	notifier := &recordingNotifier{}
	e.cloud.Engine.SetNotifier(notifier)
	c := e.cloud.Controller
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: waitProcess(), NetworkId: network})
	if err != nil {
		t.Fatal(err)
	}
	e.waitFor("the deployment on the gateway", func() bool { _, err := deploymentOn(e.gateway, d.Id); return err == nil })
	// the gateway lost the deployment, e.g. with its database, so that it cannot start it
	if err = e.gateway.Store.Tx(ctx, func(tx *store.Tx) error { return tx.DeleteDeployment(d.Id) }); err != nil {
		t.Fatal(err)
	}
	if _, err = c.PutInstance(ctx, alice, "processes", "i-1", model.StartInstanceInput{DeploymentId: d.Id}); err != nil {
		t.Fatal(err)
	}
	e.waitForTheStartToSettle("i-1")

	var inst model.Instance
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if inst, err = c.GetInstance(ctx, alice, "processes", "i-1"); err != nil {
			t.Fatal(err)
		}
		if inst.State != model.StatePending {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if inst.State != model.StateIncident || !strings.Contains(inst.EndReason, "gateway") {
		t.Fatalf("a start the gateway refuses is an incident in the cloud that names the gateway: %v %q", inst.State, inst.EndReason)
	}
	if _, found := e.desired(fogsync.ResourceInstance, "i-1"); found {
		t.Errorf("the failed start is done and not kept in the desired state of the gateway")
	}
	incidents, err := c.ListIncidents(ctx, alice, "processes", model.IncidentListOptions{InstanceId: "i-1"})
	if err != nil || len(incidents.Items) != 1 || incidents.Items[0].Message != inst.EndReason {
		t.Errorf("the failed start is listed as incident of the instance: %+v %v", incidents, err)
	}
	e.waitFor("the notification of the failed start", func() bool { return len(notifier.messages()) > 0 })
	// a second one would go out with the next pass of the notification loop
	time.Sleep(1500 * time.Millisecond)
	sent := notifier.messages()
	if len(sent) != 1 || sent[0].UserId != inst.Owner || sent[0].Title != "Process-Incident in "+inst.DeploymentName || sent[0].Message != inst.EndReason {
		t.Errorf("with notify, the owner is told once, as the gateway would have: %+v", sent)
	}
}

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

func (this *recordingNotifier) messages() []store.Notification {
	this.mux.Lock()
	defer this.mux.Unlock()
	return append([]store.Notification{}, this.sent...)
}
