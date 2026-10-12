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
)

const importPrefix = TypeImport + "."

// importHandler creates an import at import-deploy (TASK-23); behavior of
// smart-service-module-worker-import.
type importHandler struct {
	runtime *Runtime
}

// ImportInstance is the import as import-deploy answers it; the module keeps it under "import".
type ImportInstance struct {
	Id           string         `json:"id"`
	Name         string         `json:"name"`
	ImportTypeId string         `json:"import_type_id"`
	Image        string         `json:"image"`
	KafkaTopic   string         `json:"kafka_topic"`
	Configs      []ImportConfig `json:"configs"`
	Restart      *bool          `json:"restart"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	Generated    bool           `json:"generated"`
}

type ImportConfig struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

func (this *importHandler) Do(ctx context.Context, task Task) ([]model.Module, map[string]any, error) {
	request, err := this.request(task)
	if err != nil {
		return nil, nil, err
	}
	var created ImportInstance
	client := &http.Client{Timeout: 5 * time.Minute}
	if err = this.runtime.request(ctx, client, http.MethodPost, this.runtime.settings.ImportDeployUrl+"/instances", task.Owner, true, request, &created); err != nil {
		return nil, nil, err
	}
	data := moduleData(task, importPrefix)
	data["import"] = created
	module := model.Module{Id: task.ModuleId(), ModuleType: TypeImport, Data: data, Keys: []string{},
		DeleteInfo: &model.ModuleDeleteInfo{Url: this.runtime.settings.ImportDeployUrl + "/instances/" + url.PathEscape(created.Id), UserId: task.Owner}}
	return []model.Module{module}, map[string]any{"import_id": created.Id}, nil
}

// request reads import.request and applies import.config.json_overwrite.<name> to existing configs
func (this *importHandler) request(task Task) (result ImportInstance, err error) {
	name := importPrefix + "request"
	raw, ok := task.String(name)
	if !ok {
		return result, errors.New("missing value in " + name)
	}
	if err = json.Unmarshal([]byte(raw), &result); err != nil {
		return result, fmt.Errorf("unable to interpret import request (%v): %w", name, err)
	}
	for i, config := range result.Configs {
		value, exists := task.Inputs[importPrefix+"config.json_overwrite."+config.Name]
		if !exists {
			continue
		}
		switch v := value.(type) {
		case string:
			if json.Unmarshal([]byte(v), &config.Value) != nil {
				config.Value = v
			}
		case float64, float32, int, int32, int64, bool:
			config.Value = v
		case map[string]any:
			config.Value = v["value"]
		default:
			return result, fmt.Errorf("unable to interpret import config overwriter %v %v (%v)", name, config.Name, v)
		}
		result.Configs[i] = config
	}
	return result, nil
}
