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

package client

import (
	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
)

// NewTokenProvider returns tokens of a Keycloak client through client credentials, refreshed
// before they expire (CLI-10). endpoint is the Keycloak base URL, e.g. http://keycloak:8080.
func NewTokenProvider(endpoint string, clientId string, clientSecret string) func() (string, error) {
	return auth.ClientCredentials(endpoint, clientId, clientSecret)
}

// NewTokenExchange returns tokens of other users through Keycloak token exchange, for services
// that act on behalf of a user (CLI-10).
func NewTokenExchange(endpoint string, clientId string, clientSecret string) *auth.TokenExchange {
	return auth.NewTokenExchange(endpoint, clientId, clientSecret)
}
