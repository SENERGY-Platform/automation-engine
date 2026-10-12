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

// Package devices holds what device commands and device events share about services.
package devices

import (
	marshallermodel "github.com/SENERGY-Platform/marshaller/lib/marshaller/model"
	"github.com/SENERGY-Platform/models/go/models"
)

// ContentsMatch reports whether a content variable serves the function and every requested
// aspect, by the aspect itself or a descendant; without aspects the function alone decides.
// This follows the service filter of external-task-worker v0.1.2 lib/devicegroups.
func ContentsMatch(contents []models.Content, functionId string, aspects []models.AspectNode) bool {
	for _, content := range contents {
		if variableMatches(content.ContentVariable, functionId, aspects) {
			return true
		}
	}
	return false
}

func variableMatches(variable models.ContentVariable, functionId string, aspects []models.AspectNode) bool {
	if variable.FunctionId == functionId && marshallermodel.AspectMatchLevel(marshallermodel.ContentVariableAspectIds(variable), aspects) >= 0 {
		return true
	}
	for _, sub := range variable.SubContentVariables {
		if variableMatches(sub, functionId, aspects) {
			return true
		}
	}
	return false
}

// ServiceIdToTopic is the Kafka topic of the data of a service.
func ServiceIdToTopic(id string) string {
	out := []rune{}
	for _, r := range id {
		if r == ':' || r == '#' {
			r = '_'
		}
		out = append(out, r)
	}
	return string(out)
}
