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

package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
)

// A token from the exchange goes into the Authorization header as it is; services reject a
// header with the prefix twice ("tokenstring should not contain 'bearer '").
func TestTokenExchangeReturnsOneBearerPrefix(t *testing.T) {
	keycloak := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		token := strings.TrimPrefix(auth.UnsignedToken(r.Form.Get("requested_subject")), "Bearer ")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": token, "expires_in": 300})
	}))
	defer keycloak.Close()
	token, err := auth.NewTokenExchange(keycloak.URL, "engine", "secret").Token("alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "Bearer ") || strings.HasPrefix(strings.TrimPrefix(token, "Bearer "), "Bearer ") {
		t.Fatalf("one prefix: %.30v", token)
	}
}
