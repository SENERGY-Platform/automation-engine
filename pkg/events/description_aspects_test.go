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
	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// event-worker v1.2.0 pkg/model TestEventDescGetAspectIds: the deprecated single aspect of an
// event joins its aspect list. In the engine the description is derived from senergy:aspect
// and senergy:aspects of the event.
func TestDescriptionFoldsTheDeprecatedAspectIntoTheAspectList(t *testing.T) {
	cases := []struct {
		name       string
		attributes string
		expected   []string
	}{
		{"uses the deprecated aspect as a one element list", `senergy:aspect="aid"`, []string{"aid"}},
		{"uses the list when only the list is set", `senergy:aspects="aid1,aid2"`, []string{"aid1", "aid2"}},
		{"appends the deprecated aspect to a list that misses it", `senergy:aspect="aid1" senergy:aspects="aid2"`, []string{"aid2", "aid1"}},
		{"does not repeat a deprecated aspect the list already contains", `senergy:aspect="aid1" senergy:aspects="aid1,aid2"`, []string{"aid1", "aid2"}},
		{"returns nothing when no aspect is set", ``, nil},
	}
	describer := events.NewDescriber(&fakeRepo{}, aspects(), fakeImports{})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			process, _, err := bpmn.Parse(b.Process(b.Start("start"),
				`<bpmn:intermediateCatchEvent id="reading" senergy:function="`+measuring+`" `+c.attributes+` senergy:characteristic="c"><bpmn:messageEventDefinition/></bpmn:intermediateCatchEvent>`,
				b.Flow("start", "reading")))
			if err != nil {
				t.Fatal(err)
			}
			result, _, err := describer.Describe(model.Deployment{Id: "dep", Namespace: "processes", Owner: "alice"}, 1, process,
				map[string]model.Selection{"reading": {DeviceId: "d1", ServiceId: "urn:infai:ses:service:event"}})
			if err != nil || len(result) != 1 {
				t.Fatalf("%+v %v", result, err)
			}
			if !reflect.DeepEqual(result[0].AspectIds, c.expected) {
				t.Errorf("%#v", result[0].AspectIds)
			}
		})
	}
}
