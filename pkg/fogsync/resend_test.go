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

	"github.com/SENERGY-Platform/automation-engine/pkg/fogsync"
	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// The reconciler of process-sync did not resend a command that might still be under way and
// sent nothing to a network without contact (SYNC-08).

func reportStatus(t *testing.T, c *fogsync.Cloud) {
	t.Helper()
	status := fogsync.Status{Envelope: fogsync.Envelope{Protocol: fogsync.Version}}
	if err := c.Handle(ctx, "processes/hub-1/state/engine/v1/status", message(t, status)); err != nil {
		t.Fatal(err)
	}
}

func desireDeployment(t *testing.T, s *store.Store, now time.Time) {
	t.Helper()
	err := s.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.Desire("hub-1", fogsync.ResourceDeployment, "d1", false, nil, now)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func reconcile(t *testing.T, c *fogsync.Cloud, broker *mqtt.Memory) int {
	t.Helper()
	if err := c.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	return len(broker.Published(fogsync.CloudCommandTopic("hub-1", fogsync.KindDesired)))
}

func TestADesiredStateIsSentAgainOnlyAfterTheResendTime(t *testing.T) {
	c, s, broker, now := newCloud(t)
	reportStatus(t, c)
	desireDeployment(t, s, *now)
	sent := []int{reconcile(t, c, broker)}
	*now = now.Add(30 * time.Second)
	sent = append(sent, reconcile(t, c, broker))
	*now = now.Add(31 * time.Second)
	sent = append(sent, reconcile(t, c, broker))
	if sent[0] != 1 || sent[1] != 1 || sent[2] != 2 {
		t.Errorf("sent at once, not again within the resend time of 1 min, then again: %v", sent)
	}
}

func TestAGatewayWithoutContactGetsItsDesiredStateOnlyWhenItReportsAgain(t *testing.T) {
	c, s, broker, now := newCloud(t)
	reportStatus(t, c)
	*now = now.Add(16 * time.Minute)
	desireDeployment(t, s, *now)
	sent := []int{reconcile(t, c, broker)}
	reportStatus(t, c)
	sent = append(sent, reconcile(t, c, broker))
	if sent[0] != 0 || sent[1] != 1 {
		t.Errorf("nothing goes to a gateway offline for longer than 15 min, everything once it reports: %v", sent)
	}
}
