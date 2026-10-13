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

package legacyio_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/migrate/legacyio"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
	"github.com/google/uuid"
)

var ctx = context.Background()

// legacyDatabase creates a database with the table of process-io-api for Postgres
func legacyDatabase(t *testing.T) string {
	_, dsn := storetest.OpenPostgresDsn(t)
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := "legacy_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	start := strings.LastIndex(dsn, "/")
	end := strings.Index(dsn[start:], "?")
	legacy := dsn[:start+1] + name + dsn[start+end:]
	db, err := sql.Open("pgx", legacy)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	statements := []string{
		`CREATE TABLE variables (user_id VARCHAR(50) NOT NULL, variable_key VARCHAR(255) NOT NULL, process_definition_id VARCHAR(64), process_instance_id VARCHAR(64),
			unix_timestamp_in_s INT, variable_value json, PRIMARY KEY (user_id, variable_key))`,
		`INSERT INTO variables VALUES ('local', 'mode', NULL, NULL, 1700000000, '"eco"')`,
		`INSERT INTO variables VALUES ('local', 'deplid_4a7e3b0c_1d2e_4f56_8a9b_0c1d2e3f4a5b:1:3f2e_limit', 'deplid_4a7e3b0c_1d2e_4f56_8a9b_0c1d2e3f4a5b:1:3f2e', NULL, 1700000000, '20')`,
		`INSERT INTO variables VALUES ('local', 'ci-1_count', NULL, 'ci-1', 1700000000, '3')`,
	}
	for _, statement := range statements {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return legacy
}

// The values of the old process module are taken over once, those of instances are left out,
// a definition id in a key becomes the deployment id (MIG-06).
func TestValuesOfTheOldProcessModuleAreTakenOverOnce(t *testing.T) {
	legacy := legacyDatabase(t)
	s := storetest.OpenSQLite(t)
	result, done, err := legacyio.Import(ctx, s, legacy, "alice")
	if err != nil || !done || result.Imported != 2 || result.Skipped != 1 {
		t.Fatalf("%+v %v %v", result, done, err)
	}
	var values []model.IoValue
	_ = s.Tx(ctx, func(tx *store.Tx) (err error) { values, err = tx.ListIoValues("alice", "", "", ""); return err })
	if len(values) != 2 || values[0].Key != "4a7e3b0c-1d2e-4f56-8a9b-0c1d2e3f4a5b_limit" || values[0].Value != 20.0 || values[1].Value != "eco" {
		t.Errorf("%+v", values)
	}
	if _, done, err = legacyio.Import(ctx, s, legacy, "alice"); err != nil || done {
		t.Errorf("a second start imports nothing: %v %v", done, err)
	}
}
