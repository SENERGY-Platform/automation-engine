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
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// schedule stores a schedule of a deployment as the controller does, with its first due time
func (this *harness) schedule(id string, deploymentId string, cron string) {
	this.t.Helper()
	s := model.Schedule{Id: id, Namespace: "processes", DeploymentId: deploymentId, Owner: "owner", Cron: cron, CreatedAt: this.now(), UpdatedAt: this.now()}
	due, err := this.e.NextScheduleDue(s, this.now())
	if err != nil {
		this.t.Fatal(err)
	}
	s.NextDue = due
	if err = this.s.Tx(ctx, func(tx *storeTx) error { return tx.SaveSchedule(s) }); err != nil {
		this.t.Fatal(err)
	}
}

func (this *harness) instanceCount(deploymentId string) int {
	this.t.Helper()
	var page model.Page[model.Instance]
	if err := this.s.Tx(ctx, func(tx *storeTx) (err error) {
		page, err = tx.ListInstances("processes", model.InstanceListOptions{DeploymentId: deploymentId}, adminAccess)
		return err
	}); err != nil {
		this.t.Fatal(err)
	}
	return page.Total
}

func endingProcess() string {
	return b.Process(b.Start("start"), b.End("end"), b.Flow("start", "end"))
}

func TestScheduleWithFiveFieldsStartsOncePerMinute(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(endingProcess())
		h.schedule("s1", d, "* * * * *")
		h.advance(61 * time.Second)
		if n := h.instanceCount(d); n != 1 {
			t.Fatalf("expected one start within 61 seconds, got %v", n)
		}
	})
}

func TestDeletedScheduleStartsNothingMore(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(endingProcess())
		h.schedule("s1", d, "* * * * *")
		h.advance(61 * time.Second)
		if err := h.s.Tx(ctx, func(tx *storeTx) error { return tx.DeleteSchedule("s1") }); err != nil {
			t.Fatal(err)
		}
		h.advance(61 * time.Second)
		if n := h.instanceCount(d); n != 1 {
			t.Fatalf("expected no start after the deletion, got %v instances", n)
		}
	})
}

func TestScheduleWithSecondsFieldStartsEverySecond(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(endingProcess())
		h.schedule("s1", d, "* * * * * *")
		for range 5 {
			h.advance(time.Second)
		}
		if n := h.instanceCount(d); n != 5 {
			t.Fatalf("expected 5 starts in 5 seconds, got %v", n)
		}
	})
}

func TestScheduleWithSecondStepStartsEveryOtherSecond(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(endingProcess())
		h.schedule("s1", d, "*/2 * * * * *")
		for range 6 {
			h.advance(time.Second)
		}
		if n := h.instanceCount(d); n != 3 {
			t.Fatalf("expected 3 starts in 6 seconds, got %v", n)
		}
	})
}

// A schedule is persistent: an engine started later on the same store fires it.
func TestScheduleFiresAfterARestartOfTheEngine(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(endingProcess())
		h.schedule("s1", d, "* * * * *")
		restarted := h.replica()
		h.mux.Lock()
		h.clock = h.clock.Add(61 * time.Second)
		h.mux.Unlock()
		if err := restarted.ProcessDue(ctx); err != nil {
			t.Fatal(err)
		}
		if n := h.instanceCount(d); n != 1 {
			t.Fatalf("expected the restarted engine to start once, got %v", n)
		}
	})
}
