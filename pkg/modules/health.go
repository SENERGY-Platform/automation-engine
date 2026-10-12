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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/tracing"
)

// Check examines the resource of a module. A non-empty problem becomes the module error; an
// error means the check itself failed and leaves the module as it is.
type Check func(ctx context.Context, m model.Module) (problem string, err error)

// AddCheck registers the health check of a module type.
func (this *Runtime) AddCheck(moduleType string, check Check) {
	this.checks[moduleType] = check
}

// RunHealthChecks checks every module once per interval, like the module workers did (SMS-11).
func (this *Runtime) RunHealthChecks(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := this.CheckModules(ctx, interval); err != nil && ctx.Err() == nil {
			this.logger.WarnContext(ctx, "module health checks incomplete", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// CheckModules checks the modules whose last check is older than interval.
func (this *Runtime) CheckModules(ctx context.Context, interval time.Duration) error {
	// fixed for the pass, so that modules checked in it do not become due again
	cutoff := this.now().Add(-interval)
	for moduleType, check := range this.checks {
		for {
			var due []model.Module
			err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
				due, err = tx.ModulesToCheck(moduleType, cutoff, 50)
				return err
			})
			if err != nil {
				return err
			}
			if len(due) == 0 {
				break
			}
			for _, m := range due {
				problem, err := check(ctx, m)
				if err != nil {
					this.logger.WarnContext(ctx, "unable to check module", "module_id", m.Id, "module_type", m.ModuleType, "error", err)
					problem = m.Error
				} else if problem != "" {
					problem = moduleType + ": " + problem
				}
				err = this.store.Tx(ctx, func(tx *store.Tx) error {
					return tx.SetModuleCheck(m.Id, problem, this.now())
				})
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// addExternalChecks are the checks of the reused workers: the resource exists, and a pipeline runs
func (this *Runtime) addExternalChecks() {
	client := &http.Client{Timeout: 30 * time.Second}
	get := func(ctx context.Context, owner string, target string, result any) (int, error) {
		token, err := this.token(owner)
		if err != nil {
			return 0, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("Authorization", token)
		req.Header.Set("X-UserId", owner)
		if err = tracing.Inject(ctx, req); err != nil {
			return 0, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		if result != nil && resp.StatusCode < 300 {
			err = json.NewDecoder(resp.Body).Decode(result)
		}
		return resp.StatusCode, err
	}
	exists := func(base string, path func(m model.Module) string) Check {
		return func(ctx context.Context, m model.Module) (string, error) {
			id := path(m)
			if id == "" {
				return "module data names no resource", nil
			}
			status, err := get(ctx, m.Owner, base+"/"+url.PathEscape(id), nil)
			if err != nil {
				return "", err
			}
			if status >= 300 {
				return fmt.Sprintf("%v not found (%v)", id, status), nil
			}
			return "", nil
		}
	}
	if this.settings.ImportDeployUrl != "" {
		this.AddCheck(TypeImport, exists(this.settings.ImportDeployUrl+"/instances", func(m model.Module) string { return field(m.Data, "import", "id") }))
	}
	if this.settings.ServingServiceUrl != "" {
		this.AddCheck(TypeExport, exists(this.settings.ServingServiceUrl+"/instance", func(m model.Module) string { return field(m.Data, "export", "ID") }))
	}
	if this.settings.FlowEngineUrl != "" {
		this.AddCheck(TypeAnalytics, func(ctx context.Context, m model.Module) (string, error) {
			id := field(m.Data, "pipeline_id")
			if id == "" {
				id = field(m.Data, "pipeline", "id")
			}
			if id == "" {
				return "module data names no pipeline", nil
			}
			var pipeline struct {
				Running bool `json:"running"`
			}
			status, err := get(ctx, m.Owner, this.settings.FlowEngineUrl+"/pipeline/"+url.PathEscape(id), &pipeline)
			switch {
			case err != nil:
				return "", err
			case status >= 300:
				return fmt.Sprintf("pipeline %v not found (%v)", id, status), nil
			case !pipeline.Running:
				return fmt.Sprintf("pipeline %v is not running", id), nil
			}
			return "", nil
		})
	}
}

// field reads a string at a path of nested maps
func field(data map[string]any, path ...string) string {
	var current any = data
	for _, key := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = m[key]
	}
	value, _ := current.(string)
	return value
}
