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
	"slices"
	"strings"
)

// Cors are the CORS headers for browser applications that call the engine directly. In the
// platform Kong sets them, so without origins the API sets none.
type Cors struct {
	// allowed origins, e.g. https://ui.example.org; "*" allows every origin
	Origins []string
	// request headers a browser may send; "*" does not cover Authorization
	Headers []string
}

// corsMethods are the methods the routes of the API use
const corsMethods = "GET, HEAD, POST, PUT, PATCH, DELETE"

// cors answers preflight requests of allowed origins itself and adds the CORS headers to their
// other requests. Tokens travel in the Authorization header, not in cookies, so credentials are
// not allowed. WebSockets are not subject to CORS; the live endpoint accepts every origin.
func cors(next http.Handler, settings Cors) http.Handler {
	if len(settings.Origins) == 0 {
		return next
	}
	anyOrigin := slices.Contains(settings.Origins, "*")
	headers := strings.Join(settings.Headers, ", ")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		if !anyOrigin {
			// caches must not hand the answer for one origin to another
			h.Add("Vary", "Origin")
			if !slices.Contains(settings.Origins, origin) {
				next.ServeHTTP(w, r)
				return
			}
			h.Set("Access-Control-Allow-Origin", origin)
		} else {
			h.Set("Access-Control-Allow-Origin", "*")
		}
		h.Set("Access-Control-Expose-Headers", "X-Total-Count")
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Allow-Methods", corsMethods)
			if headers != "" {
				h.Set("Access-Control-Allow-Headers", headers)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
