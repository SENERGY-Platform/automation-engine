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

package devicecommand

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/SENERGY-Platform/models/go/models"
)

// brightness is the primary controlling function of the local marshaller test (SNRGY-5157)
const brightness = "urn:infai:ses:controlling-function:set-brightness"

// A service whose one input is a JSON object with brightness and transition, each with its own
// controlling function, characteristic and aspect, gets both values in one message from the
// marshaller that runs in the process (FOG-01, SNRGY-5157).
func TestLocalMarshallerWritesEveryFunctionOfACommandIntoOneMessage(t *testing.T) {
	m := aspectMirror(t, map[string]models.AspectNode{"light": {Id: "light"}, "air": {Id: "air"}})
	service := models.Service{Id: "s-local", LocalId: "local-s-local", ProtocolId: "p", Interaction: models.REQUEST, Inputs: []models.Content{{
		ProtocolSegmentId: "seg", Serialization: models.JSON, ContentVariable: models.ContentVariable{Name: "value", Type: models.Structure, SubContentVariables: []models.ContentVariable{
			{Name: "brightness", Type: models.Integer, CharacteristicId: "percent", FunctionId: brightness, AspectIds: []string{"light"}},
			{Name: "transition", Type: models.Float, CharacteristicId: "seconds", FunctionId: transition, AspectIds: []string{"air"}},
		}},
	}}}
	repo := newRepo()
	repo.deviceTypes["dt"] = models.DeviceType{Id: "dt", Services: []models.Service{service}}
	e := newEnv(t, Parallel)
	e.use(repo, aspectTree(), m, Parallel)
	extra := `<camunda:inputParameter name="inputs:transition">3</camunda:inputParameter>`
	d := e.deploy(process(Optimistic, payload(brightness, map[string]any{"version": 3, "device_id": "d1", "service_id": "s-local", "characteristic_id": "percent",
		"aspects": []map[string]any{{"id": "light"}}, "input": 80,
		"additional_functions": []any{map[string]any{"name": "transition", "function": map[string]any{"id": transition}, "characteristic_id": "seconds",
			"aspects": []map[string]any{{"id": "air"}}}}}), extra), nil)
	e.start(d)
	list := e.producer.messages()
	if len(list) != 1 {
		t.Fatalf("one message for the command: %+v", list)
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(list[0].msg.Request.Input["data"]), &value); err != nil {
		t.Fatalf("the segment holds the structure as JSON: %v %v", err, list[0].msg.Request.Input)
	}
	if expected := map[string]any{"brightness": 80.0, "transition": 3.0}; !reflect.DeepEqual(value, expected) {
		t.Errorf("expected %v in the message, got %v", expected, value)
	}
}
