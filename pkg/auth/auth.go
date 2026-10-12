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

// Package auth provides tokens for calls to other services (SEC-03, CLI-10).
package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"time"

	permclient "github.com/SENERGY-Platform/permissions-v2/pkg/client"
	"github.com/SENERGY-Platform/service-commons/pkg/jwt"
)

// TokenSource returns a token including the "Bearer " prefix.
type TokenSource func() (string, error)

// UserTokenSource returns a token of a user including the "Bearer " prefix.
type UserTokenSource func(userId string) (string, error)

// ClientCredentials gets and refreshes a token of a Keycloak client; endpoint is the Keycloak
// base URL, e.g. http://keycloak:8080.
func ClientCredentials(endpoint string, clientId string, clientSecret string) TokenSource {
	return permclient.NewTokenProvider(endpoint, clientId, clientSecret)
}

// TokenExchange gets tokens of other users through Keycloak token exchange, so that work on
// behalf of an owner runs with the owner's rights (SEC-06). Tokens are cached until shortly
// before they expire.
type TokenExchange struct {
	endpoint     string
	clientId     string
	clientSecret string
	mux          sync.Mutex
	cache        map[string]cachedToken
}

type cachedToken struct {
	token   string
	expires time.Time
}

func NewTokenExchange(endpoint string, clientId string, clientSecret string) *TokenExchange {
	return &TokenExchange{endpoint: endpoint, clientId: clientId, clientSecret: clientSecret, cache: map[string]cachedToken{}}
}

func (this *TokenExchange) Token(userId string) (string, error) {
	this.mux.Lock()
	cached, ok := this.cache[userId]
	this.mux.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cached.token, nil
	}
	token, expiration, err := jwt.ExchangeUserToken(this.endpoint, this.clientId, this.clientSecret, userId)
	if err != nil {
		return "", err
	}
	result := "Bearer " + token.Jwt()
	this.mux.Lock()
	this.cache[userId] = cachedToken{token: result, expires: time.Now().Add(expiration)}
	this.mux.Unlock()
	return result, nil
}

// UnsignedToken builds a token without signature. It is accepted only by services that do not
// verify signatures: the in-process test client, and permissions-v2 when a user's resources
// are listed for the deletion of that user, as device-repository does.
func UnsignedToken(userId string, roles ...string) string {
	encode := func(v any) string {
		raw, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	if roles == nil {
		roles = []string{"user"}
	}
	header := encode(map[string]string{"alg": "none", "typ": "JWT"})
	payload := encode(map[string]any{"sub": userId, "preferred_username": userId, "realm_access": map[string]any{"roles": roles}, "exp": time.Now().Add(time.Hour).Unix()})
	return "Bearer " + header + "." + payload + ".none"
}

// UnsignedTokens returns unsigned tokens of users with the role "user"; for tests.
func UnsignedTokens(userId string) (string, error) {
	return UnsignedToken(userId), nil
}

// Static returns a fixed token; for tests.
func Static(token string) TokenSource {
	return func() (string, error) { return token, nil }
}

// Expiry reads the exp claim of a token without verifying it; false if there is none.
func Expiry(token string) (time.Time, bool) {
	parts := strings.Split(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(token, "Bearer "), "bearer ")), ".")
	if len(parts) < 2 {
		return time.Time{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if json.Unmarshal(raw, &claims) != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(claims.Exp), 0), true
}
