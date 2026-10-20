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
	"strings"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// two ways in: the start event without definition and a message start; each marks its path
func twoStartsProcess(version string) string {
	return b.Process(
		b.Message("m", "go"), b.Message("k", "kick"),
		b.Start("start"), b.Script("viaNone", "path", "'none-"+version+"'"),
		b.StartMessage("byMessage", "k"), b.Script("viaMessage", "path", "'message-"+version+"'"),
		b.CatchMessage("wait", "m"), b.End("end"),
		b.Flow("start", "viaNone"), b.Flow("byMessage", "viaMessage"), b.Flow("viaNone", "wait"), b.Flow("viaMessage", "wait"), b.Flow("wait", "end"),
	)
}

func (this *harness) instanceIds(deploymentId string) []string {
	this.t.Helper()
	var result []string
	err := this.s.Tx(ctx, func(tx *storeTx) (err error) {
		result, err = tx.InstanceIdsOfDeployment(deploymentId, model.StateRunning)
		return err
	})
	if err != nil {
		this.t.Fatal(err)
	}
	return result
}

// A restart begins at the start event that began the current run, with the stored start
// variables (RUN-11, DEP-04), also for processes without a start event without definition.
func TestRestartBeginsAtTheStartEventOfTheRun(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(twoStartsProcess("v1"))
		byMessage := h.correlate(model.MessageInput{Name: "kick", Variables: map[string]any{"level": 3}}).Correlations[0].InstanceId
		direct := h.start(d, map[string]any{"level": 4}).Id
		h.expectVars(byMessage, map[string]any{"path": "message-v1", "level": 3.0})
		h.expectVars(direct, map[string]any{"path": "none-v1"})

		h.update(d, twoStartsProcess("v2"), model.UpdateRestart)
		restarted := h.expectState(byMessage, model.StateRunning)
		if restarted.Run != 2 || restarted.DeploymentVersion != 2 {
			t.Fatalf("unexpected %+v", restarted)
		}
		h.expectVars(byMessage, map[string]any{"path": "message-v2", "level": 3.0})
		h.expectVars(direct, map[string]any{"path": "none-v2", "level": 4.0})

		// an explicit restart follows the same rule
		if _, err := h.e.RestartInstance(ctx, byMessage, 0, model.CauseRestart); err != nil {
			t.Fatal(err)
		}
		h.expectVars(byMessage, map[string]any{"path": "message-v2", "level": 3.0})
		if _, err := h.e.RestartWith(ctx, byMessage, 0, map[string]any{"level": 5}, model.CauseRestart); err != nil {
			t.Fatal(err)
		}
		h.expectVars(byMessage, map[string]any{"path": "message-v2", "level": 5.0})
	})
}

func TestUpdateRestartsInstancesOfAProcessWithoutDirectStart(t *testing.T) {
	process := func(version string) string {
		return b.Process(
			b.Message("m", "go"), b.Message("k", "kick"),
			b.StartMessage("byMessage", "k"), b.Script("mark", "version", "'"+version+"'"), b.CatchMessage("wait", "m"), b.End("end"),
			b.Flow("byMessage", "mark"), b.Flow("mark", "wait"), b.Flow("wait", "end"),
		)
	}
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(process("v1"))
		inst := h.correlate(model.MessageInput{Name: "kick", Variables: map[string]any{"level": 3}}).Correlations[0].InstanceId
		h.update(d, process("v2"), model.UpdateRestart)
		restarted := h.expectState(inst, model.StateRunning)
		if restarted.Run != 2 || restarted.DeploymentVersion != 2 {
			t.Fatalf("unexpected %+v", restarted)
		}
		h.expectVars(inst, map[string]any{"version": "v2", "level": 3.0})
	})
}

// An instance that cannot be restarted on the new version is stopped and its owner told; the
// update and the restart of the other instances go ahead.
func TestUpdateStopsInstancesThatCannotBeRestarted(t *testing.T) {
	v1 := b.Process(
		b.Message("m", "go"), b.Message("k", "kick"),
		b.StartMessage("byMessage", "k"), b.StartTimer("hourly", "timeCycle", "0 0 * * * ?"), b.CatchMessage("wait", "m"), b.End("end"),
		b.Flow("byMessage", "wait"), b.Flow("hourly", "wait"), b.Flow("wait", "end"),
	)
	v2 := b.Process(
		b.Message("m", "go"),
		b.StartTimer("hourly", "timeCycle", "0 0 * * * ?"), b.Script("mark", "version", "'v2'"), b.CatchMessage("wait", "m"), b.End("end"),
		b.Flow("hourly", "mark"), b.Flow("mark", "wait"), b.Flow("wait", "end"),
	)
	run(t, func(t *testing.T, h *harness) {
		d := h.deploy(v1)
		byMessage := h.correlate(model.MessageInput{Name: "kick"}).Correlations[0].InstanceId
		h.advance(time.Hour)
		var byTimer string
		for _, id := range h.instanceIds(d) {
			if id != byMessage {
				byTimer = id
			}
		}
		if byTimer == "" {
			t.Fatal("the timer start did not start an instance")
		}

		h.update(d, v2, model.UpdateRestart)

		stopped := h.expectState(byMessage, model.StateTerminated)
		if !strings.Contains(stopped.EndReason, "cannot be restarted") {
			t.Errorf("the end reason names the cause: %q", stopped.EndReason)
		}
		restarted := h.expectState(byTimer, model.StateRunning)
		if restarted.Run != 2 || restarted.DeploymentVersion != 2 {
			t.Errorf("unexpected %+v", restarted)
		}
		h.expectVars(byTimer, map[string]any{"version": "v2"})
		h.process()
		notified := false
		for _, n := range h.notifier.messages() {
			if n.UserId == "owner" && strings.Contains(n.Message, byMessage) {
				notified = true
			}
		}
		if !notified {
			t.Errorf("the owner is told about the stopped instance: %+v", h.notifier.messages())
		}
	})
}

// A restart after an incident begins at the start event of the failed run (INC-02, INC-03).
func TestIncidentRestartOfAProcessWithoutDirectStart(t *testing.T) {
	run(t, func(t *testing.T, h *harness) {
		h.deployWith("processes", b.Process(
			b.Message("k", "kick"),
			b.StartMessage("byMessage", "k"), b.Script("fail", "x", "throw new Error('boom')"), b.End("end"),
			b.Flow("byMessage", "fail"), b.Flow("fail", "end"),
		), model.IncidentHandling{Restart: true, Notify: true})
		inst := h.correlate(model.MessageInput{Name: "kick", Variables: map[string]any{"level": 3}}).Correlations[0].InstanceId
		h.expectState(inst, model.StateIncident)
		h.advance(time.Hour)
		runs := h.runs(inst)
		if len(runs) != 2 || runs[1].Cause != model.CauseIncident {
			t.Fatalf("expected a second run after the incident, got %+v", runs)
		}
		for _, n := range h.notifier.messages() {
			if strings.Contains(n.Title, "unable to restart") {
				t.Errorf("unexpected %+v", n)
			}
		}
	})
}
