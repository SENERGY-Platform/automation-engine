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

package client

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/controller"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	permclient "github.com/SENERGY-Platform/permissions-v2/pkg/client"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
	"github.com/google/uuid"
)

// TestClient runs the engine in the process with an in-memory SQLite database and an in-process
// permissions-v2; it fulfills Client without network or container (CLI-09). Tokens are not
// verified, see TestToken.
type TestClient struct {
	*controller.Controller
	// to register task types or trigger due timers in tests
	Engine *engine.Engine
	server *server.Server
}

// SyncPermissions writes pending rights to permissions-v2 at once instead of in the next
// background pass, e.g. before a test shares a resource.
func (this *TestClient) SyncPermissions(ctx context.Context) error {
	return this.server.Permissions.Sync(ctx)
}

var _ Client = (*TestClient)(nil)

// NewTestClient starts the engine; it stops when ctx is done.
func NewTestClient(ctx context.Context) (*TestClient, error) {
	config := configuration.Default()
	config.Database = "sqlite"
	config.SqlitePath = "file:" + uuid.NewString() + "?mode=memory&cache=shared"
	config.PollInterval = "100ms"
	deps, err := TestDependencies(ctx)
	if err != nil {
		return nil, err
	}
	srv, err := server.New(ctx, config, slog.New(slog.NewTextHandler(io.Discard, nil)), deps)
	if err != nil {
		return nil, err
	}
	srv.Start(ctx)
	go func() {
		<-ctx.Done()
		// give a running step time to finish before the database closes
		time.Sleep(100 * time.Millisecond)
		srv.Close()
	}()
	return &TestClient{Controller: srv.Controller, Engine: srv.Engine, server: srv}, nil
}

// TestDependencies run permissions-v2 in the process with an admin token it accepts, and with
// the topics devices and device-groups that selections are checked against.
func TestDependencies(ctx context.Context) (server.Dependencies, error) {
	permissions, err := permclient.NewTestClient(ctx)
	if err != nil {
		return server.Dependencies{}, err
	}
	admin := auth.UnsignedToken("automation-engine", "admin")
	for _, topic := range []string{"devices", "device-groups"} {
		if _, err, _ = permissions.SetTopic(admin, permmodel.Topic{Id: topic}); err != nil {
			return server.Dependencies{}, err
		}
	}
	return server.Dependencies{Permissions: permissions, AdminToken: auth.Static(admin), UserToken: auth.UnsignedTokens}, nil
}

// TestToken creates an unsigned token for user with the given realm roles, e.g. "admin".
// It works with servers that do not verify signatures, like the test client.
func TestToken(user string, roles ...string) string {
	return auth.UnsignedToken(user, roles...)
}
