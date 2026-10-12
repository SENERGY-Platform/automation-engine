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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

const maxBodyBytes = 32 << 20

// Pinger reports whether the service is ready, e.g. whether the database is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// New builds the HTTP handler of the API; routes use the pattern routing of net/http.
//
//	@title			Automation-Engine API
//	@version		0.1
//	@description	Models, deployments and instances of processes and smart services.
//	@license.name	Apache 2.0
//	@license.url	http://www.apache.org/licenses/LICENSE-2.0.html
//	@BasePath		/
//	@securityDefinitions.apikey	Bearer
//	@in							header
//	@name						Authorization
//	@description				Type "Bearer" followed by a space and JWT token.
func New(controller Controller, pinger Pinger, logger *slog.Logger, settings Settings) http.Handler {
	h := &handlers{c: controller, pinger: pinger, settings: settings, logger: logger}
	mux := http.NewServeMux()
	for _, route := range h.routes() {
		mux.HandleFunc(route.pattern, route.handler)
	}
	return recovery(accessLog(mux, logger), logger)
}

// Settings of the API that depend on the deployment.
type Settings struct {
	// the engine verifies token signatures itself; then a live stream may take its token as
	// first message, which does not pass Kong (DBG-08)
	VerifiesTokens bool
}

type handlers struct {
	c        Controller
	pinger   Pinger
	settings Settings
	logger   *slog.Logger
}

type route struct {
	pattern string
	handler http.HandlerFunc
}

func (h *handlers) routes() []route {
	return []route{
		{"GET /health", h.health},
		{"GET /ready", h.ready},

		{"GET /namespaces", h.listNamespaces},
		{"GET /namespaces/{ns}", h.getNamespace},
		{"PUT /namespaces/{ns}", h.putNamespace},
		{"DELETE /namespaces/{ns}", h.deleteNamespace},
		{"POST /namespaces/{ns}/validate", h.validate},

		{"GET /namespaces/{ns}/models", h.listModels},
		{"POST /namespaces/{ns}/models", h.createModel},
		{"GET /namespaces/{ns}/models/{id}", h.getModel},
		{"PUT /namespaces/{ns}/models/{id}", h.updateModel},
		{"DELETE /namespaces/{ns}/models/{id}", h.deleteModel},
		{"POST /namespaces/{ns}/models/{id}/publish", h.publishModel},
		{"GET /namespaces/{ns}/models/{id}/versions", h.listModelVersions},
		{"GET /namespaces/{ns}/models/{id}/versions/{version}", h.getModelVersion},
		{"PUT /namespaces/{ns}/models/{id}/owner", h.setOwner(model.KindModel)},

		{"POST /namespaces/{ns}/prepared-deployments", h.prepareDeployment},
		{"GET /namespaces/{ns}/deployments", h.listDeployments},
		{"POST /namespaces/{ns}/deployments", h.createDeployment},
		{"GET /namespaces/{ns}/deployments/{id}", h.getDeployment},
		{"PUT /namespaces/{ns}/deployments/{id}", h.putDeployment},
		{"DELETE /namespaces/{ns}/deployments/{id}", h.deleteDeployment},
		{"GET /namespaces/{ns}/deployments/{id}/versions", h.listDeploymentVersions},
		{"GET /namespaces/{ns}/deployments/{id}/versions/{version}", h.getDeploymentVersion},
		{"GET /namespaces/{ns}/deployments/{id}/start-parameters", h.getStartParameters},
		{"GET /namespaces/{ns}/deployments/{id}/parameters", h.getDeploymentParameters},
		{"POST /namespaces/{ns}/deployments/{id}/reset-restarts", h.resetRestarts},
		{"GET /namespaces/{ns}/deployments/{id}/event-descriptions", h.listEventDescriptions},
		{"PUT /namespaces/{ns}/deployments/{id}/owner", h.setOwner(model.KindDeployment)},

		{"GET /namespaces/{ns}/instances", h.listInstances},
		{"POST /namespaces/{ns}/instances", h.startInstance},
		{"DELETE /namespaces/{ns}/instances", h.deleteInstances},
		{"POST /namespaces/{ns}/instances/stop", h.stopInstances},
		{"GET /namespaces/{ns}/instances/{id}", h.getInstance},
		{"PUT /namespaces/{ns}/instances/{id}", h.putInstance},
		{"DELETE /namespaces/{ns}/instances/{id}", h.deleteInstance},
		{"POST /namespaces/{ns}/instances/{id}/stop", h.stopInstance},
		{"POST /namespaces/{ns}/instances/{id}/restart", h.restartInstance},
		{"GET /namespaces/{ns}/instances/{id}/runs", h.listRuns},
		{"GET /namespaces/{ns}/instances/{id}/variables", h.getVariables},
		{"PUT /namespaces/{ns}/instances/{id}/variables/{name}", h.setVariable},
		{"GET /namespaces/{ns}/instances/{id}/activities", h.listActivities},
		{"GET /namespaces/{ns}/instances/{id}/variables/{name}", h.getVariable},
		{"PUT /namespaces/{ns}/instances/{id}/parameters", h.updateParameters},
		{"DELETE /namespaces/{ns}/instances/{id}/variables/{name}", h.deleteVariable},
		{"GET /namespaces/{ns}/instances/{id}/maintenance-procedures", h.listMaintenanceProcedures},
		{"GET /namespaces/{ns}/instances/{id}/maintenance-procedures/{procedure}/parameters", h.getMaintenanceParameters},
		{"POST /namespaces/{ns}/instances/{id}/maintenance-procedures/{procedure}/start", h.startMaintenance},
		{"GET /namespaces/{ns}/instances/{id}/debug", h.getDebugState},
		{"GET /namespaces/{ns}/instances/{id}/live", h.watchInstance},
		{"GET /namespaces/{ns}/instances/{id}/history", h.getHistoryState},
		{"GET /namespaces/{ns}/instances/{id}/history/variables/{name}", h.getHistoryVariable},
		{"GET /namespaces/{ns}/instances/{id}/event-evaluations", h.getEventEvaluations},
		{"GET /namespaces/{ns}/instances/{id}/element-stats", h.listElementStats},
		{"PUT /namespaces/{ns}/instances/{id}/owner", h.setOwner(model.KindInstance)},

		{"GET /namespaces/{ns}/incidents", h.listIncidents},

		{"GET /namespaces/{ns}/modules", h.listModules},
		{"GET /namespaces/{ns}/modules/{id}", h.getModule},
		{"DELETE /namespaces/{ns}/modules/{id}", h.deleteModule},

		{"POST /messages", h.correlate},

		{"GET /namespaces/{ns}/schedules", h.listSchedules},
		{"POST /namespaces/{ns}/schedules", h.createSchedule},
		{"GET /namespaces/{ns}/schedules/{id}", h.getSchedule},
		{"PUT /namespaces/{ns}/schedules/{id}", h.updateSchedule},
		{"DELETE /namespaces/{ns}/schedules/{id}", h.deleteSchedule},

		{"GET /io/values", h.listIoValues},
		{"GET /io/values/{key...}", h.getIoValue},
		{"PUT /io/values/{key...}", h.setIoValue},
		{"DELETE /io/values/{key...}", h.deleteIoValue},
		{"POST /io/bulk", h.ioBulk},

		{"GET /users/{id}/dependents", h.getUserDependents},
		{"DELETE /users/{id}", h.deleteUser},
		{"GET /users/{id}/settings", h.getUserSettings},
		{"PUT /users/{id}/settings", h.setUserSettings},
	}
}

func token(r *http.Request) string {
	return r.Header.Get("Authorization")
}

func writeJson(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, err error) {
	e := model.AsError(err)
	if errors.Is(err, context.DeadlineExceeded) {
		e = model.Unavailable("%v", err)
	}
	writeJson(w, e.Kind.HttpStatus(), e)
}

// respond writes the result with status, or the error
func respond[T any](w http.ResponseWriter, status int, result T, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	writeJson(w, status, result)
}

func respondEmpty(w http.ResponseWriter, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decode[T any](w http.ResponseWriter, r *http.Request) (result T, ok bool) {
	body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, model.BadRequest("invalid request body: %v", err))
		return result, false
	}
	return result, true
}

type query struct {
	values url.Values
	err    error
}

func newQuery(r *http.Request) *query {
	return &query{values: r.URL.Query()}
}

func (this *query) str(name string) string {
	if v := this.values[name]; len(v) > 0 {
		return v[0]
	}
	return ""
}

func (this *query) int(name string) int {
	s := this.str(name)
	if s == "" {
		return 0
	}
	v, err := strconv.Atoi(s)
	if err != nil && this.err == nil {
		this.err = model.BadRequest("query parameter %v must be an integer", name)
	}
	return v
}

func (this *query) int64(name string) int64 {
	s := this.str(name)
	if s == "" {
		return 0
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil && this.err == nil {
		this.err = model.BadRequest("query parameter %v must be an integer", name)
	}
	return v
}

// list reads a comma separated or repeated parameter
func (this *query) list(name string) []string {
	var result []string
	for _, v := range this.values[name] {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				result = append(result, part)
			}
		}
	}
	return result
}

func (this *query) page() model.PageOptions {
	return model.PageOptions{Limit: this.int("limit"), Offset: this.int("offset")}
}

func pathInt(r *http.Request, name string) (int, error) {
	v, err := strconv.Atoi(r.PathValue(name))
	if err != nil {
		return 0, model.BadRequest("path parameter %v must be an integer", name)
	}
	return v, nil
}

func accessLog(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		// the query may contain ids but never the token, which travels in the header
		logger.InfoContext(r.Context(), "request", "method", r.Method, "path", r.URL.Path, "status", recorder.status, "duration", time.Since(start).String())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (this *statusRecorder) WriteHeader(status int) {
	this.status = status
	this.ResponseWriter.WriteHeader(status)
}

// Unwrap gives access to the underlying writer, e.g. to hijack the connection of a WebSocket
func (this *statusRecorder) Unwrap() http.ResponseWriter {
	return this.ResponseWriter
}

func recovery(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				logger.ErrorContext(r.Context(), "panic in request handler", "method", r.Method, "path", r.URL.Path, "panic", fmt.Sprint(p))
				writeError(w, errors.New("internal error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// health godoc
//
//	@Summary	liveness probe
//	@Tags		operation
//	@Success	200
//	@Router		/health [get]
func (h *handlers) health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// ready godoc
//
//	@Summary	readiness probe, checks the database
//	@Tags		operation
//	@Success	200
//	@Failure	503	{object}	model.Error
//	@Router		/ready [get]
func (h *handlers) ready(w http.ResponseWriter, r *http.Request) {
	if h.pinger != nil {
		if err := h.pinger.Ping(r.Context()); err != nil {
			writeError(w, model.Unavailable("%v", err))
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}
