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
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

const exportPrefix = TypeExport + "."

// exportHandler creates an export at serving (TASK-24); behavior of
// smart-service-module-worker-export.
type exportHandler struct {
	runtime *Runtime
}

// ServingRequest asks serving for an export. Filter conventions: for operatorId the filter is
// "<pipeline>:<operator>" and the topic "analytics-<operator name>", for import_id the import
// id and its kafka topic.
type ServingRequest struct {
	FilterType       string                `json:"FilterType,omitempty" validate:"required"`
	Filter           string                `json:"Filter,omitempty" validate:"required"`
	Name             string                `json:"Name,omitempty" validate:"required"`
	EntityName       string                `json:"EntityName,omitempty" validate:"required"`
	ServiceName      string                `json:"ServiceName,omitempty" validate:"required"`
	Description      string                `json:"Description,omitempty"`
	Topic            string                `json:"Topic,omitempty" validate:"required"`
	TimePath         string                `json:"TimePath,omitempty"`
	TimePrecision    string                `json:"TimePrecision,omitempty"`
	Generated        bool                  `json:"generated,omitempty"`
	Offset           string                `json:"Offset,omitempty" validate:"required"`
	ForceUpdate      bool                  `json:"ForceUpdate,omitempty"`
	Values           []ServingRequestValue `json:"Values,omitempty"`
	ExportDatabaseID string                `json:"ExportDatabaseID,omitempty"`
	TimestampFormat  string                `json:"TimestampFormat,omitempty"`
}

type ServingRequestValue struct {
	Name string `json:"Name,omitempty"`
	Type string `json:"Type,omitempty"`
	Path string `json:"Path,omitempty"`
	Tag  bool   `json:"Tag"`
}

// ExportInstance is the export as serving answers it; the module keeps it under "export" with
// the field names of serving.
type ExportInstance struct {
	ID               uuid.UUID
	Name             string
	Description      string
	EntityName       string
	ServiceName      string
	Topic            string
	ApplicationId    uuid.UUID
	Database         string
	Measurement      string
	Filter           string
	FilterType       string
	TimePath         string
	TimePrecision    *string
	UserId           string
	Generated        bool
	RancherServiceId string
	Offset           string
	ExportDatabaseID string
	ExportDatabase   ExportDatabase
	TimestampFormat  string
	Values           []ExportValue
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type ExportValue struct {
	InstanceID uuid.UUID
	Name       string
	Type       string
	Path       string
	Tag        bool
}

type ExportDatabase struct {
	ID            string
	Name          string
	Description   string
	Type          string
	Deployment    string
	Url           string
	EwFilterTopic string
	UserId        string
	Public        bool
}

func (this *exportHandler) Do(ctx context.Context, task Task) ([]model.Module, map[string]any, error) {
	name := exportPrefix + "request"
	raw, ok := task.String(name)
	if !ok {
		return nil, nil, errors.New("missing value in " + name)
	}
	var request ServingRequest
	if err := json.Unmarshal([]byte(raw), &request); err != nil {
		return nil, nil, fmt.Errorf("unable to interpret export request (%v): %w", name, err)
	}
	if err := validator.New().Struct(request); err != nil {
		return nil, nil, fmt.Errorf("invalid export request: %w", err)
	}
	var created ExportInstance
	client := &http.Client{Timeout: 5 * time.Second}
	if err := this.runtime.request(ctx, client, http.MethodPost, this.runtime.settings.ServingServiceUrl+"/instance", task.Owner, true, request, &created); err != nil {
		return nil, nil, err
	}
	id := created.ID.String()
	data := moduleData(task, exportPrefix)
	data["export"] = created
	module := model.Module{Id: task.ModuleId(), ModuleType: TypeExport, Data: data, Keys: []string{},
		DeleteInfo: &model.ModuleDeleteInfo{Url: this.runtime.settings.ServingServiceUrl + "/instance/" + url.PathEscape(id), UserId: task.Owner}}
	return []model.Module{module}, map[string]any{"export_id": id}, nil
}
