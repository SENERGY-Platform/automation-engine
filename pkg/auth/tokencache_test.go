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
	"sync"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
)

// A token of a user is exchanged once and then served from the cache while it is valid; another
// user gets a token of their own.
func TestTokenExchangeAsksKeycloakOncePerUserWhileTheTokenIsValid(t *testing.T) {
	var mux sync.Mutex
	asked := map[string]int{}
	keycloak := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		user := r.Form.Get("requested_subject")
		mux.Lock()
		asked[user]++
		mux.Unlock()
		token := strings.TrimPrefix(auth.UnsignedToken(user), "Bearer ")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": token, "expires_in": 3600})
	}))
	defer keycloak.Close()
	exchange := auth.NewTokenExchange(keycloak.URL, "engine", "secret")
	first, err := exchange.Token("alice")
	if err != nil {
		t.Fatal(err)
	}
	second, err := exchange.Token("alice")
	if err != nil {
		t.Fatal(err)
	}
	other, err := exchange.Token("bob")
	if err != nil {
		t.Fatal(err)
	}
	mux.Lock()
	defer mux.Unlock()
	if first == "" || second != first || other == first || asked["alice"] != 1 || asked["bob"] != 1 {
		t.Fatalf("keycloak asked %v; alice got the same token twice: %v, bob another: %v", asked, second == first, other != first)
	}
}
