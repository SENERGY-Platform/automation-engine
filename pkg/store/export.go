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

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// MinGatewayExportMigration is the schema of the oldest gateway an export reads: process
// module v2.0.0 shipped the engine v0.4.0 with migration 12.
const MinGatewayExportMigration = 12

// exportTables are the tables an export reads; columns they lack read as their default
var exportTables = []string{"namespaces", "deployments", "deployment_versions", "event_descriptions", "deployment_device_groups", "sync_applied"}

// OpenReadOnly opens the SQLite database of a gateway for an export beside the running engine
// (SNRGY-5066): read-only, without migrations, also on a read-only volume. While the engine
// runs, WAL mode gives a consistent state; without a -wal file the engine has closed the
// database, which then cannot change and is opened as immutable, since SQLite cannot create
// the -wal and -shm files on a read-only volume. Columns of migrations later than those of
// the database read as the default of the current schema.
func OpenReadOnly(ctx context.Context, path string) (*Store, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	// a file URI ends the path at ? and #
	dsn := "file:" + strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path) + "?mode=ro&_pragma=busy_timeout(10000)&_pragma=query_only(1)"
	if _, err := os.Stat(path + "-wal"); errors.Is(err, os.ErrNotExist) {
		dsn += "&immutable=1"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	result := &Store{db: db, dialect: SQLite}
	if result.missing, err = missingColumns(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return result, nil
}

// missingColumns compares the export tables of db with the current schema of a new database
func missingColumns(ctx context.Context, db *sql.DB) (map[string]map[string]string, error) {
	reference, err := Open(ctx, SQLite, ":memory:")
	if err != nil {
		return nil, err
	}
	defer reference.Close()
	result := map[string]map[string]string{}
	for _, table := range exportTables {
		present, err := tableColumns(ctx, db, table)
		if err != nil {
			return nil, err
		}
		if len(present) == 0 {
			return nil, fmt.Errorf("table %v missing; not a database of the automation-engine", table)
		}
		current, err := tableColumns(ctx, reference.db, table)
		if err != nil {
			return nil, err
		}
		for column, fallback := range current {
			if _, ok := present[column]; ok {
				continue
			}
			if result[table] == nil {
				result[table] = map[string]string{}
			}
			result[table][column] = fallback
		}
	}
	return result, nil
}

// tableColumns returns the columns of a table with their default as SQL, NULL without one
func tableColumns(ctx context.Context, db *sql.DB, table string) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT name, COALESCE(dflt_value, 'NULL') FROM pragma_table_info(?)", table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var name, fallback string
		if err = rows.Scan(&name, &fallback); err != nil {
			return nil, err
		}
		result[name] = fallback
	}
	return result, rows.Err()
}

// columns adapts a column list to a database that lacks some of them
func (this *Tx) columns(table string, list string) string {
	missing := this.missing[table]
	if len(missing) == 0 {
		return list
	}
	columns := strings.Split(list, ", ")
	for i, column := range columns {
		if fallback, ok := missing[column]; ok {
			columns[i] = fallback + " AS " + column
		}
	}
	return strings.Join(columns, ", ")
}

// SchemaMigration is the highest migration applied to the database.
func (this *Tx) SchemaMigration() (result int, err error) {
	err = this.queryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&result)
	return result, err
}

// GatewayExport reads namespaces, deployments with all versions, event descriptions and device
// groups, and the applied desired state, as stored (SNRGY-5066). The header of the document is
// left to the caller.
func (this *Tx) GatewayExport() (result model.GatewayExport, err error) {
	if result.SchemaMigration, err = this.SchemaMigration(); err != nil {
		return result, err
	}
	if result.SchemaMigration < MinGatewayExportMigration {
		return result, fmt.Errorf("database schema %v is older than that of process module v2.0.0 (%v)", result.SchemaMigration, MinGatewayExportMigration)
	}
	if result.Namespaces, err = this.exportNamespaces(); err != nil {
		return result, err
	}
	deployments, err := this.exportDeployments()
	if err != nil {
		return result, err
	}
	result.Deployments = []model.ExportedDeployment{}
	for _, d := range deployments {
		entry := model.ExportedDeployment{Deployment: d}
		if entry.Versions, err = this.exportDeploymentVersions(d.Id); err != nil {
			return result, err
		}
		if entry.EventDescriptions, err = this.exportEventDescriptions(d.Id); err != nil {
			return result, err
		}
		if entry.DeviceGroups, err = this.DeviceGroupsOfDeployment(d.Id); err != nil {
			return result, err
		}
		if entry.DeviceGroups == nil {
			entry.DeviceGroups = []string{}
		}
		result.Deployments = append(result.Deployments, entry)
	}
	applied, err := this.ListApplied()
	if err != nil {
		return result, err
	}
	result.Applied = []model.ExportedApplied{}
	for _, a := range applied {
		result.Applied = append(result.Applied, model.ExportedApplied{Kind: a.Kind, Id: a.Id, Version: a.Version, Error: a.Error, AppliedAt: a.At})
	}
	return result, nil
}

func (this *Tx) exportNamespaces() ([]model.Namespace, error) {
	list, err := this.ListNamespaces()
	if list == nil {
		list = []model.Namespace{}
	}
	return list, err
}

func (this *Tx) exportDeployments() (result []model.Deployment, err error) {
	rows, err := this.query("SELECT " + this.columns("deployments", deploymentColumns) + " FROM deployments ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

func (this *Tx) exportDeploymentVersions(deploymentId string) (result []model.DeploymentVersion, err error) {
	rows, err := this.query("SELECT "+this.columns("deployment_versions", deploymentVersionColumns)+" FROM deployment_versions WHERE deployment_id = ? ORDER BY version", deploymentId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result = []model.DeploymentVersion{}
	for rows.Next() {
		v, err := scanDeploymentVersion(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

func (this *Tx) exportEventDescriptions(deploymentId string) (result []model.EventDescription, err error) {
	rows, err := this.query("SELECT "+this.columns("event_descriptions", eventColumns)+" FROM event_descriptions WHERE deployment_id = ? ORDER BY element_id, id", deploymentId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result = []model.EventDescription{}
	for rows.Next() {
		d, err := scanEventDescription(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}
