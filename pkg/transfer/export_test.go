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

package transfer_test

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/transfer"
)

var ctx = context.Background()

// deploy writes a deployment of a gateway in the given version with all versions before it,
// an event description and a device group
func deploy(tx *store.Tx, id string, version int) error {
	now := time.Now().UTC()
	d := model.Deployment{Id: id, Namespace: "processes", Name: "heating " + id, Owner: "alice", ModelId: "m-" + id, ModelVersion: version, Version: version, CreatedAt: now, UpdatedAt: now}
	if err := tx.InsertDeployment(d); err != nil {
		return err
	}
	for v := 1; v <= version; v++ {
		err := tx.InsertDeploymentVersion(model.DeploymentVersion{DeploymentId: id, Version: v, ModelVersion: v, Xml: fmt.Sprintf("<xml version=\"%v\"/>", v),
			IncidentHandling: model.IncidentHandling{Notify: true}, Parameters: map[string]model.ElementParameters{}, CreatedAt: now})
		if err != nil {
			return err
		}
	}
	return tx.ReplaceEventDescriptions(id, []model.EventDescription{{Id: "e-" + id, Version: version, ElementId: "wait", Namespace: "processes", Owner: "alice",
		MessageName: id + "_wait", DeviceGroupId: "g-1", Topic: "t", Script: "x", ValueVariable: "v"}}, []string{"g-1"}, now)
}

// gatewayDatabase creates the database of a gateway with a deployment in version 3
func gatewayDatabase(t *testing.T) (string, *store.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "automation-engine.db")
	s, err := store.Open(ctx, store.SQLite, path)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Tx(ctx, func(tx *store.Tx) error {
		if err := deploy(tx, "d-1", 3); err != nil {
			return err
		}
		return tx.SetApplied(store.Applied{Kind: "deployment", Id: "d-1", Version: 1761800000000, At: time.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
	return path, s
}

// execute changes the closed database directly
func execute(t *testing.T, path string, statements ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range statements {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExportHoldsDeploymentsWithAllVersions(t *testing.T) {
	path, s := gatewayDatabase(t)
	s.Close()
	var stdout, stderr bytes.Buffer
	if code := transfer.Main([]string{"-sqlite", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code %v: %v", code, stderr.String())
	}
	var export model.GatewayExport
	if err := json.Unmarshal(stdout.Bytes(), &export); err != nil {
		t.Fatal(err)
	}
	if export.Format != model.GatewayExportFormat || export.FormatVersion != 1 || export.SchemaMigration < store.MinGatewayExportMigration || export.ExporterVersion == "" || export.ExportedAt.IsZero() {
		t.Errorf("header: %+v", export)
	}
	if len(export.Deployments) != 1 {
		t.Fatalf("deployments: %+v", export.Deployments)
	}
	d := export.Deployments[0]
	if d.Deployment.Id != "d-1" || d.Deployment.Version != 3 || len(d.Versions) != 3 || d.Versions[0].Version != 1 || d.Versions[2].Xml != `<xml version="3"/>` {
		t.Errorf("the deployment with its versions ascending: %+v", d)
	}
	if len(d.EventDescriptions) != 1 || d.EventDescriptions[0].DeviceGroupId != "g-1" || len(d.DeviceGroups) != 1 {
		t.Errorf("event descriptions and device groups: %+v", d)
	}
	if len(export.Applied) != 1 || export.Applied[0].Version != 1761800000000 || export.Namespaces == nil {
		t.Errorf("applied state and namespaces: %+v", export)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("the export leaves nothing beside the closed database: %v", entries)
	}
}

func TestExportFillsColumnsOfLaterMigrations(t *testing.T) {
	path, s := gatewayDatabase(t)
	s.Close()
	// as in a database from before a migration that adds the columns
	execute(t, path, "ALTER TABLE deployments DROP COLUMN managed_by", "ALTER TABLE deployment_versions DROP COLUMN parameters", "ALTER TABLE event_descriptions DROP COLUMN aspect_ids")
	export, err := transfer.Export(ctx, path, "test", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	d := export.Deployments[0]
	if d.Deployment.ManagedBy != "" || d.Deployment.Version != 3 || len(d.Versions) != 3 || d.Versions[0].Parameters != nil || len(d.EventDescriptions) != 1 {
		t.Errorf("missing columns read as their default: %+v", d)
	}
}

func TestExportRefusesDatabasesOlderThanTheProcessModule(t *testing.T) {
	path, s := gatewayDatabase(t)
	s.Close()
	execute(t, path, fmt.Sprintf("DELETE FROM schema_migrations WHERE version >= %v", store.MinGatewayExportMigration))
	var stdout, stderr bytes.Buffer
	if code := transfer.Main([]string{"-sqlite", path}, &stdout, &stderr); code == 0 || stdout.Len() > 0 || !strings.Contains(stderr.String(), "v2.0.0") {
		t.Errorf("exit code %v, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	if code := transfer.Main([]string{"-sqlite", filepath.Join(t.TempDir(), "missing.db")}, &stdout, &stderr); code == 0 {
		t.Errorf("a missing database is an error, not an empty one")
	}
}

// TestWriterProcess is the engine on the gateway for TestExportWhileTheEngineWrites: it writes a
// deployment after the other until it is killed.
func TestWriterProcess(t *testing.T) {
	path := os.Getenv("AE_EXPORT_WRITER")
	if path == "" {
		t.Skip("only as the writer of TestExportWhileTheEngineWrites")
	}
	s, err := store.Open(ctx, store.SQLite, path)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("ready")
	for i := 0; ; i++ {
		err = s.Tx(ctx, func(tx *store.Tx) error {
			if err := deploy(tx, fmt.Sprint("w-", i), 1+i%4); err != nil {
				return err
			}
			return tx.SetApplied(store.Applied{Kind: "deployment", Id: "d-1", Version: int64(i), At: time.Now()})
		})
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// SNRGY-5066: the export runs beside the module, which keeps writing, with the volume mounted
// read-only; WAL mode gives it a consistent state
func TestExportWhileTheEngineWrites(t *testing.T) {
	path, s := gatewayDatabase(t)
	s.Close()
	writer := exec.Command(os.Args[0], "-test.run=^TestWriterProcess$", "-test.v")
	writer.Env = append(os.Environ(), "AE_EXPORT_WRITER="+path)
	stdout, err := writer.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	writer.Stderr = os.Stderr
	if err = writer.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Process.Kill(); _ = writer.Wait() })
	lines := bufio.NewScanner(stdout)
	for lines.Scan() && !strings.Contains(lines.Text(), "ready") {
	}
	go func() {
		for lines.Scan() {
		}
	}()
	time.Sleep(100 * time.Millisecond)

	// read-only as far as the file system goes; root ignores it
	dir := filepath.Dir(path)
	for _, file := range []string{path, path + "-wal", path + "-shm"} {
		if err = os.Chmod(file, 0o444); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if os.Geteuid() == 0 {
		t.Log("running as root: the files stay writable for the export")
	}

	previous := 0
	for i := 0; i < 20; i++ {
		export, err := transfer.Export(ctx, path, "test", time.Now())
		if err != nil {
			t.Fatalf("export %v: %v", i, err)
		}
		if len(export.Deployments) < previous {
			t.Fatalf("export %v went back from %v to %v deployments", i, previous, len(export.Deployments))
		}
		previous = len(export.Deployments)
		for _, d := range export.Deployments {
			if len(d.Versions) != d.Deployment.Version || len(d.EventDescriptions) != 1 {
				t.Fatalf("export %v holds a deployment of an unfinished transaction: %+v", i, d)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if previous < 2 {
		t.Errorf("the writer wrote meanwhile: %v deployments", previous)
	}
	t.Logf("last export: %v deployments", previous)
}
