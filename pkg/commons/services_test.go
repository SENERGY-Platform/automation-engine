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

package commons_test

import (
	"reflect"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/commons"
	"github.com/SENERGY-Platform/models/go/models"
)

// lampSet is the set service of a zigbee2mqtt lamp: one object with brightness and transition,
// both for the aspect light
func lampSet() []models.Content {
	return []models.Content{{ContentVariable: models.ContentVariable{Name: "value", Type: models.Structure, SubContentVariables: []models.ContentVariable{
		{Name: "brightness", Type: models.Integer, FunctionId: "set-brightness", AspectId: "light"},
		{Name: "transition", Type: models.Float, FunctionId: "set-transition", AspectId: "light"},
	}}}}
}

func TestMissingFunctionsIsEmptyWhenTheContentsServeEveryFunction(t *testing.T) {
	light := []models.AspectNode{{Id: "light"}}
	missing := commons.MissingFunctions(lampSet(), []commons.FunctionCriteria{{FunctionId: "set-brightness", Aspects: light}, {FunctionId: "set-transition", Aspects: light}})
	if len(missing) != 0 {
		t.Fatalf("expected no missing function, got %v", missing)
	}
}

func TestMissingFunctionsNamesEachFunctionOrAspectTheContentsDoNotServe(t *testing.T) {
	missing := commons.MissingFunctions(lampSet(), []commons.FunctionCriteria{
		{FunctionId: "set-brightness"},
		{FunctionId: "set-color"},
		{FunctionId: "set-transition", Aspects: []models.AspectNode{{Id: "fan"}}},
	})
	if !reflect.DeepEqual(missing, []int{1, 2}) {
		t.Fatalf("expected the indices 1 and 2, got %v", missing)
	}
}
