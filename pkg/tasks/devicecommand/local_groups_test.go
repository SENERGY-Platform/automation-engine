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

package devicecommand

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
)

// answerLocal answers every command on the broker with data on response/{device}/{service}
func answerLocal(t *testing.T, broker *mqtt.Memory, data string) {
	t.Helper()
	sent, topics := commands(t, broker)
	for i, envelope := range sent {
		answer, _ := json.Marshal(LocalEnvelope{CommandId: envelope.CommandId, Data: data})
		if err := broker.Publish(ctx, strings.Replace(topics[i], "command/", "response/", 1), 2, answer); err != nil {
			t.Fatal(err)
		}
	}
}

// mgw-external-task-worker TestCommand: a controlling command to a group on a gateway
func TestLocalGroupCommandCompletesWhenEveryConnectorAnswered(t *testing.T) {
	e, broker := newLocalEnv(t)
	inst := e.start(e.deploy(process(Pessimistic, payload(controlling, map[string]any{"device_group_id": "g", "input": 7}), ""), nil))
	_, topics := commands(t, broker)
	slices.Sort(topics)
	if !reflect.DeepEqual(topics, []string{"command/local-d1/local-s-set", "command/local-d2/local-s-set"}) {
		t.Fatalf("one command per device of the group: %v", topics)
	}
	answerLocal(t, broker, "")
	e.expectState(inst.Id, model.StateCompleted)
	if out := e.variable(inst.Id, "out"); !reflect.DeepEqual(out, []any{nil, nil}) {
		t.Errorf("a controlling command has no value per device: %#v", out)
	}
}

// mgw-external-task-worker TestGroup: a measuring command to a group on a gateway
func TestLocalGroupMeasurementListsTheValueOfEveryDevice(t *testing.T) {
	e, broker := newLocalEnv(t)
	inst := e.start(e.deploy(process(Pessimistic, payload(measuring, map[string]any{"version": 3, "device_group_id": "g"}), ""), nil))
	answerLocal(t, broker, "21.5")
	e.expectState(inst.Id, model.StateCompleted)
	if out := e.variable(inst.Id, "out"); !reflect.DeepEqual(out, []any{21.5, 21.5}) {
		t.Errorf("one value per device: %#v", out)
	}
}
