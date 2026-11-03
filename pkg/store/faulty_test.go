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
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

// A gateway lists its faulty deployments for its status; the cloud lists those of one gateway
// with or without a mark and sets the mark alone, the restart counter untouched (SYNC-02).
func TestFaultyMarksByGateway(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		marked := t0.Add(time.Minute)
		err := s.Tx(context.Background(), func(tx *store.Tx) error {
			for _, d := range []model.Deployment{
				{Id: "local", Namespace: "processes", Name: "d", Owner: "alice", Version: 1, CreatedAt: t0, UpdatedAt: t0},
				{Id: "g1", Namespace: "processes", Name: "d", Owner: "alice", Version: 1, NetworkId: "hub-1", CreatedAt: t0, UpdatedAt: t0},
				{Id: "g2", Namespace: "processes", Name: "d", Owner: "alice", Version: 1, NetworkId: "hub-1", CreatedAt: t0, UpdatedAt: t0},
				{Id: "other", Namespace: "processes", Name: "d", Owner: "alice", Version: 1, NetworkId: "hub-2", CreatedAt: t0, UpdatedAt: t0},
			} {
				if err := tx.InsertDeployment(d); err != nil {
					return err
				}
			}
			if err := tx.SetRestartStats("local", store.RestartStats{WindowStart: t0, Count: 4, FaultySince: &marked}); err != nil {
				return err
			}
			faulty, err := tx.FaultySince("")
			if err != nil || len(faulty) != 1 || faulty["local"] == nil || !faulty["local"].Equal(marked) {
				return fmt.Errorf("only the marked deployment: %v %v", faulty, err)
			}

			if err = tx.SetFaultySince("g2", &marked); err != nil {
				return err
			}
			ofGateway, err := tx.FaultySince("hub-1")
			if err != nil || len(ofGateway) != 2 || ofGateway["g1"] != nil || ofGateway["g2"] == nil || !ofGateway["g2"].Equal(marked) {
				return fmt.Errorf("every deployment of the gateway, marked or not: %v %v", ofGateway, err)
			}
			if err = tx.SetFaultySince("local", nil); err != nil {
				return err
			}
			stats, err := tx.GetRestartStats("local")
			if err != nil || stats.FaultySince != nil || stats.Count != 4 {
				return fmt.Errorf("the mark goes, the count stays: %+v %v", stats, err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}
