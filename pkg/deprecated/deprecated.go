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

// Package deprecated serves the HTTP APIs of the services the engine replaces, under
// /deprecated/ (API-02). Kong rewrites the old paths onto it (API-08), e.g.
// /process/engine/v2/deployments/{id}/start onto /deprecated/engine/v2/deployments/{id}/start.
//
// The layer only translates onto the API of the engine, with the token of the caller; it has
// no logic and no storage of its own, so that it can be removed without side effects (API-03).
// Old identifiers map onto new ones without a table: a deployment keeps its id, a process
// definition id is deplid_<id>:<version>:<id>, a business key is the instance id. Only routes
// with known callers are served (decided for M7); status codes follow the new API.
package deprecated

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/api"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// Prefix of all routes of the layer.
const Prefix = "/deprecated"

const processes = "processes"

// Counter counts requests per route (API-03).
type Counter interface {
	DeprecatedRequest(route string)
}

type Handler struct {
	c       api.Controller
	counter Counter
	mux     *http.ServeMux
}

func New(c api.Controller, counter Counter) *Handler {
	this := &Handler{c: c, counter: counter, mux: http.NewServeMux()}
	this.engine()
	this.deployments()
	this.repository()
	this.io()
	this.incidents()
	this.scheduler()
	return this
}

func (this *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	this.mux.ServeHTTP(w, r)
}

// handle registers a route below the prefix; every call is counted and marked as deprecated
func (this *Handler) handle(pattern string, handler http.HandlerFunc) {
	method, path, _ := strings.Cut(pattern, " ")
	route := method + " " + Prefix + path
	this.mux.HandleFunc(route, func(w http.ResponseWriter, r *http.Request) {
		if this.counter != nil {
			this.counter.DeprecatedRequest(route)
		}
		w.Header().Set("Deprecation", "true")
		handler(w, r)
	})
}

func token(r *http.Request) string {
	return r.Header.Get("Authorization")
}

func writeJson(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// writeError answers with the status of the new API and a text body, as the old services did
func writeError(w http.ResponseWriter, err error) {
	e := model.AsError(err)
	if errors.Is(err, context.DeadlineExceeded) {
		e = model.Unavailable("%v", err)
	}
	http.Error(w, e.Error(), e.Kind.HttpStatus())
}

func respond(w http.ResponseWriter, value any, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	writeJson(w, http.StatusOK, value)
}

func decode[T any](r *http.Request) (result T, err error) {
	if err = json.NewDecoder(http.MaxBytesReader(nil, r.Body, 16<<20)).Decode(&result); err != nil {
		return result, model.BadRequest("invalid body: %v", err)
	}
	return result, nil
}

// intParam reads an integer query parameter; missing is the fallback
func intParam(r *http.Request, name string, fallback int) (int, error) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0, model.BadRequest("%v must be a non-negative integer", name)
	}
	return n, nil
}

func boolParam(r *http.Request, name string) bool {
	v, _ := strconv.ParseBool(r.URL.Query().Get(name))
	return v
}

// pageSize is the largest page the layer reads at once when an old route answered with all items
const pageSize = 1000

// all reads every page of a list
func all[T any](list func(page model.PageOptions) (model.Page[T], error)) ([]T, error) {
	result := []T{}
	for offset := 0; ; offset += pageSize {
		page, err := list(model.PageOptions{Limit: pageSize, Offset: offset})
		if err != nil {
			return nil, err
		}
		result = append(result, page.Items...)
		if len(page.Items) < pageSize || offset+pageSize >= page.Total {
			return result, nil
		}
	}
}

// paged reads a window of a list that may be larger than a page of the new API; limit 0 is all
func paged[T any](limit int, offset int, list func(page model.PageOptions) (model.Page[T], error)) (items []T, total int, err error) {
	if limit == 0 {
		items, err = all(func(page model.PageOptions) (model.Page[T], error) {
			page.Offset += offset
			result, err := list(page)
			total = result.Total
			return result, err
		})
		return items, total, err
	}
	items = []T{}
	for len(items) < limit {
		page, err := list(model.PageOptions{Limit: min(limit-len(items), pageSize), Offset: offset + len(items)})
		if err != nil {
			return nil, 0, err
		}
		total = page.Total
		items = append(items, page.Items...)
		if len(page.Items) == 0 || offset+len(items) >= page.Total {
			break
		}
	}
	return items, total, nil
}

// sort translates <field>.<asc|desc> of an old API onto the fields of the new one; unknown fields
// keep the default order, as the old services silently did
func sort(value string, fields map[string]string) string {
	field, direction, _ := strings.Cut(value, ".")
	mapped := fields[field]
	if mapped == "" {
		return ""
	}
	if direction == "desc" {
		return mapped + ".desc"
	}
	return mapped + ".asc"
}

// definitionKey is the key Camunda had for a deployment: deplid_ and the id with - : # as _
func definitionKey(deploymentId string) string {
	return "deplid_" + strings.NewReplacer("-", "_", ":", "_", "#", "_").Replace(deploymentId)
}

// definitionId is a process definition id in Camunda's form key:version:id; the last part is
// the deployment id, so that it maps back without a table
func definitionId(deploymentId string, version int) string {
	return definitionKey(deploymentId) + ":" + strconv.Itoa(version) + ":" + deploymentId
}

var underscoredUuid = regexp.MustCompile(`^[0-9a-f]{8}_[0-9a-f]{4}_[0-9a-f]{4}_[0-9a-f]{4}_[0-9a-f]{12}$`)

// deploymentOf reads the deployment id from a process definition id. Ids of Camunda carry the
// key deplid_<deployment id with _>; the engine's carry the deployment id as last part. A
// plain deployment id stays as it is.
func deploymentOf(definition string) string {
	parts := strings.Split(definition, ":")
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "deplid_") {
		return definition
	}
	key := strings.TrimPrefix(parts[0], "deplid_")
	if underscoredUuid.MatchString(key) {
		return strings.ReplaceAll(key, "_", "-")
	}
	return parts[2]
}

// camundaTime is the format of times in the REST API of Camunda
const camundaTime = "2006-01-02T15:04:05.000-0700"

func formatTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(camundaTime)
}

// startVariables reads the query of a start: every parameter but business_key is a variable,
// its value JSON if it parses, text otherwise, as the engine wrapper did
func startVariables(r *http.Request) map[string]any {
	result := map[string]any{}
	for name, values := range r.URL.Query() {
		if name == "business_key" || len(values) == 0 {
			continue
		}
		var value any
		if err := json.Unmarshal([]byte(values[0]), &value); err != nil {
			value = values[0]
		}
		result[name] = value
	}
	return result
}

// splitList reads a comma separated list; spaces and empty entries are dropped
func splitList(value string) []string {
	result := []string{}
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
