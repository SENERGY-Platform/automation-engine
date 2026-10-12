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
	"sort"
	"strings"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

const infoPrefix = TypeInfo + "."

// infoHandler writes module information, e.g. a dashboard widget, without a resource elsewhere
// (TASK-26); behavior of smart-service-module-worker-info.
type infoHandler struct {
	runtime *Runtime
}

func (this *infoHandler) Do(ctx context.Context, task Task) ([]model.Module, map[string]any, error) {
	data, err := this.moduleData(task)
	if err != nil {
		return nil, nil, err
	}
	if this.runtime.settings.InfoAdditionalModuleDataFields {
		for key, value := range this.additionalFields(task) {
			data[key] = value
		}
	}
	moduleType := TypeInfo
	if t, ok := task.String(infoPrefix + "module_type"); ok {
		moduleType = t
	}
	module := model.Module{Id: task.ModuleId(), ModuleType: moduleType, Data: data, Keys: []string{}}
	// a key replaces the module with that key, whatever its type
	if key, ok := task.String(infoPrefix + "key"); ok && key != "" {
		module.Keys = []string{key}
		existing, err := this.runtime.existing(ctx, task, key, "")
		if err != nil {
			return nil, nil, err
		}
		if len(existing) > 0 {
			if len(existing) > 1 {
				this.runtime.logger.WarnContext(ctx, "more than one module with the key", "key", key, "instance_id", task.RootInstanceId)
			}
			module.Id, module.CreatedAt = existing[0].Id, existing[0].CreatedAt
		}
	}
	return []model.Module{module}, map[string]any{}, nil
}

// moduleData joins the inputs info.module_data* in the order of their names and reads them as
// one JSON object, so that a large object can be split over several inputs
func (this *infoHandler) moduleData(task Task) (map[string]any, error) {
	type part struct{ key, value string }
	parts := []part{}
	for key, value := range task.Inputs {
		if strings.HasPrefix(key, infoPrefix+"module_data") {
			str, ok := value.(string)
			if !ok {
				return nil, errors.New("module_data is not string")
			}
			parts = append(parts, part{key, str})
		}
	}
	result := map[string]any{}
	if len(parts) == 0 {
		return result, nil
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].key < parts[j].key })
	joined := strings.Builder{}
	for _, p := range parts {
		joined.WriteString(p.value)
	}
	if err := json.Unmarshal([]byte(joined.String()), &result); err != nil {
		return nil, fmt.Errorf("invalid json for module_data: %w, (%v)", err, joined.String())
	}
	return result, nil
}

// additionalFields are the other info.* string inputs as module data, read as JSON where they
// are JSON. The worker stopped at the first input that was no string, in random order; here
// such inputs are skipped.
func (this *infoHandler) additionalFields(task Task) map[string]any {
	result := map[string]any{}
	for key, value := range task.Inputs {
		if !strings.HasPrefix(key, infoPrefix) || strings.HasPrefix(key, infoPrefix+"module_data") {
			continue
		}
		key = strings.TrimPrefix(key, infoPrefix)
		str, ok := value.(string)
		if !ok || key == "module_type" || key == "delete_info" || key == "key" {
			continue
		}
		var parsed any
		if json.Unmarshal([]byte(str), &parsed) != nil {
			result[key] = str
		} else {
			result[key] = parsed
		}
	}
	return result
}
