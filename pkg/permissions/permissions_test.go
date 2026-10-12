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
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
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
	service := New(nil, nil, nil, s, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
