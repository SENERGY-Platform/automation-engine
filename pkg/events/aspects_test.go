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

package events_test

import (
	"reflect"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/devices"
	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/models/go/models"
)

func eventWith(attributes string) string {
	return b.Process(
		b.Start("start"),
		`<bpmn:intermediateCatchEvent id="reading" senergy:function="`+measuring+`" senergy:characteristic="c" senergy:script="x &gt; 20" senergy:value_variable_name="x" `+attributes+`><bpmn:messageEventDefinition/></bpmn:intermediateCatchEvent>`,
		b.Flow("start", "reading"),
	)
}

func describe(t *testing.T, describer *events.Describer, xml string, selection model.Selection) []model.EventDescription {
	t.Helper()
	process, _, err := bpmn.Parse(xml)
	if err != nil {
		t.Fatal(err)
	}
	result, _, err := describer.Describe(model.Deployment{Id: "dep", Namespace: "processes", Owner: "alice"}, 1, process,
		map[string]model.Selection{"reading": selection})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// The aspects of a device event reach its description in one list; the deprecated single
// aspect is an alias for a list with one element (event-deployment setSelectionCriteria).
func TestDescriptionCarriesTheAspectsOfTheEvent(t *testing.T) {
	cases := []struct {
		name       string
		attributes string
		expected   []string
	}{
		{"carries an aspect list", `senergy:aspects="aid1,aid2"`, []string{"aid1", "aid2"}},
		{"carries a deprecated single aspect as a list with one element", `senergy:aspect="aid"`, []string{"aid"}},
		{"gives a deprecated aspect and a one element list the same aspects", `senergy:aspects="aid"`, []string{"aid"}},
		{"carries both spellings when the event names both", `senergy:aspect="aid1" senergy:aspects="aid2"`, []string{"aid2", "aid1"}},
		{"leaves the aspects unset when the event names none", ``, nil},
	}
	describer := events.NewDescriber(&fakeRepo{}, aspects(), fakeImports{})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result := describe(t, describer, eventWith(c.attributes), model.Selection{DeviceId: "d1", ServiceId: "s-request"})
			if len(result) != 1 {
				t.Fatalf("expected one description, got %+v", result)
			}
			if !reflect.DeepEqual(result[0].AspectIds, c.expected) || result[0].FunctionId != measuring {
				t.Errorf("expected function %v with aspects %v, got %v with %v", measuring, c.expected, result[0].FunctionId, result[0].AspectIds)
			}
		})
	}
}

// aspectRepo offers a device type with one event service per combination of aspects
type aspectRepo struct{ fakeRepo }

func (*aspectRepo) GetDeviceType(devices.Token, string) (models.DeviceType, error) {
	service := func(id string, aspectIds ...string) models.Service {
		return models.Service{Id: id, Interaction: models.EVENT, Outputs: []models.Content{{ContentVariable: models.ContentVariable{
			Name: "value", FunctionId: measuring, AspectIds: aspectIds}}}}
	}
	return models.DeviceType{Id: "dt", Services: []models.Service{
		service("s-air", "air"),
		service("s-air-inside", "air", "inside"),
		service("s-inside", "inside"),
	}}, nil
}

// A device selected without service is expanded to the services that serve the function and
// every aspect of the event (event-deployment selectableCriteria).
func TestDeviceWithoutServiceIsExpandedToServicesWithAllAspects(t *testing.T) {
	describer := events.NewDescriber(&aspectRepo{}, aspects(), fakeImports{})
	cases := map[string]struct {
		attributes string
		expected   []string
	}{
		"aspect list":       {`senergy:aspects="air,inside"`, []string{"s-air-inside"}},
		"deprecated aspect": {`senergy:aspect="inside"`, []string{"s-air-inside", "s-inside"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := []string{}
			for _, d := range describe(t, describer, eventWith(c.attributes), model.Selection{DeviceId: "d1"}) {
				got = append(got, d.ServiceId)
			}
			if !reflect.DeepEqual(got, c.expected) {
				t.Errorf("expected %v, got %v", c.expected, got)
			}
		})
	}
}
