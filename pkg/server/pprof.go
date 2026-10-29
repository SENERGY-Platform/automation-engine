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

package server

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"strconv"
	"time"
)

// pprofServer serves net/http/pprof on its own mux; the import also registers the handlers at
// http.DefaultServeMux, which the engine does not serve. It listens on 127.0.0.1, so other pods
// cannot reach it, while kubectl port-forward connects inside the network namespace of the pod.
func pprofServer(port int) *http.Server {
	mux := http.NewServeMux()
	// Index also serves the named profiles: heap, goroutine, allocs, block, mutex, threadcreate
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	// no WriteTimeout: CPU profiles and traces write after the requested seconds
	return &http.Server{Addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), Handler: mux, ReadHeaderTimeout: 10 * time.Second}
}

// servePprof serves the profiles until ctx is done; port 0 disables it.
func servePprof(ctx context.Context, port int, logger *slog.Logger) {
	if port <= 0 {
		return
	}
	srv := pprofServer(port)
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	logger.Info("pprof listening", "address", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		// profiling is a diagnostic aid, the engine keeps running without it
		logger.Warn("pprof server stopped", "error", err)
	}
}
