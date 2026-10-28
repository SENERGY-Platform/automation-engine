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
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/models/go/models"
)

// memberProcess waits for the device event and keeps the device and service that triggered it
func memberProcess() string {
	return b.Process(
		b.Start("start"), b.Gateway("exclusive", "loop", ""),
		`<bpmn:intermediateCatchEvent id="reading" senergy:function="`+measuring+`" senergy:aspect="air" senergy:characteristic="c" senergy:script="x &gt; 20" senergy:value_variable_name="x" senergy:qos="1">`+
			b.IO(b.OutScript("device", "device_id"), b.OutScript("service", "service_id"))+`<bpmn:messageEventDefinition/></bpmn:intermediateCatchEvent>`,
		b.Flow("start", "loop"), b.Flow("loop", "reading"), b.Flow("reading", "loop"),
	)
}

func (this *env) variable(instanceId string, name string) any {
	this.t.Helper()
	vars, err := this.srv.Controller.GetVariables(ctx, alice, "processes", instanceId)
	if err != nil {
		this.t.Fatal(err)
	}
	for _, v := range vars {
		if v.Name == name {
			return v.Value
		}
	}
	return nil
}

// An event on a device group tells which member triggered it: device_id and service_id are the
// device and service of that member (SNRGY-5054).
func TestAGroupEventNamesTheMemberThatTriggered(t *testing.T) {
	e := newEnv(t)
	c := e.srv.Controller
	e.repo.mux.Lock()
	e.repo.groups["g"] = models.DeviceGroup{Id: "g", DeviceIds: []string{"d1", "d2"}}
	e.repo.mux.Unlock()
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: memberProcess(), Selections: map[string]model.Selection{"reading": {DeviceGroupId: "g"}}})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "processes", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	e.sync()
	e.send(1, "d2", 25.0, time.Now())
	if device, service := e.variable(inst.Id, "device"), e.variable(inst.Id, "service"); device != "d2" || service != "urn:infai:ses:service:event" {
		t.Fatalf("the token carries the member that triggered: %v %v", device, service)
	}
	e.send(2, "d1", 25.0, time.Now())
	if device := e.variable(inst.Id, "device"); device != "d1" {
		t.Errorf("and the next one: %v", device)
	}
}
