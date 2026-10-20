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

package engine

import (
	"fmt"
	"html"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

func runWithMinimum(t *testing.T, minimum time.Duration, fn func(t *testing.T, h *harness)) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		fn(t, newHarness(t, s, func(c *Config) { c.MinTimerDuration = minimum }))
	})
}

func startTimerExpression(id string, kind string, expression string) string {
	return fmt.Sprintf(`<bpmn:startEvent id="%v"><bpmn:timerEventDefinition><bpmn:%v xsi:type="bpmn:tFormalExpression" language="javascript">%v</bpmn:%v></bpmn:timerEventDefinition></bpmn:startEvent>`, id, kind, html.EscapeString(expression), kind)
}

func timerStartProcess(start string) string {
	return b.Process(start, b.End("end"), b.Flow("start", "end"))
}

// startsWithin advances the clock in steps and returns the number of instances started
func (this *harness) startsWithin(deploymentId string, total time.Duration, step time.Duration) int {
	this.t.Helper()
	for elapsed := time.Duration(0); elapsed < total; elapsed += step {
		this.advance(step)
	}
	return this.instanceCount(deploymentId)
}

func TestTimerStartWithRepeatingIntervalFiresEveryInterval(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(timerStartProcess(b.StartTimer("start", "timeCycle", "R/PT10M")))
		h.advance(9 * time.Minute)
		if n := h.instanceCount(d); n != 0 {
			t.Fatalf("started too early: %v", n)
		}
		h.advance(time.Minute)
		if n := h.instanceCount(d); n != 1 {
			t.Fatalf("expected one start after 10 minutes, got %v", n)
		}
		h.advance(10 * time.Minute)
		if n := h.instanceCount(d); n != 2 {
			t.Fatalf("expected a second start after 20 minutes, got %v", n)
		}
	})
}

func TestTimerStartCyclesOfASecondWithoutMinimum(t *testing.T) {
	for _, cycle := range []string{"R/PT1S", "* * * * * ?"} {
		t.Run(cycle, func(t *testing.T) {
			runWithMinimum(t, 0, func(t *testing.T, h *harness) {
				d := h.deploy(timerStartProcess(b.StartTimer("start", "timeCycle", cycle)))
				if n := h.startsWithin(d, 20*time.Second, time.Second); n != 20 {
					t.Fatalf("expected 20 starts in 20 seconds, got %v", n)
				}
			})
		})
	}
}

func TestTimerStartCyclesAreLimitedToTheMinimum(t *testing.T) {
	for _, cycle := range []string{"R/PT1S", "* * * * * ?"} {
		t.Run(cycle, func(t *testing.T) {
			runWithMinimum(t, 5*time.Second, func(t *testing.T, h *harness) {
				d := h.deploy(timerStartProcess(b.StartTimer("start", "timeCycle", cycle)))
				h.advance(4 * time.Second)
				if n := h.instanceCount(d); n != 0 {
					t.Fatalf("the first start comes the minimum after the deploy, got %v starts", n)
				}
				if n := h.startsWithin(d, 16*time.Second, time.Second); n != 4 {
					t.Fatalf("expected 4 starts in 20 seconds, got %v", n)
				}
			})
		})
	}
}

// The minimum counts from the due time, not from the late moment the scheduler fired.
func TestTimerStartCronAtTheMinimumKeepsItsRhythmWhenFiredLate(t *testing.T) {
	runWithMinimum(t, 5*time.Second, func(t *testing.T, h *harness) {
		d := h.deploy(timerStartProcess(b.StartTimer("start", "timeCycle", "*/5 * * * * ?")))
		if n := h.startsWithin(d, 30*time.Second, 700*time.Millisecond); n != 6 {
			t.Fatalf("expected 6 starts in 30 seconds, got %v", n)
		}
	})
}

func TestTimerStartExpressionIsLimitedToTheMinimum(t *testing.T) {
	runWithMinimum(t, 5*time.Second, func(t *testing.T, h *harness) {
		d := h.deploy(timerStartProcess(startTimerExpression("start", "timeCycle", "'R/PT' + 1 + 'S'")))
		if n := h.startsWithin(d, 20*time.Second, time.Second); n != 4 {
			t.Fatalf("expected 4 starts in 20 seconds, got %v", n)
		}
	})
}

func TestScheduleWithSecondsIsLimitedToTheMinimum(t *testing.T) {
	runWithMinimum(t, 5*time.Second, func(t *testing.T, h *harness) {
		d := h.deploy(endingProcess())
		h.schedule("s1", d, "* * * * * *")
		if n := h.startsWithin(d, 20*time.Second, time.Second); n != 4 {
			t.Fatalf("expected 4 starts in 20 seconds, got %v", n)
		}
	})
}

func TestIntermediateTimersAreLimitedToTheMinimum(t *testing.T) {
	cases := map[string]string{
		"computed duration": b.CatchTimerExpression("wait", "timeDuration", "'PT1S'"),
		"computed cycle":    b.CatchTimerExpression("wait", "timeCycle", "'R/PT1S'"),
		"cron of seconds":   b.CatchTimer("wait", "timeCycle", "* * * * * ?"),
	}
	for name, wait := range cases {
		t.Run(name, func(t *testing.T) {
			runWithMinimum(t, 5*time.Second, func(t *testing.T, h *harness) {
				d := h.deploy(b.Process(b.Start("start"), wait, b.End("end"), b.Flow("start", "wait"), b.Flow("wait", "end")))
				inst := h.start(d, nil)
				h.advance(4 * time.Second)
				h.expectState(inst.Id, model.StateRunning)
				scheduled := h.activities(inst.Id, model.ActivityTimerScheduled)
				if len(scheduled) != 1 || scheduled[0].Data["minimum"] != "5s" {
					t.Fatalf("expected the limit in the activity, got %+v", scheduled)
				}
				h.advance(time.Second)
				h.expectState(inst.Id, model.StateCompleted)
			})
		})
	}
}

func TestIntermediateTimerOfASecondWithoutMinimum(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(b.Start("start"), b.CatchTimerExpression("wait", "timeDuration", "'PT1S'"), b.End("end"), b.Flow("start", "wait"), b.Flow("wait", "end")))
		inst := h.start(d, nil)
		h.advance(time.Second)
		h.expectState(inst.Id, model.StateCompleted)
	})
}
