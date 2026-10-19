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

package prepare

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// importDeploy lists the import instances the user sees, as import-deploy answers /instances
func importDeploy(t *testing.T, ids ...string) string {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/instances" || r.Header.Get("Authorization") != "token" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		instances := []map[string]string{}
		for _, id := range ids {
			instances = append(instances, map[string]string{"id": id})
		}
		_ = json.NewEncoder(w).Encode(instances)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// A deployment may select only imports the user sees in import-deploy (process-deployment
// TestImportDeployments, "missing access" and "access ok").
func TestImportSelectionNeedsAnImportTheUserSees(t *testing.T) {
	selections := map[string]model.Selection{"event": {ImportId: "urn:infai:ses:import:123"}}
	t.Run("rejects an import the user does not see", func(t *testing.T) {
		err := NewAccess(nil, importDeploy(t, "urn:infai:ses:import:other")).Check(context.Background(), "token", selections)
		if !errors.Is(err, model.ErrForbidden) {
			t.Errorf("expected forbidden, got %v", err)
		}
	})
	t.Run("accepts an import the user sees", func(t *testing.T) {
		if err := NewAccess(nil, importDeploy(t, "urn:infai:ses:import:123")).Check(context.Background(), "token", selections); err != nil {
			t.Errorf("expected access, got %v", err)
		}
	})
}
