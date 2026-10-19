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
	"strconv"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// external-task-worker TestRetries: an unanswered command is sent once more per retry of the
// payload, then the task becomes an incident (TASK-07)
func TestUnansweredCommandIsResentPerRetryThenBecomesAnIncident(t *testing.T) {
	for retries, sends := range map[int]int{0: 1, 2: 3} {
		t.Run(strconv.Itoa(retries), func(t *testing.T) {
			e := newEnv(t, Parallel)
			inst := e.start(e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_id": "d1", "service_id": "s-get", "retries": retries}), ""), nil))
			// timeouts and the growing backoff between the attempts
			for range 10 {
				e.advance(2 * time.Minute)
			}
			if got := len(e.producer.messages()); got != sends {
				t.Errorf("expected %v commands, got %v", sends, got)
			}
			e.expectState(inst.Id, model.StateIncident)
		})
	}
}
