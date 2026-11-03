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
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/fogsync"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// deployOnGateway deploys the wait process to the gateway and waits until the gateway has it
func deployOnGateway(t *testing.T, e *env) model.Deployment {
	t.Helper()
	d, err := e.cloud.Controller.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: waitProcess(), NetworkId: network})
	if err != nil {
		t.Fatal(err)
	}
	e.waitFor("the deployment on the gateway", func() bool { _, err := e.gatewayDeployment(d.Id); return err == nil })
	return d
}

// markFaulty sets the mark the gateway sets after a restart loop (INC-04); the loop itself is
// tested with the engine, here only what the synchronisation makes of the mark
func markFaulty(t *testing.T, e *env, id string, since time.Time) {
	t.Helper()
	err := e.gateway.Store.Tx(ctx, func(tx *store.Tx) error {
		return tx.SetRestartStats(id, store.RestartStats{WindowStart: since, Count: 4, FaultySince: &since})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func cloudFaultySince(e *env, id string) *time.Time {
	d, _ := e.cloud.Controller.GetDeployment(ctx, alice, "processes", id)
	return d.FaultySince
}

func gatewayFaultySince(t *testing.T, e *env, id string) *time.Time {
	t.Helper()
	var stats store.RestartStats
	err := e.gateway.Store.Tx(ctx, func(tx *store.Tx) (err error) { stats, err = tx.GetRestartStats(id); return err })
	if err != nil {
		t.Fatal(err)
	}
	return stats.FaultySince
}

// A gateway counts the restarts of its instances; a deployment it stopped restarting after a
// restart loop shows as faulty in the cloud as well, and a reset in the cloud reaches the
// gateway (SYNC-02, INC-04).
func TestAFaultyDeploymentOfAGatewayIsReportedAndResetFromTheCloud(t *testing.T) {
	e := newEnv(t)
	d := deployOnGateway(t, e)
	since := time.UnixMilli(time.Now().Add(-time.Minute).UnixMilli()).UTC()
	markFaulty(t, e, d.Id, since)

	if err := e.gateway.Sync.SendStatus(ctx); err != nil {
		t.Fatal(err)
	}
	e.waitFor("the faulty mark in the cloud", func() bool {
		s := cloudFaultySince(e, d.Id)
		return s != nil && s.Equal(since)
	})

	result, err := e.cloud.Controller.ResetRestarts(ctx, alice, "processes", d.Id)
	if err != nil {
		t.Fatal(err)
	}
	if result.FaultySince != nil {
		t.Errorf("the reset answers with the deployment without mark: %+v", result.FaultySince)
	}
	if s := gatewayFaultySince(t, e, d.Id); s != nil {
		t.Errorf("the gateway restarts the instances of the deployment again: %v", s)
	}
	// the gateway reports at once after a reset, so that a status sent before it does not mark
	// the deployment again for an interval; this one keeps it unmarked
	if err = e.gateway.Sync.SendStatus(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if s := cloudFaultySince(e, d.Id); s != nil {
		t.Errorf("the mark does not come back after the reset: %v", s)
	}

	// a mark the gateway clears itself, e.g. with a new version of the deployment, goes in the
	// cloud with the next status
	markFaulty(t, e, d.Id, since)
	_ = e.gateway.Sync.SendStatus(ctx)
	e.waitFor("the mark again", func() bool { return cloudFaultySince(e, d.Id) != nil })
	if err = e.gateway.Store.Tx(ctx, func(tx *store.Tx) error { return tx.SetRestartStats(d.Id, store.RestartStats{}) }); err != nil {
		t.Fatal(err)
	}
	_ = e.gateway.Sync.SendStatus(ctx)
	e.waitFor("the mark cleared by the gateway", func() bool { return cloudFaultySince(e, d.Id) == nil })
}

// The reset of a deployment of an unreachable gateway fails and changes nothing, since the
// gateway would go on with its mark and report it again (SYNC-02).
func TestTheResetOfAnUnreachableGatewayFails(t *testing.T) {
	e := newEnv(t)
	d := deployOnGateway(t, e)
	since := time.UnixMilli(time.Now().UnixMilli()).UTC()
	markFaulty(t, e, d.Id, since)
	_ = e.gateway.Sync.SendStatus(ctx)
	e.waitFor("the faulty mark in the cloud", func() bool { return cloudFaultySince(e, d.Id) != nil })

	e.online.Store(false)
	_, err := e.cloud.Controller.ResetRestarts(ctx, alice, "processes", d.Id)
	if model.AsError(err).Kind != model.KindUnavailable {
		t.Fatalf("an unreachable gateway answers unavailable: %v", err)
	}
	if cloudFaultySince(e, d.Id) == nil || gatewayFaultySince(t, e, d.Id) == nil {
		t.Errorf("nothing changes without the gateway")
	}
	if _, err = e.cloud.Controller.ResetRestarts(ctx, bob, "processes", d.Id); err == nil {
		t.Errorf("bob may not change the deployment of alice")
	}
}

// A gateway of an older version reports no marks; its status leaves those in the cloud as they
// are instead of clearing them.
func TestTheStatusOfAnOlderGatewayKeepsTheMarks(t *testing.T) {
	e := newEnv(t)
	d := deployOnGateway(t, e)
	since := time.UnixMilli(time.Now().UnixMilli()).UTC()
	markFaulty(t, e, d.Id, since)
	_ = e.gateway.Sync.SendStatus(ctx)
	e.waitFor("the faulty mark in the cloud", func() bool { return cloudFaultySince(e, d.Id) != nil })

	var contact time.Time
	_ = e.cloud.Store.Tx(ctx, func(tx *store.Tx) (err error) {
		n, _, err := tx.GetNetwork(network)
		contact = n.LastContact
		return err
	})
	payload, err := json.Marshal(map[string]any{"protocol": fogsync.Version, "engine": "v0.24.0", "sent_at": time.Now().UTC(), "started_at": time.Now().UTC(), "storage": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.gatewayBroker.Publish(ctx, fogsync.GatewayStateTopic(fogsync.KindStatus), 1, payload); err != nil {
		t.Fatal(err)
	}
	e.waitFor("the status of the older gateway", func() bool {
		var n store.Network
		_ = e.cloud.Store.Tx(ctx, func(tx *store.Tx) (err error) { n, _, err = tx.GetNetwork(network); return err })
		return n.LastContact.After(contact)
	})
	if s := cloudFaultySince(e, d.Id); s == nil || !s.Equal(since) {
		t.Errorf("the mark stays: %v", s)
	}
}
