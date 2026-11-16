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
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/fogsync"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// slowNamespaces holds the first lookup after arm until release, which keeps the gateway inside
// the apply of a deployment for as long as the test needs
type slowNamespaces struct {
	fogsync.Namespaces
	mux     sync.Mutex
	armed   bool
	entered chan struct{}
	release chan struct{}
}

func (this *slowNamespaces) Get(name string) (model.Namespace, bool) {
	this.mux.Lock()
	hold := this.armed
	this.armed = false
	this.mux.Unlock()
	if hold {
		close(this.entered)
		<-this.release
	}
	return this.Namespaces.Get(name)
}

// SNRGY-5156: the MQTT client hands every message to its own goroutine, so a start the cloud
// sends right after its deployment reached the gateway while the deployment was still being
// applied, failed with "deployment not found", and the cloud ended the instance with an
// incident. Messages of desired state now apply one after the other.
func TestAStartSentAfterItsDeploymentWaitsForTheDeployment(t *testing.T) {
	e := newGatewayEnv(t)
	slow := &slowNamespaces{Namespaces: e.srv.Namespaces, armed: true, entered: make(chan struct{}), release: make(chan struct{})}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	settings := fogsync.DefaultGatewaySettings()
	settings.EngineVersion = "test"
	agent := fogsync.NewGateway(settings, e.srv.Store, e.srv.Engine, slow, e.broker, logger)
	agent.SetClock(e.now)

	handle := func(message fogsync.Desired) <-chan error {
		done := make(chan error, 1)
		raw, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		go func() { done <- agent.Handle(ctx, "processes/cmd/engine/v1/"+fogsync.KindDesired, raw) }()
		return done
	}
	deployed := handle(fogsync.Desired{Envelope: envelope(), Resources: []fogsync.Resource{deployment("d1", 1, waitProcess())}})
	select {
	case <-slow.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the deployment is not being applied")
	}
	// the start arrives while the deployment is still being applied, as two messages handed to
	// two goroutines; before the fix it failed at once
	started := handle(fogsync.Desired{Envelope: envelope(), Resources: []fogsync.Resource{start("i1", "d1", 1)}})
	select {
	case err := <-started:
		t.Fatalf("the start was applied while its deployment was still being applied: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(slow.release)
	for _, done := range []<-chan error{deployed, started} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a message is not applied")
		}
	}

	acks := received[fogsync.Applied](e, fogsync.KindDesiredAck)
	if len(acks) != 2 {
		t.Fatalf("both messages are acknowledged: %+v", acks)
	}
	for _, ack := range acks {
		for _, r := range ack.Results {
			if r.Error != "" {
				t.Errorf("%v %v is applied without error: %v", r.Kind, r.Id, r.Error)
			}
		}
	}
	inst, err := e.instance("i1")
	if err != nil || inst.DeploymentId != "d1" || inst.State != model.StateRunning {
		t.Fatalf("the instance runs: %+v %v", inst, err)
	}
}
