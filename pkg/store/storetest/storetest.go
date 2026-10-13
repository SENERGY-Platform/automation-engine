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

// Package storetest opens stores for tests: SQLite in memory and Postgres in a container.
package storetest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// MemoryDsn returns a SQLite DSN for a fresh in-memory database.
func MemoryDsn() string {
	return "file:" + uuid.NewString() + "?mode=memory&cache=shared"
}

func OpenSQLite(t testing.TB) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), store.SQLite, MemoryDsn())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

var pg struct {
	once sync.Once
	dsn  string
	err  error
}

// OpenPostgres opens a fresh database in a shared Postgres container. The test is skipped
// if AE_TEST_POSTGRES=off or no container runtime is available.
func OpenPostgres(t testing.TB) *store.Store {
	t.Helper()
	s, _ := OpenPostgresDsn(t)
	return s
}

// OpenPostgresDsn is OpenPostgres that also returns the connection string of the database,
// e.g. for a second connection that listens for notifications.
func OpenPostgresDsn(t testing.TB) (*store.Store, string) {
	t.Helper()
	if os.Getenv("AE_TEST_POSTGRES") == "off" {
		t.Skip("AE_TEST_POSTGRES=off")
	}
	pg.once.Do(func() {
		pg.dsn, pg.err = startPostgres()
	})
	if pg.err != nil {
		t.Skip("postgres container not available:", pg.err)
	}
	dsn, err := NewPostgresDatabase()
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(context.Background(), store.Postgres, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dsn
}

// NewPostgresDatabase creates an empty database in the shared Postgres container and returns
// its connection string, e.g. for a whole server. Unlike OpenPostgres, a container that cannot
// start is an error.
func NewPostgresDatabase() (string, error) {
	pg.once.Do(func() {
		pg.dsn, pg.err = startPostgres()
	})
	if pg.err != nil {
		return "", pg.err
	}
	name := "t_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	admin, err := sql.Open("pgx", pg.dsn)
	if err != nil {
		return "", err
	}
	defer admin.Close()
	if _, err = admin.Exec("CREATE DATABASE " + name); err != nil {
		return "", err
	}
	return strings.Replace(pg.dsn, "/postgres?", "/"+name+"?", 1), nil
}

func startPostgres() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// the container lives until the test binary exits; testcontainers' reaper removes it
	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("postgres"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(time.Minute)),
	)
	if err != nil {
		return "", err
	}
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return "", err
	}
	if !strings.Contains(dsn, "/postgres?") {
		return "", fmt.Errorf("unexpected connection string format")
	}
	return dsn, nil
}

// Backend names a way to open a store.
type Backend struct {
	Name string
	Open func(t testing.TB) *store.Store
}

func Backends() []Backend {
	return []Backend{{Name: "sqlite", Open: OpenSQLite}, {Name: "postgres", Open: OpenPostgres}}
}

// Run executes fn as subtest for every backend.
func Run(t *testing.T, fn func(t *testing.T, s *store.Store)) {
	for _, backend := range Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			fn(t, backend.Open(t))
		})
	}
}
