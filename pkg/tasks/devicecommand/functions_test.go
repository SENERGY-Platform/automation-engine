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
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/models/go/models"
)

// transition is the additional controlling function of the payloads below (SNRGY-5157)
const transition = "urn:infai:ses:controlling-function:transition"

// functionRepo serves d1 with the services s-set (set-on only) and s-both (set-on and transition),
// d2 with s-set only, and the group g of both devices
func functionRepo() *fakeRepo {
	repo := newRepo()
	both := serviceWith("s-both", models.REQUEST, false, variable("power", controlling), variable("transition", transition))
	repo.deviceTypes["dt"] = models.DeviceType{Id: "dt", Services: []models.Service{service("s-set", models.REQUEST, controlling), both}}
	repo.devices["d2"] = models.Device{Id: "d2", LocalId: "local-d2", DeviceTypeId: "dt2"}
	repo.deviceTypes["dt2"] = models.DeviceType{Id: "dt2", Services: []models.Service{service("s-set", models.REQUEST, controlling)}}
	return repo
}

// transitionEntry is the additional function "transition" of a payload, with the characteristic
// seconds and the given input
func transitionEntry(input any) map[string]any {
	return map[string]any{"name": "transition", "function": map[string]any{"id": transition}, "characteristic_id": "seconds", "input": input}
}

func TestAdditionalFunctionsGoInOneMessageWithTheirInputs(t *testing.T) {
	t.Run("whole input", func(t *testing.T) {
		e := newEnv(t, Parallel)
		m := &recordingMarshaller{}
		e.use(functionRepo(), e.aspects, m, Parallel)
		extra := `<camunda:inputParameter name="inputs:transition">3</camunda:inputParameter>`
		d := e.deploy(process(Optimistic, payload(controlling, map[string]any{"version": 3, "device_id": "d1", "service_id": "s-both", "characteristic_id": "percent",
			"input": 80, "additional_functions": []any{transitionEntry(nil)}}), extra), nil)
		e.start(d)
		calls := m.v2Calls()
		if len(calls) != 1 || len(calls[0]) != 2 {
			t.Fatalf("the primary and the additional function are one marshalling request: %+v", calls)
		}
		primary, additional := calls[0][0], calls[0][1]
		if primary.FunctionId != controlling || primary.CharacteristicId != "percent" || primary.Value != 80.0 || len(primary.Paths) != 0 {
			t.Errorf("the primary function keeps its input: %+v", primary)
		}
		if additional.FunctionId != transition || additional.CharacteristicId != "seconds" || additional.Value != 3.0 || len(additional.Paths) != 0 {
			t.Errorf("the additional function takes inputs:transition without path: %+v", additional)
		}
		if list := e.producer.messages(); len(list) != 1 || list[0].key != "d1" || list[0].msg.Metadata.Version != 3 || list[0].msg.Metadata.Service.Id != "s-both" {
			t.Fatalf("one command to the service serving both functions: %+v", list)
		}
	})
	t.Run("path in structured input", func(t *testing.T) {
		e := newEnv(t, Parallel)
		m := &recordingMarshaller{}
		e.use(functionRepo(), e.aspects, m, Parallel)
		extra := `<camunda:inputParameter name="inputs:transition.duration">7</camunda:inputParameter>`
		d := e.deploy(process(Optimistic, payload(controlling, map[string]any{"version": 3, "device_id": "d1", "service_id": "s-both", "input": 80,
			"additional_functions": []any{transitionEntry(map[string]any{"duration": 5, "unit": "s"})}}), extra), nil)
		e.start(d)
		calls := m.v2Calls()
		if len(calls) != 1 || len(calls[0]) != 2 {
			t.Fatalf("one marshalling request: %+v", calls)
		}
		expected := map[string]any{"duration": 7.0, "unit": "s"}
		if !reflect.DeepEqual(calls[0][1].Value, expected) {
			t.Errorf("the path sets the duration and keeps the rest of the additional input: %#v", calls[0][1].Value)
		}
	})
}

func TestInputOfAnUnknownAdditionalFunctionIsIgnored(t *testing.T) {
	e := newEnv(t, Parallel)
	m := &recordingMarshaller{}
	e.use(functionRepo(), e.aspects, m, Parallel)
	extra := `<camunda:inputParameter name="inputs:unknown">1</camunda:inputParameter>`
	d := e.deploy(process(Optimistic, payload(controlling, map[string]any{"version": 3, "device_id": "d1", "service_id": "s-both", "input": 80,
		"additional_functions": []any{transitionEntry(2)}}), extra), nil)
	e.start(d)
	calls := m.v2Calls()
	if len(calls) != 1 || len(calls[0]) != 2 || calls[0][1].Value != 2.0 {
		t.Fatalf("the unknown input changes nothing, the command goes out: %+v", calls)
	}
	if list := e.producer.messages(); len(list) != 1 {
		t.Fatalf("the command is sent nonetheless: %+v", list)
	}
}

func TestVersion2CommandWithAdditionalFunctionsIsMarshalledAsVersion3(t *testing.T) {
	e := newEnv(t, Parallel)
	m := &recordingMarshaller{}
	e.use(functionRepo(), e.aspects, m, Parallel)
	d := e.deploy(process(Optimistic, payload(controlling, map[string]any{"device_id": "d1", "service_id": "s-both", "input": 80,
		"additional_functions": []any{transitionEntry(2)}}), ""), nil)
	e.start(d)
	if len(m.v1Calls()) != 0 || len(m.v2Calls()) != 1 || len(m.v2Calls()[0]) != 2 {
		t.Fatalf("the command goes through marshaller v2 in one request: v1 %+v v2 %+v", m.v1Calls(), m.v2Calls())
	}
	if list := e.producer.messages(); len(list) != 1 || list[0].msg.Metadata.Version != 3 {
		t.Fatalf("the command goes out as version 3: %+v", list)
	}
}

func TestDeviceWithoutServiceAddressesOnlyTheServiceServingEveryFunction(t *testing.T) {
	e := newEnv(t, Parallel)
	m := &recordingMarshaller{}
	e.use(functionRepo(), e.aspects, m, Parallel)
	d := e.deploy(process(Optimistic, payload(controlling, map[string]any{"version": 3, "device_id": "d1", "input": 80,
		"additional_functions": []any{transitionEntry(2)}}), ""), nil)
	e.start(d)
	list := e.producer.messages()
	if len(list) != 1 || list[0].msg.Metadata.Service.Id != "s-both" {
		t.Fatalf("the service s-set lacks the transition, only s-both is sent to: %+v", list)
	}
}

func TestDeviceGroupSendsToTheMemberWhoseServiceServesEveryFunction(t *testing.T) {
	e := newEnv(t, Parallel)
	m := &recordingMarshaller{}
	e.use(functionRepo(), e.aspects, m, Parallel)
	d := e.deploy(process(Optimistic, payload(controlling, map[string]any{"version": 3, "device_group_id": "g", "input": 80,
		"additional_functions": []any{transitionEntry(2)}}), ""), nil)
	e.start(d)
	list := e.producer.messages()
	if len(list) != 1 || list[0].key != "d1" || list[0].msg.Metadata.Service.Id != "s-both" {
		t.Fatalf("d1 serves both functions, d2 is skipped: %+v", list)
	}
}

func TestFixedServiceWithoutAnAdditionalFunctionBecomesAnIncidentWithoutRetry(t *testing.T) {
	e := newEnv(t, Parallel)
	m := &recordingMarshaller{}
	e.use(functionRepo(), e.aspects, m, Parallel)
	d := e.deploy(process(Pessimistic, payload(controlling, map[string]any{"version": 3, "device_id": "d1", "service_id": "s-set", "input": 80,
		"additional_functions": []any{transitionEntry(2)}}), ""), nil)
	inst := e.start(d)
	got := e.expectState(inst.Id, model.StateIncident)
	const expected = "service s-set of device d1 does not serve transition (urn:infai:ses:controlling-function:transition)"
	if got.EndReason != expected {
		t.Errorf("expected the reason %q, got %q", expected, got.EndReason)
	}
	e.advance(time.Minute)
	if len(m.v2Calls()) != 0 || len(e.producer.messages()) != 0 {
		t.Errorf("nothing is marshalled or sent, and no retry follows: %+v %+v", m.v2Calls(), e.producer.messages())
	}
}

func TestMeasuringPrimaryFunctionWithAdditionalFunctionsBecomesAnIncidentWithoutRetry(t *testing.T) {
	e := newEnv(t, Parallel)
	m := &recordingMarshaller{}
	e.use(functionRepo(), e.aspects, m, Parallel)
	d := e.deploy(process(Pessimistic, payload(measuring, map[string]any{"version": 3, "device_id": "d1", "service_id": "s-get",
		"additional_functions": []any{transitionEntry(nil)}}), ""), nil)
	inst := e.start(d)
	got := e.expectState(inst.Id, model.StateIncident)
	const expected = "a device command with additional functions takes only controlling functions, " + measuring + " is not one"
	if got.EndReason != expected {
		t.Errorf("expected the reason %q, got %q", expected, got.EndReason)
	}
	if len(m.v2Calls()) != 0 || len(e.producer.messages()) != 0 {
		t.Errorf("nothing is marshalled or sent: %+v", e.producer.messages())
	}
}

func TestAdditionalMeasuringFunctionBecomesAnIncidentWithoutRetry(t *testing.T) {
	e := newEnv(t, Parallel)
	m := &recordingMarshaller{}
	e.use(functionRepo(), e.aspects, m, Parallel)
	entry := map[string]any{"name": "temperature", "function": map[string]any{"id": measuring}, "characteristic_id": "celsius"}
	d := e.deploy(process(Pessimistic, payload(controlling, map[string]any{"version": 3, "device_id": "d1", "service_id": "s-both", "input": 80,
		"additional_functions": []any{entry}}), ""), nil)
	inst := e.start(d)
	got := e.expectState(inst.Id, model.StateIncident)
	if !strings.Contains(got.EndReason, "takes only controlling functions, "+measuring+" is not one") {
		t.Errorf("the measuring additional function is named: %q", got.EndReason)
	}
	if len(m.v2Calls()) != 0 || len(e.producer.messages()) != 0 {
		t.Errorf("nothing is marshalled or sent: %+v", e.producer.messages())
	}
}

// the configurables before version 3 go only through the marshalling of version 2, which a
// command with additional functions leaves behind; they are refused rather than dropped
func TestConfigurablesBeforeVersion3WithAdditionalFunctionsBecomeAnIncidentWithoutRetry(t *testing.T) {
	e := newEnv(t, Parallel)
	m := &recordingMarshaller{}
	e.use(functionRepo(), e.aspects, m, Parallel)
	d := e.deploy(process(Pessimistic, payload(controlling, map[string]any{"version": 2, "device_id": "d1", "service_id": "s-both", "input": 80,
		"configurables": []any{map[string]any{"path": "duration"}}, "additional_functions": []any{transitionEntry(nil)}}), ""), nil)
	inst := e.start(d)
	got := e.expectState(inst.Id, model.StateIncident)
	const expected = "a device command with additional functions takes no configurables before version 3"
	if got.EndReason != expected {
		t.Errorf("expected the reason %q, got %q", expected, got.EndReason)
	}
	if len(m.v2Calls()) != 0 || len(e.producer.messages()) != 0 {
		t.Errorf("nothing is marshalled or sent: %+v", e.producer.messages())
	}
}
