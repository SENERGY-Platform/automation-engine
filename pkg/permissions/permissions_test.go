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

package permissions

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
	permclient "github.com/SENERGY-Platform/permissions-v2/pkg/client"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
)

func pending(t *testing.T, s *store.Store) []store.Registration {
	t.Helper()
	var result []store.Registration
	if err := s.Tx(context.Background(), func(tx *store.Tx) (err error) {
		result, err = tx.PendingRegistrations(model.KindModel, 10)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRightsMessageWithoutOwnerAdministrateRegistersAgain(t *testing.T) {
	ctx := context.Background()
	s := storetest.OpenSQLite(t)
	now := time.Now()
	err := s.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.InsertModel(model.Model{Id: "m", Namespace: "processes", Name: "m", Owner: "alice", Version: 1, CreatedAt: now, UpdatedAt: now}); err != nil {
			return err
		}
		_, err := tx.MarkRegistered(model.KindModel, "m", "alice")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	service := New(nil, nil, nil, nil, s, slog.New(slog.NewTextHandler(io.Discard, nil)))
	kept := `{"command":"RIGHTS","id":"m","rights":{"user_rights":{"alice":{"read":true,"write":true,"execute":true,"administrate":true},"bob":{"read":true}}}}`
	if err = service.HandleRightsMessage(ctx, []byte(kept)); err != nil {
		t.Fatal(err)
	}
	if len(pending(t, s)) != 0 {
		t.Fatalf("rights that keep the owner change nothing")
	}
	withdrawn := `{"command":"RIGHTS","id":"m","rights":{"user_rights":{"bob":{"read":true,"administrate":true}}}}`
	if err = service.HandleRightsMessage(ctx, []byte(withdrawn)); err != nil {
		t.Fatal(err)
	}
	if p := pending(t, s); len(p) != 1 || p[0].Owner != "alice" {
		t.Fatalf("a withdrawn owner right is registered again (SEC-10): %+v", p)
	}
}

// fakePermissions answers GetResource from a list of states, one per read, and records writes
type fakePermissions struct {
	permclient.Client
	reads  []permmodel.ResourcePermissions
	read   int
	writes []permmodel.ResourcePermissions
}

func (this *fakePermissions) GetResourceContext(_ context.Context, _ string, topic string, id string) (permmodel.Resource, error, int) {
	state := this.reads[min(this.read, len(this.reads)-1)]
	this.read++
	return permmodel.Resource{Id: id, TopicId: topic, ResourcePermissions: state}, nil, http.StatusOK
}

func (this *fakePermissions) SetPermissionContext(_ context.Context, _ string, _ string, _ string, permissions permmodel.ResourcePermissions) (permmodel.ResourcePermissions, error, int) {
	this.writes = append(this.writes, permissions)
	return permissions, nil, http.StatusOK
}

type staticNamespaces []model.Namespace

func (this staticNamespaces) Get(name string) (model.Namespace, bool) {
	for _, ns := range this {
		if ns.Name == name {
			return ns.WithDefaults(), true
		}
	}
	return model.Namespace{}, false
}

func (this staticNamespaces) List() []model.Namespace { return this }

func users(entries ...string) permmodel.ResourcePermissions {
	result := permmodel.ResourcePermissions{UserPermissions: map[string]permmodel.PermissionsMap{}}
	for _, user := range entries {
		result.UserPermissions[user] = permmodel.PermissionsMap{Read: true}
	}
	return result
}

// permissions-v2 takes only whole resources; a share written while register works is read
// again before the write and kept (SNRGY-5022)
func TestRegisterKeepsAChangeMadeWhileItWorks(t *testing.T) {
	fake := &fakePermissions{reads: []permmodel.ResourcePermissions{users("alice"), users("alice", "carol"), users("alice", "carol"), users("alice", "carol")}}
	service := New(fake, auth.Static("Bearer admin"), nil, staticNamespaces(configuration.DefaultNamespaces()), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := service.register(context.Background(), "Bearer admin", store.Registration{Kind: model.KindModel, Id: "m", Namespace: "processes", Owner: "bob"}); err != nil {
		t.Fatal(err)
	}
	if len(fake.writes) != 1 {
		t.Fatalf("one write: %+v", fake.writes)
	}
	written := fake.writes[0].UserPermissions
	if !written["bob"].Administrate || !written["carol"].Read || !written["alice"].Read {
		t.Fatalf("the share of carol stays: %+v", written)
	}
}

func TestRegisterGivesUpWhenTheResourceKeepsChanging(t *testing.T) {
	var states []permmodel.ResourcePermissions
	for i := range 10 {
		states = append(states, users("alice", "u"+strconv.Itoa(i)))
	}
	fake := &fakePermissions{reads: states}
	service := New(fake, auth.Static("Bearer admin"), nil, staticNamespaces(configuration.DefaultNamespaces()), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := service.register(context.Background(), "Bearer admin", store.Registration{Kind: model.KindModel, Id: "m", Namespace: "processes", Owner: "bob"})
	if model.AsError(err).Kind != model.KindConflict || len(fake.writes) != 0 {
		t.Fatalf("no write over changes it has not seen: %v %+v", err, fake.writes)
	}
}
