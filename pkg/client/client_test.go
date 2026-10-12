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
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
	permclient "github.com/SENERGY-Platform/permissions-v2/pkg/client"
)

var alice = client.TestToken("alice")
var bob = client.TestToken("bob")
var admin = client.TestToken("ops", "admin")

const ns = "processes"

// env is a client with access to the permissions-v2 behind it
type env struct {
	client.Client
	permissions permclient.Client
	server      *server.Server
}

// sync writes pending rights to permissions-v2 at once
func (this env) sync(t *testing.T) {
	t.Helper()
	if err := this.server.Permissions.Sync(ctx); err != nil {
		t.Fatal(err)
	}
}

func newServer(t *testing.T, ctx context.Context) (*server.Server, server.Dependencies) {
	t.Helper()
	config := configuration.Default()
	config.SqlitePath = storetest.MemoryDsn()
	config.PollInterval = "50ms"
	deps, err := client.TestDependencies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.New(ctx, config, slog.New(slog.NewTextHandler(io.Discard, nil)), deps)
	if err != nil {
		t.Fatal(err)
	}
	srv.Start(ctx)
	return srv, deps
}

// envs returns the HTTP client against a running server and the controller in the process,
// which is what the test client consists of; every scenario runs against both (CLI-07)
func envs(t *testing.T) map[string]env {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	httpSrv, httpDeps := newServer(t, ctx)
	httpServer := httptest.NewServer(httpSrv.Handler)
	t.Cleanup(httpServer.Close)
	localSrv, localDeps := newServer(t, ctx)
	return map[string]env{
		"http": {Client: client.New(httpServer.URL), permissions: httpDeps.Permissions, server: httpSrv},
		"test": {Client: localSrv.Controller, permissions: localDeps.Permissions, server: localSrv},
	}
}

func eachEnv(t *testing.T, fn func(t *testing.T, e env)) {
	for name, e := range envs(t) {
		t.Run(name, func(t *testing.T) { fn(t, e) })
	}
}

// clients returns the HTTP client against a running server and the in-process test client;
// every scenario runs against both, so that both implement the same behavior (CLI-07)
func clients(t *testing.T) map[string]client.Client {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	config := configuration.Default()
	config.SqlitePath = storetest.MemoryDsn()
	config.PollInterval = "50ms"
	deps, err := client.TestDependencies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.New(ctx, config, slog.New(slog.NewTextHandler(io.Discard, nil)), deps)
	if err != nil {
		t.Fatal(err)
	}
	srv.Start(ctx)
	httpServer := httptest.NewServer(srv.Handler)
	t.Cleanup(httpServer.Close)
	testClient, err := client.NewTestClient(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]client.Client{"http": client.New(httpServer.URL), "test": testClient}
}

func each(t *testing.T, fn func(t *testing.T, c client.Client)) {
	for name, c := range clients(t) {
		t.Run(name, func(t *testing.T) { fn(t, c) })
	}
}

var ctx = context.Background()

func waitingModel() string {
	return b.Process(
		b.Message("m", "go"),
		b.StartWithForm("start", "level=5"),
		b.Script("init", "seen", "Number(level) + 1"),
		b.CatchMessage("wait", "m", b.OutScript("received", "payload")),
		b.End("end"),
		b.Flow("start", "init"), b.Flow("init", "wait"), b.Flow("wait", "end"),
	)
}

func TestModelLifecycleWithVersionsAndValidationHints(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		namespaces, err := c.ListNamespaces(ctx, alice)
		if err != nil || len(namespaces) != 2 {
			t.Fatalf("expected the shipped namespaces: %+v %v", namespaces, err)
		}
		invalid := strings.Replace(waitingModel(), `<bpmn:endEvent id="end"/>`, `<bpmn:userTask id="user"/><bpmn:endEvent id="end"/>`, 1)
		m, err := c.CreateModel(ctx, alice, ns, model.ModelInput{Xml: invalid, Svg: "<svg/>"})
		if err != nil {
			t.Fatal(err)
		}
		if m.Version != 1 || m.Owner != "alice" || m.Name != "test process" || len(m.Issues) != 1 || m.Issues[0].ElementId != "user" {
			t.Fatalf("a model with issues is stored with hints (MOD-04): %+v", m)
		}
		m, err = c.UpdateModel(ctx, alice, ns, m.Id, model.ModelInput{Name: "Waiting", Xml: waitingModel()})
		if err != nil || m.Version != 2 || len(m.Issues) != 0 || m.Name != "Waiting" {
			t.Fatalf("unexpected update %+v %v", m, err)
		}
		versions, err := c.ListModelVersions(ctx, alice, ns, m.Id, model.PageOptions{})
		if err != nil || versions.Total != 2 || versions.Items[0].Version != 2 || versions.Items[0].Xml != "" {
			t.Fatalf("versions newest first without content: %+v %v", versions, err)
		}
		first, err := c.GetModelVersion(ctx, alice, ns, m.Id, 1)
		if err != nil || first.Xml != invalid || first.Svg != "<svg/>" {
			t.Fatalf("version 1 keeps its content: %v", err)
		}
		if _, err = c.GetModel(ctx, bob, ns, m.Id); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("another user must not see the model, got %v", err)
		}
		if _, err = c.GetModel(ctx, admin, ns, m.Id); err != nil {
			t.Fatalf("admins see everything: %v", err)
		}
		if _, err = c.GetModel(ctx, alice, "smart-services", m.Id); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("models are separated by namespace, got %v", err)
		}
		published, err := c.PublishModel(ctx, alice, ns, m.Id, model.PublishInput{Published: true, Description: "for everyone"})
		if err != nil || !published.Published || published.PublishedAt == nil {
			t.Fatalf("publish: %+v %v", published, err)
		}
		if _, err = c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{ModelId: m.Id, ModelVersion: 1}); !errors.Is(err, model.ErrValidation) {
			t.Fatalf("version 1 must be rejected at deploy (BPMN-18), got %v", err)
		} else if e := model.AsError(err); len(e.Issues) != 1 || e.Issues[0].ElementId != "user" {
			t.Fatalf("the validation error names the element: %+v", e)
		}
		d, err := c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{ModelId: m.Id})
		if err != nil || d.ModelVersion != 2 || d.Name != "Waiting" {
			t.Fatalf("deploy: %+v %v", d, err)
		}
		if err = c.DeleteModel(ctx, alice, ns, m.Id); err != nil {
			t.Fatal(err)
		}
		d, err = c.GetDeployment(ctx, alice, ns, d.Id)
		if err != nil || !d.ModelDeleted {
			t.Fatalf("a deployment keeps its BPMN when the model is deleted (MOD-08): %+v %v", d, err)
		}
	})
}

func TestInstanceLifecycle(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		d, err := c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: waitingModel(), Name: "waiting"})
		if err != nil {
			t.Fatal(err)
		}
		params, err := c.GetStartParameters(ctx, alice, ns, d.Id)
		if err != nil || len(params) != 1 || params[0].Id != "level" || params[0].Default != "5" {
			t.Fatalf("start parameters: %+v %v", params, err)
		}
		inst, err := c.PutInstance(ctx, alice, ns, "my-instance", model.StartInstanceInput{DeploymentId: d.Id, Variables: map[string]any{"level": 7}})
		if err != nil || inst.Id != "my-instance" || inst.State != model.StateRunning || inst.Owner != "alice" {
			t.Fatalf("start: %+v %v", inst, err)
		}
		again, err := c.PutInstance(ctx, alice, ns, "my-instance", model.StartInstanceInput{DeploymentId: d.Id, Variables: map[string]any{"level": 7}})
		if err != nil || again.Run != 1 {
			t.Fatalf("idempotent start: %+v %v", again, err)
		}
		if _, err = c.PutInstance(ctx, alice, ns, "my-instance", model.StartInstanceInput{DeploymentId: d.Id}); !errors.Is(err, model.ErrConflict) {
			t.Fatalf("different content must conflict, got %v", err)
		}
		if _, err = c.StartInstance(ctx, bob, ns, model.StartInstanceInput{DeploymentId: d.Id}); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("bob cannot see alice's deployment, got %v", err)
		}
		if err = c.SetVariable(ctx, alice, ns, inst.Id, "extra", map[string]any{"a": 1}); err != nil {
			t.Fatal(err)
		}
		vars, err := c.GetVariables(ctx, alice, ns, inst.Id)
		if err != nil || len(vars) != 3 {
			t.Fatalf("variables: %+v %v", vars, err)
		}
		result, err := c.CorrelateMessage(ctx, alice, model.MessageInput{Name: "go", All: true, LocalVariables: map[string]any{"payload": "hello"}})
		if err != nil || len(result.Correlations) != 1 {
			t.Fatalf("correlate: %+v %v", result, err)
		}
		if other, err := c.CorrelateMessage(ctx, bob, model.MessageInput{Name: "go", All: true}); err != nil || len(other.Correlations) != 0 {
			t.Fatalf("bob's message must not reach alice's instance: %+v %v", other, err)
		}
		inst, err = c.GetInstance(ctx, alice, ns, inst.Id)
		if err != nil || inst.State != model.StateCompleted || inst.EndedAt == nil {
			t.Fatalf("completed: %+v %v", inst, err)
		}
		vars, _ = c.GetVariables(ctx, alice, ns, inst.Id)
		if value := variable(vars, "received"); value != "hello" {
			t.Errorf("output mapping of the correlation: %v", value)
		}
		if value := variable(vars, "seen"); value != 8.0 {
			t.Errorf("script result: %v", value)
		}
		activities, err := c.ListActivities(ctx, alice, ns, inst.Id, model.ActivityListOptions{})
		if err != nil || len(activities.Items) == 0 || activities.Truncated {
			t.Fatalf("activities: %v %v", len(activities.Items), err)
		}
		last := activities.Items[len(activities.Items)-1]
		if last.Kind != model.ActivityRunEnded {
			t.Errorf("the last activity ends the run, got %v", last.Kind)
		}
		later, err := c.ListActivities(ctx, alice, ns, inst.Id, model.ActivityListOptions{AfterSeq: last.Seq - 1})
		if err != nil || len(later.Items) != 1 {
			t.Fatalf("activities after a sequence number: %v %v", len(later.Items), err)
		}
		stats, err := c.ListElementStats(ctx, alice, ns, inst.Id, 0)
		if err != nil || len(stats) != 4 {
			t.Fatalf("element stats: %+v %v", stats, err)
		}

		restarted, err := c.RestartInstance(ctx, alice, ns, inst.Id, model.RestartInput{})
		if err != nil || restarted.Run != 2 || restarted.State != model.StateRunning {
			t.Fatalf("restart: %+v %v", restarted, err)
		}
		runs, err := c.ListRuns(ctx, alice, ns, inst.Id)
		if err != nil || len(runs) != 2 || runs[0].State != model.StateCompleted {
			t.Fatalf("runs: %+v %v", runs, err)
		}
		if err = c.StopInstances(ctx, alice, ns, []string{inst.Id}); err != nil {
			t.Fatal(err)
		}
		finished, err := c.ListInstances(ctx, alice, ns, model.InstanceListOptions{State: "finished", DeploymentName: "WAIT"})
		if err != nil || finished.Total != 1 || finished.Items[0].State != model.StateTerminated {
			t.Fatalf("finished instances: %+v %v", finished, err)
		}
		if err = c.DeleteInstances(ctx, alice, ns, []string{inst.Id}); err != nil {
			t.Fatal(err)
		}
		if _, err = c.GetInstance(ctx, alice, ns, inst.Id); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("deleted instance: %v", err)
		}
	})
}

func variable(vars []model.Variable, name string) any {
	for _, v := range vars {
		if v.Name == name {
			return v.Value
		}
	}
	return nil
}

func TestDeploymentUpdateAndDelete(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		d, err := c.PutDeployment(ctx, alice, ns, "dep-1", model.DeploymentInput{Xml: waitingModel()})
		if err != nil || d.Id != "dep-1" || d.Version != 1 {
			t.Fatalf("create with id: %+v %v", d, err)
		}
		same, err := c.PutDeployment(ctx, alice, ns, "dep-1", model.DeploymentInput{Xml: waitingModel()})
		if err != nil || same.Version != 1 {
			t.Fatalf("repeating the same deployment changes nothing: %+v %v", same, err)
		}
		inst, err := c.StartInstance(ctx, alice, ns, model.StartInstanceInput{DeploymentId: d.Id})
		if err != nil {
			t.Fatal(err)
		}
		changed := strings.Replace(waitingModel(), "Number(level) + 1", "Number(level) + 2", 1)
		if _, err = c.PutDeployment(ctx, alice, ns, "dep-1", model.DeploymentInput{Xml: changed}); !errors.Is(err, model.ErrBadRequest) {
			t.Fatalf("an update needs a strategy, got %v", err)
		}
		if _, err = c.PutDeployment(ctx, bob, ns, "dep-1", model.DeploymentInput{Xml: changed, UpdateStrategy: model.UpdateStop}); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("bob cannot update alice's deployment, got %v", err)
		}
		d, err = c.PutDeployment(ctx, alice, ns, "dep-1", model.DeploymentInput{Xml: changed, UpdateStrategy: model.UpdateRestart})
		if err != nil || d.Version != 2 {
			t.Fatalf("update: %+v %v", d, err)
		}
		inst, _ = c.GetInstance(ctx, alice, ns, inst.Id)
		if inst.Run != 2 || inst.DeploymentVersion != 2 {
			t.Fatalf("restart strategy: %+v", inst)
		}
		versions, err := c.ListDeploymentVersions(ctx, alice, ns, d.Id, model.PageOptions{})
		if err != nil || versions.Total != 2 || versions.Items[0].UpdateStrategy != model.UpdateRestart {
			t.Fatalf("versions: %+v %v", versions, err)
		}
		v1, err := c.GetDeploymentVersion(ctx, alice, ns, d.Id, 1)
		if err != nil || v1.Xml != waitingModel() {
			t.Fatalf("version 1 keeps its BPMN: %v", err)
		}
		if err = c.DeleteDeployment(ctx, alice, ns, d.Id); err != nil {
			t.Fatal(err)
		}
		if _, err = c.GetInstance(ctx, alice, ns, inst.Id); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("deleting the deployment deletes its instances (DEP-11): %v", err)
		}
	})
}

func TestPagingIteratorVisitsAllItems(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		for range 7 {
			if _, err := c.CreateModel(ctx, alice, ns, model.ModelInput{Xml: waitingModel()}); err != nil {
				t.Fatal(err)
			}
		}
		count := 0
		for _, err := range client.All(func(page model.PageOptions) (model.Page[model.Model], error) {
			page.Limit = 3
			return c.ListModels(ctx, alice, ns, model.ModelListOptions{PageOptions: page})
		}) {
			if err != nil {
				t.Fatal(err)
			}
			count++
		}
		if count != 7 {
			t.Fatalf("expected 7 models, got %v", count)
		}
	})
}

func TestTimersRunInTheBackground(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		d, err := c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: b.Process(
			b.Start("start"), b.CatchTimer("wait", "timeDuration", "PT0.2S"), b.End("end"), b.Flow("start", "wait"), b.Flow("wait", "end"),
		)})
		if err != nil {
			t.Fatal(err)
		}
		inst, err := c.StartInstance(ctx, alice, ns, model.StartInstanceInput{DeploymentId: d.Id})
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			inst, err = c.GetInstance(ctx, alice, ns, inst.Id)
			if err != nil {
				t.Fatal(err)
			}
			if inst.State == model.StateCompleted {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("timer did not fire: %+v", inst)
	})
}

func TestErrorsAreTyped(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		cases := map[error]func() error{
			model.ErrForbidden: func() error { _, err := c.ListNamespaces(ctx, ""); return err },
			model.ErrNotFound:  func() error { _, err := c.GetNamespace(ctx, alice, "unknown"); return err },
			model.ErrBadRequest: func() error {
				_, err := c.ListModels(ctx, alice, ns, model.ModelListOptions{Sort: "unknown"})
				return err
			},
			model.ErrValidation: func() error {
				_, err := c.CreateDeployment(ctx, alice, "smart-services", model.DeploymentInput{Xml: b.Process(b.StartTimer("s", "timeCycle", "0 0 * * * ?"))})
				return err
			},
		}
		for expected, call := range cases {
			if err := call(); !errors.Is(err, expected) {
				t.Errorf("expected %v, got %v", expected, err)
			}
		}
	})
}
