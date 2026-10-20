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

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// The resources of a network removed for lack of contact leave like any other deletion: their
// rights are removed from permissions-v2 and the change stream reports the instances as
// deleted (SYNC-07).
func TestCleaningUpANetworkRemovesTheRightsOfItsResources(t *testing.T) {
	c, s, _, now := newCloud(t)
	reportStatus(t, c)
	seedNetworkInstance(t, s, "i1", model.StateRunning, true)
	seedNetworkInstance(t, s, "i2", model.StateCompleted, true)
	*now = now.Add(731 * time.Hour)
	if err := c.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	removals := map[string]bool{}
	var deleted []string
	err := s.Tx(ctx, func(tx *store.Tx) error {
		due, err := tx.DueRemovals(time.Now().Add(time.Hour), 100)
		for _, r := range due {
			removals[r.Topic+"/"+r.ResourceId] = true
		}
		if err != nil {
			return err
		}
		changes, err := tx.ChangesAfter(0, 100)
		for _, change := range changes {
			if change.Deleted {
				deleted = append(deleted, change.InstanceId)
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"processes_deployments/d1", "processes_instances/i1", "processes_instances/i2"} {
		if !removals[want] {
			t.Errorf("the rights of %v are removed: %v", want, removals)
		}
	}
	if len(deleted) != 2 {
		t.Errorf("the change stream reports both instances as deleted: %v", deleted)
	}
	var instances, deployments int
	_ = s.Tx(ctx, func(tx *store.Tx) error {
		ids, err := tx.NetworkInstanceIds("hub-1")
		instances = len(ids)
		if err != nil {
			return err
		}
		page, err := tx.ListDeployments("processes", model.DeploymentListOptions{NetworkId: "hub-1"}, store.Access{Admin: true})
		deployments = page.Total
		return err
	})
	if instances != 0 || deployments != 0 {
		t.Errorf("the resources of the network are gone: %v instances, %v deployments", instances, deployments)
	}
}
