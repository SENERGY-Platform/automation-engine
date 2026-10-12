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

package client_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

func waitFor(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %v", what)
}

func finished(t *testing.T, c client.Client, namespace string, id string) model.Instance {
	t.Helper()
	var inst model.Instance
	waitFor(t, "instance "+id, func() bool {
		var err error
		inst, err = c.GetInstance(ctx, alice, namespace, id)
		return err == nil && inst.State != model.StateRunning
	})
	return inst
}

func TestIoApi(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		if err := c.SetIoValue(ctx, alice, "rooms/kitchen", model.IoValueInput{Value: map[string]any{"t": 21.0}}); err != nil {
			t.Fatal(err)
		}
		if err := c.SetIoValue(ctx, alice, "limit", model.IoValueInput{Value: 5}); err != nil {
			t.Fatal(err)
		}
		v, err := c.GetIoValue(ctx, alice, "rooms/kitchen")
		if err != nil || v.Value.(map[string]any)["t"] != 21.0 {
			t.Fatalf("keys may contain slashes: %+v %v", v, err)
		}
		if _, err = c.GetIoValue(ctx, bob, "limit"); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("values belong to their user, got %v", err)
		}
		page, err := c.ListIoValues(ctx, alice, model.IoListOptions{KeyRegex: "^rooms/"})
		if err != nil || page.Total != 1 {
			t.Fatalf("regex filter: %+v %v", page, err)
		}
		result, err := c.IoBulk(ctx, alice, model.IoBulk{Set: []model.IoValue{{Key: "limit", Value: 6}}, Get: []string{"limit", "calculate_UtcOffset_UTC"}})
		if err != nil || result[0].Value != 6.0 || result[1].Value != 0.0 {
			t.Fatalf("bulk writes before it reads, and computes calculate_ keys: %+v %v", result, err)
		}
		if err = c.SetIoValue(ctx, alice, "x", model.IoValueInput{Value: 1, InstanceId: "unknown"}); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("a bound instance must exist, got %v", err)
		}
		if err = c.DeleteIoValue(ctx, alice, "limit"); err != nil {
			t.Fatal(err)
		}
		if _, err = c.GetIoValue(ctx, alice, "limit"); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("deleted: %v", err)
		}
	})
}

func TestProcessIoTask(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		xml := b.Process(
			b.Start("start"),
			b.Service("write", "process_io", b.InText("io.write.{{InstanceId}}_count", "3"), b.InText("io.write.shared", `{"a":1}`)),
			b.Service("read", "process_io", b.InText("io.read.count", "{{InstanceId}}_count"), b.InText("io.read.missing", "{{InstanceId}}_missing"),
				b.InText("io.default.{{InstanceId}}_missing", "7"), b.OutScript("total", "count + missing")),
			b.End("end"), b.Flow("start", "write"), b.Flow("write", "read"), b.Flow("read", "end"),
		)
		d, err := c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: xml})
		if err != nil {
			t.Fatal(err)
		}
		inst, err := c.StartInstance(ctx, alice, ns, model.StartInstanceInput{DeploymentId: d.Id})
		if err != nil {
			t.Fatal(err)
		}
		inst = finished(t, c, ns, inst.Id)
		vars, _ := c.GetVariables(ctx, alice, ns, inst.Id)
		if inst.State != model.StateCompleted || variable(vars, "total") != 10.0 {
			t.Fatalf("read value plus default (IO-01): %+v %v", inst, vars)
		}
		bound, err := c.ListIoValues(ctx, alice, model.IoListOptions{InstanceId: inst.Id})
		if err != nil || bound.Total != 1 {
			t.Fatalf("a key with {{InstanceId}} is bound to the instance: %+v %v", bound, err)
		}
		if err = c.DeleteInstances(ctx, alice, ns, []string{inst.Id}); err != nil {
			t.Fatal(err)
		}
		if bound, _ = c.ListIoValues(ctx, alice, model.IoListOptions{KeyRegex: "_count$"}); bound.Total != 0 {
			t.Fatalf("bound values go with the instance (IO-03): %+v", bound)
		}
		if shared, err := c.GetIoValue(ctx, alice, "shared"); err != nil || shared.Value.(map[string]any)["a"] != 1.0 {
			t.Fatalf("unbound values stay, written JSON is parsed: %+v %v", shared, err)
		}
	})
}

func TestNotificationAndHttpTasks(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(r.Method + " " + r.Header.Get("X-Test")))
	}))
	defer target.Close()
	eachEnv(t, func(t *testing.T, e env) {
		call := func(namespace string) model.Instance {
			xml := b.Process(
				b.Start("start"),
				b.Service("notify", "notification", b.InText("title", "hello"), b.InText("message", "world")),
				`<bpmn:serviceTask id="call" senergy:taskType="http" senergy:retries="0">`+b.IO(
					b.InText("method", "POST"), b.InText("url", target.URL+"/ta"), b.InScript("headers", `({"X-Test": "yes"})`),
					b.OutScript("status", "statusCode"), b.OutScript("answer", "response"),
				)+`</bpmn:serviceTask>`,
				b.End("end"), b.Flow("start", "notify"), b.Flow("notify", "call"), b.Flow("call", "end"),
			)
			d, err := e.CreateDeployment(ctx, alice, namespace, model.DeploymentInput{Xml: xml})
			if err != nil {
				t.Fatal(err)
			}
			inst, err := e.StartInstance(ctx, alice, namespace, model.StartInstanceInput{DeploymentId: d.Id})
			if err != nil {
				t.Fatal(err)
			}
			return finished(t, e, namespace, inst.Id)
		}
		denied := call(ns)
		if denied.State != model.StateIncident || !strings.Contains(denied.EndReason, "disabled") {
			t.Fatalf("http calls are off in the cloud by default (BPMN-10): %+v", denied)
		}
		if _, err := e.PutNamespace(ctx, admin, "lab", model.Namespace{
			TaskTypes: model.ListRule{Allow: []string{"notification", "http"}},
			Http:      model.HttpSettings{Cloud: model.HttpPolicy{Enabled: true, AllowNetworks: []string{"127.0.0.0/8"}, DenyNetworks: []string{"10.0.0.0/8"}}},
		}); err != nil {
			t.Fatal(err)
		}
		allowed := call("lab")
		vars, _ := e.GetVariables(ctx, alice, "lab", allowed.Id)
		if allowed.State != model.StateCompleted || variable(vars, "status") != 202.0 || variable(vars, "answer") != "POST yes" {
			t.Fatalf("allowed call: %+v %v", allowed, vars)
		}
		namespace, _ := e.GetNamespace(ctx, admin, "lab")
		namespace.Http.Cloud.AllowNetworks = []string{"10.0.0.0/8"}
		if _, err := e.PutNamespace(ctx, admin, "lab", namespace); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "namespace reload", func() bool {
			n, _ := e.GetNamespace(ctx, admin, "lab")
			return n.Http.Cloud.AllowNetworks[0] == "10.0.0.0/8"
		})
		blocked := call("lab")
		if blocked.State != model.StateIncident || !strings.Contains(blocked.EndReason, "not allowed") {
			t.Fatalf("an address outside the allowed networks is refused on connect: %+v", blocked)
		}
	})
}

func TestSchedulesStartInstances(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		d, err := c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: b.Process(b.Start("start"), b.End("end"), b.Flow("start", "end"))})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = c.CreateSchedule(ctx, alice, ns, model.ScheduleInput{DeploymentId: d.Id, Cron: "not a cron"}); !errors.Is(err, model.ErrBadRequest) {
			t.Fatalf("invalid cron, got %v", err)
		}
		if _, err = c.CreateSchedule(ctx, bob, ns, model.ScheduleInput{DeploymentId: d.Id, Cron: "* * * * * *"}); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("a schedule needs execute on the deployment, got %v", err)
		}
		s, err := c.CreateSchedule(ctx, alice, ns, model.ScheduleInput{DeploymentId: d.Id, Cron: "* * * * * *", Alias: "every second"})
		if err != nil || s.NextDue == nil {
			t.Fatalf("schedule: %+v %v", s, err)
		}
		waitFor(t, "scheduled starts", func() bool {
			page, _ := c.ListInstances(ctx, alice, ns, model.InstanceListOptions{DeploymentId: d.Id})
			return page.Total >= 2
		})
		s.Disabled = true
		s, err = c.UpdateSchedule(ctx, alice, ns, s.Id, model.ScheduleInput{DeploymentId: d.Id, Cron: s.Cron, Alias: s.Alias, Disabled: true})
		if err != nil || s.NextDue != nil {
			t.Fatalf("a disabled schedule has no next start: %+v %v", s, err)
		}
		list, err := c.ListSchedules(ctx, alice, ns, model.ScheduleListOptions{DeploymentId: d.Id})
		if err != nil || list.Total != 1 || list.Items[0].Alias != "every second" {
			t.Fatalf("list: %+v %v", list, err)
		}
		if err = c.DeleteDeployment(ctx, alice, ns, d.Id); err != nil {
			t.Fatal(err)
		}
		if _, err = c.GetSchedule(ctx, alice, ns, s.Id); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("schedules go with the deployment, got %v", err)
		}
	})
}
