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
	"reflect"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

// seedLists stores deployments and models in "smart-services" with versions:
//   - own: alice's, versions 1 to 3, version 2 removed;
//   - shared: bob's, versions 1 and 2;
//   - foreign: bob's, version 1, not shared with alice;
//   - gateway: bob's on network n1, version 1, granted to alice on the gateway;
//   - other: alice's in the namespace "processes", version 1.
func seedLists(tx *store.Tx) error {
	for _, d := range []struct {
		id, namespace, owner, network string
		versions                      []int
	}{
		{"own", "smart-services", "alice", "", []int{1, 3}},
		{"shared", "smart-services", "bob", "", []int{1, 2}},
		{"foreign", "smart-services", "bob", "", []int{1}},
		{"gateway", "smart-services", "bob", "n1", []int{1}},
		{"other", "processes", "alice", "", []int{1}},
	} {
		current := d.versions[len(d.versions)-1]
		if err := tx.InsertDeployment(model.Deployment{Id: d.id, Namespace: d.namespace, Name: d.id, Owner: d.owner, NetworkId: d.network, Version: current, CreatedAt: t0, UpdatedAt: t0}); err != nil {
			return err
		}
		if err := tx.InsertModel(model.Model{Id: d.id, Namespace: d.namespace, Name: d.id, Owner: d.owner, Version: current, CreatedAt: t0, UpdatedAt: t0}); err != nil {
			return err
		}
		for _, version := range d.versions {
			xml := fmt.Sprintf("<%v v=%v/>", d.id, version)
			if err := tx.InsertDeploymentVersion(model.DeploymentVersion{DeploymentId: d.id, Version: version, Xml: xml, CreatedAt: t0}); err != nil {
				return err
			}
			if err := tx.InsertModelVersion(model.ModelVersion{ModelId: d.id, Version: version, Xml: xml, CreatedAt: t0}); err != nil {
				return err
			}
		}
	}
	return nil
}

var listIds = []string{"own", "shared", "foreign", "gateway", "other", "missing"}

// The versions of a list of deployments or models hold only what the access allows in the
// namespace, also when the ids take several queries.
func TestVersionsOfAListFollowTheAccess(t *testing.T) {
	alice := store.Access{Owner: "alice", Ids: []string{"shared"}, Gateway: []string{"gateway"}}
	for _, perQuery := range []int{500, 1} {
		t.Run(fmt.Sprintf("%v per query", perQuery), func(t *testing.T) {
			storetest.Run(t, func(t *testing.T, s *store.Store) {
				defer store.SetIdsPerQueryForTest(perQuery)()
				err := s.Tx(context.Background(), func(tx *store.Tx) error {
					if err := seedLists(tx); err != nil {
						return err
					}
					for _, c := range []struct {
						name     string
						access   store.Access
						current  bool
						expected map[string][]int
					}{
						{"alice, all", alice, false, map[string][]int{"own": {3, 1}, "shared": {2, 1}, "gateway": {1}}},
						{"alice, current", alice, true, map[string][]int{"own": {3}, "shared": {2}, "gateway": {1}}},
						{"bob without shares", store.Access{Owner: "bob"}, false, map[string][]int{"shared": {2, 1}, "foreign": {1}, "gateway": {1}}},
						{"admin", store.Access{Admin: true}, true, map[string][]int{"own": {3}, "shared": {2}, "foreign": {1}, "gateway": {1}}},
					} {
						numbers, err := tx.DeploymentVersionNumbers("smart-services", listIds, c.current, c.access)
						if err != nil {
							return err
						}
						if !reflect.DeepEqual(numbers, c.expected) {
							t.Errorf("deployment versions, %v: %v, want %v", c.name, numbers, c.expected)
						}
					}

					refs := []store.VersionRef{{"own", 3}, {"own", 2}, {"shared", 1}, {"gateway", 1}}
					versions, err := tx.GetDeploymentVersions(refs)
					if err != nil {
						return err
					}
					if len(versions) != 3 || versions[refs[0]].Xml != "<own v=3/>" || versions[refs[2]].Xml != "<shared v=1/>" || versions[refs[3]].Xml != "<gateway v=1/>" {
						t.Errorf("versions by ref, without the removed one: %+v", versions)
					}

					// the gateway grant applies to deployments and instances only, as in the lists
					current, err := tx.CurrentModelVersions("smart-services", listIds, store.Access{Owner: "alice", Ids: []string{"shared"}})
					if err != nil {
						return err
					}
					xml := map[string]string{}
					for id, v := range current {
						xml[id] = v.Xml
					}
					if expected := map[string]string{"own": "<own v=3/>", "shared": "<shared v=2/>"}; !reflect.DeepEqual(xml, expected) {
						t.Errorf("current model versions: %v, want %v", xml, expected)
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}
