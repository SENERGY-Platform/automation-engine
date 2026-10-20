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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/modules/compat"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/tracing"
	devicerepository "github.com/SENERGY-Platform/device-repository/v3/lib/client"
)

// Settings are the configuration values of the former module workers (TASK-29). The parameter
// prefixes are part of the model format and stay those of the workers: "<task type>.".
type Settings struct {
	DeviceRepositoryUrl string
	DeviceSelectionUrl  string
	DeviceManagerUrl    string
	FlowEngineUrl       string
	FlowParserUrl       string
	ImportDeployUrl     string
	ServingServiceUrl   string
	// repositories of flows and import types, asked whether used resources changed (SMS-15)
	FlowRepoUrl         string
	ImportRepositoryUrl string

	AnalyticsEnableMultiplePaths  bool
	AnalyticsDevicePathPrefix     string
	AnalyticsGroupPathPrefix      string
	AnalyticsImportPathPrefix     string
	AnalyticsRemoveImportPathRoot bool

	DeviceGroupNamePrefix      string
	DeviceGroupAttributeOrigin string

	InfoAdditionalModuleDataFields bool
}

// Task types of the former module workers (TASK-22 to TASK-26).
const (
	TypeInfo             = "info"
	TypeImport           = "import"
	TypeExport           = "export"
	TypeAnalytics        = "analytics"
	TypeDeviceRepository = "device_repository"
)

// Task is a module task as a handler sees it: the inputs after the templates were rendered.
type Task struct {
	// the job; module ids are "<instance>.<job>", as "<process instance>.<task>" before
	Id             string
	InstanceId     string
	RootInstanceId string
	DeploymentId   string
	ElementId      string
	Namespace      string
	Owner          string
	Inputs         map[string]any
}

// ModuleId is the id of a module the task creates; suffixes tell several apart.
func (this Task) ModuleId(suffix ...string) string {
	return strings.Join(append([]string{this.InstanceId, this.Id}, suffix...), ".")
}

// String reads an input that has to be a string.
func (this Task) String(name string) (string, bool) {
	value, ok := this.Inputs[name].(string)
	return value, ok
}

// Handler implements a module task type. Modules it returns become effects of the task; the
// runtime removes new ones again if the engine cannot apply the result.
type Handler interface {
	Do(ctx context.Context, task Task) (modules []model.Module, outputs map[string]any, err error)
}

// Runtime runs module handlers like the module workers did: string inputs are rendered as
// templates against the variables, prescripts and postscripts run with the script API of the
// worker library, and modules and variable changes become effects of the task (TASK-28).
type Runtime struct {
	store    *store.Store
	settings Settings
	tokens   auth.UserTokenSource
	iot      devicerepository.Interface
	scripts  compat.Scripts
	http     *http.Client
	logger   *slog.Logger
	types    map[string]*moduleType
	// health checks per module type (SMS-11)
	checks map[string]Check
	// nil until AddWatcher
	watcher *watcher
	now     func() time.Time
	// the token of the engine, for resources the owner may no longer read
	adminToken auth.TokenSource
	// set by Register
	engine *engine.Engine
	// the controller once AddProcessModules is called
	resources Resources
}

// SetAdminToken sets the token of the engine for the check of used resources.
func (this *Runtime) SetAdminToken(token auth.TokenSource) {
	this.adminToken = token
}

// NewRuntime creates the runtime; tokens gives the tokens of owners, e.g. the token exchange.
func NewRuntime(s *store.Store, settings Settings, tokens auth.UserTokenSource, logger *slog.Logger) *Runtime {
	iot := devicerepository.NewClient(settings.DeviceRepositoryUrl, nil)
	result := &Runtime{store: s, settings: settings, tokens: tokens, iot: iot, logger: logger, http: &http.Client{Timeout: 30 * time.Second},
		types: map[string]*moduleType{}, checks: map[string]Check{}, now: time.Now, resources: storeResources{s}}
	result.scripts = compat.Scripts{Logger: logger, Tokens: result.token, Iot: iot}
	result.addExternalChecks()
	result.add(TypeInfo, &infoHandler{runtime: result})
	result.add(TypeImport, &importHandler{runtime: result})
	result.add(TypeExport, &exportHandler{runtime: result})
	result.add(TypeAnalytics, &analyticsHandler{runtime: result})
	result.add(TypeDeviceRepository, &deviceGroupHandler{runtime: result})
	return result
}

func (this *Runtime) add(name string, handler Handler) {
	this.types[name] = &moduleType{runtime: this, name: name, handler: handler}
}

// Register makes the module task types available to an engine whose services are configured,
// so that a deploy reports a missing one instead of an incident later.
func (this *Runtime) Register(e *engine.Engine, settings Settings) {
	this.engine = e
	configured := map[string]bool{
		TypeInfo:                   true,
		TypeProcessDeployment:      true,
		TypeProcessDeploymentStart: true,
		TypeWatcher:                this.watcher != nil && settings.DeviceSelectionUrl != "",
		TypeImport:                 settings.ImportDeployUrl != "",
		TypeExport:                 settings.ServingServiceUrl != "",
		TypeAnalytics:              settings.FlowEngineUrl != "" && settings.FlowParserUrl != "" && settings.DeviceRepositoryUrl != "",
		TypeDeviceRepository:       settings.DeviceManagerUrl != "" && settings.DeviceSelectionUrl != "",
	}
	for name, t := range this.types {
		if configured[name] {
			e.RegisterTaskType(name, t)
		}
	}
}

// token returns a token of a user, the owner of the instance in general
func (this *Runtime) token(userId string) (string, error) {
	if this.tokens == nil {
		return "", fmt.Errorf("no token source for user %v", userId)
	}
	return this.tokens(userId)
}

// existing lists the modules of the root instance of a task with a key and a type; an empty
// type selects every type
func (this *Runtime) existing(ctx context.Context, task Task, key string, moduleType string) (result []model.Module, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		result, err = tx.ModulesOfInstance(task.RootInstanceId, key, moduleType)
		return err
	})
	return result, err
}

type moduleType struct {
	runtime *Runtime
	name    string
	handler Handler
}

func (this *moduleType) Execute(ctx context.Context, task engine.Task) (engine.TaskResult, error) {
	rt := this.runtime
	variables := map[string]any{}
	err := rt.store.Tx(ctx, func(tx *store.Tx) error {
		list, err := tx.GetVariables(task.RootInstanceId)
		for _, v := range list {
			variables[v.Name] = v.Value
		}
		return err
	})
	if err != nil {
		return engine.TaskResult{}, err
	}
	// the order of the worker library: prescripts on the raw inputs, templates against the
	// variables they changed, the handler, postscripts with all outputs
	changes, outputs, err := rt.scripts.Run(ctx, task.Owner, compat.PreScriptPrefix, task.Input, nil, variables)
	if err != nil {
		return engine.TaskResult{}, err
	}
	maps.Copy(variables, changes)
	inputs, err := compat.Render(task.Input, variables)
	if err != nil {
		return engine.TaskResult{}, err
	}
	moduleTask := Task{Id: task.JobId, InstanceId: task.InstanceId, RootInstanceId: task.RootInstanceId, DeploymentId: task.DeploymentId,
		ElementId: task.ElementId, Namespace: task.Namespace, Owner: task.Owner, Inputs: inputs}
	modules, handlerOutputs, err := this.handler.Do(ctx, moduleTask)
	if err != nil {
		// an incident of the instance (TASK-28); the worker library stopped the process instead
		return engine.TaskResult{}, err
	}
	maps.Copy(outputs, handlerOutputs)
	postChanges, outputs, err := rt.scripts.Run(ctx, task.Owner, compat.PostScriptPrefix, task.Input, outputs, variables)
	if err != nil {
		return engine.TaskResult{}, err
	}
	maps.Copy(changes, postChanges)
	effects := &engine.Effects{Modules: modules}
	if len(changes) > 0 {
		effects.Variables = changes
	}
	// a done event, e.g. deployment_done_<id> or permission_done_<group>, arrives once the
	// instance waits for it; nothing sends it otherwise (SMS-04, SMS-05)
	if done, ok := outputs["done_event"].(string); ok && done != "" {
		effects.Messages = append(effects.Messages, done)
	}
	return engine.TaskResult{Output: outputs, Effects: effects}, nil
}

// Undo removes the resources of modules a result created that the engine could not apply.
// Modules that existed before stay: a handler that updated one by its key would otherwise
// remove a resource that is still in use.
func (this *moduleType) Undo(ctx context.Context, task engine.Task, effects *engine.Effects, reason error) {
	rt := this.runtime
	for _, m := range effects.Modules {
		var exists bool
		err := rt.store.Tx(ctx, func(tx *store.Tx) error {
			_, err := tx.GetModule(m.Id)
			exists = err == nil
			if errors.Is(err, model.ErrNotFound) {
				err = nil
			}
			return err
		})
		if err != nil || exists || m.DeleteInfo == nil {
			continue
		}
		if m.DeleteInfo.Resource != nil {
			ref := *m.DeleteInfo.Resource
			err = rt.resources.RemoveResource(ctx, ref)
		} else if m.DeleteInfo.Url != "" {
			err = Delete(ctx, rt.http, rt.tokens, *m.DeleteInfo)
		}
		if err != nil {
			rt.logger.WarnContext(ctx, "unable to undo a module", "task_type", this.name, "module_id", m.Id, "reason", reason, "error", err)
		}
	}
}

// moduleData reads the input <prefix>module_data, a JSON object; anything else gives an empty
// object, as with the workers
func moduleData(task Task, prefix string) map[string]any {
	result := map[string]any{}
	if raw, ok := task.String(prefix + "module_data"); ok {
		if json.Unmarshal([]byte(raw), &result) != nil {
			return map[string]any{}
		}
	}
	return result
}

// moduleUpdateVersion counts updates of a module by its key, as the workers did
const moduleUpdateVersion = "module_update_version"

func setModuleUpdateVersion(m *model.Module) {
	version, _ := m.Data[moduleUpdateVersion].(float64)
	m.Data[moduleUpdateVersion] = version + 1
}

// request sends a request to a platform service with the token of the owner; with result, a
// JSON answer is decoded
func (this *Runtime) request(ctx context.Context, client *http.Client, method string, target string, owner string, userIdHeader bool, body any, result any) error {
	token, err := this.token(owner)
	if err != nil {
		return err
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", token)
	if userIdHeader {
		req.Header.Set("X-UserId", owner)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if err = tracing.Inject(ctx, req); err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &statusError{method: method, target: target, status: resp.StatusCode, body: strings.TrimSpace(string(raw))}
	}
	if result == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(result)
}

type statusError struct {
	method, target string
	status         int
	body           string
}

func (this *statusError) Error() string {
	return fmt.Sprintf("%v %v: unexpected status %v: %v", this.method, this.target, this.status, this.body)
}
