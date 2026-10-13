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
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/fogsync"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

func newCloud(t *testing.T) (*fogsync.Cloud, *store.Store, *mqtt.Memory, *time.Time) {
	s := storetest.OpenSQLite(t)
	broker := mqtt.NewMemory()
	c := fogsync.NewCloud(fogsync.DefaultCloudSettings(), s, broker, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	c.SetClock(func() time.Time { return now })
	return c, s, broker, &now
}

func message(t *testing.T, value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAGatewayOutsideTheProtocolWindowIsMarkedAndNotServed(t *testing.T) {
	c, s, broker, _ := newCloud(t)
	old := fogsync.Status{Envelope: fogsync.Envelope{Protocol: fogsync.Version - 2, Engine: "old"}}
	if err := c.Handle(ctx, "processes/hub-1/state/engine/v1/status", message(t, old)); err != nil {
		t.Fatal(err)
	}
	var network store.Network
	_ = s.Tx(ctx, func(tx *store.Tx) (err error) { network, _, err = tx.GetNetwork("hub-1"); return err })
	if network.Compatible || network.Protocol != fogsync.Version-2 {
		t.Errorf("an incompatible gateway is marked, not served wrongly (SYNC-11): %+v", network)
	}
	if len(broker.Published("#")) != 0 {
		t.Errorf("nothing goes to an incompatible gateway: %+v", broker.Published("#"))
	}
}

func TestBatchesAreTakenOnlyInOrder(t *testing.T) {
	c, s, broker, _ := newCloud(t)
	acks := func() (result []int64) {
		for _, p := range broker.Published("processes/hub-1/cmd/engine/v1/ack") {
			var ack fogsync.UpAck
			_ = json.Unmarshal(p.Payload, &ack)
			result = append(result, ack.Acked)
		}
		return result
	}
	envelope := fogsync.Envelope{Protocol: fogsync.Version}
	send := func(after, upto int64) {
		if err := c.Handle(ctx, "processes/hub-1/state/engine/v1/changes", message(t, fogsync.Changes{Envelope: envelope, After: after, Upto: upto})); err != nil {
			t.Fatal(err)
		}
	}
	send(5, 9)
	send(0, 4)
	send(0, 4)
	send(4, 9)
	if got := acks(); len(got) != 4 || got[0] != 0 || got[1] != 4 || got[2] != 4 || got[3] != 9 {
		t.Errorf("a gap is answered with the position, a duplicate again, the continuation taken (SYNC-10): %v", got)
	}
	var network store.Network
	_ = s.Tx(ctx, func(tx *store.Tx) (err error) { network, _, err = tx.GetNetwork("hub-1"); return err })
	if network.Acked != 9 {
		t.Errorf("%+v", network)
	}
}

func TestNetworksWithoutContactAreRemoved(t *testing.T) {
	c, s, _, now := newCloud(t)
	envelope := fogsync.Envelope{Protocol: fogsync.Version}
	if err := c.Handle(ctx, "processes/hub-1/state/engine/v1/status", message(t, fogsync.Status{Envelope: envelope})); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(729 * time.Hour)
	if err := c.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	found := func() (found bool) {
		_ = s.Tx(ctx, func(tx *store.Tx) (err error) { _, found, err = tx.GetNetwork("hub-1"); return err })
		return found
	}
	if !found() {
		t.Fatal("a network in contact within 730 h stays")
	}
	*now = now.Add(2 * time.Hour)
	if err := c.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if found() {
		t.Error("a network without contact for longer than cleanup_max_age is removed (SYNC-07)")
	}
}

func seedNetworkInstance(t *testing.T, s *store.Store, id string, state model.InstanceState, applied bool) {
	t.Helper()
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	err := s.Tx(ctx, func(tx *store.Tx) error {
		if _, err := tx.GetDeployment("d1", false); err != nil {
			if err = tx.InsertDeployment(model.Deployment{Id: "d1", Namespace: "processes", Name: "d", Owner: "alice", Version: 1, NetworkId: "hub-1", CreatedAt: now, UpdatedAt: now}); err != nil {
				return err
			}
		}
		inst := model.Instance{Id: id, Namespace: "processes", Owner: "alice", DeploymentId: "d1", DeploymentVersion: 1, State: state, NetworkId: "hub-1", CreatedAt: now, StartedAt: now, LastActivityAt: now}
		if err := tx.InsertInstance(store.Instance{Instance: inst, ActivityMinSeq: 1}); err != nil {
			return err
		}
		version, err := tx.Desire("hub-1", fogsync.ResourceInstance, id, false, fogsync.InstanceSpec{DeploymentId: "d1", Owner: "alice"}, now)
		if err != nil || !applied {
			return err
		}
		return tx.SetDesiredApplied("hub-1", fogsync.ResourceInstance, id, version, "", now)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A snapshot lacks what the gateway removed: running instances are gone, ended ones were
// removed for retention or storage and stay in the cloud with its own retention, and a
// pending instance is gone only if the gateway applied its start (LONG-10).
func TestASnapshotKeepsEndedInstancesOfTheCloud(t *testing.T) {
	c, s, _, _ := newCloud(t)
	envelope := fogsync.Envelope{Protocol: fogsync.Version}
	seedNetworkInstance(t, s, "ended", model.StateCompleted, true)
	seedNetworkInstance(t, s, "running", model.StateRunning, true)
	seedNetworkInstance(t, s, "applied", model.StatePending, true)
	seedNetworkInstance(t, s, "new", model.StatePending, false)
	snapshot := fogsync.Changes{Envelope: envelope, After: 0, Upto: 7, Snapshot: true}
	if err := c.Handle(ctx, "processes/hub-1/state/engine/v1/changes", message(t, snapshot)); err != nil {
		t.Fatal(err)
	}
	for id, kept := range map[string]bool{"ended": true, "running": false, "applied": false, "new": true} {
		var err error
		_ = s.Tx(ctx, func(tx *store.Tx) error { _, err = tx.GetInstance(id, true); return nil })
		if (err == nil) != kept {
			t.Errorf("%v kept %v, want %v", id, err == nil, kept)
		}
	}
}

// The start of an instance is done once the gateway reports its end; the cloud forgets it, so
// that the desired state does not grow with every start.
func TestAnEndedInstanceIsNoLongerDesired(t *testing.T) {
	c, s, _, _ := newCloud(t)
	envelope := fogsync.Envelope{Protocol: fogsync.Version}
	seedNetworkInstance(t, s, "i1", model.StatePending, false)
	ended := time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)
	report := fogsync.Changes{Envelope: envelope, After: 0, Upto: 3, Instances: []fogsync.InstanceState{{Instance: model.Instance{
		Id: "i1", Namespace: "processes", Owner: "alice", DeploymentId: "d1", DeploymentVersion: 1, Run: 1, State: model.StateCompleted, EndedAt: &ended,
	}}}}
	if err := c.Handle(ctx, "processes/hub-1/state/engine/v1/changes", message(t, report)); err != nil {
		t.Fatal(err)
	}
	var found bool
	_ = s.Tx(ctx, func(tx *store.Tx) (err error) {
		_, found, err = tx.GetDesired("hub-1", fogsync.ResourceInstance, "i1")
		return err
	})
	if found {
		t.Error("the start of an ended instance is forgotten")
	}
}
