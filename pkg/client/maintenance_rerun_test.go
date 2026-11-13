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
	"path/filepath"
	"sync"
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
	"github.com/SENERGY-Platform/go-service-base/config-hdl/types"
)

// SNRGY-5143: a watch does not start a second run of a procedure beside a running one, it
// queues a rerun that starts once that run ended.

// refreshService offers the procedure refresh, which waits an hour with mode "hold" and ends in
// an incident with mode "fail"
func refreshService() string {
	return b.Process(
		b.Message("m", "refresh"),
		b.StartWithForm("start", "room="),
		b.End("end"),
		`<bpmn:startEvent id="refreshStart"><bpmn:extensionElements><camunda:formData><camunda:formField id="mode" label="mode" type="string"/></camunda:formData></bpmn:extensionElements><bpmn:messageEventDefinition messageRef="m"/></bpmn:startEvent>`,
		b.Script("check", "checked", `if (mode === "fail") { throw new Error("refresh failed") }; true`),
		b.CatchTimer("hold", "timeDuration", "PT1H"),
		b.End("refreshed"),
		b.Flow("start", "end"), b.Flow("refreshStart", "check"), b.Flow("check", "hold"), b.Flow("hold", "refreshed"),
	)
}

// readyRefreshService deploys refreshService and returns a ready instance of it
func readyRefreshService(t *testing.T, c *client.TestClient) model.Instance {
	t.Helper()
	d, err := c.CreateDeployment(ctx, alice, "smart-services", model.DeploymentInput{Xml: refreshService()})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "smart-services", model.StartInstanceInput{DeploymentId: d.Id, Variables: map[string]any{"room": "kitchen"}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ready", func() bool {
		current, _ := c.GetInstance(ctx, alice, "smart-services", inst.Id)
		return current.Status == model.StatusReady
	})
	return inst
}

// procedures lists the maintenance procedures of an instance by state
func procedures(t *testing.T, s *store.Store, parent string) map[model.InstanceState][]store.Instance {
	t.Helper()
	result := map[model.InstanceState][]store.Instance{}
	err := s.Tx(ctx, func(tx *store.Tx) error {
		children, err := tx.ChildInstances(parent)
		if err != nil {
			return err
		}
		for _, id := range children {
			child, err := tx.GetInstance(id, false)
			if err != nil {
				return err
			}
			result[child.State] = append(result[child.State], child)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func queued(t *testing.T, s *store.Store, parent string, variables map[string]any) bool {
	t.Helper()
	var result bool
	err := s.Tx(ctx, func(tx *store.Tx) (err error) {
		result, err = tx.MaintenanceRerunQueued(parent, "refresh", store.RerunHash(variables))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestASecondInternalStartQueuesARerunThatStartsOnceTheRunEnded(t *testing.T) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	srv, err := client.NewTestServer(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &client.TestClient{Controller: srv.Controller, Engine: srv.Engine}
	parent := readyRefreshService(t, c)
	hold := map[string]any{"mode": "hold"}

	first, err := c.StartMaintenanceInternal(ctx, parent.Id, "refresh", hold)
	if err != nil || first.Id == "" || first.State != model.StateRunning {
		t.Fatalf("the first start starts: %+v %v", first, err)
	}
	if child := procedures(t, srv.Store, parent.Id)[model.StateRunning]; len(child) != 1 || child[0].Procedure != "refresh" {
		t.Fatalf("the procedure knows what it runs: %+v", child)
	}
	second, err := c.StartMaintenanceInternal(ctx, parent.Id, "refresh", hold)
	if err != nil || second.Id != "" {
		t.Fatalf("a second start while the first runs starts nothing: %+v %v", second, err)
	}
	if !queued(t, srv.Store, parent.Id, hold) {
		t.Fatal("the second start queues a rerun")
	}
	if err = srv.Modules.StartReruns(ctx); err != nil {
		t.Fatal(err)
	}
	if children := procedures(t, srv.Store, parent.Id); len(children[model.StateRunning]) != 1 || len(children) != 1 {
		t.Fatalf("the rerun waits while the first run runs: %+v", children)
	}

	if err = c.StopInstances(ctx, alice, "smart-services", []string{first.Id}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = srv.Modules.StartReruns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	children := procedures(t, srv.Store, parent.Id)
	if len(children[model.StateRunning]) != 1 || children[model.StateRunning][0].Id == first.Id || children[model.StateRunning][0].StartVariables["mode"] != "hold" {
		t.Fatalf("the rerun starts once after the first run ended, with its variables: %+v", children)
	}
	if queued(t, srv.Store, parent.Id, hold) {
		t.Fatal("the started rerun is no longer queued")
	}

	// a run in an incident ended: it does not hold back the next start
	if err = c.StopInstances(ctx, alice, "smart-services", []string{children[model.StateRunning][0].Id}); err != nil {
		t.Fatal(err)
	}
	failed, err := c.StartMaintenanceInternal(ctx, parent.Id, "refresh", map[string]any{"mode": "fail"})
	if err != nil || failed.State != model.StateIncident {
		t.Fatalf("the procedure ends in an incident: %+v %v", failed, err)
	}
	next, err := c.StartMaintenanceInternal(ctx, parent.Id, "refresh", hold)
	if err != nil || next.Id == "" || next.State != model.StateRunning {
		t.Fatalf("a run in an incident does not hold back the next start: %+v %v", next, err)
	}

	// a run a user started counts as running, although a user may start one beside it
	if err = c.StopInstances(ctx, alice, "smart-services", []string{next.Id}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.StartMaintenance(ctx, alice, "smart-services", parent.Id, "refresh", model.MaintenanceStartInput{Variables: hold}); err != nil {
		t.Fatal(err)
	}
	if byUser, err := c.StartMaintenance(ctx, alice, "smart-services", parent.Id, "refresh", model.MaintenanceStartInput{Variables: hold}); err != nil || byUser.Id == "" {
		t.Fatalf("a user starts beside a running run: %+v %v", byUser, err)
	}
	if internal, err := c.StartMaintenanceInternal(ctx, parent.Id, "refresh", hold); err != nil || internal.Id != "" || !queued(t, srv.Store, parent.Id, hold) {
		t.Fatalf("a run a user started holds back an internal start: %+v %v", internal, err)
	}
}

// Two replicas that start the same procedure at the same time start one run and queue a rerun.
func TestReplicasStartOneRunOfAProcedureAtATime(t *testing.T) {
	backends := map[string]func(t *testing.T) func(config *configuration.Config){
		// a file, which two stores share like two replicas their database
		"sqlite": func(t *testing.T) func(config *configuration.Config) {
			path := filepath.Join(t.TempDir(), "engine.db")
			return func(config *configuration.Config) { config.SqlitePath = path }
		},
		"postgres": func(t *testing.T) func(config *configuration.Config) {
			s, dsn := storetest.OpenPostgresDsn(t)
			s.Close()
			return func(config *configuration.Config) {
				config.Database, config.PostgresDsn = "postgres", types.Secret(dsn)
			}
		},
	}
	for name, backend := range backends {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			database := backend(t)
			deps, err := client.TestDependencies(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var replicas []*server.Server
			for range 2 {
				srv, err := client.NewTestServer(ctx, func(config *configuration.Config, d *server.Dependencies) {
					database(config)
					*d = deps
				})
				if err != nil {
					t.Fatal(err)
				}
				replicas = append(replicas, srv)
			}
			parent := readyRefreshService(t, &client.TestClient{Controller: replicas[0].Controller, Engine: replicas[0].Engine})

			const starts = 16
			hold := map[string]any{"mode": "hold"}
			var wg sync.WaitGroup
			begin := make(chan struct{})
			started := make(chan string, starts)
			failed := make(chan error, starts)
			for i := range starts {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-begin
					inst, err := replicas[i%2].Controller.StartMaintenanceInternal(ctx, parent.Id, "refresh", hold)
					if err != nil {
						failed <- err
					} else if inst.Id != "" {
						started <- inst.Id
					}
				}()
			}
			close(begin)
			wg.Wait()
			close(started)
			close(failed)
			for err := range failed {
				t.Errorf("a start failed: %v", err)
			}
			if len(started) != 1 {
				t.Errorf("one of the starts starts a run, not %v", len(started))
			}
			if children := procedures(t, replicas[1].Store, parent.Id); len(children[model.StateRunning]) != 1 || len(children) != 1 {
				t.Errorf("one run runs: %+v", children)
			}
			if !queued(t, replicas[1].Store, parent.Id, hold) {
				t.Error("the other starts queue a rerun")
			}
		})
	}
}
