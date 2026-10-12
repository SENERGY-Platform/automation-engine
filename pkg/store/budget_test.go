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
	"strconv"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

// A write that finds no space is rolled back and marks the store full, which the readiness
// check reports; the next successful write clears it (LONG-07). The page limit of SQLite
// stands in for a full disk.
func TestFullStorageIsReportedAndClearedByTheNextWrite(t *testing.T) {
	s := storetest.OpenSQLite(t)
	var pages int64
	tx(t, s, func(tx *store.Tx) error {
		used, err := tx.UsedBytes()
		pages = used/4096 + 20
		return err
	})
	tx(t, s, func(tx *store.Tx) error {
		return store.ExecForTest(tx, "PRAGMA max_page_count = "+strconv.FormatInt(pages, 10))
	})
	big := strings.Repeat("x", 64<<10)
	var err error
	for i := 0; i < 100 && err == nil; i++ {
		err = s.Tx(ctx, func(tx *store.Tx) error {
			_, err := tx.InsertNamespace(model.Namespace{Name: "ns" + strconv.Itoa(i), Description: big}, t0)
			return err
		})
	}
	if !store.IsDiskFull(err) || !s.Full() {
		t.Fatalf("a write beyond the limit fails as full: %v %v", err, s.Full())
	}
	if err = s.Ping(ctx); err == nil || !strings.Contains(err.Error(), "full") {
		t.Errorf("the readiness check reports the full storage: %v", err)
	}
	tx(t, s, func(tx *store.Tx) error { return store.ExecForTest(tx, "PRAGMA max_page_count = 1000000") })
	if s.Full() || s.Ping(ctx) != nil {
		t.Errorf("a successful write clears the state")
	}
}
