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
	"fmt"
	"maps"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

func startValueLabels(tx *store.Tx, id string) (map[string]string, error) {
	i, err := tx.GetInstance(id, false)
	return i.StartValueLabels, err
}

// The labels of the start variables are stored with the instance and replaced together with the
// variables (SNRGY-5107).
func TestStartValueLabelsAreReplacedWithTheStartVariables(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		tx(t, s, func(tx *store.Tx) error {
			if err := tx.InsertDeployment(model.Deployment{Id: "d1", Namespace: "smart-services", Name: "d", Owner: "alice", Version: 1, CreatedAt: t0, UpdatedAt: t0}); err != nil {
				return err
			}
			return tx.InsertInstance(store.Instance{Instance: model.Instance{Id: "i1", Namespace: "smart-services", Owner: "alice", DeploymentId: "d1", DeploymentVersion: 1, Run: 1,
				State: model.StateRunning, StartVariables: map[string]any{"device": "d-1"}, StartValueLabels: map[string]string{"device": "Lamp"}, CreatedAt: t0, StartedAt: t0, LastActivityAt: t0}, ActivityMinSeq: 1})
		})
		tx(t, s, func(tx *store.Tx) error {
			labels, err := startValueLabels(tx, "i1")
			if err != nil || !maps.Equal(labels, map[string]string{"device": "Lamp"}) {
				return fmt.Errorf("the labels of the start: %v %v", labels, err)
			}
			if err = tx.SetStartVariables("i1", map[string]any{"device": "d-2"}, map[string]string{}); err != nil {
				return err
			}
			labels, err = startValueLabels(tx, "i1")
			if err != nil || len(labels) != 0 {
				return fmt.Errorf("new variables without labels leave none: %v %v", labels, err)
			}
			return nil
		})
	})
}

// While engines with and without the column run side by side, an engine without it inserts
// instances without labels, which read as none (SNRGY-5107).
func TestAnInstanceInsertedWithoutTheLabelColumnHasNoLabels(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		tx(t, s, func(tx *store.Tx) error {
			if err := tx.InsertDeployment(model.Deployment{Id: "d1", Namespace: "smart-services", Name: "d", Owner: "alice", Version: 1, CreatedAt: t0, UpdatedAt: t0}); err != nil {
				return err
			}
			// the column list of migration 21
			return store.ExecForTest(tx, "INSERT INTO instances (id, namespace, owner, deployment_id, deployment_name, deployment_version, model_id, model_version, run, state, end_reason, "+
				"start_variables, created_at, started_at, ended_at, last_activity_at, activity_seq, activity_min_seq, restart_at, snapshot_seq, snapshot_at, parent_id, network_id, name, description) "+
				`VALUES ('i1', 'smart-services', 'alice', 'd1', 'd', 1, '', 0, 1, 'running', '', '{"device":"d-1"}', 0, 0, NULL, 0, 0, 1, NULL, 0, 0, '', '', 'old', '')`)
		})
		tx(t, s, func(tx *store.Tx) error {
			i, err := tx.GetInstance("i1", false)
			if err != nil || i.StartValueLabels == nil || len(i.StartValueLabels) != 0 || i.StartVariables["device"] != "d-1" {
				return fmt.Errorf("an instance of an older engine: %+v %v", i.Instance, err)
			}
			return nil
		})
	})
}

// The state a gateway reports leaves the labels of the cloud alone, as name and description.
func TestAReplicatedInstanceKeepsTheLabelsOfTheCloud(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		inst := model.Instance{Id: "i1", Namespace: "processes", Owner: "alice", DeploymentId: "d1", DeploymentVersion: 1, State: model.StatePending,
			StartVariables: map[string]any{"device": "d-1"}, StartValueLabels: map[string]string{"device": "Lamp"}, CreatedAt: t0, StartedAt: t0, LastActivityAt: t0, NetworkId: "n1"}
		tx(t, s, func(tx *store.Tx) error {
			if err := tx.InsertDeployment(model.Deployment{Id: "d1", Namespace: "processes", Name: "d", Owner: "alice", Version: 1, CreatedAt: t0, UpdatedAt: t0, NetworkId: "n1"}); err != nil {
				return err
			}
			return tx.InsertInstance(store.Instance{Instance: inst, ActivityMinSeq: 1})
		})
		reported := inst
		reported.State, reported.Run, reported.StartValueLabels = model.StateRunning, 1, nil
		tx(t, s, func(tx *store.Tx) error {
			if err := tx.UpsertReplicatedInstance("n1", reported); err != nil {
				return err
			}
			i, err := tx.GetInstance("i1", false)
			if err != nil || i.State != model.StateRunning || !maps.Equal(i.StartValueLabels, map[string]string{"device": "Lamp"}) {
				return fmt.Errorf("the reported state with the labels of the cloud: %+v %v", i.Instance, err)
			}
			return nil
		})
	})
}
