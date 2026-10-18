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

// Command webui serves a minimal web interface for trying the engine by hand in the local
// Docker Compose setup. It serves the pages and passes /api/ to the engine and /auth/ to
// devauth, so that the browser talks to one origin and the engine needs no CORS.
//
// A browser cannot set headers on a WebSocket, so a request to /api/ with the query parameter
// access_token gets it as its Authorization header instead. That puts tokens into URLs, which
// is acceptable for a local test tool and for nothing else.
//
// Environment: PORT (default 8080), ENGINE_URL (default http://engine:8080), DEVAUTH_URL
// (default http://devauth:8080).
package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
)

//go:embed static
var static embed.FS

func env(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// proxy passes requests below prefix to target, without the prefix
func proxy(prefix string, target string) http.Handler {
	base, err := url.Parse(target)
	if err != nil {
		log.Fatalf("%v: %v", target, err)
	}
	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(base)
			r.Out.URL.Path = strings.TrimSuffix(base.Path, "/") + strings.TrimPrefix(r.In.URL.Path, prefix)
			r.Out.URL.RawPath = ""
			query := r.Out.URL.Query()
			if token := query.Get("access_token"); token != "" && r.Out.Header.Get("Authorization") == "" {
				r.Out.Header.Set("Authorization", token)
				query.Del("access_token")
				r.Out.URL.RawQuery = query.Encode()
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "webui: "+target+" is not reachable: "+err.Error(), http.StatusBadGateway)
		},
	}
}

func main() {
	pages, err := fs.Sub(static, "static")
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", proxy("/api", env("ENGINE_URL", "http://engine:8080")))
	mux.Handle("/auth/", proxy("/auth", env("DEVAUTH_URL", "http://devauth:8080")))
	mux.Handle("/", http.FileServerFS(pages))
	address := ":" + env("PORT", "8080")
	log.Println("webui listening on", address)
	log.Fatal(http.ListenAndServe(address, mux))
}
