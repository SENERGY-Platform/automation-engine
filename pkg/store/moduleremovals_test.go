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
	"slices"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

func queueRemovals(tx *store.Tx, ids ...string) error {
	for _, id := range ids {
		if err := store.ExecForTest(tx, fmt.Sprintf("INSERT INTO module_removals (module_id, namespace, owner, module_type, delete_info, next_at, created_at) "+
			"VALUES ('%v', 'smart-services', 'alice', 'analytics', '{\"url\":\"http://pipeline/%v\"}', %v, %v)", id, id, t0.UnixMilli(), t0.UnixMilli())); err != nil {
			return err
		}
	}
	return nil
}

func claimedIds(removals []store.ModuleRemoval) []string {
	var result []string
	for _, r := range removals {
		result = append(result, r.ModuleId)
	}
	slices.Sort(result)
	return result
}

// SNRGY-5140: a removal is claimed by one replica until the end of its claim, so that the
// resource is deleted once; a claim that ran out makes it due again
func TestAModuleRemovalIsClaimedOnceUntilItsClaimRunsOut(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		now, until := t0.Add(time.Minute), t0.Add(10*time.Minute)
		tx(t, s, func(tx *store.Tx) error {
			if err := queueRemovals(tx, "m1", "m2", "m3"); err != nil {
				return err
			}
			first, err := tx.ClaimModuleRemovals(now, until, 2)
			if err != nil || len(first) != 2 {
				return fmt.Errorf("the first claim takes two: %+v %v", first, err)
			}
			second, err := tx.ClaimModuleRemovals(now, until, 2)
			if err != nil || len(second) != 1 || slices.Contains(claimedIds(first), second[0].ModuleId) {
				return fmt.Errorf("the second claim takes the one left: %+v %+v %v", first, second, err)
			}
			if second[0].Namespace != "smart-services" || second[0].ModuleType != "analytics" || second[0].DeleteInfo.Url == "" {
				return fmt.Errorf("a claimed removal carries what the remover needs: %+v", second[0])
			}
			if again, err := tx.ClaimModuleRemovals(until.Add(-time.Second), until, 10); err != nil || len(again) != 0 {
				return fmt.Errorf("nothing is due while claimed: %+v %v", again, err)
			}
			if err = tx.FinishModuleRemoval(second[0].ModuleId); err != nil {
				return err
			}
			again, err := tx.ClaimModuleRemovals(until, until.Add(10*time.Minute), 10)
			if err != nil || !slices.Equal(claimedIds(again), claimedIds(first)) {
				return fmt.Errorf("the unfinished ones are due again once the claim ran out: %+v %v", again, err)
			}
			return nil
		})
	})
}
