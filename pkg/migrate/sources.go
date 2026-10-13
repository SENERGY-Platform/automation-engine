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

package migrate

import (
	"context"
	"time"

	"github.com/SENERGY-Platform/models/go/models"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
)

// ProcessModel is a model of process-model-repository.
type ProcessModel struct {
	Id              string
	Name            string
	Owner           string
	BpmnXml         string
	SvgXml          string
	Description     string
	Publish         bool
	PublishDate     string
	Date            int64
	LastUpdatedUnix int64
}

// ProcessDeployment is a deployment of process-deployment; its id is the vid of the engine
// wrapper and the deployment id in the engine.
type ProcessDeployment struct {
	Id         string
	Owner      string
	Source     string
	Deployment models.Deployment
}

// CamundaDeployment is a deployment in a Camunda engine with its process definition.
type CamundaDeployment struct {
	Engine       string
	Id           string
	Name         string
	Source       string
	TenantId     string
	Time         time.Time
	DefinitionId string
	// deplid_<vid with _> for deployments of process-deployment, the process id otherwise
	Key  string
	Bpmn string
}

// CamundaInstance is a running process instance with its current variables.
type CamundaInstance struct {
	Id           string
	BusinessKey  string
	TenantId     string
	DefinitionId string
	Variables    map[string]any
}

type Schedule struct {
	Id           string
	User         string
	Cron         string
	DeploymentId string
	Alias        string
	Disabled     bool
	CreatedBy    string
}

// IoValue is a value of process-io; definition and instance are those of Camunda.
type IoValue struct {
	UserId       string
	Key          string
	Value        any
	DefinitionId string
	InstanceId   string
	Timestamp    int64
}

// Sources are the databases of the old services.
type Sources interface {
	ProcessModels(ctx context.Context) ([]ProcessModel, error)
	ProcessDeployments(ctx context.Context) ([]ProcessDeployment, error)
	Schedules(ctx context.Context) ([]Schedule, error)
	IoValues(ctx context.Context) ([]IoValue, error)
	// the Camunda engines of the processes namespace, all shards
	CamundaDeployments(ctx context.Context) ([]CamundaDeployment, error)
	RunningInstances(ctx context.Context, d CamundaDeployment) ([]CamundaInstance, error)
	// rights in permissions-v2
	Rights(ctx context.Context, topic string, id string) (permmodel.ResourcePermissions, bool, error)
}
