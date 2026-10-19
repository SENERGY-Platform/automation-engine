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
	"reflect"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/devices"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/models/go/models"
)

// The marshaller sets only the variables of a service that carry the aspect of the task, so
// the task has to name its aspects in the request, resolved to their nodes (process-deployment
// TestAspectTaskCommand, SNRGY-4845).
func TestCommandAsksTheMarshallerWithTheResolvedAspectsOfTheTask(t *testing.T) {
	e := newEnv(t, Parallel)
	aspects := devices.NewAspectNodesWith(func(_ devices.Token, id string) (models.AspectNode, error) {
		return models.AspectNode{Id: id, Name: "name of " + id, RootId: id}, nil
	})
	marshaller := &recordingMarshaller{}
	e.dc = New(Config{GroupScheduler: Parallel, ResponseTo: "response", ErrorTo: "errors"}, e.s, newRepo(), aspects, marshaller, fakeTimescale{}, e.producer, e.metrics, e.logger)
	e.dc.SetCompleter(e.e)
	e.e.RegisterTaskType(TaskType, e.dc)

	command := payload(controlling, map[string]any{"version": 3, "input": 21, "aspects": []map[string]any{{"id": "reading"}, {"id": "inside"}}})
	e.start(e.deploy(process(Optimistic, command, ""), map[string]model.Selection{"cmd": {DeviceId: "d1", ServiceId: "s-set"}}))

	var data []MarshallingV2RequestData
	for _, call := range marshaller.v2Calls() {
		data = append(data, call...)
	}
	if len(data) != 1 {
		t.Fatalf("expected one value to set, got %+v", data)
	}
	expected := []models.AspectNode{{Id: "reading", Name: "name of reading", RootId: "reading"}, {Id: "inside", Name: "name of inside", RootId: "inside"}}
	if !reflect.DeepEqual(data[0].AspectNodes, expected) {
		t.Errorf("expected %+v, got %+v", expected, data[0].AspectNodes)
	}
}
