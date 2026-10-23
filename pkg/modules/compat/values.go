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

package compat

import (
	"encoding/json"
	"strings"
)

// parsedValue undoes the JSON text the platform kept values in for Camunda: a text holding a
// JSON object or list becomes that value, and so does each such text in a list, e.g. the
// selections of a multiple IoT parameter. Plain texts, numbers and booleans stay, so that a
// text "42" remains a text.
func parsedValue(value any) any {
	switch v := value.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
			return v
		}
		var parsed any
		if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
			return v
		}
		if list, ok := parsed.([]any); ok {
			return parsedList(list)
		}
		return parsed
	case []any:
		return parsedList(v)
	}
	return value
}

func parsedList(list []any) []any {
	result := make([]any, len(list))
	for i, element := range list {
		if text, ok := element.(string); ok {
			result[i] = parsedValue(text)
		} else {
			result[i] = element
		}
	}
	return result
}
