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

package store_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

// The dependents of a user are resources of others deleted with the user's (SEC-13): instances
// on the user's deployments, deployments of others an instance deleted with the user manages
// (NS-08), and instances of others on those.
func TestUserDependentsIncludeManagedDeployments(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		err := s.Tx(context.Background(), func(tx *store.Tx) error {
			deployments := []model.Deployment{
				{Id: "release", Namespace: "smart-services", Owner: "alice"},
				{Id: "bobs-release", Namespace: "smart-services", Owner: "bob"},
				// managed by alice's instance, owned by bob as on his gateway
				{Id: "managed", Namespace: "processes", Owner: "bob", ManagedBy: "alices"},
				// managed by bob's instance on alice's release, which goes with alice
				{Id: "managed-of-shared", Namespace: "processes", Owner: "carol", ManagedBy: "bobs-on-release"},
				// alice's own goes as hers, not as a dependent
				{Id: "managed-own", Namespace: "processes", Owner: "alice", ManagedBy: "alices"},
				// managed by an instance that stays
				{Id: "managed-elsewhere", Namespace: "processes", Owner: "carol", ManagedBy: "bobs-own"},
			}
			for _, d := range deployments {
				d.Name, d.Version, d.CreatedAt, d.UpdatedAt = d.Id, 1, t0, t0
				if err := tx.InsertDeployment(d); err != nil {
					return err
				}
			}
			for _, i := range []struct{ id, deployment, owner, namespace string }{
				{"alices", "release", "alice", "smart-services"},
				{"bobs-on-release", "release", "bob", "smart-services"},
				{"bobs-own", "bobs-release", "bob", "smart-services"},
				{"on-managed", "managed", "dave", "processes"},
				{"alices-on-managed", "managed", "alice", "processes"},
			} {
				inst := store.Instance{Instance: model.Instance{Id: i.id, Namespace: i.namespace, Owner: i.owner, DeploymentId: i.deployment, DeploymentVersion: 1, Run: 1,
					State: model.StateRunning, CreatedAt: t0, StartedAt: t0, LastActivityAt: t0}, ActivityMinSeq: 1}
				if err := tx.InsertInstance(inst); err != nil {
					return err
				}
			}
			dependents, err := tx.UserDependents("alice")
			if err != nil {
				return err
			}
			var got []string
			for _, d := range dependents {
				got = append(got, fmt.Sprintf("%v/%v/%v", d.Kind, d.Id, d.Owner))
			}
			slices.Sort(got)
			expected := []string{"deployments/managed-of-shared/carol", "deployments/managed/bob", "instances/bobs-on-release/bob", "instances/on-managed/dave"}
			if !slices.Equal(got, expected) {
				return fmt.Errorf("dependents %v, want %v", got, expected)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}
