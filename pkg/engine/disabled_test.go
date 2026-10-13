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
	"errors"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// A deployment switched off starts nothing: no timer start, no message start, no schedule and
// no start by request; switched on again, it starts as before (DEP-12).
func TestDisabledDeploymentStartsNothing(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(b.Process(b.Message("m", "go"), b.StartTimer("hourly", "timeCycle", "0 0 * * * ?"), b.StartMessage("byMessage", "m"), b.End("end"),
			b.Flow("hourly", "end"), b.Flow("byMessage", "end")))
		count := func() int {
			var page model.Page[model.Instance]
			if err := h.s.Tx(ctx, func(tx *storeTx) (err error) {
				page, err = tx.ListInstances("processes", model.InstanceListOptions{DeploymentId: d}, adminAccess)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			return page.Total
		}
		setDisabled := func(disabled bool) {
			if err := h.s.Tx(ctx, func(tx *storeTx) error { return tx.SetDeploymentDisabled(d, disabled) }); err != nil {
				t.Fatal(err)
			}
		}
		due := h.now().Add(time.Minute)
		if err := h.s.Tx(ctx, func(tx *storeTx) error {
			return tx.SaveSchedule(model.Schedule{Id: "s1", Namespace: "processes", DeploymentId: d, Owner: "owner", Cron: "* * * * *", NextDue: &due, CreatedAt: h.now(), UpdatedAt: h.now()})
		}); err != nil {
			t.Fatal(err)
		}
		setDisabled(true)
		h.advance(30 * time.Minute) // 11:00 for the timer, the schedule due several times
		h.advance(0)
		h.correlate(model.MessageInput{Name: "go"})
		if _, _, err := h.e.StartInstance(ctx, StartRequest{DeploymentId: d, Owner: "owner"}); !errors.Is(err, model.ErrConflict) {
			t.Errorf("a start by request is refused: %v", err)
		}
		if n := count(); n != 0 {
			t.Fatalf("nothing starts while disabled, got %v instances", n)
		}
		var schedule model.Schedule
		_ = h.s.Tx(ctx, func(tx *storeTx) (err error) { schedule, err = tx.GetSchedule("s1"); return err })
		if schedule.NextDue == nil || !schedule.NextDue.After(due) {
			t.Errorf("a skipped schedule moves on to its next time: %v", schedule.NextDue)
		}

		setDisabled(false)
		h.correlate(model.MessageInput{Name: "go"})
		if n := count(); n != 1 {
			t.Fatalf("switched on, a message starts again: %v", n)
		}
	})
}
