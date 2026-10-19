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
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/models/go/models"
)

func sentServices(list []sent) []string {
	result := []string{}
	for _, s := range list {
		result = append(result, s.key+"/"+s.msg.Metadata.Service.Id)
	}
	slices.Sort(result)
	return result
}

// external-task-worker lib/devicegroups TestGetGroupSubTasksAspects and
// TestMultiAspectGroupCommandReachesOnlyDevicesServingEveryAspect
func TestDeviceGroupAddressesServicesServingTheFunctionAndEveryAspect(t *testing.T) {
	legacy := variable("legacy", measuring)
	legacy.AspectId = "air"
	services := []models.Service{
		serviceWith("s_air", models.REQUEST, true, variable("v", measuring, "air")),
		serviceWith("s_air_humidity", models.REQUEST, true, variable("v", measuring, "air", "humidity")),
		serviceWith("s_humidity", models.REQUEST, true, variable("v", measuring, "humidity")),
		serviceWith("s_inside_air", models.REQUEST, true, variable("v", measuring, "inside_air")),
		serviceWith("s_kitchen_light", models.REQUEST, true, variable("v", measuring, "kitchen_air", "light")),
		serviceWith("s_outside_light", models.REQUEST, true, variable("v", measuring, "outside_air", "light")),
		serviceWith("s_legacy_air", models.REQUEST, true, legacy),
		serviceWith("s_other_function", models.REQUEST, true, variable("v", measuring+"-other", "air")),
	}
	cases := []struct {
		name     string
		aspects  map[string]any
		expected []string
	}{
		{"every service serving the aspect or one of its descendants", map[string]any{"aspects": []map[string]any{{"id": "air"}}},
			[]string{"d1/s_air", "d1/s_air_humidity", "d1/s_inside_air", "d1/s_kitchen_light", "d1/s_legacy_air", "d1/s_outside_light"}},
		{"only services serving every named aspect", map[string]any{"aspects": []map[string]any{{"id": "air"}, {"id": "humidity"}}},
			[]string{"d1/s_air_humidity"}},
		{"the subtree of each named aspect", map[string]any{"aspects": []map[string]any{{"id": "inside_air"}, {"id": "light"}}},
			[]string{"d1/s_kitchen_light"}},
		{"the deprecated single aspect like a list with one element", map[string]any{"aspect": map[string]any{"id": "air"}},
			[]string{"d1/s_air", "d1/s_air_humidity", "d1/s_inside_air", "d1/s_kitchen_light", "d1/s_legacy_air", "d1/s_outside_light"}},
		{"every service of the function without a named aspect", map[string]any{},
			[]string{"d1/s_air", "d1/s_air_humidity", "d1/s_humidity", "d1/s_inside_air", "d1/s_kitchen_light", "d1/s_legacy_air", "d1/s_outside_light"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, Parallel)
			e.use(repoWith([]string{"d1"}, map[string][]models.Service{"d1": services}), aspectTree(), fakeMarshaller{}, Parallel)
			fields := map[string]any{"device_group_id": "g"}
			for k, v := range c.aspects {
				fields[k] = v
			}
			e.start(e.deploy(process(Optimistic, payload(measuring, fields), ""), nil))
			if got := sentServices(e.producer.messages()); !reflect.DeepEqual(got, c.expected) {
				t.Errorf("%v", got)
			}
		})
	}
}

// external-task-worker TestGroupResponse (SNRGY-1807)
func TestDeviceGroupCollectsTheAnswersOfEveryMatchingServiceOfEveryDevice(t *testing.T) {
	e := newEnv(t, Parallel)
	// the services serve a descendant of the requested aspect
	matching := func(id string) models.Service {
		return serviceWith(id, models.REQUEST, true, variable("v", measuring, "inside_air"))
	}
	e.use(repoWith([]string{"d1", "d2", "d3"}, map[string][]models.Service{
		"d1": {matching("s1"), matching("s2")},
		"d2": {matching("s1"), matching("s2")},
		"d3": {matching("s3"), serviceWith("s4", models.REQUEST, true, variable("v", measuring+"-other", "humidity"))},
	}), aspectTree(), fakeMarshaller{}, Parallel)
	inst := e.start(e.deploy(process(Pessimistic, payload(measuring, map[string]any{"version": 3, "device_group_id": "g", "aspect": map[string]any{"id": "air"}}), ""), nil))
	list := e.producer.messages()
	if got := sentServices(list); !reflect.DeepEqual(got, []string{"d1/s1", "d1/s2", "d2/s1", "d2/s2", "d3/s3"}) {
		t.Fatalf("one command per device and matching service: %v", got)
	}
	for i, s := range list {
		e.respond(s, float64(i))
	}
	e.expectState(inst.Id, model.StateCompleted)
	if out, ok := e.variable(inst.Id, "out").([]any); !ok || len(out) != 5 {
		t.Fatalf("the result lists every answer: %#v", e.variable(inst.Id, "out"))
	}
}

// external-task-worker TestWorkerGroupCommand
func TestControllingCommandToADeviceGroupSendsTheInputToEveryMatchingService(t *testing.T) {
	e := newEnv(t, Parallel)
	m := &recordingMarshaller{}
	setter := func(id string) models.Service {
		return serviceWith(id, models.REQUEST, false, variable("kelvin", controlling))
	}
	e.use(repoWith([]string{"d1"}, map[string][]models.Service{"d1": {setter("s1"), setter("s2"), serviceWith("s3", models.REQUEST, true, variable("v", measuring))}}),
		e.aspects, m, Parallel)
	inst := e.start(e.deploy(process(Pessimistic, payload(controlling, map[string]any{"version": 3, "device_group_id": "g", "input": 5641}), ""), nil))
	list := e.producer.messages()
	if got := sentServices(list); !reflect.DeepEqual(got, []string{"d1/s1", "d1/s2"}) {
		t.Fatalf("services whose inputs serve the function: %v", got)
	}
	for _, s := range list {
		if s.msg.Request.Input["data"] != "5641" {
			t.Errorf("every service gets the input: %v", s.msg.Request.Input)
		}
	}
	for _, s := range list {
		e.respond(s, nil)
	}
	e.expectState(inst.Id, model.StateCompleted)
}

// external-task-worker TestDeviceWithoutServiceResponses, TestWorkerDeviceWithoutServiceCommand
func TestDeviceWithoutServiceAddressesEveryMatchingService(t *testing.T) {
	e := newEnv(t, Parallel)
	e.use(repoWith([]string{"d1"}, map[string][]models.Service{"d1": {
		serviceWith("s1", models.REQUEST, true, variable("v", measuring, "air")),
		serviceWith("s2", models.REQUEST, true, variable("v", measuring, "air")),
		serviceWith("s3", models.REQUEST, true, variable("v", measuring, "humidity")),
	}}), aspectTree(), fakeMarshaller{}, Parallel)
	inst := e.start(e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_id": "d1", "aspect": map[string]any{"id": "air"}}), ""), nil))
	list := e.producer.messages()
	if got := sentServices(list); !reflect.DeepEqual(got, []string{"d1/s1", "d1/s2"}) {
		t.Fatalf("one command per matching service: %v", got)
	}
	e.respond(list[0], 0.0)
	e.respond(list[1], 1.0)
	e.expectState(inst.Id, model.StateCompleted)
	if out := e.variable(inst.Id, "out"); !reflect.DeepEqual(out, []any{0.0, 1.0}) {
		t.Fatalf("the result lists both answers: %#v", out)
	}
}

func eventServices(ids ...string) []models.Service {
	result := []models.Service{}
	for _, id := range ids {
		result = append(result, serviceWith(id, models.EVENT, true, variable("v", measuring, "air")))
	}
	return result
}

// external-task-worker TestDeviceEventWithoutServiceResponses, TestWorkerEventDeviceWithoutServiceCommand
func TestDeviceWithoutServiceReadsEveryMatchingEventService(t *testing.T) {
	e := newEnv(t, Parallel)
	e.use(repoWith([]string{"d1"}, map[string][]models.Service{"d1": eventServices("s1", "s2")}), aspectTree(), fakeMarshaller{}, Parallel)
	inst := e.start(e.deploy(process(Pessimistic, payload(measuring, map[string]any{"version": 3, "device_id": "d1", "aspect": map[string]any{"id": "air"}}), ""), nil))
	e.expectState(inst.Id, model.StateCompleted)
	if out, ok := e.variable(inst.Id, "out").([]any); !ok || len(out) != 2 || out[0] == nil || out[1] == nil {
		t.Fatalf("the last value of each event service is a result (TASK-08): %#v", e.variable(inst.Id, "out"))
	}
	if len(e.producer.messages()) != 0 {
		t.Errorf("no command for event services: %+v", e.producer.messages())
	}
}

// external-task-worker TestGroupEventResponses, TestGroupEventResponsesWithMemcached
func TestDeviceGroupOfEventServicesReadsEveryLastValue(t *testing.T) {
	e := newEnv(t, Parallel)
	e.use(repoWith([]string{"d1", "d2"}, map[string][]models.Service{"d1": eventServices("s1", "s2"), "d2": eventServices("s3")}), aspectTree(), fakeMarshaller{}, Parallel)
	inst := e.start(e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_group_id": "g", "aspect": map[string]any{"id": "air"}}), ""), nil))
	e.expectState(inst.Id, model.StateCompleted)
	if out, ok := e.variable(inst.Id, "out").([]any); !ok || len(out) != 3 {
		t.Fatalf("one value per device and event service: %#v", e.variable(inst.Id, "out"))
	}
	if len(e.producer.messages()) != 0 {
		t.Errorf("no command for event services: %+v", e.producer.messages())
	}
}

func eventAndRequestServices(ids ...string) []models.Service {
	result := []models.Service{}
	for _, id := range ids {
		result = append(result, serviceWith(id, models.EVENT_AND_REQUEST, true, variable("v", measuring, "air")))
	}
	return result
}

// external-task-worker TestWorkerPreferEventResponseV2
func TestPreferEventReadsTheLastValueOfAnEventAndRequestService(t *testing.T) {
	e := newEnv(t, Parallel)
	e.use(repoWith([]string{"d1"}, map[string][]models.Service{"d1": eventAndRequestServices("s1")}), aspectTree(), fakeMarshaller{}, Parallel)
	inst := e.start(e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_id": "d1", "service_id": "s1", "prefer_event": true}), ""), nil))
	e.expectState(inst.Id, model.StateCompleted)
	if len(e.producer.messages()) != 0 {
		t.Errorf("the value is read instead of requested (TASK-08): %+v", e.producer.messages())
	}
}

// external-task-worker TestWorkerPreferEventResponseV2
func TestEventAndRequestServiceWithoutPreferEventIsAskedByCommand(t *testing.T) {
	e := newEnv(t, Parallel)
	e.use(repoWith([]string{"d1"}, map[string][]models.Service{"d1": eventAndRequestServices("s1")}), aspectTree(), fakeMarshaller{}, Parallel)
	inst := e.start(e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_id": "d1", "service_id": "s1"}), ""), nil))
	list := e.producer.messages()
	if len(list) != 1 {
		t.Fatalf("a command is sent: %+v", list)
	}
	e.respond(list[0], 3.0)
	e.expectState(inst.Id, model.StateCompleted)
	if out := e.variable(inst.Id, "out"); !reflect.DeepEqual(out, []any{3.0}) {
		t.Errorf("the answer is the result: %#v", out)
	}
}

// external-task-worker TestGroupPreferEventResponses; device and service next to the group
// are ignored there
func TestDeviceGroupWithPreferEventReadsEveryLastValue(t *testing.T) {
	e := newEnv(t, Parallel)
	e.use(repoWith([]string{"d1", "d2"}, map[string][]models.Service{"d1": eventAndRequestServices("s1", "s2"), "d2": eventAndRequestServices("s3")}), aspectTree(), fakeMarshaller{}, Parallel)
	inst := e.start(e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_group_id": "g", "device_id": "d1", "service_id": "s1",
		"aspect": map[string]any{"id": "air"}, "prefer_event": true}), ""), nil))
	e.expectState(inst.Id, model.StateCompleted)
	if out, ok := e.variable(inst.Id, "out").([]any); !ok || len(out) != 3 {
		t.Fatalf("the group, not the device, is read: %#v", e.variable(inst.Id, "out"))
	}
	if len(e.producer.messages()) != 0 {
		t.Errorf("no command is sent: %+v", e.producer.messages())
	}
}

// fiveDevices is the group of external-task-worker TestGroupScheduler: d0 to d4 with one
// requestable service each
func fiveDevices(e *env, scheduler string) {
	ids := []string{}
	services := map[string][]models.Service{}
	for i := range 5 {
		id := "d" + strconv.Itoa(i)
		ids = append(ids, id)
		services[id] = []models.Service{serviceWith("s", models.REQUEST, true, variable("v", measuring))}
	}
	e.use(repoWith(ids, services), e.aspects, fakeMarshaller{}, scheduler)
}

// answerAs answers every command of a device not yet answered; d1 never answers, d2 only
// its second command
func answerAs(e *env, answered map[int]bool) {
	for i, s := range e.producer.messages() {
		if answered[i] {
			continue
		}
		answered[i] = true
		device := s.key
		if device == "d1" || (device == "d2" && !answered[-1]) {
			if device == "d2" {
				answered[-1] = true
			}
			continue
		}
		value, _ := strconv.Atoi(device[1:])
		e.respond(s, float64(value))
	}
}

// external-task-worker TestGroupScheduler "simple parallel": with one retry, the second
// attempt resends only what is unanswered; the last timeout completes with what arrived
func TestParallelGroupRetryResendsOnlyTheDevicesWithoutAnswer(t *testing.T) {
	e := newEnv(t, Parallel)
	fiveDevices(e, Parallel)
	inst := e.start(e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_group_id": "g", "retries": 1}), ""), nil))
	answered := map[int]bool{}
	answerAs(e, answered)
	e.advance(2 * time.Minute)
	e.advance(time.Second)
	if got := sentServices(e.producer.messages()[5:]); !reflect.DeepEqual(got, []string{"d1/s", "d2/s"}) {
		t.Fatalf("the retry resends the unanswered commands: %v", got)
	}
	answerAs(e, answered)
	e.advance(2 * time.Minute)
	e.expectState(inst.Id, model.StateCompleted)
	if out := e.variable(inst.Id, "out"); !reflect.DeepEqual(out, []any{0.0, 2.0, 3.0, 4.0}) {
		t.Fatalf("results of all answering devices in device order: %#v", out)
	}
}
