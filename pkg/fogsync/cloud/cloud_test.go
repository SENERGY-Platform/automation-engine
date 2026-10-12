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

package cloud_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/fogsync/cloud"
	"github.com/SENERGY-Platform/automation-engine/pkg/fogsync/protocol"
	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

var ctx = context.Background()

func newCloud(t *testing.T) (*cloud.Cloud, *store.Store, *mqtt.Memory, *time.Time) {
	s := storetest.OpenSQLite(t)
	broker := mqtt.NewMemory()
	c := cloud.New(cloud.DefaultSettings(), s, broker, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	old := protocol.Status{Envelope: protocol.Envelope{Protocol: protocol.Version - 2, Engine: "old"}}
	if err := c.Handle(ctx, "processes/hub-1/state/engine/v1/status", message(t, old)); err != nil {
		t.Fatal(err)
	}
	var network store.Network
	_ = s.Tx(ctx, func(tx *store.Tx) (err error) { network, _, err = tx.GetNetwork("hub-1"); return err })
	if network.Compatible || network.Protocol != protocol.Version-2 {
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
			var ack protocol.UpAck
			_ = json.Unmarshal(p.Payload, &ack)
			result = append(result, ack.Acked)
		}
		return result
	}
	envelope := protocol.Envelope{Protocol: protocol.Version}
	send := func(after, upto int64) {
		if err := c.Handle(ctx, "processes/hub-1/state/engine/v1/changes", message(t, protocol.Changes{Envelope: envelope, After: after, Upto: upto})); err != nil {
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
	envelope := protocol.Envelope{Protocol: protocol.Version}
	if err := c.Handle(ctx, "processes/hub-1/state/engine/v1/status", message(t, protocol.Status{Envelope: envelope})); err != nil {
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
