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
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/fogsync"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

var admin = client.TestToken("gateway", "admin")

// gatewayEnv is a gateway with its agent; the test plays the cloud on the broker of the gateway
type gatewayEnv struct {
	t      *testing.T
	srv    *server.Server
	agent  *fogsync.Gateway
	broker *mqtt.Memory
	mux    sync.Mutex
	clock  time.Time
}

func newGatewayEnv(t *testing.T) *gatewayEnv {
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	config := configuration.Default()
	config.SqlitePath = storetest.MemoryDsn()
	config.PollInterval = "10ms"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := server.New(ctx, config, logger, server.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	srv.Start(ctx)
	this := &gatewayEnv{t: t, srv: srv, broker: mqtt.NewMemory(), clock: time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)}
	settings := fogsync.DefaultGatewaySettings()
	settings.EngineVersion = "test"
	this.agent = fogsync.NewGateway(settings, srv.Store, srv.Engine, srv.Namespaces, this.broker, logger)
	this.agent.SetClock(this.now)
	if err = this.broker.Subscribe(fogsync.GatewayCommandFilter, 1, func(topic string, payload []byte) error {
		return this.agent.Handle(ctx, topic, payload)
	}); err != nil {
		t.Fatal(err)
	}
	return this
}

func (this *gatewayEnv) now() time.Time {
	this.mux.Lock()
	defer this.mux.Unlock()
	return this.clock
}

func (this *gatewayEnv) advance(d time.Duration) {
	this.mux.Lock()
	this.clock = this.clock.Add(d)
	this.mux.Unlock()
}

// send plays the cloud: a message of a kind to the gateway
func (this *gatewayEnv) send(kind string, message any) {
	this.t.Helper()
	raw, err := json.Marshal(message)
	if err != nil {
		this.t.Fatal(err)
	}
	if err = this.broker.Publish(ctx, "processes/cmd/engine/v1/"+kind, 1, raw); err != nil {
		this.t.Fatal(err)
	}
}

func envelope() fogsync.Envelope {
	return fogsync.Envelope{Protocol: fogsync.Version, Engine: "cloud", SentAt: time.Now()}
}

// received decodes what the gateway sent of a kind
func received[T any](this *gatewayEnv, kind string) (result []T) {
	this.t.Helper()
	for _, p := range this.broker.Published(fogsync.GatewayStateTopic(kind)) {
		var message T
		if err := json.Unmarshal(p.Payload, &message); err != nil {
			this.t.Fatal(err)
		}
		result = append(result, message)
	}
	return result
}

func (this *gatewayEnv) tick() {
	this.t.Helper()
	if err := this.agent.Tick(ctx); err != nil {
		this.t.Fatal(err)
	}
}

func deployment(id string, version int, xml string) fogsync.Resource {
	return fogsync.Resource{Kind: fogsync.ResourceDeployment, Id: id, Version: int64(version), Deployment: &fogsync.DeploymentSpec{
		Deployment: model.Deployment{Namespace: "processes", Name: "loop", Owner: "alice", Version: version, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		Version:    model.DeploymentVersion{Version: version, Xml: xml, UpdateStrategy: model.UpdateDrain},
	}}
}

func start(id string, deploymentId string, version int64) fogsync.Resource {
	return fogsync.Resource{Kind: fogsync.ResourceInstance, Id: id, Version: version, Instance: &fogsync.InstanceSpec{DeploymentId: deploymentId, Owner: "alice", Variables: map[string]any{"room": "kitchen"}}}
}

func (this *gatewayEnv) instance(id string) (model.Instance, error) {
	var result model.Instance
	err := this.srv.Store.Tx(ctx, func(tx *store.Tx) error {
		inst, err := tx.GetInstance(id, false)
		result = inst.Instance
		return err
	})
	return result, err
}

func TestDesiredStateIsAppliedIdempotentlyWithTheIdsAndVersionsOfTheCloud(t *testing.T) {
	e := newGatewayEnv(t)
	e.send(fogsync.KindDesired, fogsync.Desired{Envelope: envelope(), Resources: []fogsync.Resource{deployment("d1", 3, waitProcess()), start("i1", "d1", 1)}})
	acks := received[fogsync.Applied](e, fogsync.KindDesiredAck)
	if len(acks) != 1 || len(acks[0].Results) != 2 || acks[0].Results[0].Error != "" || acks[0].Results[1].Error != "" {
		t.Fatalf("both resources are applied and acknowledged: %+v", acks)
	}
	d, err := e.srv.Controller.GetDeployment(ctx, admin, "processes", "d1")
	if err != nil || d.Version != 3 || d.Owner != "alice" {
		t.Fatalf("the deployment has the id, version and owner of the cloud (SYNC-01): %+v %v", d, err)
	}
	inst, err := e.instance("i1")
	if err != nil || inst.DeploymentId != "d1" || inst.State != model.StateRunning || inst.StartVariables["room"] != "kitchen" {
		t.Fatalf("the instance runs with the id of the cloud: %+v %v", inst, err)
	}

	// a repeated message changes nothing and is acknowledged with the same result
	e.send(fogsync.KindDesired, fogsync.Desired{Envelope: envelope(), Resources: []fogsync.Resource{deployment("d1", 3, waitProcess()), start("i1", "d1", 1)}})
	if acks = received[fogsync.Applied](e, fogsync.KindDesiredAck); len(acks) != 2 || acks[1].Results[0].Version != 3 || acks[1].Results[0].Error != "" {
		t.Fatalf("a repeated message is acknowledged again: %+v", acks)
	}
	if d, _ = e.srv.Controller.GetDeployment(ctx, admin, "processes", "d1"); d.Version != 3 {
		t.Errorf("no new version: %v", d.Version)
	}

	// a newer version updates the deployment, a failing resource reports its error (SYNC-13)
	broken := deployment("d2", 1, "<no bpmn/>")
	e.send(fogsync.KindDesired, fogsync.Desired{Envelope: envelope(), Resources: []fogsync.Resource{deployment("d1", 4, waitProcess()), broken}})
	acks = received[fogsync.Applied](e, fogsync.KindDesiredAck)
	last := acks[len(acks)-1].Results
	if last[0].Error != "" || last[1].Error == "" {
		t.Fatalf("the update applies, the broken deployment fails with a message: %+v", last)
	}
	if d, _ = e.srv.Controller.GetDeployment(ctx, admin, "processes", "d1"); d.Version != 4 {
		t.Errorf("the deployment has version 4: %v", d.Version)
	}

	// stop and delete follow the desired state
	stopped := start("i1", "d1", 2)
	stopped.Instance.Stopped = true
	e.send(fogsync.KindDesired, fogsync.Desired{Envelope: envelope(), Resources: []fogsync.Resource{stopped}})
	if inst, _ = e.instance("i1"); inst.State != model.StateTerminated {
		t.Fatalf("the instance is stopped: %+v", inst)
	}
	e.send(fogsync.KindDesired, fogsync.Desired{Envelope: envelope(), Resources: []fogsync.Resource{{Kind: fogsync.ResourceInstance, Id: "i1", Version: 3, Deleted: true}, {Kind: fogsync.ResourceDeployment, Id: "d1", Version: 5, Deleted: true}}})
	if _, err = e.instance("i1"); err == nil {
		t.Errorf("the instance is deleted")
	}
	if _, err = e.srv.Controller.GetDeployment(ctx, admin, "processes", "d1"); err == nil {
		t.Errorf("the deployment is deleted")
	}
}

func TestMessagesOfAnotherProtocolVersionAreNotApplied(t *testing.T) {
	e := newGatewayEnv(t)
	other := envelope()
	other.Protocol = fogsync.Version + 1
	e.send(fogsync.KindDesired, fogsync.Desired{Envelope: other, Resources: []fogsync.Resource{deployment("d1", 1, waitProcess())}})
	if acks := received[fogsync.Applied](e, fogsync.KindDesiredAck); len(acks) != 0 {
		t.Fatalf("a gateway does not guess the meaning of a newer protocol (SYNC-11): %+v", acks)
	}
}

func TestChangeStreamIsFoldedAcknowledgedAndSentAgain(t *testing.T) {
	e := newGatewayEnv(t)
	e.send(fogsync.KindDesired, fogsync.Desired{Envelope: envelope(), Resources: []fogsync.Resource{deployment("d1", 1, waitProcess()), start("i1", "d1", 1), start("i2", "d1", 1)}})
	e.tick()
	statuses := received[fogsync.Status](e, fogsync.KindStatus)
	if len(statuses) != 1 || statuses[0].Protocol != fogsync.Version || statuses[0].Engine != "test" || len(statuses[0].Applied) != 3 {
		t.Fatalf("the status reports versions and applied resources (SYNC-11, SYNC-13): %+v", statuses)
	}
	batches := received[fogsync.Changes](e, fogsync.KindChanges)
	if len(batches) != 1 || batches[0].After != 0 || batches[0].Snapshot || len(batches[0].Instances) != 2 {
		t.Fatalf("a batch with the latest state of each instance, not one entry per step (LONG-09): %+v", batches)
	}
	upto := batches[0].Upto

	// without acknowledgement nothing new goes out until the resend time, then the same batch
	e.tick()
	if n := len(received[fogsync.Changes](e, fogsync.KindChanges)); n != 1 {
		t.Fatalf("one batch in flight: %v", n)
	}
	e.advance(31 * time.Second)
	e.tick()
	batches = received[fogsync.Changes](e, fogsync.KindChanges)
	if len(batches) != 2 || batches[1].After != 0 || batches[1].Upto != upto {
		t.Fatalf("the batch goes again after the resend time: %+v", batches)
	}

	e.send(fogsync.KindUpAck, fogsync.UpAck{Envelope: envelope(), Acked: upto})
	e.tick()
	if n := len(received[fogsync.Changes](e, fogsync.KindChanges)); n != 2 {
		t.Fatalf("nothing changed after the acknowledged position: %v", n)
	}
	if _, err := e.srv.Engine.Correlate(ctx, engine.CorrelationRequest{MessageInput: model.MessageInput{Name: "go", InstanceId: "i1"}}); err != nil {
		t.Fatal(err)
	}
	e.tick()
	batches = received[fogsync.Changes](e, fogsync.KindChanges)
	last := batches[len(batches)-1]
	if last.After != upto || len(last.Instances) != 1 || last.Instances[0].Instance.Id != "i1" || last.Instances[0].Instance.State != model.StateCompleted {
		t.Fatalf("the next batch continues after the acknowledged position: %+v", last)
	}
}

func TestPurgedChangesAreReplacedByASnapshot(t *testing.T) {
	e := newGatewayEnv(t)
	e.send(fogsync.KindDesired, fogsync.Desired{Envelope: envelope(), Resources: []fogsync.Resource{deployment("d1", 1, waitProcess()), start("i1", "d1", 1)}})
	err := e.srv.Store.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.PurgeChanges(time.Now().Add(time.Hour), 1000)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// a later change; the earlier ones are gone
	e.send(fogsync.KindDesired, fogsync.Desired{Envelope: envelope(), Resources: []fogsync.Resource{start("i2", "d1", 1)}})
	e.tick()
	batches := received[fogsync.Changes](e, fogsync.KindChanges)
	if len(batches) != 1 || !batches[0].Snapshot || len(batches[0].Instances) != 2 {
		t.Fatalf("a gateway that cannot continue sends a snapshot (LONG-10): %+v", batches)
	}
	if batches[0].Instances[0].Instance.Id != "i1" || batches[0].Instances[1].Instance.Id != "i2" {
		t.Errorf("the snapshot holds every instance: %+v", batches[0].Instances)
	}
}
