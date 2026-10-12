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

package debug

import (
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// FirstSeq is the earliest step of an instance that can be reconstructed: the beginning while
// no entry was evicted, otherwise the oldest snapshot (DBG-14). 0 means none.
func FirstSeq(tx *store.Tx, inst store.Instance) (int64, error) {
	if inst.ActivityMinSeq <= 1 {
		return min(1, inst.ActivitySeq), nil
	}
	seq, found, err := tx.OldestSnapshotSeq(inst.Id)
	if err != nil || !found {
		return 0, err
	}
	return max(seq, 1), nil
}

// At reconstructs the state after the activity entry seq from the latest snapshot before it
// and the entries in between (DBG-13); seq 0 selects the latest entry.
func At(tx *store.Tx, inst store.Instance, seq int64) (model.DebugState, error) {
	if seq <= 0 || seq > inst.ActivitySeq {
		seq = inst.ActivitySeq
	}
	first, err := FirstSeq(tx, inst)
	if err != nil {
		return model.DebugState{}, err
	}
	if first == 0 || seq < first {
		return model.DebugState{}, model.NotFound("step %v of instance %v is no longer available, the earliest is %v", seq, inst.Id, first)
	}
	base, found, err := tx.SnapshotAtOrBefore(inst.Id, seq)
	if err != nil {
		return base, err
	}
	if !found || base.Seq < inst.ActivityMinSeq-1 {
		if inst.ActivityMinSeq > 1 {
			return model.DebugState{}, model.NotFound("step %v of instance %v is no longer available, the earliest is %v", seq, inst.Id, first)
		}
		base = Begin(inst.Instance)
	}
	state := base
	for state.Seq < seq {
		activities, err := tx.ListActivities(inst.Id, model.ActivityListOptions{AfterSeq: state.Seq, Limit: int(min(int64(model.MaxPageLimit), seq-state.Seq))})
		if err != nil {
			return state, err
		}
		if len(activities) == 0 {
			break
		}
		state = Replay(state, activities)
	}
	state.Reconstructed, state.FirstSeq, state.Truncated = true, first, inst.ActivityMinSeq > 1
	return state, nil
}
