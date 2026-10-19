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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/models/go/models"
)

// In external-task-worker the retries of a sequential or round robin group count per device:
// a device that does not answer is given up after its attempts and the next device is asked,
// so that one offline device does not keep the others from being asked.

// external-task-worker TestSequentialExecution "0 retries 0 lost" and TestConstraintGroup
// "sequential, first missing"
func TestSequentialGroupAsksTheNextDeviceWhenOneDoesNotAnswer(t *testing.T) {
	e := newEnv(t, Sequential)
	ids := []string{"d0", "d1", "d2"}
	services := map[string][]models.Service{}
	for _, id := range ids {
		services[id] = []models.Service{serviceWith("s", models.REQUEST, true, variable("v", measuring))}
	}
	e.use(repoWith(ids, services), e.aspects, fakeMarshaller{}, Sequential)
	inst := e.start(e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_group_id": "g", "retries": 0}), ""), nil))
	answered := map[int]bool{0: true} // the first command is lost
	for range 10 {
		for i, s := range e.producer.messages() {
			if !answered[i] {
				answered[i] = true
				value, _ := strconv.Atoi(s.key[1:])
				e.respond(s, float64(value))
			}
		}
		e.advance(2 * time.Minute)
	}
	e.expectState(inst.Id, model.StateCompleted)
	if out := e.variable(inst.Id, "out"); !reflect.DeepEqual(out, []any{1.0, 2.0}) {
		t.Fatalf("the devices after the silent one answer: %#v", out)
	}
}

// external-task-worker TestGroupScheduler "simple sequential" and "simple round robin"
func TestGroupOneDeviceAtATimeGivesUpOnASilentDevice(t *testing.T) {
	for _, scheduler := range []string{Sequential, RoundRobin} {
		t.Run(scheduler, func(t *testing.T) {
			e := newEnv(t, scheduler)
			fiveDevices(e, scheduler)
			inst := e.start(e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_group_id": "g", "retries": 1}), ""), nil))
			answered := map[int]bool{}
			for range 10 {
				answerAs(e, answered)
				e.advance(2 * time.Minute)
			}
			e.expectState(inst.Id, model.StateCompleted)
			if out := e.variable(inst.Id, "out"); !reflect.DeepEqual(out, []any{0.0, 2.0, 3.0, 4.0}) {
				t.Fatalf("results of all answering devices in device order: %#v", out)
			}
		})
	}
}

// a single device gets one command per try and then becomes an incident (TASK-07)
func TestSequentialUnansweredDeviceBecomesAnIncidentAfterItsTries(t *testing.T) {
	e := newEnv(t, Sequential)
	inst := e.start(e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_id": "d1", "service_id": "s-get", "retries": 2}), ""), nil))
	for range 10 {
		e.advance(2 * time.Minute)
	}
	if got := len(e.producer.messages()); got != 3 {
		t.Errorf("expected 3 commands, got %v", got)
	}
	e.expectState(inst.Id, model.StateIncident)
}

// an error before any command is sent ends the task after the retries of the payload
func TestSequentialGroupThatCannotBeResolvedBecomesAnIncident(t *testing.T) {
	e := newEnv(t, Sequential)
	repo := repoWith(nil, nil)
	repo.groups["g"] = models.DeviceGroup{Id: "g", DeviceIds: []string{"unknown"}}
	e.use(repo, e.aspects, fakeMarshaller{}, Sequential)
	inst := e.start(e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_group_id": "g", "retries": 1}), ""), nil))
	e.expectState(inst.Id, model.StateRunning)
	e.advance(time.Second)
	e.expectState(inst.Id, model.StateIncident)
}

// a timeout of one device is no failed attempt; when no device answers within its tries the
// task becomes an incident
func TestSequentialGroupWithoutAnyAnswerBecomesAnIncident(t *testing.T) {
	e := newEnv(t, Sequential)
	inst := e.start(e.deploy(process(Pessimistic, payload(measuring, map[string]any{"device_group_id": "g", "retries": 1}), ""), nil))
	for range 10 {
		e.advance(2 * time.Minute)
	}
	if got := sentServices(e.producer.messages()); !reflect.DeepEqual(got, []string{"d1/s-get", "d1/s-get", "d2/s-get", "d2/s-get"}) {
		t.Errorf("two tries per device: %v", got)
	}
	got := e.expectState(inst.Id, model.StateIncident)
	if !strings.Contains(got.EndReason, "no answer from any device") {
		t.Errorf("unexpected reason %v", got.EndReason)
	}
}
