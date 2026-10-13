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

package integration_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/models/go/models"
)

// waitingProcess doubles the start value level and then waits for the message go
func waitingProcess() string {
	return b.Process(
		b.Message("m", "go"),
		b.StartWithForm("start", "level=5"),
		b.Script("double", "doubled", "level * 2"),
		b.CatchMessage("wait", "m"),
		b.End("end"),
		b.Flow("start", "double"), b.Flow("double", "wait"), b.Flow("wait", "end"),
	)
}

// A user models a process, revises the model, deploys it, starts it with a value, and an event
// lets it finish; the history shows what happened. Others see none of it.
func TestProcessFromModelToCompletion(t *testing.T) {
	eachVariant(t, func(t *testing.T, e *env) {
		if issues := must[[]model.Issue](t)(e.c.Validate(ctx, alice, processes, waitingProcess())); len(issues) != 0 {
			t.Fatalf("the model is valid: %+v", issues)
		}
		m := must[model.Model](t)(e.c.CreateModel(ctx, alice, processes, model.ModelInput{Name: "Heating", Xml: waitingProcess()}))
		m = must[model.Model](t)(e.c.UpdateModel(ctx, alice, processes, m.Id, model.ModelInput{Name: "Heating", Description: "doubles the level", Xml: waitingProcess()}))
		if m.Version != 2 {
			t.Fatalf("a change is a new version of the model: %+v", m)
		}
		d := must[model.Deployment](t)(e.c.CreateDeployment(ctx, alice, processes, model.DeploymentInput{ModelId: m.Id}))
		if d.ModelId != m.Id || d.ModelVersion != 2 || d.Name != "Heating" {
			t.Fatalf("the deployment refers to the latest version of the model: %+v", d)
		}
		parameters := must[[]model.StartParameter](t)(e.c.GetStartParameters(ctx, alice, processes, d.Id))
		if len(parameters) != 1 || parameters[0].Id != "level" {
			t.Fatalf("the start form: %+v", parameters)
		}

		inst := must[model.Instance](t)(e.c.StartInstance(ctx, alice, processes, model.StartInstanceInput{DeploymentId: d.Id, Variables: map[string]any{"level": 7}}))
		e.waitFor("the instance waiting for the message", func() bool {
			vars, _ := e.c.GetVariables(ctx, alice, processes, inst.Id)
			return variable(vars, "doubled") == 14.0
		})
		if _, err := e.c.GetInstance(ctx, bob, processes, inst.Id); err == nil {
			t.Fatal("others do not see the instance")
		}
		if _, err := e.c.CorrelateMessage(ctx, bob, model.MessageInput{Name: "go", All: true}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
		if current := must[model.Instance](t)(e.c.GetInstance(ctx, alice, processes, inst.Id)); current.State != model.StateRunning {
			t.Fatalf("a message of another user does not reach the instance: %v", current.State)
		}
		result := must[model.CorrelationResult](t)(e.c.CorrelateMessage(ctx, alice, model.MessageInput{Name: "go", InstanceId: inst.Id}))
		if len(result.Correlations) != 1 {
			t.Fatalf("the message reaches the waiting instance: %+v", result)
		}
		done := e.instance(processes, inst.Id, model.StateCompleted)
		if done.ModelId != m.Id || done.StartVariables["level"] != 7.0 {
			t.Errorf("the instance keeps its origin and start values: %+v", done)
		}

		finished := must[model.Page[model.Instance]](t)(e.c.ListInstances(ctx, alice, processes, model.InstanceListOptions{State: "finished", DeploymentId: d.Id}))
		if finished.Total != 1 {
			t.Errorf("the finished instance is listed: %+v", finished)
		}
		stats := must[[]model.ElementStats](t)(e.c.ListElementStats(ctx, alice, processes, inst.Id, 0))
		passed := map[string]int64{}
		for _, s := range stats {
			passed[s.ElementId] = s.Count
		}
		if passed["double"] != 1 || passed["wait"] != 1 || passed["end"] != 1 {
			t.Errorf("the history shows the path: %+v", passed)
		}

		if err := e.c.DeleteDeployment(ctx, alice, processes, d.Id); err != nil {
			t.Fatal(err)
		}
		if err := e.c.DeleteModel(ctx, alice, processes, m.Id, false); err != nil {
			t.Fatal(err)
		}
	})
}

// A process started by a schedule waits on a timer. Switched off, the deployment starts
// nothing, not even by its schedule; switched on again, the schedule starts it again.
func TestScheduledProcessSwitchedOffAndOn(t *testing.T) {
	eachVariant(t, func(t *testing.T, e *env) {
		d := must[model.Deployment](t)(e.c.CreateDeployment(ctx, alice, processes, model.DeploymentInput{Name: "Every second", Xml: b.Process(
			b.Start("start"), b.CatchTimer("wait", "timeDuration", "PT0.1S"), b.End("end"),
			b.Flow("start", "wait"), b.Flow("wait", "end"),
		)}))
		schedule := must[model.Schedule](t)(e.c.CreateSchedule(ctx, alice, processes, model.ScheduleInput{DeploymentId: d.Id, Cron: "* * * * * *", Alias: "every second"}))
		count := func(state string) int {
			page := must[model.Page[model.Instance]](t)(e.c.ListInstances(ctx, alice, processes, model.InstanceListOptions{DeploymentId: d.Id, State: state}))
			return page.Total
		}
		e.waitFor("a scheduled instance that passed its timer", func() bool { return count("finished") > 0 })

		if _, err := e.c.SetDeploymentDisabled(ctx, alice, processes, d.Id, model.DisabledInput{Disabled: true}); err != nil {
			t.Fatal(err)
		}
		// an instance may have started in the same second; it ends with the switch-off or its timer
		e.waitFor("no instance running", func() bool { return count("running") == 0 })
		before := count("")
		time.Sleep(2500 * time.Millisecond)
		if after := count(""); after != before {
			t.Fatalf("a switched-off deployment is not started by its schedule: %v -> %v", before, after)
		}
		if _, err := e.c.StartInstance(ctx, alice, processes, model.StartInstanceInput{DeploymentId: d.Id}); !errors.Is(err, model.ErrConflict) {
			t.Fatalf("nor by hand: %v", err)
		}

		if _, err := e.c.SetDeploymentDisabled(ctx, alice, processes, d.Id, model.DisabledInput{Disabled: false}); err != nil {
			t.Fatal(err)
		}
		e.waitFor("the schedule starting again", func() bool { return count("") > before })
		if err := e.c.DeleteSchedule(ctx, alice, processes, schedule.Id); err != nil {
			t.Fatal(err)
		}
	})
}

// A faulty script stops an instance with an incident. The user fixes the process in a new
// version of the deployment and restarts the instance on it.
func TestIncidentRepairedByANewVersion(t *testing.T) {
	eachVariant(t, func(t *testing.T, e *env) {
		process := func(script string) string {
			return b.Process(b.StartWithForm("start", "level=5"), b.Script("compute", "result", script), b.End("end"),
				b.Flow("start", "compute"), b.Flow("compute", "end"))
		}
		d := must[model.Deployment](t)(e.c.CreateDeployment(ctx, alice, processes, model.DeploymentInput{Name: "Faulty", Xml: process("levle * 2")}))
		inst := must[model.Instance](t)(e.c.StartInstance(ctx, alice, processes, model.StartInstanceInput{DeploymentId: d.Id, Variables: map[string]any{"level": 4}}))
		e.instance(processes, inst.Id, model.StateIncident)
		incidents := must[model.Page[model.Incident]](t)(e.c.ListIncidents(ctx, alice, processes, model.IncidentListOptions{DeploymentId: d.Id}))
		if incidents.Total != 1 || incidents.Items[0].ElementId != "compute" || incidents.Items[0].InstanceId != inst.Id {
			t.Fatalf("the incident names the element: %+v", incidents)
		}

		d = must[model.Deployment](t)(e.c.PutDeployment(ctx, alice, processes, d.Id, model.DeploymentInput{Name: "Fixed", Xml: process("level * 2"), UpdateStrategy: model.UpdateDrain}))
		if d.Version != 2 {
			t.Fatalf("the fix is version 2: %+v", d)
		}
		restarted := must[model.Instance](t)(e.c.RestartInstance(ctx, alice, processes, inst.Id, model.RestartInput{DeploymentVersion: 2}))
		if restarted.Run != 2 {
			t.Fatalf("a restart is a new run: %+v", restarted)
		}
		e.instance(processes, inst.Id, model.StateCompleted)
		if vars := must[[]model.Variable](t)(e.c.GetVariables(ctx, alice, processes, inst.Id)); variable(vars, "result") != 8.0 {
			t.Errorf("the new run computes with the start values: %+v", vars)
		}
		runs := must[[]model.Run](t)(e.c.ListRuns(ctx, alice, processes, inst.Id))
		if len(runs) != 2 || runs[0].State != model.StateIncident || runs[0].DeploymentVersion != 1 || runs[1].DeploymentVersion != 2 {
			t.Errorf("the runs keep their versions and ends: %+v", runs)
		}
	})
}

// Values a user stores in process-io are read and written by processes, across instances.
func TestProcessesShareValuesThroughIo(t *testing.T) {
	eachVariant(t, func(t *testing.T, e *env) {
		if err := e.c.SetIoValue(ctx, alice, "threshold", model.IoValueInput{Value: 10}); err != nil {
			t.Fatal(err)
		}
		d := must[model.Deployment](t)(e.c.CreateDeployment(ctx, alice, processes, model.DeploymentInput{Name: "IO", Xml: b.Process(
			b.Start("start"),
			b.Service("read", "process_io", b.InText("io.read.threshold", "threshold"), b.InText("io.default.counter", "0"), b.InText("io.read.counter", "counter"),
				b.OutScript("threshold", "threshold"), b.OutScript("counter", "counter")),
			b.Service("write", "process_io", b.InScript("io.write.limit", "threshold * 2"), b.InScript("io.write.counter", "counter + 1")),
			b.End("end"),
			b.Flow("start", "read"), b.Flow("read", "write"), b.Flow("write", "end"),
		)}))
		for range 2 {
			inst := must[model.Instance](t)(e.c.StartInstance(ctx, alice, processes, model.StartInstanceInput{DeploymentId: d.Id}))
			e.instance(processes, inst.Id, model.StateCompleted)
		}
		if v := must[model.IoValue](t)(e.c.GetIoValue(ctx, alice, "limit")); v.Value != 20.0 {
			t.Errorf("the process wrote the value: %+v", v)
		}
		if v := must[model.IoValue](t)(e.c.GetIoValue(ctx, alice, "counter")); v.Value != 2.0 {
			t.Errorf("each instance read what the last one wrote: %+v", v)
		}
		if _, err := e.c.GetIoValue(ctx, bob, "limit"); !errors.Is(err, model.ErrNotFound) {
			t.Errorf("the values belong to the user: %v", err)
		}
	})
}

// The web-ui as it is today: the designer stores a model, the deploy dialog prepares and
// deploys it, a copied start URL starts it, and the monitor shows it finished, all over the
// routes of the old services (API-08).
func TestWebUiProcessFlowOverTheDeprecatedRoutes(t *testing.T) {
	eachVariant(t, func(t *testing.T, e *env) {
		var stored struct {
			Id string `json:"_id"`
		}
		if status := e.call(alice, http.MethodPost, "/repository/processes", map[string]any{"name": "Heating", "bpmn_xml": waitingProcess(), "svgXML": "<svg/>"}, &stored); status != http.StatusOK {
			t.Fatalf("designer: %v", status)
		}
		var prepared models.Deployment
		if status := e.call(alice, http.MethodGet, "/deployment/v3/prepared-deployments/"+stored.Id+"?with_options=false", nil, &prepared); status != http.StatusOK || len(prepared.StartParameter) != 1 {
			t.Fatalf("deploy dialog: %v %+v", status, prepared)
		}
		prepared.Name = "Heating in the kitchen"
		var deployed models.Deployment
		if status := e.call(alice, http.MethodPost, "/deployment/v3/deployments?source=sepl", prepared, &deployed); status != http.StatusOK {
			t.Fatalf("deploy: %v", status)
		}
		if status := e.call(alice, http.MethodGet, "/engine/v2/deployments/"+deployed.Id+"/start?business_key=kitchen&level=8", nil, nil); status != http.StatusOK {
			t.Fatalf("start url: %v", status)
		}
		e.waitFor("the waiting instance", func() bool {
			vars, _ := e.c.GetVariables(ctx, alice, processes, "kitchen")
			return variable(vars, "doubled") == 16.0
		})
		if _, err := e.c.CorrelateMessage(ctx, alice, model.MessageInput{Name: "go", InstanceId: "kitchen"}); err != nil {
			t.Fatal(err)
		}
		e.instance(processes, "kitchen", model.StateCompleted)
		var finished []struct{ Id, State, ProcessDefinitionName string }
		if e.call(alice, http.MethodGet, "/engine/v2/history/process-instances?finished=true", nil, &finished); len(finished) != 1 || finished[0].Id != "kitchen" || finished[0].State != "COMPLETED" {
			t.Fatalf("monitor: %+v", finished)
		}
		var ok bool
		if status := e.call(alice, http.MethodDelete, "/deployment/v3/deployments/"+deployed.Id, nil, &ok); status != http.StatusOK || !ok {
			t.Fatalf("delete deployment: %v", status)
		}
		if status := e.call(alice, http.MethodDelete, "/repository/processes/"+stored.Id, nil, nil); status != http.StatusOK {
			t.Fatalf("delete model: %v", status)
		}
	})
}
