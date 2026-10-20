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
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/fogsync"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// linkedCloud connects a cloud of one protocol version with a gateway of another, as the
// connector does; the protocol versions are raised for the test, only version 1 exists
func linkedCloud(t *testing.T, cloudProtocol int, gatewayProtocol int) (*fogsync.Cloud, *store.Store, *gatewayEnv) {
	t.Helper()
	c, s, cloudBroker, _ := newCloud(t)
	c.SetProtocol(cloudProtocol)
	g := newGatewayEnv(t)
	g.agent.SetProtocol(gatewayProtocol)
	err := cloudBroker.Subscribe("processes/hub-1/cmd/#", 1, func(topic string, payload []byte) error {
		return g.broker.Publish(ctx, "processes/cmd/"+strings.TrimPrefix(topic, "processes/hub-1/cmd/"), 1, payload)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = g.broker.Subscribe("processes/state/#", 1, func(topic string, payload []byte) error {
		return c.Handle(ctx, "processes/hub-1/state/"+strings.TrimPrefix(topic, "processes/state/"), payload)
	})
	if err != nil {
		t.Fatal(err)
	}
	return c, s, g
}

// exchange lets the gateway report, the cloud send the desired deployment and the gateway
// acknowledge it; it returns the version of the deployment the cloud sees applied
func exchange(t *testing.T, c *fogsync.Cloud, s *store.Store, g *gatewayEnv) int64 {
	t.Helper()
	spec := deployment("d1", 1, waitProcess())
	err := s.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.Desire("hub-1", fogsync.ResourceDeployment, "d1", false, fogsync.Resource{Deployment: spec.Deployment}, g.now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = g.agent.SendStatus(ctx); err != nil {
		t.Fatal(err)
	}
	if err = c.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	var desired store.Desired
	_ = s.Tx(ctx, func(tx *store.Tx) (err error) {
		desired, _, err = tx.GetDesired("hub-1", fogsync.ResourceDeployment, "d1")
		return err
	})
	return desired.AppliedVersion
}

func protocols(t *testing.T, g *gatewayEnv, kind string) (result []int) {
	t.Helper()
	for _, p := range g.broker.Published("processes/cmd/engine/v1/" + kind) {
		var envelope fogsync.Envelope
		if err := json.Unmarshal(p.Payload, &envelope); err != nil {
			t.Fatal(err)
		}
		result = append(result, envelope.Protocol)
	}
	return result
}

// A cloud updated to a new protocol serves a gateway of the previous one within the window in
// the format and with the version of the gateway, so that cloud and gateways can be updated one
// after the other (SYNC-11).
func TestACloudServesAGatewayOfTheProtocolBefore(t *testing.T) {
	c, s, g := linkedCloud(t, fogsync.Version+1, fogsync.Version)
	applied := exchange(t, c, s, g)
	if _, err := g.srv.Controller.GetDeployment(ctx, admin, "processes", "d1"); err != nil || applied == 0 {
		t.Fatalf("the gateway of the previous protocol applies the deployment: applied %v, %v", applied, err)
	}
	for _, kind := range []string{fogsync.KindDesired, fogsync.KindUpAck} {
		for _, p := range protocols(t, g, kind) {
			if p != fogsync.Version {
				t.Errorf("%v goes out with the protocol of the gateway: %v", kind, p)
			}
		}
	}
}

func TestACloudServesAGatewayOfItsOwnProtocol(t *testing.T) {
	c, s, g := linkedCloud(t, fogsync.Version+1, fogsync.Version+1)
	applied := exchange(t, c, s, g)
	if _, err := g.srv.Controller.GetDeployment(ctx, admin, "processes", "d1"); err != nil || applied == 0 {
		t.Fatalf("the gateway applies the deployment: applied %v, %v", applied, err)
	}
	if got := protocols(t, g, fogsync.KindDesired); len(got) != 1 || got[0] != fogsync.Version+1 {
		t.Errorf("the desired state goes out with the own protocol: %v", got)
	}
}
