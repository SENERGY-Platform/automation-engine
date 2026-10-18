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

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// teapot stands for the routes; a preflight that reaches it was not answered by cors
var teapot = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })

func corsRequest(t *testing.T, handler http.Handler, method string, origin string, preflight bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "/namespaces", nil)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if preflight {
		r.Header.Set("Access-Control-Request-Method", http.MethodPost)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestCorsWithoutOriginsSetsNoHeaders(t *testing.T) {
	w := corsRequest(t, cors(teapot, Cors{}), http.MethodOptions, "https://ui.example.org", true)
	if w.Code != http.StatusTeapot || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("got %v %v", w.Code, w.Header())
	}
}

func TestCorsAnswersPreflightOfAllowedOrigin(t *testing.T) {
	handler := cors(teapot, Cors{Origins: []string{"https://ui.example.org"}, Headers: []string{"Authorization", "Content-Type"}})
	w := corsRequest(t, handler, http.MethodOptions, "https://ui.example.org", true)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status %v", w.Code)
	}
	expected := map[string]string{
		"Access-Control-Allow-Origin":  "https://ui.example.org",
		"Access-Control-Allow-Methods": corsMethods,
		"Access-Control-Allow-Headers": "Authorization, Content-Type",
		"Vary":                         "Origin",
	}
	for name, value := range expected {
		if got := w.Header().Get(name); got != value {
			t.Errorf("%v: got %q, expected %q", name, got, value)
		}
	}
}

func TestCorsAddsHeadersToRequestsOfAllowedOrigin(t *testing.T) {
	w := corsRequest(t, cors(teapot, Cors{Origins: []string{"https://ui.example.org"}}), http.MethodGet, "https://ui.example.org", false)
	if w.Code != http.StatusTeapot || w.Header().Get("Access-Control-Allow-Origin") != "https://ui.example.org" || w.Header().Get("Access-Control-Expose-Headers") != "X-Total-Count" {
		t.Fatalf("got %v %v", w.Code, w.Header())
	}
}

func TestCorsLeavesOtherOriginsWithoutHeaders(t *testing.T) {
	handler := cors(teapot, Cors{Origins: []string{"https://ui.example.org"}})
	w := corsRequest(t, handler, http.MethodOptions, "https://other.example.org", true)
	if w.Code != http.StatusTeapot || w.Header().Get("Access-Control-Allow-Origin") != "" || w.Header().Get("Vary") != "Origin" {
		t.Fatalf("got %v %v", w.Code, w.Header())
	}
}

func TestCorsWildcardAllowsEveryOrigin(t *testing.T) {
	w := corsRequest(t, cors(teapot, Cors{Origins: []string{"*"}}), http.MethodGet, "https://any.example.org", false)
	if w.Header().Get("Access-Control-Allow-Origin") != "*" || w.Header().Get("Vary") != "" {
		t.Fatalf("got %v", w.Header())
	}
}

func TestCorsPassesRequestsWithoutOrigin(t *testing.T) {
	w := corsRequest(t, cors(teapot, Cors{Origins: []string{"*"}}), http.MethodGet, "", false)
	if w.Code != http.StatusTeapot || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("got %v %v", w.Code, w.Header())
	}
}
