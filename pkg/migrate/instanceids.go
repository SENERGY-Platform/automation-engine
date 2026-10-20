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

package migrate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// candidate is an instance of the old services that gets an instance id in the engine: its
// business key where that is unambiguous, otherwise an id derived from its old id (MIG-02)
type candidate struct {
	// the old id, e.g. of the Camunda instance
	oldId string
	// names it in the report
	origin string
	// business key without wardened:
	key string
	// derived from the old id, the same in every run
	generated    string
	owner        string
	deploymentId string
	// empty is the cloud
	networkId string
	values    map[string]any
	// zero if unknown
	started time.Time
}

// assigned is the id of a candidate, or why it gets none
type assigned struct {
	id    string
	notes []string
	// the candidate is not started and needs a decision
	manual string
	err    error
}

// keyRef is a business key in a network, empty for the cloud
type keyRef struct{ networkId, key string }

func usableKey(key string) bool {
	return key != "" && len(key) <= 64 && !strings.ContainsAny(key, "/?# ")
}

// assign gives the candidates of a step their instance ids, unique in the engine (RUN-01). A
// business key becomes the id if one candidate alone has it and no other instance of this run
// took it as id. If the engine has an instance with that id from an earlier run, the key stays
// with the candidate that instance was started for: same namespace, owner, deployment and
// network, started before the instance and, among several, with the same start values; so ids
// do not change between runs. Where that leaves several, none of them is started.
func (this *migration) assign(ctx context.Context, candidates []candidate) []assigned {
	result := make([]assigned, len(candidates))
	byKey := map[string][]int{}
	var keys []string
	for i, c := range candidates {
		result[i].id = c.generated
		if !usableKey(c.key) {
			continue
		}
		if _, ok := byKey[c.key]; !ok {
			keys = append(keys, c.key)
		}
		byKey[c.key] = append(byKey[c.key], i)
	}
	note := func(i int, format string, args ...any) {
		result[i].notes = append(result[i].notes, fmt.Sprintf(format, args...))
	}
	for _, key := range keys {
		indices := byKey[key]
		if holder, taken := this.claimed[key]; taken {
			for _, i := range indices {
				note(i, "business key %q is the id of %v, the id is generated", key, holder)
			}
			continue
		}
		existing, found, err := this.target.instance(ctx, key)
		if err != nil {
			for _, i := range indices {
				result[i].err = err
			}
			continue
		}
		if !found {
			if len(indices) == 1 {
				result[indices[0]].id = key
				continue
			}
			for _, i := range indices {
				note(i, "business key %q is not unique (%v instances have it), the id is generated", key, len(indices))
			}
			continue
		}
		var matching []int
		for _, i := range indices {
			if candidates[i].startedAs(existing) {
				matching = append(matching, i)
			}
		}
		if len(matching) > 1 {
			var same []int
			for _, i := range matching {
				if sameValues(candidates[i].values, existing.StartVariables) {
					same = append(same, i)
				}
			}
			if len(same) > 0 {
				matching = same
			}
		}
		var names []string
		for _, i := range matching {
			names = append(names, candidates[i].oldId)
		}
		for _, i := range indices {
			switch {
			case len(matching) == 0:
				note(i, "business key %q is the id of another instance, the id is generated", key)
			case len(matching) == 1 && i == matching[0]:
				result[i].id = key
			case slices.Contains(matching, i):
				result[i].manual = fmt.Sprintf("business key %q is the id of an instance of an earlier run for one of %v; which one is unknown, so none of them is restarted",
					key, strings.Join(names, ", "))
			default:
				note(i, "business key %q is the id of the instance of an earlier run for %v, the id is generated", key, strings.Join(names, ", "))
			}
		}
	}
	for i, c := range candidates {
		id := result[i].id
		if result[i].manual != "" {
			id = ""
		} else {
			this.claimed[id] = c.origin
		}
		if c.key != "" {
			ref := keyRef{c.networkId, c.key}
			this.keyIds[ref] = append(this.keyIds[ref], id)
		}
	}
	return result
}

// startedAs reports whether an instance of the engine may have been started for the candidate
func (this candidate) startedAs(i model.Instance) bool {
	return i.Namespace == processes && i.Owner == this.owner && i.DeploymentId == this.deploymentId && i.NetworkId == this.networkId &&
		(this.started.IsZero() || !this.started.After(i.CreatedAt))
}

func sameValues(a map[string]any, b map[string]any) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	x, err := json.Marshal(a)
	if err != nil {
		return false
	}
	y, err := json.Marshal(b)
	return err == nil && bytes.Equal(x, y)
}

// instanceOfKey resolves a reference by business key, e.g. in the delete info of a module, to
// the instance that had the key in this run; a key several had, or one that is the id of
// another instance, is not resolved (MIG-01)
func (this *migration) instanceOfKey(networkId string, key string) (string, string) {
	ids := this.keyIds[keyRef{networkId, key}]
	switch {
	case len(ids) == 1 && ids[0] != "":
		return ids[0], ""
	case len(ids) > 0:
		return "", fmt.Sprintf("business key %q belongs to %v instances, which one is meant is unknown", key, len(ids))
	}
	if holder, taken := this.claimed[key]; taken {
		return "", fmt.Sprintf("business key %q belongs to no migrated instance, but is the id of %v", key, holder)
	}
	// an instance that ended in the meantime
	return key, ""
}
