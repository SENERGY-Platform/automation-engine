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
	"fmt"
	"reflect"

	"github.com/SENERGY-Platform/models/go/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// MongoDatabases names the databases of the old services; the defaults are those of the
// platform since the move to the central MongoDB.
type MongoDatabases struct {
	ProcessModels       string
	ProcessDeployments  string
	Scheduler           string
	SchedulerCollection string
	Io                  string
	SmartServices       string
	ProcessSync         string
}

func DefaultMongoDatabases() MongoDatabases {
	return MongoDatabases{ProcessModels: "process_repository", ProcessDeployments: "process_deployment", Scheduler: "process_scheduler",
		SchedulerCollection: "process_schedule", Io: "process_io",
		SmartServices: "smart_service_repository", ProcessSync: "process_sync"}
}

// Mongo reads the collections of the old services. The services wrote with the default codec
// of the MongoDB driver, which stores a field without bson tag under its lower-cased Go name
// and an embedded struct without inline as a sub-document. The documents here are decoded
// into structs with the same field names, so that they read back exactly as written.
type Mongo struct {
	client    *mongo.Client
	databases MongoDatabases
}

func OpenMongo(ctx context.Context, url string, databases MongoDatabases) (*Mongo, error) {
	registry := bson.NewRegistry()
	// documents in interface{} fields, e.g. path option values, read as maps, not as key lists
	registry.RegisterTypeMapEntry(bson.TypeEmbeddedDocument, reflect.TypeOf(bson.M{}))
	client, err := mongo.Connect(options.Client().ApplyURI(url).SetRegistry(registry))
	if err != nil {
		return nil, err
	}
	if err = client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(ctx)
		return nil, fmt.Errorf("mongodb: %w", err)
	}
	return &Mongo{client: client, databases: databases}, nil
}

func (this *Mongo) Close(ctx context.Context) error {
	return this.client.Disconnect(ctx)
}

func readAll[T any](ctx context.Context, collection *mongo.Collection, filter any) ([]T, error) {
	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("%v.%v: %w", collection.Database().Name(), collection.Name(), err)
	}
	var result []T
	if err = cursor.All(ctx, &result); err != nil {
		return nil, fmt.Errorf("%v.%v: %w", collection.Database().Name(), collection.Name(), err)
	}
	return result, nil
}

// processDocument is model.Process of process-model-repository
type processDocument struct {
	Id              string `bson:"_id"`
	Name            string `bson:"name"`
	Date            int64  `bson:"date"`
	Owner           string `bson:"owner"`
	BpmnXml         string `bson:"bpmn_xml"`
	SvgXml          string `bson:"svgXML"`
	Publish         bool   `bson:"publish"`
	PublishDate     string `bson:"publish_date"`
	Description     string `bson:"description"`
	LastUpdatedUnix int64  `bson:"last_updated_unix"`
}

func (this *Mongo) ProcessModels(ctx context.Context) ([]ProcessModel, error) {
	documents, err := readAll[processDocument](ctx, this.client.Database(this.databases.ProcessModels).Collection("process"), bson.M{})
	if err != nil {
		return nil, err
	}
	result := make([]ProcessModel, 0, len(documents))
	for _, d := range documents {
		result = append(result, ProcessModel{Id: d.Id, Name: d.Name, Owner: d.Owner, BpmnXml: d.BpmnXml, SvgXml: d.SvgXml, Description: d.Description,
			Publish: d.Publish, PublishDate: d.PublishDate, Date: d.Date, LastUpdatedUnix: d.LastUpdatedUnix})
	}
	return result, nil
}

// deploymentDocument is messages.DeploymentCommand of process-deployment with its sync marks
type deploymentDocument struct {
	Command    string
	Id         string
	Owner      string
	Deployment *models.Deployment
	Source     string
	Version    int64
	SyncDelete bool `bson:"sync_delete"`
}

func (this *Mongo) ProcessDeployments(ctx context.Context) ([]ProcessDeployment, error) {
	documents, err := readAll[deploymentDocument](ctx, this.client.Database(this.databases.ProcessDeployments).Collection("deployments"), bson.M{"sync_delete": bson.M{"$ne": true}})
	if err != nil {
		return nil, err
	}
	result := make([]ProcessDeployment, 0, len(documents))
	for _, d := range documents {
		if d.Deployment == nil {
			continue
		}
		result = append(result, ProcessDeployment{Id: d.Id, Owner: d.Owner, Source: d.Source, Deployment: *d.Deployment})
	}
	return result, nil
}

// scheduleDocument is model.ScheduleEntry of process-scheduler
type scheduleDocument struct {
	Id                  string  `bson:"id"`
	User                string  `bson:"user"`
	Cron                string  `bson:"cron"`
	ProcessDeploymentId string  `bson:"process_deployment_id"`
	ProcessAlias        *string `bson:"process_alias"`
	Disabled            *bool   `bson:"disabled"`
	CreatedBy           *string `bson:"created_by"`
}

func (this *Mongo) Schedules(ctx context.Context) ([]Schedule, error) {
	documents, err := readAll[scheduleDocument](ctx, this.client.Database(this.databases.Scheduler).Collection(this.databases.SchedulerCollection), bson.M{})
	if err != nil {
		return nil, err
	}
	result := make([]Schedule, 0, len(documents))
	for _, d := range documents {
		s := Schedule{Id: d.Id, User: d.User, Cron: d.Cron, DeploymentId: d.ProcessDeploymentId}
		if d.ProcessAlias != nil {
			s.Alias = *d.ProcessAlias
		}
		if d.Disabled != nil {
			s.Disabled = *d.Disabled
		}
		if d.CreatedBy != nil {
			s.CreatedBy = *d.CreatedBy
		}
		result = append(result, s)
	}
	return result, nil
}

// ioDocument is model.VariableWithUser of process-io-api: the embedded structs are
// sub-documents named after their types
type ioDocument struct {
	VariableWithUnixTimestamp struct {
		Variable struct {
			Key                 string
			Value               any
			ProcessDefinitionId string
			ProcessInstanceId   string
		}
		UnixTimestampInS int64
	}
	UserId string
}

func (this *Mongo) IoValues(ctx context.Context) ([]IoValue, error) {
	documents, err := readAll[ioDocument](ctx, this.client.Database(this.databases.Io).Collection("variables"), bson.M{})
	if err != nil {
		return nil, err
	}
	result := make([]IoValue, 0, len(documents))
	for _, d := range documents {
		v := d.VariableWithUnixTimestamp.Variable
		result = append(result, IoValue{UserId: d.UserId, Key: v.Key, Value: normalize(v.Value), DefinitionId: v.ProcessDefinitionId,
			InstanceId: v.ProcessInstanceId, Timestamp: d.VariableWithUnixTimestamp.UnixTimestampInS})
	}
	return result, nil
}

// normalize turns BSON values into JSON values: integers become numbers, arrays and documents
// plain slices and maps
func normalize(value any) any {
	switch v := value.(type) {
	case int32:
		return float64(v)
	case int64:
		return float64(v)
	case bson.A:
		result := make([]any, len(v))
		for i, item := range v {
			result[i] = normalize(item)
		}
		return result
	case bson.M:
		result := map[string]any{}
		for k, item := range v {
			result[k] = normalize(item)
		}
		return result
	case bson.D:
		result := map[string]any{}
		for _, e := range v {
			result[e.Key] = normalize(e.Value)
		}
		return result
	}
	return value
}
