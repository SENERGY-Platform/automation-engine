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

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// Controller is the complete API of the engine. The server implements it, the HTTP client and
// the in-process test client implement it as well (CLI-08, CLI-09). Every call takes the user
// token (CLI-10).
type Controller interface {
	ListNamespaces(ctx context.Context, token string) ([]model.Namespace, error)
	GetNamespace(ctx context.Context, token string, namespace string) (model.Namespace, error)
	// Validate checks BPMN against the rules of a namespace without storing it (BPMN-18)
	Validate(ctx context.Context, token string, namespace string, xml string) ([]model.Issue, error)
	// PutNamespace creates or changes a namespace; admins only (NS-02)
	PutNamespace(ctx context.Context, token string, name string, ns model.Namespace) (model.Namespace, error)
	// DeleteNamespace removes a namespace without resources; admins only
	DeleteNamespace(ctx context.Context, token string, name string) error

	// SetOwner changes the owner of a model, deployment or instance (SEC-11)
	SetOwner(ctx context.Context, token string, namespace string, kind model.Kind, id string, input model.OwnerInput) error

	CreateModel(ctx context.Context, token string, namespace string, input model.ModelInput) (model.Model, error)
	// UpdateModel stores a new version of the model (MOD-05)
	UpdateModel(ctx context.Context, token string, namespace string, id string, input model.ModelInput) (model.Model, error)
	GetModel(ctx context.Context, token string, namespace string, id string) (model.Model, error)
	ListModels(ctx context.Context, token string, namespace string, options model.ModelListOptions) (model.Page[model.Model], error)
	DeleteModel(ctx context.Context, token string, namespace string, id string) error
	PublishModel(ctx context.Context, token string, namespace string, id string, input model.PublishInput) (model.Model, error)
	ListModelVersions(ctx context.Context, token string, namespace string, id string, options model.PageOptions) (model.Page[model.ModelVersion], error)
	GetModelVersion(ctx context.Context, token string, namespace string, id string, version int) (model.ModelVersion, error)

	CreateDeployment(ctx context.Context, token string, namespace string, input model.DeploymentInput) (model.Deployment, error)
	// PutDeployment creates a deployment with the given id or updates it with a new version (DEP-04)
	PutDeployment(ctx context.Context, token string, namespace string, id string, input model.DeploymentInput) (model.Deployment, error)
	GetDeployment(ctx context.Context, token string, namespace string, id string) (model.Deployment, error)
	ListDeployments(ctx context.Context, token string, namespace string, options model.DeploymentListOptions) (model.Page[model.Deployment], error)
	DeleteDeployment(ctx context.Context, token string, namespace string, id string) error
	ListDeploymentVersions(ctx context.Context, token string, namespace string, id string, options model.PageOptions) (model.Page[model.DeploymentVersion], error)
	GetDeploymentVersion(ctx context.Context, token string, namespace string, id string, version int) (model.DeploymentVersion, error)
	GetStartParameters(ctx context.Context, token string, namespace string, deploymentId string) ([]model.StartParameter, error)
	// PrepareDeployment lists start parameters and the selectable devices per element (DEP-01)
	PrepareDeployment(ctx context.Context, token string, namespace string, input model.PrepareInput) (model.PreparedDeployment, error)
	// ListEventDescriptions shows what event workers evaluate for a deployment (DEP-07)
	ListEventDescriptions(ctx context.Context, token string, namespace string, deploymentId string) ([]model.EventDescription, error)
	// ResetRestarts clears the faulty mark of a deployment after a restart loop (INC-04)
	ResetRestarts(ctx context.Context, token string, namespace string, id string) (model.Deployment, error)

	StartInstance(ctx context.Context, token string, namespace string, input model.StartInstanceInput) (model.Instance, error)
	// PutInstance starts an instance with the given id idempotently (RUN-01)
	PutInstance(ctx context.Context, token string, namespace string, id string, input model.StartInstanceInput) (model.Instance, error)
	GetInstance(ctx context.Context, token string, namespace string, id string) (model.Instance, error)
	ListInstances(ctx context.Context, token string, namespace string, options model.InstanceListOptions) (model.Page[model.Instance], error)
	// DeleteInstances removes instances with their history (HIST-03)
	DeleteInstances(ctx context.Context, token string, namespace string, ids []string) error
	StopInstances(ctx context.Context, token string, namespace string, ids []string) error
	RestartInstance(ctx context.Context, token string, namespace string, id string, input model.RestartInput) (model.Instance, error)
	ListRuns(ctx context.Context, token string, namespace string, id string) ([]model.Run, error)
	GetVariables(ctx context.Context, token string, namespace string, id string) ([]model.Variable, error)
	SetVariable(ctx context.Context, token string, namespace string, id string, name string, value any) error
	ListActivities(ctx context.Context, token string, namespace string, id string, options model.ActivityListOptions) (model.ActivityPage, error)
	ListElementStats(ctx context.Context, token string, namespace string, id string, run int) ([]model.ElementStats, error)

	ListIncidents(ctx context.Context, token string, namespace string, options model.IncidentListOptions) (model.Page[model.Incident], error)

	CorrelateMessage(ctx context.Context, token string, input model.MessageInput) (model.CorrelationResult, error)

	// schedules start instances of deployments by cron (RUN-07)
	ListSchedules(ctx context.Context, token string, namespace string, options model.ScheduleListOptions) (model.Page[model.Schedule], error)
	GetSchedule(ctx context.Context, token string, namespace string, id string) (model.Schedule, error)
	CreateSchedule(ctx context.Context, token string, namespace string, input model.ScheduleInput) (model.Schedule, error)
	UpdateSchedule(ctx context.Context, token string, namespace string, id string, input model.ScheduleInput) (model.Schedule, error)
	DeleteSchedule(ctx context.Context, token string, namespace string, id string) error

	// the process-io store of the caller (IO-02)
	ListIoValues(ctx context.Context, token string, options model.IoListOptions) (model.Page[model.IoValue], error)
	GetIoValue(ctx context.Context, token string, key string) (model.IoValue, error)
	SetIoValue(ctx context.Context, token string, key string, input model.IoValueInput) error
	DeleteIoValue(ctx context.Context, token string, key string) error
	IoBulk(ctx context.Context, token string, bulk model.IoBulk) ([]model.IoValue, error)

	// GetUserDependents is the dry run of DeleteUser; admins only (SEC-14)
	GetUserDependents(ctx context.Context, token string, userId string) (model.UserDependents, error)
	// DeleteUser removes everything a user owns; admins only (SEC-08, SEC-13)
	DeleteUser(ctx context.Context, token string, userId string) error
	GetUserSettings(ctx context.Context, token string, userId string) (model.UserSettings, error)
	// SetUserSettings sets limits of a user; admins only (HIST-04)
	SetUserSettings(ctx context.Context, token string, userId string, settings model.UserSettings) (model.UserSettings, error)
}
