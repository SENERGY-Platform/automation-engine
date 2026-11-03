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
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/models/go/models"
)

func selection(deviceId string) map[string]model.Selection {
	return map[string]model.Selection{"reading": {DeviceId: deviceId, ServiceId: "urn:infai:ses:service:event"}}
}

// An instance that kept running on an older version before descriptions were kept per version
// has no descriptions of its own; it gets the events of the current version, as before (DEP-04).
func TestAnInstanceOnAVersionWithoutDescriptionsTakesThoseOfTheCurrentVersion(t *testing.T) {
	e := newEnv(t)
	c := e.srv.Controller
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: eventProcess(), Selections: selection("d1")})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "processes", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.PutDeployment(ctx, alice, "processes", d.Id, model.DeploymentInput{Xml: eventProcess(), UpdateStrategy: model.UpdateDrain, Selections: selection("d2")}); err != nil {
		t.Fatal(err)
	}
	// what an older engine left: descriptions of the current version only
	err = e.srv.Store.Tx(ctx, func(tx *store.Tx) error { return tx.ReplaceEventDescriptions(d.Id, 1, nil, nil, time.Now()) })
	if err != nil {
		t.Fatal(err)
	}
	e.sync()
	e.send(1, "d1", 30.0, time.Now())
	if e.count(inst.Id) != nil {
		t.Fatalf("the device of version 1 has no description any more: %v", e.count(inst.Id))
	}
	e.send(2, "d2", 30.0, time.Now())
	if e.count(inst.Id) != 1.0 {
		t.Fatalf("the instance on version 1 takes the events of the current version: %v", e.count(inst.Id))
	}
}

// The descriptions of an older version serve the instances still waiting on it; one of a start
// event starts nothing, only those of the current version do (DEP-04).
func TestAStartEventOfAnOlderVersionStartsNothing(t *testing.T) {
	e := newEnv(t)
	c := e.srv.Controller
	xml := b.Process(
		`<bpmn:startEvent id="reading" senergy:function="`+measuring+`" senergy:aspect="air" senergy:characteristic="c" senergy:script="x &gt; 20" senergy:value_variable_name="x" senergy:qos="1"><bpmn:messageEventDefinition/></bpmn:startEvent>`,
		b.Message("m", "go"), b.CatchMessage("wait", "m"), b.End("end"),
		b.Flow("reading", "wait"), b.Flow("wait", "end"),
	)
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: xml, Selections: selection("d1")})
	if err != nil {
		t.Fatal(err)
	}
	e.sync()
	count := func() int {
		page, err := c.ListInstances(ctx, alice, "processes", model.InstanceListOptions{DeploymentId: d.Id})
		if err != nil {
			t.Fatal(err)
		}
		return page.Total
	}
	e.send(1, "d1", 30.0, time.Now())
	if count() != 1 {
		t.Fatalf("the device starts an instance: %v", count())
	}
	if _, err = c.PutDeployment(ctx, alice, "processes", d.Id, model.DeploymentInput{Xml: xml, UpdateStrategy: model.UpdateDrain, Selections: selection("d2")}); err != nil {
		t.Fatal(err)
	}
	e.sync()
	e.send(2, "d1", 30.0, time.Now())
	if count() != 1 {
		t.Fatalf("the device of version 1, kept for its waiting instance, starts nothing: %v", count())
	}
	e.send(3, "d2", 30.0, time.Now())
	if count() != 2 {
		t.Fatalf("the device of the current version starts: %v", count())
	}
}

// A changed device group derives the descriptions again for every version that still has them,
// so an instance on an older version gets the events of a new member as well (DEP-04, DEP-08).
func TestADeviceGroupChangeReachesOlderVersions(t *testing.T) {
	e := newEnv(t)
	c := e.srv.Controller
	group := map[string]model.Selection{"reading": {DeviceGroupId: "g"}}
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: eventProcess(), Selections: group})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := c.StartInstance(ctx, alice, "processes", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.PutDeployment(ctx, alice, "processes", d.Id, model.DeploymentInput{Xml: eventProcess(), UpdateStrategy: model.UpdateDrain, Selections: group, Name: "second"}); err != nil {
		t.Fatal(err)
	}
	e.repo.mux.Lock()
	e.repo.groups["g"] = models.DeviceGroup{Id: "g", DeviceIds: []string{"d1", "d2"}}
	e.repo.mux.Unlock()
	if err = c.RecomputeDeviceGroup(ctx, "g"); err != nil {
		t.Fatal(err)
	}
	var perVersion map[int]int
	_ = e.srv.Store.Tx(ctx, func(tx *store.Tx) error {
		all, err := tx.ListEventDescriptions(d.Id, 0)
		perVersion = map[int]int{}
		for _, description := range all {
			perVersion[description.Version]++
		}
		return err
	})
	if perVersion[1] != 2 || perVersion[2] != 2 {
		t.Fatalf("both versions describe both members: %v", perVersion)
	}
	e.sync()
	e.send(1, "d2", 30.0, time.Now())
	if e.count(inst.Id) != 1.0 {
		t.Fatalf("the new member reaches the instance on version 1: %v", e.count(inst.Id))
	}
}
