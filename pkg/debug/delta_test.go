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
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

func state(seq int64, active []string, variables map[string]any, counts map[string]int64) model.DebugState {
	at := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	s := model.DebugState{Instance: model.Instance{Id: "i", State: model.StateRunning, Run: 1}, Seq: seq, Active: []model.ActiveElement{}, Waits: []model.Wait{},
		Variables: map[string]model.DebugVariable{}, Incidents: []model.Incident{}, Elements: []model.ElementStats{}}
	for _, id := range active {
		s.Active = append(s.Active, model.ActiveElement{ElementId: id, Tokens: 1, Since: at})
		s.Waits = append(s.Waits, model.Wait{ElementId: id, Kind: model.WaitMessage, Message: &model.MessageWait{Name: id}})
	}
	for name, value := range variables {
		s.Variables[name] = Variable(value, at, "", 0)
	}
	for id, count := range counts {
		s.Elements = append(s.Elements, model.ElementStats{ElementId: id, Count: count, LastAt: at})
	}
	return s
}

func jsonOf(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestApplyingTheDeltaYieldsTheCurrentState(t *testing.T) {
	cases := map[string][2]model.DebugState{
		"variable changes and element moves": {
			state(3, []string{"a"}, map[string]any{"x": 1.0, "gone": true}, map[string]int64{"a": 1}),
			state(7, []string{"b"}, map[string]any{"x": 2.0, "new": "v"}, map[string]int64{"a": 1, "b": 1, "flow": 1}),
		},
		"no element left": {
			state(3, []string{"a", "b"}, nil, map[string]int64{"a": 2}),
			state(5, nil, nil, map[string]int64{"a": 3}),
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			previous, current := c[0], c[1]
			delta := Delta(previous, current, []model.Activity{{Seq: current.Seq, Time: current.Time}})
			if Empty(delta) {
				t.Fatal("the states differ, the delta must not be empty")
			}
			merged := Apply(previous, delta)
			merged.Time = current.Time
			if got, want := jsonOf(t, merged), jsonOf(t, current); got != want {
				t.Fatalf("merged state differs\ngot:  %v\nwant: %v", got, want)
			}
		})
	}
}

func TestEqualStatesGiveAnEmptyDelta(t *testing.T) {
	s := state(4, []string{"a"}, map[string]any{"x": 1.0}, map[string]int64{"a": 1})
	if delta := Delta(s, s, nil); !Empty(delta) {
		t.Fatalf("nothing changed: %+v", delta)
	}
}

func TestCutMarksLargeValuesAndKeepsTheirSize(t *testing.T) {
	large := strings.Repeat("x", 100)
	s := state(1, []string{"a"}, map[string]any{"large": large, "small": "y"}, nil)
	s.Active[0].Locals = map[string]model.DebugVariable{"local": Variable(large, time.Time{}, "a", 0)}
	s.Instance.StartVariables = map[string]any{"input": large}
	cut := Cut(s, 10)
	if v := cut.Variables["large"]; !v.Truncated || v.Size != 102 || len(v.Value.(string)) != 10 {
		t.Fatalf("large value: %+v", v)
	}
	if v := cut.Variables["small"]; v.Truncated || v.Value != "y" {
		t.Fatalf("small value: %+v", v)
	}
	if v := cut.Active[0].Locals["local"]; !v.Truncated {
		t.Fatalf("local variables are cut as well: %+v", v)
	}
	if start, ok := cut.Instance.StartVariables["input"].(map[string]any); !ok || start["truncated"] != true {
		t.Fatalf("start variables are cut in the form of the activity log: %v", cut.Instance.StartVariables)
	}
	if s.Variables["large"].Truncated || s.Variables["large"].Value != large || s.Instance.StartVariables["input"] != large {
		t.Fatal("cutting leaves the original state alone")
	}
}
