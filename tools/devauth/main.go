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

// Command devauth stands in for Keycloak in the local Docker Compose setup. It issues unsigned
// tokens, which the engine and the platform services accept as long as they do not verify
// signatures (no KEYCLOAK_CERT_URL; behind Kong, Kong verifies). Never run it anywhere else.
// It also takes the messages services send to developer-notifications and logs them.
//
//   - POST .../token with grant_type=client_credentials: a token of the client with the realm
//     role admin, for the internal calls of the engine
//   - POST .../token with requested_subject: a token of that user (token exchange)
//   - GET /token/{user}?roles=admin,user: "Bearer ..." for curl and API tools
//   - POST /messages: a developer notification, logged
package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
)

func main() {
	address := ":8080"
	if port := os.Getenv("PORT"); port != "" {
		address = ":" + port
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /token/{user}", func(w http.ResponseWriter, r *http.Request) {
		var roles []string
		if raw := r.URL.Query().Get("roles"); raw != "" {
			roles = strings.Split(raw, ",")
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(auth.UnsignedToken(r.PathValue("user"), roles...) + "\n"))
	})
	mux.HandleFunc("POST /messages", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		log.Println("developer notification:", string(body))
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/token") {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		client := r.Form.Get("client_id")
		if user, _, ok := r.BasicAuth(); ok && client == "" {
			client = user
		}
		if client == "" {
			client = "automation-engine"
		}
		token := auth.UnsignedToken(client, "admin")
		if user := r.Form.Get("requested_subject"); user != "" {
			token = auth.UnsignedToken(user)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": strings.TrimPrefix(token, "Bearer "), "token_type": "bearer", "expires_in": 3600,
		})
	})
	log.Println("devauth listening on", address)
	log.Fatal(http.ListenAndServe(address, mux))
}
