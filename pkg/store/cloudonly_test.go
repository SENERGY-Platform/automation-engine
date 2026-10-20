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

package store_test

import (
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

// A database of an engine before cloud_only holds smart-services without the setting, and the
// seed does not overwrite it; migration 12 marks it once, an admin's later choice stays (NS-10).
func TestTheStoredSmartServicesNamespaceOfAnOlderDatabaseRunsOnlyInTheCloud(t *testing.T) {
	backends := []struct {
		name string
		open func(t *testing.T) (store.Dialect, string)
	}{
		{"sqlite", func(t *testing.T) (store.Dialect, string) { return store.SQLite, storetest.MemoryDsn() }},
		{"postgres", func(t *testing.T) (store.Dialect, string) {
			_, dsn := storetest.OpenPostgresDsn(t)
			return store.Postgres, dsn
		}},
	}
	for _, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			dialect, dsn := backend.open(t)
			open := func() *store.Store {
				s, err := store.Open(ctx, dialect, dsn)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { s.Close() })
				return s
			}
			// the first connection keeps the in-memory SQLite database alive
			older := open()
			tx(t, older, func(tx *store.Tx) error {
				for _, name := range []string{"processes", "smart-services"} {
					if _, err := tx.InsertNamespace(model.Namespace{Name: name, Description: name}, t0); err != nil {
						return err
					}
				}
				return store.ExecForTest(tx, "DELETE FROM schema_migrations WHERE version >= 12")
			})
			cloudOnly := func(s *store.Store) map[string]bool {
				result := map[string]bool{}
				tx(t, s, func(tx *store.Tx) error {
					list, err := tx.ListNamespaces()
					for _, ns := range list {
						result[ns.Name] = ns.CloudOnly
						if ns.Description != ns.Name {
							t.Errorf("the rest of the namespace stays: %+v", ns)
						}
					}
					return err
				})
				return result
			}
			migrated := open()
			if got := cloudOnly(migrated); !got["smart-services"] || got["processes"] {
				t.Fatalf("only smart-services runs only in the cloud: %v", got)
			}
			tx(t, migrated, func(tx *store.Tx) error {
				return tx.UpdateNamespace(model.Namespace{Name: "smart-services", Description: "smart-services"}, t0)
			})
			if got := cloudOnly(open()); got["smart-services"] {
				t.Errorf("an admin may allow gateways afterwards: %v", got)
			}
		})
	}
}
