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

package bpmn

import (
	"testing"
	"time"
)

// process-deployment rejected timer durations below 5 s at deploy (models/go
// Element.Validate, "time-event duration below 5s"); the engine takes the minimum from its rules.
func TestTimerDurationBelowTheMinimumIsRejected(t *testing.T) {
	if issues := issuesOf(t, timerProcess("PT2S"), Rules{MinTimerDuration: 5 * time.Second}); !hasIssue(issues, "wait", "below the minimum") {
		t.Errorf("expected a timer issue for PT2S, got %v", issues)
	}
}

func TestTimerDurationsFromTheMinimumOnAreValid(t *testing.T) {
	for _, value := range []string{"PT5S", "PT1M", "P1D", "P1M"} {
		if issues := issuesOf(t, timerProcess(value), Rules{MinTimerDuration: 5 * time.Second}); len(issues) > 0 {
			t.Errorf("%v: %v", value, issues)
		}
	}
}

func TestWithoutMinimumEveryTimerDurationIsValid(t *testing.T) {
	if issues := issuesOf(t, timerProcess("PT1S"), Rules{}); len(issues) > 0 {
		t.Errorf("%v", issues)
	}
}
