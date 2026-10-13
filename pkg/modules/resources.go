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
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

var importTypeIdPattern = regexp.MustCompile(`"import_type_id"\s*:\s*("(?:[^"\\]|\\.)*")`)

// UsedResources reads the process models, flows and import types the module tasks of a process
// name as literal text, as smart-service-repository indexes its releases: an id in an
// expression or a script is not known before the task runs (SMS-15).
func UsedResources(process *bpmn.Process) model.UsedResources {
	result := model.UsedResources{ProcessModels: []string{}, Flows: []string{}, ImportTypes: []string{}}
	for _, e := range process.Ordered() {
		for _, input := range e.Inputs {
			if input.Value.Kind != bpmn.ValueText {
				continue
			}
			value := strings.TrimSpace(input.Value.Text)
			switch {
			case e.TaskType == TypeProcessDeployment && input.Name == TypeProcessDeployment+".process_model_id" && literal(value):
				result.ProcessModels = append(result.ProcessModels, value)
			case e.TaskType == TypeAnalytics && input.Name == TypeAnalytics+".flow_id" && literal(value):
				result.Flows = append(result.Flows, value)
			case e.TaskType == TypeImport && input.Name == TypeImport+".request":
				result.ImportTypes = append(result.ImportTypes, importTypeIds(value)...)
			}
		}
	}
	for _, list := range []*[]string{&result.ProcessModels, &result.Flows, &result.ImportTypes} {
		slices.Sort(*list)
		*list = slices.Compact(*list)
	}
	return result
}

func literal(value string) bool {
	return value != "" && !strings.Contains(value, "${") && !strings.Contains(value, "{{")
}

// importTypeIds reads import_type_id of an import request, which may be JSON or a template
// that only parses after rendering
func importTypeIds(request string) (result []string) {
	var parsed map[string]any
	if json.Unmarshal([]byte(request), &parsed) == nil {
		if id, ok := parsed["import_type_id"].(string); ok && literal(strings.TrimSpace(id)) {
			result = append(result, strings.TrimSpace(id))
		}
		return result
	}
	for _, match := range importTypeIdPattern.FindAllStringSubmatch(request, -1) {
		var id string
		if json.Unmarshal([]byte(match[1]), &id) == nil && literal(strings.TrimSpace(id)) {
			result = append(result, strings.TrimSpace(id))
		}
	}
	return result
}
