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

package deprecated

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/models/go/models"
)

// A device selection of the old deploy dialog survives the way into the engine and back,
// including the path and its configurables (DEP-05).
func TestDeviceSelectionMapsBothWays(t *testing.T) {
	device, service := "d1", "s1"
	old := models.Selection{SelectedDeviceId: &device, SelectedServiceId: &service, SelectedPath: &models.PathOption{
		Path: "value.level", CharacteristicId: "c", Configurables: []models.Configurable{{Path: "duration", CharacteristicId: "c2", FunctionId: "f", Value: 3.0, AspectNodes: []models.AspectNode{{Id: "a"}}}},
	}}
	selection, err := newSelection(old)
	if err != nil {
		t.Fatal(err)
	}
	expected := model.Selection{DeviceId: "d1", ServiceId: "s1", Path: &model.SelectedPath{Path: "value.level", CharacteristicId: "c",
		Configurables: []model.Configurable{{Path: "duration", CharacteristicId: "c2", FunctionId: "f", Value: 3.0, AspectNodeIds: []string{"a"}}}}}
	if !reflect.DeepEqual(selection, expected) {
		t.Fatalf("%+v", selection)
	}
	back := oldSelection(model.Criteria{FunctionId: "f", AspectIds: []string{"a"}}, selection, []model.SelectionOption{{
		Device: &model.NamedRef{Id: "d1", Name: "Lamp"}, Services: []model.NamedRef{{Id: "s1", Name: "set"}},
		PathOptions: map[string]json.RawMessage{"s1": json.RawMessage(`[{"path":"value.level","characteristicId":"c"}]`)},
	}})
	if *back.SelectedDeviceId != "d1" || back.SelectedPath.Configurables[0].AspectNode.Id != "a" || *back.FilterCriteria.AspectId != "a" {
		t.Errorf("%+v", back)
	}
	if o := back.SelectionOptions[0]; o.Device.Name != "Lamp" || o.Services[0].Name != "set" || o.PathOptions["s1"][0].Path != "value.level" {
		t.Errorf("options keep names and paths: %+v", o)
	}
	if _, err = newSelection(models.Selection{SelectedGenericEventSource: &models.GenericEventSource{}}); err == nil {
		t.Error("generic event sources are rejected")
	}
}
