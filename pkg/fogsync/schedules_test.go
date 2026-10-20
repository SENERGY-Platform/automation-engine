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

package fogsync_test

import (
	"errors"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// Schedules start instances in the cloud; a deployment of a gateway runs only there, so it
// takes no schedule, and one that exists anyway, e.g. from a migration, starts nothing in the
// cloud (FOG-05, RUN-07).
func TestADeploymentOfAGatewayTakesNoScheduleAndTheCloudStartsNothingForIt(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: b.Process(b.Start("start"), b.End("end"), b.Flow("start", "end")), NetworkId: network})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.CreateSchedule(ctx, alice, "processes", model.ScheduleInput{DeploymentId: d.Id, Cron: "* * * * * *"}); !errors.Is(err, model.ErrBadRequest) {
		t.Errorf("a schedule for a deployment of a gateway is rejected, got %v", err)
	}
	cloudOnly, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Xml: b.Process(b.Start("start"), b.End("end"), b.Flow("start", "end"))})
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.CreateSchedule(ctx, alice, "processes", model.ScheduleInput{DeploymentId: cloudOnly.Id, Cron: "* * * * * *"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.UpdateSchedule(ctx, alice, "processes", s.Id, model.ScheduleInput{DeploymentId: d.Id, Cron: "* * * * * *"}); !errors.Is(err, model.ErrBadRequest) {
		t.Errorf("a schedule is not moved to a deployment of a gateway, got %v", err)
	}
	// a schedule written past the controller, as an old one could be
	now := time.Now().UTC()
	due := now.Add(-time.Second)
	if err = e.cloud.Store.Tx(ctx, func(tx *store.Tx) error {
		return tx.SaveSchedule(model.Schedule{Id: "old", Namespace: "processes", DeploymentId: d.Id, Owner: "alice", Cron: "* * * * * *", NextDue: &due, CreatedAt: now, UpdatedAt: now})
	}); err != nil {
		t.Fatal(err)
	}
	if err = e.cloud.Engine.ProcessDue(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := c.ListInstances(ctx, alice, "processes", model.InstanceListOptions{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 {
		t.Errorf("the cloud starts no instance of a gateway deployment for a schedule: %+v", page.Items)
	}
}
