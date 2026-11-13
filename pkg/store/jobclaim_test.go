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
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

// SNRGY-5139: a job claim skips an instance another transaction holds instead of waiting for it,
// e.g. the claim of another replica for the same job; Postgres only, SQLite transactions are
// exclusive
func TestAJobClaimSkipsAnInstanceAnotherTransactionHolds(t *testing.T) {
	s := storetest.OpenPostgres(t)
	ctx := context.Background()
	tx(t, s, func(tx *store.Tx) error { return seedInstances(tx, 1) })
	tx(t, s, func(tx *store.Tx) error {
		if inst, err := tx.GetInstanceUnlessLocked("i-0"); err != nil || inst.Id != "i-0" {
			t.Errorf("a free instance is read: %+v %v", inst.Instance, err)
		}
		return nil
	})

	held, release := make(chan struct{}), make(chan struct{})
	holder := make(chan error, 1)
	go func() {
		holder <- s.Tx(ctx, func(tx *store.Tx) error {
			if _, err := tx.GetInstance("i-0", true); err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	start := time.Now()
	tx(t, s, func(tx *store.Tx) error {
		if _, err := tx.GetInstanceUnlessLocked("i-0"); !errors.Is(err, model.ErrNotFound) {
			t.Errorf("an instance held by another transaction reads as not found: %v", err)
		}
		return nil
	})
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("the claim waited %v for the lock", elapsed)
	}
	close(release)
	if err := <-holder; err != nil {
		t.Fatal(err)
	}
}
