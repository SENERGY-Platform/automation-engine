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

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
)

func timerProcess(duration string) string {
	return b.Process(b.Start("start"), b.CatchTimer("wait", "timeDuration", duration), b.End("end"),
		b.Flow("start", "wait"), b.Flow("wait", "end"))
}

// Durations the deploy dialog accepted or rejected, in the cases of process-deployment.
func TestTimerDurationsWithMonthsAndMinutesAreValid(t *testing.T) {
	for _, duration := range []string{"PT1M", "P1MT1M", "P1MT1M2S", "PT8S"} {
		t.Run(duration, func(t *testing.T) {
			if issues := issuesOf(t, timerProcess(duration), Rules{}); len(issues) > 0 {
				t.Errorf("unexpected issues %v", issues)
			}
		})
	}
}

func TestMalformedTimerDurationsAreRejectedNamingTheTimer(t *testing.T) {
	for _, duration := range []string{"PTS", "PT", "P", "", "T", "2S", "2", "8"} {
		t.Run(duration, func(t *testing.T) {
			if issues := issuesOf(t, timerProcess(duration), Rules{}); !hasIssue(issues, "wait", "timer") {
				t.Errorf("expected a timer issue for %q, got %v", duration, issues)
			}
		})
	}
}
