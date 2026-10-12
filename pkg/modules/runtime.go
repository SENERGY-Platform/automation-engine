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
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	devicerepository "github.com/SENERGY-Platform/device-repository/v2/lib/client"
	analytics "github.com/SENERGY-Platform/smart-service-module-worker-analytics/pkg/analytics"
	analyticsdevices "github.com/SENERGY-Platform/smart-service-module-worker-analytics/pkg/devices"
	analyticsimports "github.com/SENERGY-Platform/smart-service-module-worker-analytics/pkg/imports"
	devicegroups "github.com/SENERGY-Platform/smart-service-module-worker-device-repository/pkg/worker"
	"github.com/SENERGY-Platform/smart-service-module-worker-export/pkg/export"
	"github.com/SENERGY-Platform/smart-service-module-worker-import/pkg/imports"
	info "github.com/SENERGY-Platform/smart-service-module-worker-info/pkg"
	libauth "github.com/SENERGY-Platform/smart-service-module-worker-lib/pkg/auth"
	"github.com/SENERGY-Platform/smart-service-module-worker-lib/pkg/camunda"
	libconfig "github.com/SENERGY-Platform/smart-service-module-worker-lib/pkg/configuration"
	"github.com/SENERGY-Platform/smart-service-module-worker-lib/pkg/middleware"
	libmodel "github.com/SENERGY-Platform/smart-service-module-worker-lib/pkg/model"
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
	AuthEndpoint        string
	AuthClientId        string
	AuthClientSecret    string
	LogLevel            string

	AnalyticsEnableMultiplePaths  bool
	AnalyticsDevicePathPrefix     string
	AnalyticsGroupPathPrefix      string
	AnalyticsImportPathPrefix     string
	AnalyticsRemoveImportPathRoot bool

	DeviceGroupNamePrefix      string
	DeviceGroupAttributeOrigin string

	InfoAdditionalModuleDataFields bool
}

// Task types of the former module workers whose handlers are reused unchanged.
const (
	TypeInfo             = "info"
	TypeImport           = "import"
	TypeExport           = "export"
	TypeAnalytics        = "analytics"
	TypeDeviceRepository = "device_repository"
)

// Runtime runs module handlers like the worker library did: string inputs are rendered as
// templates against the variables, prescripts and postscripts run with the script API of the
// library, and modules and variable changes become effects of the task (TASK-28).
type Runtime struct {
	store    *store.Store
	settings Settings
	lib      libconfig.Config
	auth     *libauth.Auth
	iot      devicerepository.Interface
	logger   *slog.Logger
	types    map[string]*moduleType
	// health checks per module type (SMS-11)
	checks map[string]Check
	// nil until AddWatcher
	watcher *watcher
	now     func() time.Time
}

func NewRuntime(ctx context.Context, wg *sync.WaitGroup, s *store.Store, settings Settings, logger *slog.Logger) (*Runtime, error) {
	lib := libconfig.Config{
		DeviceRepositoryUrl: settings.DeviceRepositoryUrl, AuthEndpoint: settings.AuthEndpoint, AuthClientId: settings.AuthClientId,
		AuthClientSecret: settings.AuthClientSecret, TokenCacheDefaultExpirationInSeconds: 60, LogLevel: settings.LogLevel,
	}
	result := &Runtime{store: s, settings: settings, lib: lib, auth: libauth.New(lib), iot: devicerepository.NewClient(settings.DeviceRepositoryUrl, nil), logger: logger,
		types: map[string]*moduleType{}, checks: map[string]Check{}, now: time.Now}
	result.addExternalChecks()
	repo := &repository{store: s, userToken: userTokens(result.auth)}
	// the handlers name their modules and errors after the worker topic, now the task type
	topic := func(name string) libconfig.Config {
		c := lib
		c.CamundaWorkerTopic, c.CamundaWorkerId = name, name
		return c
	}
	result.add(TypeInfo, info.New(info.Config{WorkerParamPrefix: TypeInfo + ".", EnableAdditionalModuleDataFields: settings.InfoAdditionalModuleDataFields}, topic(TypeInfo), repo))
	result.add(TypeImport, imports.New(imports.Config{ImportDeployUrl: settings.ImportDeployUrl, WorkerParamPrefix: TypeImport + "."}, topic(TypeImport), result.auth, repo))
	result.add(TypeExport, export.New(export.Config{ServingServiceUrl: settings.ServingServiceUrl, WorkerParamPrefix: TypeExport + "."}, topic(TypeExport), result.auth, repo))
	result.add(TypeAnalytics, analytics.New(analytics.Config{
		WorkerParamPrefix: TypeAnalytics + ".", FlowEngineUrl: settings.FlowEngineUrl, FlowParserUrl: settings.FlowParserUrl, ImportDeployUrl: settings.ImportDeployUrl,
		DeviceRepositoryUrl: settings.DeviceRepositoryUrl, EnableMultiplePaths: settings.AnalyticsEnableMultiplePaths, DevicePathPrefix: settings.AnalyticsDevicePathPrefix,
		GroupPathPrefix: settings.AnalyticsGroupPathPrefix, ImportPathPrefix: settings.AnalyticsImportPathPrefix, RemoveImportPathRoot: settings.AnalyticsRemoveImportPathRoot,
	}, topic(TypeAnalytics), result.auth, repo, analyticsimports.New(settings.ImportDeployUrl), analyticsdevices.New(settings.DeviceRepositoryUrl)))
	groups, err := devicegroups.New(ctx, wg, devicegroups.Config{
		DeviceManagerUrl: settings.DeviceManagerUrl, DeviceSelectionUrl: settings.DeviceSelectionUrl, WorkerParamPrefix: TypeDeviceRepository + ".",
		CreateDeviceGroupModuleType: "device_repository_create_device_group", DefaultNamePrefix: settings.DeviceGroupNamePrefix, AttributeOrigin: settings.DeviceGroupAttributeOrigin,
	}, topic(TypeDeviceRepository), result.auth, repo)
	if err != nil {
		return nil, err
	}
	result.add(TypeDeviceRepository, groups)
	return result, nil
}

func (this *Runtime) add(name string, handler camunda.Handler) {
	this.types[name] = &moduleType{runtime: this, name: name, handler: handler}
}

// Register makes the module task types available to an engine whose services are configured,
// so that a deploy reports a missing one instead of an incident later.
func (this *Runtime) Register(e *engine.Engine, settings Settings) {
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

// Wrap runs a handler of this package with the template and script handling of the library,
// for the task types the engine implements itself (process_deployment, watcher ...).
func (this *Runtime) Wrap(name string, handler camunda.Handler) engine.TaskType {
	return &moduleType{runtime: this, name: name, handler: handler}
}

type moduleType struct {
	runtime *Runtime
	name    string
	handler camunda.Handler
}

func (this *moduleType) Execute(ctx context.Context, task engine.Task) (engine.TaskResult, error) {
	ctx, x := withExtras(ctx)
	variables := &variables{store: this.runtime.store, task: task}
	lib := this.runtime.lib
	lib.CamundaWorkerTopic = this.name
	m := middleware.New(lib, this.handler, variables, this.runtime.auth, this.runtime.iot)
	modules, outputs, err := m.Do(ctx, externalTask(task))
	if err != nil {
		// an incident of the instance (TASK-28); the worker library stopped the process instead
		return engine.TaskResult{}, err
	}
	effects := &engine.Effects{Variables: variables.changes, Messages: x.messages}
	// a done event of a module, e.g. permission_done_<group> of device_repository, arrives once
	// the instance waits for it; nothing sends it otherwise (SMS-04, SMS-05)
	if done, ok := outputs["done_event"].(string); ok && done != "" && !slices.Contains(effects.Messages, done) {
		effects.Messages = append(effects.Messages, done)
	}
	for _, module := range modules {
		m := fromLib(module)
		if ref, ok := x.resources[m.Id]; ok {
			m.DeleteInfo = &model.ModuleDeleteInfo{Resource: &ref, UserId: task.Owner}
		}
		effects.Modules = append(effects.Modules, m)
	}
	return engine.TaskResult{Output: outputs, Effects: effects}, nil
}

// Undo reverts the modules of a result the engine could not apply. Modules that existed before
// are left alone: a handler that updated one by its key would otherwise remove a resource that
// is still in use (the analytics worker deletes the pipeline it only updated).
func (this *moduleType) Undo(ctx context.Context, task engine.Task, effects *engine.Effects, reason error) {
	var created []libmodel.Module
	err := this.runtime.store.Tx(ctx, func(tx *store.Tx) error {
		for _, m := range effects.Modules {
			if _, err := tx.GetModule(m.Id); errors.Is(err, model.ErrNotFound) {
				created = append(created, toLib(m, task.RootInstanceId))
			} else if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		this.runtime.logger.WarnContext(ctx, "unable to undo modules", "task_type", this.name, "job_id", task.JobId, "error", err)
		return
	}
	if len(created) > 0 {
		this.handler.Undo(ctx, created, reason)
	}
}

// externalTask presents a job like the external task the handlers were written for. The
// process instance id is the instance of the task; module ids become "<instance>.<job>".
func externalTask(task engine.Task) libmodel.CamundaExternalTask {
	input := map[string]libmodel.CamundaVariable{}
	for name, value := range task.Input {
		input[name] = libmodel.CamundaVariable{Value: value}
	}
	return libmodel.CamundaExternalTask{
		Id: task.JobId, Variables: input, ActivityId: task.ElementId, Retries: int64(task.Retries - task.Attempt + 1), ExecutionId: task.JobId,
		ProcessInstanceId: task.InstanceId, ProcessDefinitionId: task.DeploymentId,
	}
}

func fromLib(m libmodel.Module) model.Module {
	result := model.Module{Id: m.Id, ModuleType: m.ModuleType, Data: maps.Clone(m.ModuleData), Keys: m.Keys}
	if result.Keys == nil {
		result.Keys = []string{}
	}
	if m.DeleteInfo != nil {
		result.DeleteInfo = &model.ModuleDeleteInfo{Url: m.DeleteInfo.Url, UserId: m.DeleteInfo.UserId}
	}
	return result
}

func toLib(m model.Module, processInstanceId string) libmodel.Module {
	result := libmodel.Module{Id: m.Id, ProcesInstanceId: processInstanceId, SmartServiceModuleInit: libmodel.SmartServiceModuleInit{ModuleType: m.ModuleType, ModuleData: m.Data, Keys: m.Keys}}
	if m.DeleteInfo != nil && m.DeleteInfo.Url != "" {
		result.DeleteInfo = &libmodel.ModuleDeleteInfo{Url: m.DeleteInfo.Url, UserId: m.DeleteInfo.UserId}
	}
	return result
}

func toLibModule(m model.Module) libmodel.SmartServiceModule {
	return libmodel.SmartServiceModule{
		SmartServiceModuleBase: libmodel.SmartServiceModuleBase{Id: m.Id, UserId: m.Owner, InstanceId: m.InstanceId, LastUpdate: m.UpdatedAt.Unix(), Error: m.Error},
		SmartServiceModuleInit: toLib(m, "").SmartServiceModuleInit,
	}
}

// variables are the variables of the root instance (SMS-13); changes become effects of the task
type variables struct {
	store   *store.Store
	task    engine.Task
	changes map[string]any
}

func (this *variables) GetVariables(ctx context.Context, _ string) (result map[string]interface{}, err error) {
	result = map[string]any{}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		list, err := tx.GetVariables(this.task.RootInstanceId)
		for _, v := range list {
			result[v.Name] = v.Value
		}
		return err
	})
	return result, err
}

func (this *variables) SetVariables(_ context.Context, _ string, changes map[string]interface{}) error {
	if this.changes == nil {
		this.changes = map[string]any{}
	}
	maps.Copy(this.changes, changes)
	return nil
}

func (this *variables) GetCachedSmartServiceInstance(_ context.Context, _ string) (libmodel.SmartServiceInstance, error) {
	return libmodel.SmartServiceInstance{Id: this.task.RootInstanceId, UserId: this.task.Owner, ReleaseId: this.task.DeploymentId}, nil
}

// repository answers the handlers' questions to smart-service-repository from the database
type repository struct {
	store     *store.Store
	userToken func(userId string) (string, error)
}

func (this *repository) root(tx *store.Tx, instanceId string) (store.Instance, error) {
	inst, err := tx.GetInstance(instanceId, false)
	if err != nil || inst.ParentId == "" {
		return inst, err
	}
	return tx.GetInstance(inst.ParentId, false)
}

func (this *repository) GetInstanceUser(ctx context.Context, instanceId string) (userId string, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		inst, err := tx.GetInstance(instanceId, false)
		userId = inst.Owner
		return err
	})
	return userId, err
}

func (this *repository) ListExistingModules(ctx context.Context, instanceId string, query libmodel.ModulQuery) (result []libmodel.SmartServiceModule, err error) {
	result = []libmodel.SmartServiceModule{}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		root, err := this.root(tx, instanceId)
		if err != nil {
			return err
		}
		key, moduleType := "", ""
		if query.KeyFilter != nil {
			key = *query.KeyFilter
		}
		if query.TypeFilter != nil {
			moduleType = *query.TypeFilter
		}
		modules, err := tx.ModulesOfInstance(root.Id, key, moduleType)
		for _, m := range modules {
			result = append(result, toLibModule(m))
		}
		return err
	})
	return result, err
}

// UseModuleDeleteInfo removes a resource at once; the handlers call it when they undo
func (this *repository) UseModuleDeleteInfo(ctx context.Context, info libmodel.ModuleDeleteInfo) error {
	return Delete(ctx, &http.Client{Timeout: 30 * time.Second}, this.userToken, model.ModuleDeleteInfo{Url: info.Url, UserId: info.UserId})
}

// userTokens adapts the token exchange of the worker library
func userTokens(a *libauth.Auth) func(userId string) (string, error) {
	return func(userId string) (string, error) {
		token, err := a.ExchangeUserToken(userId)
		return token.Token, err
	}
}
