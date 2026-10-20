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

	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/fogsync"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// An instance that ends while the gateway is offline for longer than the change stream is kept
// stays until the cloud has taken its end, whatever retention and storage budget say; the
// change stream keeps one entry per such instance instead of every step (LONG-07, LONG-10).
func TestAnInstanceEndedDuringALongOfflinePeriodWaitsForTheCloud(t *testing.T) {
	e := newGatewayEnv(t)
	e.mux.Lock()
	// the change stream is stamped with the wall clock
	e.clock = time.Now()
	e.mux.Unlock()
	e.srv.Engine.SetClock(e.now)
	e.send(fogsync.KindDesired, fogsync.Desired{Envelope: envelope(), Resources: []fogsync.Resource{deployment("d1", 1, waitProcess()), start("i0", "d1", 1), start("i1", "d1", 1)}})
	correlate := func(id string) {
		t.Helper()
		if _, err := e.srv.Engine.Correlate(ctx, engine.CorrelationRequest{MessageInput: model.MessageInput{Name: "go", InstanceId: id}}); err != nil {
			t.Fatal(err)
		}
	}
	correlate("i0")
	e.tick()
	batches := received[fogsync.Changes](e, fogsync.KindChanges)
	if len(batches) != 1 {
		t.Fatalf("one batch: %+v", batches)
	}
	acked := batches[0].Upto
	e.send(fogsync.KindUpAck, fogsync.UpAck{Envelope: envelope(), Acked: acked})

	// offline: i1 ends, i2 starts on the gateway, nothing reaches the cloud
	correlate("i1")
	if _, _, err := e.srv.Engine.StartInstance(ctx, engine.StartRequest{InstanceId: "i2", DeploymentId: "d1", Owner: "alice"}); err != nil {
		t.Fatal(err)
	}
	e.advance(30 * 24 * time.Hour)
	onGateway := func(id string) bool {
		_, err := e.instance(id)
		return err == nil
	}
	// the housekeeping of the engine runs on the fake clock; i0, ended and taken by the cloud,
	// goes with the retention, which judges i1 in the same pass
	deadline := time.Now().Add(10 * time.Second)
	for onGateway("i0") {
		if time.Now().After(deadline) {
			t.Fatal("the retention removes the instance the cloud has taken")
		}
		time.Sleep(10 * time.Millisecond)
	}
	err := e.srv.Store.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.TrimOldestHistory(1000)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !onGateway("i1") {
		t.Fatal("an instance whose end the cloud has not taken stays, however long the gateway is offline")
	}
	var changes []store.Change
	_ = e.srv.Store.Tx(ctx, func(tx *store.Tx) (err error) {
		changes, err = tx.ChangesAfter(acked, 1000)
		return err
	})
	perInstance := map[string]int{}
	for _, c := range changes {
		perInstance[c.InstanceId]++
	}
	if len(perInstance) != 2 || perInstance["i1"] != 1 || perInstance["i2"] != 1 {
		t.Errorf("the change stream keeps the latest entry of each instance the cloud has not taken: %v", perInstance)
	}

	// back online: the cloud learns the end, then the gateway may remove the instance
	e.tick()
	batches = received[fogsync.Changes](e, fogsync.KindChanges)
	last := batches[len(batches)-1]
	var reported *model.Instance
	for _, s := range last.Instances {
		if s.Instance.Id == "i1" {
			reported = &s.Instance
		}
	}
	if reported == nil || reported.State != model.StateCompleted {
		t.Fatalf("the batch after the reconnect carries the end of i1: %+v", last)
	}
	e.send(fogsync.KindUpAck, fogsync.UpAck{Envelope: envelope(), Acked: last.Upto})
	err = e.srv.Store.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.DeleteFinishedInstances("deployment_id", "d1", e.now(), 1000)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if onGateway("i1") {
		t.Error("once the cloud has taken the end, the retention removes the instance")
	}
}
