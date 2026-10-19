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

package modules

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/tracing"
)

const TypeWatcher = "watcher"

// Maintenance starts maintenance procedures without a caller (SMS-13).
type Maintenance interface {
	StartMaintenanceInternal(ctx context.Context, id string, procedure string, variables map[string]any) (model.Instance, error)
}

// GenericHttp sends requests from models under the HTTP policy of a namespace (NS-04).
type GenericHttp interface {
	Do(namespace string, req *http.Request) (*http.Response, error)
}

// WatcherSettings are the values of the former watcher worker (TASK-29).
type WatcherSettings struct {
	AllowGenericRequests bool
	MinInterval          time.Duration
	DefaultInterval      time.Duration
}

// AddWatcher registers the watcher task type (TASK-27): instead of an own database and API, the
// watches are rows of the engine, checked by every replica in turn.
func (this *Runtime) AddWatcher(maintenance Maintenance, generic GenericHttp, settings WatcherSettings) {
	w := &watcher{runtime: this, maintenance: maintenance, generic: generic, settings: settings, http: &http.Client{Timeout: 5 * time.Second}}
	this.watcher = w
	this.add(TypeWatcher, w)
}

type watcher struct {
	runtime     *Runtime
	maintenance Maintenance
	generic     GenericHttp
	settings    WatcherSettings
	http        *http.Client
}

const watchPrefix = TypeWatcher + "."

func (this *watcher) Do(ctx context.Context, task Task) (modules []model.Module, outputs map[string]any, err error) {
	var root store.Instance
	err = this.runtime.store.Tx(ctx, func(tx *store.Tx) error {
		root, err = tx.GetInstance(task.RootInstanceId, false)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	procedure, _ := input(task, watchPrefix+"maintenance_procedure")
	if procedure == "" {
		return nil, nil, fmt.Errorf("missing variable: %vmaintenance_procedure", watchPrefix)
	}
	parameters := map[string]any{}
	for name, value := range task.Inputs {
		if id, ok := strings.CutPrefix(name, watchPrefix+"maintenance_procedure_inputs."); ok {
			if s, ok := value.(string); ok {
				var parsed any
				if json.Unmarshal([]byte(s), &parsed) == nil {
					value = parsed
				}
			}
			parameters[id] = value
		}
	}
	interval := this.settings.DefaultInterval
	if raw, ok := input(task, watchPrefix+"watch_interval"); ok {
		if d, err := time.ParseDuration(raw); err == nil {
			interval = d
		}
	}
	interval = max(interval, this.settings.MinInterval)
	hashType, _ := input(task, watchPrefix+"hash_type")
	if hashType == "" {
		hashType = "md5"
	}
	request, err := this.request(task)
	if err != nil {
		return nil, nil, err
	}
	id := task.ModuleId()
	now := this.runtime.now()
	err = this.runtime.store.Tx(ctx, func(tx *store.Tx) error {
		return tx.SaveWatch(store.Watch{Id: id, InstanceId: root.Id, Namespace: root.Namespace, Owner: root.Owner, Procedure: procedure, Parameters: parameters,
			Request: request, HashType: hashType, Interval: interval, NextAt: now, CreatedAt: now})
	})
	if err != nil {
		return nil, nil, err
	}
	modules = []model.Module{{Id: id, ModuleType: TypeWatcher, Data: map[string]any{"watcher_id": id}, Keys: []string{},
		DeleteInfo: &model.ModuleDeleteInfo{UserId: root.Owner, Resource: &model.ResourceRef{Kind: model.ResourceWatch, Namespace: root.Namespace, Id: id}}}}
	return modules, map[string]any{"watcher_id": id}, nil
}

// request reads what to watch: devices matching criteria through device-selection, or a
// generic request if allowed; the first variable present wins, as with the worker
func (this *watcher) request(task Task) (model.WatchRequest, error) {
	selectables := func(raw string, query string) (model.WatchRequest, error) {
		// the body goes to device-selection unchanged; aspect_ids is checked like the worker did,
		// so that a wrong type fails the task and not every later check
		var criteria []struct {
			AspectIds []string `json:"aspect_ids"`
		}
		if err := json.Unmarshal([]byte(raw), &criteria); err != nil {
			return model.WatchRequest{}, fmt.Errorf("invalid criteria: %w", err)
		}
		return model.WatchRequest{Method: http.MethodPost, Endpoint: this.runtime.settings.DeviceSelectionUrl + "/v2/query/selectables?" + query, Body: []byte(raw), AddAuthToken: true}, nil
	}
	if raw, ok := input(task, watchPrefix+"watch_devices_by_criteria"); ok {
		return selectables(raw, "include_devices=true")
	}
	if raw, ok := input(task, watchPrefix+"watch_modified_devices_by_criteria"); ok {
		return selectables(raw, "include_devices=true&include_id_modified=true")
	}
	if raw, ok := input(task, watchPrefix+"watch_request"); ok && this.settings.AllowGenericRequests {
		var request struct {
			Method       string `json:"method"`
			Endpoint     string `json:"endpoint"`
			Body         []byte `json:"body"`
			AddAuthToken bool   `json:"add_auth_token"`
		}
		if err := json.Unmarshal([]byte(raw), &request); err != nil {
			return model.WatchRequest{}, fmt.Errorf("invalid watch request: %w", err)
		}
		return model.WatchRequest{Method: request.Method, Endpoint: request.Endpoint, Body: request.Body, AddAuthToken: request.AddAuthToken, Generic: true}, nil
	}
	return model.WatchRequest{}, fmt.Errorf("missing variable: no known watch request found")
}

// RunWatches checks due watches until ctx is done.
func (this *Runtime) RunWatches(ctx context.Context, tick time.Duration) {
	if this.watcher == nil {
		return
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		if err := this.CheckWatches(ctx); err != nil && ctx.Err() == nil {
			this.logger.WarnContext(ctx, "watches incomplete", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// CheckWatches checks the due watches once.
func (this *Runtime) CheckWatches(ctx context.Context) error {
	// claimed watches are due one interval after the start of the pass, not again within it
	start := this.now()
	for {
		var due []store.Watch
		err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
			due, err = tx.ClaimDueWatches(start, 20)
			return err
		})
		if err != nil || len(due) == 0 {
			return err
		}
		for _, w := range due {
			hash, message := this.watcher.check(ctx, w)
			if message == "" && hash != w.LastHash && w.LastHash != "" {
				// a change starts the procedure; the first check only sets the baseline
				if _, err := this.watcher.maintenance.StartMaintenanceInternal(ctx, w.InstanceId, w.Procedure, w.Parameters); err != nil {
					// the hash stays, the next check triggers again instead of losing the change
					hash, message = w.LastHash, "trigger: "+err.Error()
				}
			}
			if message != "" {
				hash = w.LastHash
			}
			err = this.store.Tx(ctx, func(tx *store.Tx) error {
				return tx.UpdateWatch(w.Id, hash, message)
			})
			if err != nil {
				return err
			}
		}
	}
}

// check requests the watched resource and hashes its answer; message reports a failure
func (this *watcher) check(ctx context.Context, w store.Watch) (hash string, message string) {
	req, err := http.NewRequestWithContext(ctx, w.Request.Method, w.Request.Endpoint, bytes.NewReader(w.Request.Body))
	if err != nil {
		return "", err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	if w.Request.AddAuthToken {
		token, err := this.runtime.token(w.Owner)
		if err != nil {
			return "", "token: " + err.Error()
		}
		req.Header.Set("Authorization", token)
	}
	var resp *http.Response
	if w.Request.Generic {
		if this.generic == nil {
			return "", "generic watch requests are not available"
		}
		resp, err = this.generic.Do(w.Namespace, req)
	} else {
		if err = tracing.Inject(ctx, req); err != nil {
			return "", err.Error()
		}
		resp, err = this.http.Do(req)
	}
	if err != nil {
		return "", err.Error()
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return "", err.Error()
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Sprintf("%v %v: %v", w.Request.Method, w.Request.Endpoint, resp.Status)
	}
	return hashOf(w.HashType, body), ""
}

var deviceIdPattern = regexp.MustCompile(`urn:infai:ses:device:[0-9a-x-]{36}`)

// hashOf hashes an answer like the watcher worker; deviceids compares only the sorted device ids
func hashOf(hashType string, body []byte) string {
	switch hashType {
	case "sha256":
		sum := sha256.Sum256(body)
		return hex.EncodeToString(sum[:])
	case "deviceids":
		ids := deviceIdPattern.FindAllString(string(body), -1)
		slices.Sort(ids)
		raw, _ := json.Marshal(ids)
		sum := md5.Sum(raw)
		return hex.EncodeToString(sum[:])
	default:
		sum := md5.Sum(body)
		return hex.EncodeToString(sum[:])
	}
}
