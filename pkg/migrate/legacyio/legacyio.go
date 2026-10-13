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

// Package legacyio takes the values of process-io-worker on a gateway into the engine once,
// when the process module is updated and the old Postgres volume is still there (MIG-06).
package legacyio

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// marker is the sync position that records the import
const marker = "legacy_io_imported"

// Result counts what an import did.
type Result struct {
	Imported int
	Existing int
	// values of an instance: the instances of Camunda ended with the update
	Skipped int
}

var definitionKey = regexp.MustCompile(`deplid_([0-9a-f]{8}_[0-9a-f]{4}_[0-9a-f]{4}_[0-9a-f]{4}_[0-9a-f]{12})(?:_\d+)?(?::\d+:[0-9a-f-]+)?`)

// Import reads the table variables of process-io and writes the values that belong to no
// instance; a process definition id of Camunda in a key becomes the deployment id. owner
// replaces the user of every value if set, since the gateway's Camunda had no users of the
// platform. It runs once per database; later calls do nothing.
func Import(ctx context.Context, s *store.Store, dsn string, owner string) (result Result, done bool, err error) {
	var position int64
	if err = s.Tx(ctx, func(tx *store.Tx) (err error) { position, err = tx.SyncPosition(marker); return err }); err != nil || position > 0 {
		return result, false, err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return result, false, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SELECT user_id, variable_key, COALESCE(process_definition_id, ''), COALESCE(process_instance_id, ''), COALESCE(unix_timestamp_in_s, 0), variable_value FROM variables")
	if err != nil {
		return result, false, fmt.Errorf("legacy process-io: %w", err)
	}
	type row struct {
		user, key, definition, instance string
		timestamp                       int64
		raw                             []byte
	}
	var values []row
	for rows.Next() {
		var r row
		if err = rows.Scan(&r.user, &r.key, &r.definition, &r.instance, &r.timestamp, &r.raw); err != nil {
			rows.Close()
			return result, false, err
		}
		values = append(values, r)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return result, false, err
	}
	err = s.Tx(ctx, func(tx *store.Tx) error {
		for _, r := range values {
			if r.instance != "" {
				result.Skipped++
				continue
			}
			user := r.user
			if owner != "" {
				user = owner
			}
			key := r.key
			if r.definition != "" {
				if m := definitionKey.FindStringSubmatch(r.definition); m != nil {
					key = strings.ReplaceAll(key, r.definition, strings.ReplaceAll(m[1], "_", "-"))
				}
			}
			if _, exists, err := tx.GetIoValue(user, key); err != nil {
				return err
			} else if exists {
				result.Existing++
				continue
			}
			var value any
			if err := json.Unmarshal(r.raw, &value); err != nil {
				value = string(r.raw)
			}
			if err := tx.SetIoValue(user, model.IoValue{Key: key, Value: value, UpdatedAt: time.Unix(r.timestamp, 0).UTC()}); err != nil {
				return err
			}
			result.Imported++
		}
		return tx.SetSyncPosition(marker, time.Now().Unix())
	})
	return result, err == nil, err
}
