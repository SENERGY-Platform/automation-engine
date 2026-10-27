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

package modules

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

var logger = slog.New(slog.NewTextHandler(io.Discard, nil))

// instanceWith creates a deployment and an instance with the given modules directly in the store
func instanceWith(t *testing.T, s *store.Store, namespace string, modules ...model.Module) string {
	t.Helper()
	now := time.Now()
	err := s.Tx(context.Background(), func(tx *store.Tx) error {
		if err := tx.InsertDeployment(model.Deployment{Id: "d-" + namespace, Namespace: namespace, Name: "d", Owner: "alice", Version: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		if err := tx.InsertInstance(store.Instance{Instance: model.Instance{Id: "i-" + namespace, Namespace: namespace, Owner: "alice", DeploymentId: "d-" + namespace, DeploymentName: "d", DeploymentVersion: 1, Run: 1, State: model.StateCompleted, CreatedAt: now, StartedAt: now, LastActivityAt: now}, ActivityMinSeq: 1}); err != nil {
			return err
		}
		for _, m := range modules {
			m.InstanceId, m.Namespace, m.Owner, m.CreatedAt, m.UpdatedAt = "i-"+namespace, namespace, "alice", now, now
			if err := tx.UpsertModule(m); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return "i-" + namespace
}

func TestRemoverDeletesResourcesOfDeletedInstancesAndRetriesFailures(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		var mux sync.Mutex
		calls := map[string]string{}
		status := http.StatusServiceUnavailable
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mux.Lock()
			defer mux.Unlock()
			calls[r.URL.Path] = r.Method + " " + r.Header.Get("Authorization")
			w.WriteHeader(status)
		}))
		defer server.Close()
		managed := instanceWith(t, s, "processes")
		instanceWith(t, s, "other")
		instance := instanceWith(t, s, "smart-services",
			model.Module{Id: "pipeline", ModuleType: "analytics", DeleteInfo: &model.ModuleDeleteInfo{Url: server.URL + "/pipeline/1", UserId: "alice"}},
			model.Module{Id: "deployment", ModuleType: "process_deployment", DeleteInfo: &model.ModuleDeleteInfo{Resource: &model.ResourceRef{Kind: model.ResourceDeployment, Namespace: "processes", Id: "d-processes"}}},
			model.Module{Id: "foreign", ModuleType: "process_deployment", DeleteInfo: &model.ModuleDeleteInfo{Resource: &model.ResourceRef{Kind: model.ResourceDeployment, Namespace: "smart-services", Id: "d-other"}}},
			model.Module{Id: "info", ModuleType: "info"},
		)
		err := s.Tx(context.Background(), func(tx *store.Tx) error {
			return tx.DeleteInstance(instance)
		})
		if err != nil {
			t.Fatal(err)
		}
		clock := time.Now()
		remover := NewRemover(s, func(user string) (string, error) { return "Bearer token-of-" + user, nil }, logger)
		remover.now = func() time.Time { return clock }
		if err = remover.Process(context.Background()); err != nil {
			t.Fatal(err)
		}
		if calls["/pipeline/1"] != "DELETE Bearer token-of-alice" {
			t.Fatalf("the delete url is called with the token of the module user: %v", calls)
		}
		var exists bool
		_ = s.Tx(context.Background(), func(tx *store.Tx) error {
			_, err := tx.GetInstance(managed, false)
			exists = err == nil
			return nil
		})
		if exists {
			t.Fatal("the managed deployment and its instances go with the module (NS-08)")
		}
		pending := func() map[string]int {
			result := map[string]int{}
			_ = s.Tx(context.Background(), func(tx *store.Tx) error {
				due, err := tx.DueModuleRemovals(clock.Add(24*time.Hour), 10)
				for _, r := range due {
					result[r.ModuleId] = r.Attempts
				}
				return err
			})
			return result
		}
		if p := pending(); len(p) != 2 || p["pipeline"] != 1 || p["foreign"] != 1 {
			t.Fatalf("a failed removal and a resource of another namespace stay queued: %v", p)
		}
		mux.Lock()
		status = http.StatusNotFound
		mux.Unlock()
		clock = clock.Add(2 * time.Minute)
		if err = remover.Process(context.Background()); err != nil {
			t.Fatal(err)
		}
		if p := pending(); len(p) != 1 || p["foreign"] == 0 {
			t.Fatalf("404 counts as removed: %v", p)
		}
	})
}

func TestRemoverDeletesImportsOfTheModuleWorkerAtImportDeploy(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		var mux sync.Mutex
		calls := map[string]string{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mux.Lock()
			defer mux.Unlock()
			calls[r.URL.Path] = r.Method + " " + r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()
		instance := instanceWith(t, s, "smart-services",
			model.Module{Id: "migrated", ModuleType: TypeImport, DeleteInfo: &model.ModuleDeleteInfo{Url: "http://kong.invalid:8000/import/deploy/instances/urn:infai:ses:import:1", UserId: "alice"}},
			model.Module{Id: "created", ModuleType: TypeImport, DeleteInfo: &model.ModuleDeleteInfo{Url: server.URL + "/instances/urn:infai:ses:import:2", UserId: "alice"}},
		)
		err := s.Tx(context.Background(), func(tx *store.Tx) error {
			return tx.DeleteInstance(instance)
		})
		if err != nil {
			t.Fatal(err)
		}
		remover := NewRemover(s, func(user string) (string, error) { return "Bearer token-of-" + user, nil }, logger)
		remover.SetImportDeployUrl(server.URL)
		if err = remover.Process(context.Background()); err != nil {
			t.Fatal(err)
		}
		if calls["/instances/urn:infai:ses:import:1"] != "DELETE Bearer token-of-alice" || calls["/instances/urn:infai:ses:import:2"] != "DELETE Bearer token-of-alice" {
			t.Fatalf("imports are deleted at import-deploy, not through Kong: %v", calls)
		}
		var due []store.ModuleRemoval
		_ = s.Tx(context.Background(), func(tx *store.Tx) error {
			due, err = tx.DueModuleRemovals(time.Now().Add(24*time.Hour), 10)
			return err
		})
		if len(due) != 0 {
			t.Fatalf("both removals are done: %v", due)
		}
	})
}
