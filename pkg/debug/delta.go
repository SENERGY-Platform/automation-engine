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
	"cmp"
	"encoding/json"
	"slices"
	"sort"
	"strings"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// Delta derives a message of the live stream from the state sent last and the current one; it
// carries the new activity entries and what changed (DBG-03). Fast loops are thereby merged
// into one delta with the latest values (DBG-10).
func Delta(previous model.DebugState, current model.DebugState, activities []model.Activity) model.DebugMessage {
	result := model.DebugMessage{Type: model.DebugDelta, Seq: current.Seq, Activities: activities}
	if !same(previous.Instance, current.Instance) {
		instance := current.Instance
		result.Instance = &instance
	}
	if !same(previous.Active, current.Active) {
		result.Active, result.ActiveChanged = current.Active, true
	}
	if !same(previous.Waits, current.Waits) {
		result.Waits, result.WaitsChanged = current.Waits, true
	}
	if !same(previous.Incidents, current.Incidents) {
		result.Incidents, result.IncidentsChanged = current.Incidents, true
	}
	for name, v := range current.Variables {
		if old, ok := previous.Variables[name]; !ok || !same(old, v) {
			if result.Variables == nil {
				result.Variables = map[string]model.DebugVariable{}
			}
			result.Variables[name] = v
		}
	}
	for name := range previous.Variables {
		if _, ok := current.Variables[name]; !ok {
			result.RemovedVariables = append(result.RemovedVariables, name)
		}
	}
	sort.Strings(result.RemovedVariables)
	old := map[string]model.ElementStats{}
	for _, e := range previous.Elements {
		old[e.ElementId] = e
	}
	for _, e := range current.Elements {
		if o, ok := old[e.ElementId]; !ok || !same(o, e) {
			result.Elements = append(result.Elements, e)
		}
	}
	return result
}

// Empty reports whether a delta carries nothing.
func Empty(delta model.DebugMessage) bool {
	return len(delta.Activities) == 0 && delta.Instance == nil && !delta.ActiveChanged && !delta.WaitsChanged && !delta.IncidentsChanged &&
		len(delta.Variables) == 0 && len(delta.RemovedVariables) == 0 && len(delta.Elements) == 0
}

// Apply merges a delta into a state, as clients do (CLI-05).
func Apply(state model.DebugState, delta model.DebugMessage) model.DebugState {
	state = clone(state)
	state.Seq = delta.Seq
	if delta.Instance != nil {
		state.Instance = *delta.Instance
	}
	if delta.ActiveChanged {
		state.Active = nonNil(delta.Active)
	}
	if delta.WaitsChanged {
		state.Waits = nonNil(delta.Waits)
	}
	if delta.IncidentsChanged {
		state.Incidents = nonNil(delta.Incidents)
	}
	for name, v := range delta.Variables {
		state.Variables[name] = v
	}
	for _, name := range delta.RemovedVariables {
		delete(state.Variables, name)
	}
	for _, e := range delta.Elements {
		replaced := false
		for i := range state.Elements {
			if state.Elements[i].ElementId == e.ElementId {
				state.Elements[i], replaced = e, true
			}
		}
		if !replaced {
			state.Elements = append(state.Elements, e)
		}
	}
	// the order of the store, so that an applied state equals a read one
	slices.SortStableFunc(state.Elements, func(a, b model.ElementStats) int {
		if a.Run != b.Run {
			return cmp.Compare(a.Run, b.Run)
		}
		return strings.Compare(a.ElementId, b.ElementId)
	})
	if n := len(delta.Activities); n > 0 {
		state.Time = delta.Activities[n-1].Time
	}
	return state
}

func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

func same(a, b any) bool {
	rawA, errA := json.Marshal(a)
	rawB, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(rawA) == string(rawB)
}
