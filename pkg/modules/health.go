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
	"slices"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/tracing"
	"github.com/google/uuid"
)

// Check examines the resource of a module. A non-empty problem becomes the module error once
// confirmed; an error means the check itself failed and leaves the module as it is.
type Check func(ctx context.Context, m model.Module) (problem string, err error)

// AddCheck registers the health check of a module type.
func (this *Runtime) AddCheck(moduleType string, check Check) {
	this.checks[moduleType] = check
}

// RunHealthChecks checks every module once per interval, like the module workers did (SMS-11), and
// a module with an error or a suspected finding once per recheck.
func (this *Runtime) RunHealthChecks(ctx context.Context, interval time.Duration, recheck time.Duration) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := this.CheckModules(ctx, interval, recheck); err != nil && ctx.Err() == nil {
			this.logger.WarnContext(ctx, "module health checks incomplete", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// CheckModules checks the modules whose last check is older than interval, and those with an
// error or a suspected finding whose last check is older than recheck. A finding becomes the module
// error only when a check at least recheck later still has it, so a pipeline whose pod is replaced
// does not show as an error.
func (this *Runtime) CheckModules(ctx context.Context, interval time.Duration, recheck time.Duration) error {
	// fixed for the pass; a module checked in the same millisecond as the cutoff is due again
	// by its time, so the pass also remembers what it checked
	now := this.now()
	cutoff, recheckCutoff := now.Add(-interval), now.Add(-recheck)
	checked := map[string]bool{}
	for moduleType, check := range this.checks {
		for {
			var due []model.Module
			err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
				due, err = tx.ModulesToCheck(moduleType, cutoff, recheckCutoff, 50)
				return err
			})
			if err != nil {
				return err
			}
			due = slices.DeleteFunc(due, func(m model.Module) bool { return checked[m.Id] })
			if len(due) == 0 {
				break
			}
			for _, m := range due {
				checked[m.Id] = true
				problem, err := check(ctx, m)
				undecided := err != nil
				if undecided {
					this.logger.WarnContext(ctx, "unable to check module", "module_id", m.Id, "module_type", m.ModuleType, "error", err)
				} else if problem != "" {
					problem = moduleType + ": " + problem
				}
				at := this.now()
				err = this.store.Tx(ctx, func(tx *store.Tx) error {
					if undecided {
						return tx.SetModuleChecked(m.Id, at)
					}
					confirmed, err := tx.SetModuleCheck(m.Id, problem, at, at.Add(-recheck))
					if err != nil || !confirmed {
						return err
					}
					return this.notifyModuleError(tx, m, problem, at)
				})
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// notifyModuleError tells the owner of the instance about a confirmed module error, once per
// error, as smart-service-repository did; the notifier sends developer-notifications a copy, so
// that errors of smart services reach the developers also when they are no incident (SNRGY-4537).
func (this *Runtime) notifyModuleError(tx *store.Tx, m model.Module, problem string, at time.Time) error {
	inst, err := tx.GetInstance(m.InstanceId, false)
	if err != nil {
		return err
	}
	name := inst.Name
	if name == "" {
		name = inst.DeploymentName
	}
	title := "Module Error in " + name
	if m.Namespace == "smart-services" {
		title = "Smart-Service-Module Error in " + name
	}
	message := fmt.Sprintf("Instance-Name: %v\nInstance-ID: %v\nModule-ID: %v\nModule-Type: %v\nError: %v", name, inst.Id, m.Id, m.ModuleType, problem)
	return tx.InsertNotification(store.Notification{Id: uuid.NewString(), UserId: inst.Owner, Title: title, Message: message,
		Topic: engine.NotificationTopic, Namespace: m.Namespace, DuplicateWindow: store.DefaultDuplicateWindow, CreatedAt: at})
}

// addExternalChecks are the checks of the reused workers: the resource exists, a pipeline runs, and
// since SNRGY-4539 the container of an import runs or, run once, did not fail
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
		this.AddCheck(TypeImport, func(ctx context.Context, m model.Module) (string, error) {
			id := field(m.Data, "import", "id")
			if id == "" {
				return "module data names no resource", nil
			}
			var instance importInstance
			status, err := get(ctx, m.Owner, this.settings.ImportDeployUrl+"/instances/"+url.PathEscape(id), &instance)
			switch {
			case err != nil:
				return "", err
			case status >= 300:
				return fmt.Sprintf("%v not found (%v)", id, status), nil
			}
			return importProblem(id, instance), nil
		})
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

// importInstance is the part of an import instance of import-deploy (since v0.1.0) the health
// check reads: the status of its container, filled on read, and whether it restarts
type importInstance struct {
	Restart *bool `json:"restart"`
	Status  *struct {
		Running       bool   `json:"running"`
		Transitioning bool   `json:"transitioning"`
		Message       string `json:"message"`
	} `json:"status"`
}

// importProblem judges the container of an import instance (SNRGY-4539). An import that restarts
// runs as a deployment and has to run, also while it restarts in a loop. One that does not
// restart runs once as a job and is fine running, starting or done; done is "completed" on
// Kubernetes and "exited" on Docker, which does not tell success from failure. A short stop, such
// as a rollout, is no error: a finding counts only when the recheck still has it. Without a
// status, as from a backend that has none, the instance only has to exist.
func importProblem(id string, instance importInstance) string {
	status := instance.Status
	reason := func(text string) string {
		if status.Message == "" || status.Message == "failed" {
			return text
		}
		return text + ": " + status.Message
	}
	switch {
	case status == nil:
		return ""
	case status.Message == "not deployed":
		return fmt.Sprintf("import %v is not deployed", id)
	case instance.Restart != nil && !*instance.Restart:
		if status.Running || status.Transitioning || status.Message == "completed" || status.Message == "exited" {
			return ""
		}
		return reason(fmt.Sprintf("import %v failed", id))
	case !status.Running:
		return reason(fmt.Sprintf("import %v is not running", id))
	}
	return ""
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
