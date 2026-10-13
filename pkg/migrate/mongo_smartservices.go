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

	"github.com/SENERGY-Platform/models/go/models"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// the documents of smart-service-repository; fields without bson tag are lower-cased there

type designDocument struct {
	Id          string `bson:"id"`
	UserId      string `bson:"user_id"`
	Name        string `bson:"name"`
	Description string `bson:"description"`
	BpmnXml     string `bson:"bpmn_xml"`
	SvgXml      string `bson:"svg_xml"`
	UpdatedAt   int64  `bson:"updated_at"`
}

func (this *Mongo) Designs(ctx context.Context) ([]Design, error) {
	documents, err := readAll[designDocument](ctx, this.client.Database(this.databases.SmartServices).Collection("designs"), bson.M{})
	if err != nil {
		return nil, err
	}
	result := make([]Design, 0, len(documents))
	for _, d := range documents {
		result = append(result, Design(d))
	}
	return result, nil
}

type releaseDocument struct {
	Id          string `bson:"id"`
	DesignId    string `bson:"design_id"`
	Name        string `bson:"name"`
	Description string `bson:"description"`
	CreatedAt   int64  `bson:"created_at"`
	Creator     string `bson:"creator"`
	BpmnXml     string `bson:"bpmn_xml"`
	SvgXml      string `bson:"svg_xml"`
}

// Releases lists the finished releases; those marked as deleted or unfinished are left out
func (this *Mongo) Releases(ctx context.Context) ([]Release, error) {
	filter := bson.M{"marked_as_deleted": bson.M{"$ne": true}, "marked_as_unfinished": bson.M{"$ne": true}}
	documents, err := readAll[releaseDocument](ctx, this.client.Database(this.databases.SmartServices).Collection("releases"), filter)
	if err != nil {
		return nil, err
	}
	result := make([]Release, 0, len(documents))
	for _, d := range documents {
		result = append(result, Release{Id: d.Id, DesignId: d.DesignId, Name: d.Name, Description: d.Description, Creator: d.Creator,
			BpmnXml: d.BpmnXml, SvgXml: d.SvgXml, CreatedAt: d.CreatedAt})
	}
	return result, nil
}

type instanceDocument struct {
	Name        string `bson:"name"`
	Description string `bson:"description"`
	Parameters  []struct {
		Id    string
		Value any
	} `bson:"parameters"`
	Id        string `bson:"id"`
	UserId    string `bson:"user_id"`
	ReleaseId string `bson:"release_id"`
	Ready     bool   `bson:"ready"`
	Deleting  bool   `bson:"deleting"`
	Error     string `bson:"error"`
	CreatedAt int64  `bson:"created_at"`
	UpdatedAt int64  `bson:"updated_at"`
}

// SmartServiceInstances lists the instances; those being deleted are left out
func (this *Mongo) SmartServiceInstances(ctx context.Context) ([]SmartServiceInstance, error) {
	documents, err := readAll[instanceDocument](ctx, this.client.Database(this.databases.SmartServices).Collection("instances"), bson.M{"deleting": bson.M{"$ne": true}})
	if err != nil {
		return nil, err
	}
	result := make([]SmartServiceInstance, 0, len(documents))
	for _, d := range documents {
		i := SmartServiceInstance{Id: d.Id, UserId: d.UserId, ReleaseId: d.ReleaseId, Name: d.Name, Description: d.Description, Ready: d.Ready,
			Error: d.Error, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt}
		for _, p := range d.Parameters {
			i.Parameters = append(i.Parameters, Parameter{Id: p.Id, Value: normalize(p.Value)})
		}
		result = append(result, i)
	}
	return result, nil
}

type moduleDocument struct {
	Id         string `bson:"id"`
	UserId     string `bson:"user_id"`
	InstanceId string `bson:"instance_id"`
	LastUpdate int64  `bson:"last_update"`
	Error      string `bson:"error"`
	DeleteInfo *struct {
		Url    string `bson:"url"`
		UserId string `bson:"user_id"`
	} `bson:"delete_info"`
	ModuleType string         `bson:"module_type"`
	ModuleData map[string]any `bson:"module_data"`
	Keys       []string       `bson:"keys"`
}

func (this *Mongo) Modules(ctx context.Context) ([]Module, error) {
	documents, err := readAll[moduleDocument](ctx, this.client.Database(this.databases.SmartServices).Collection("modules"), bson.M{})
	if err != nil {
		return nil, err
	}
	result := make([]Module, 0, len(documents))
	for _, d := range documents {
		m := Module{Id: d.Id, UserId: d.UserId, InstanceId: d.InstanceId, Type: d.ModuleType, Keys: d.Keys, Error: d.Error, LastUpdate: d.LastUpdate,
			Data: map[string]any{}}
		for k, v := range d.ModuleData {
			m.Data[k] = normalize(v)
		}
		if d.DeleteInfo != nil {
			m.DeleteUrl, m.DeleteUser = d.DeleteInfo.Url, d.DeleteInfo.UserId
		}
		result = append(result, m)
	}
	return result, nil
}

type variableDocument struct {
	InstanceId string `bson:"instance_id"`
	Name       string `bson:"name"`
	Value      any    `bson:"value"`
}

func (this *Mongo) SmartServiceVariables(ctx context.Context) ([]InstanceVariable, error) {
	documents, err := readAll[variableDocument](ctx, this.client.Database(this.databases.SmartServices).Collection("variables"), bson.M{})
	if err != nil {
		return nil, err
	}
	result := make([]InstanceVariable, 0, len(documents))
	for _, d := range documents {
		result = append(result, InstanceVariable{InstanceId: d.InstanceId, Name: d.Name, Value: normalize(d.Value)})
	}
	return result, nil
}

// metadataDocument is model.DeploymentMetadata of process-sync (master): Metadata and SyncInfo
// are embedded without inline, the deployment model inlines the deployment
type metadataDocument struct {
	Metadata struct {
		CamundaDeploymentId string
		DeploymentModel     struct {
			models.Deployment `bson:",inline"`
		}
	}
	SyncInfo struct {
		NetworkId       string
		MarkedForDelete bool
	}
}

func (this *Mongo) FogDeployments(ctx context.Context) ([]FogDeployment, error) {
	documents, err := readAll[metadataDocument](ctx, this.client.Database(this.databases.ProcessSync).Collection("deployment_metadata"), bson.M{})
	if err != nil {
		return nil, err
	}
	result := make([]FogDeployment, 0, len(documents))
	for _, d := range documents {
		if d.SyncInfo.MarkedForDelete {
			continue
		}
		result = append(result, FogDeployment{NetworkId: d.SyncInfo.NetworkId, CamundaDeploymentId: d.Metadata.CamundaDeploymentId, Deployment: d.Metadata.DeploymentModel.Deployment})
	}
	return result, nil
}

type wardenDocument struct {
	NetworkId           string         `bson:"network_id"`
	BusinessKey         string         `bson:"business_key"`
	ProcessDeploymentId string         `bson:"process_deployment_id"`
	StartParameters     map[string]any `bson:"start_parameters"`
}

func (this *Mongo) FogStarts(ctx context.Context) ([]FogStart, error) {
	documents, err := readAll[wardenDocument](ctx, this.client.Database(this.databases.ProcessSync).Collection("warden"), bson.M{})
	if err != nil {
		return nil, err
	}
	result := make([]FogStart, 0, len(documents))
	for _, d := range documents {
		parameters := map[string]any{}
		for k, v := range d.StartParameters {
			parameters[k] = normalize(v)
		}
		result = append(result, FogStart{NetworkId: d.NetworkId, BusinessKey: d.BusinessKey, DeploymentId: d.ProcessDeploymentId, Parameters: parameters})
	}
	return result, nil
}
