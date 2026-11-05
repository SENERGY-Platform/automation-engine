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
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// After an error answer the retry finds the try of the device still in its window. With one
// place, the task keeps the timing it had before GROUP_CONCURRENCY (SNRGY-5093).

func respondError(t *testing.T, e *env, s sent) {
	t.Helper()
	s.msg.Response.Output = map[string]string{"error": "device offline"}
	raw, _ := json.Marshal(s.msg)
	if err := e.dc.HandleError(ctx, raw); err != nil {
		t.Fatal(err)
	}
}

func TestGroupErrorAnswerKeepsTheTimingOfOnePlace(t *testing.T) {
	e := newEnv(t, Sequential)
	fiveDevices(e, Sequential)
	e.start(e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_group_id": "g", "retries": 1}), ""), nil))
	e.advance(10 * time.Second)
	respondError(t, e, e.producer.messages()[0])
	e.advance(2 * time.Second)
	// d0's window ended at 120s; the retry after the error waits for the end of its own timeout
	e.advance(109 * time.Second)
	if got := len(e.producer.messages()); got != 1 {
		t.Fatalf("expected 1 command at 121s, got %v", got)
	}
	e.advance(11 * time.Second)
	if got := len(e.producer.messages()); got != 2 {
		t.Fatalf("expected 2 commands at 132s, got %v", got)
	}
}

func TestSingleDeviceErrorAnswerKeepsItsTiming(t *testing.T) {
	e := newEnv(t, Sequential)
	inst := e.start(e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_id": "d1", "service_id": "s-get", "retries": 2}), ""), nil))
	e.advance(10 * time.Second)
	respondError(t, e, e.producer.messages()[0])
	e.advance(2 * time.Second)
	// the window of the try ended at 120s, the timeout of the retry after the error ends at 132s
	e.advance(109 * time.Second)
	e.advance(3 * time.Second)
	if got := len(e.producer.messages()); got != 1 {
		t.Fatalf("expected 1 command at 124s, got %v", got)
	}
	e.expectState(inst.Id, model.StateRunning)
}
