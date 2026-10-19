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

package client_test

import (
	"errors"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// dailySchedule creates a deployment of the user with a disabled daily schedule
func dailySchedule(t *testing.T, c client.Client, token string) model.Schedule {
	t.Helper()
	d, err := c.CreateDeployment(ctx, token, ns, model.DeploymentInput{Xml: waitingModel()})
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.CreateSchedule(ctx, token, ns, model.ScheduleInput{DeploymentId: d.Id, Cron: "@daily", Disabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSchedulesOfOtherUsersAreNotListed(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		own := dailySchedule(t, c, alice)
		dailySchedule(t, c, bob)
		page, err := c.ListSchedules(ctx, alice, ns, model.ScheduleListOptions{})
		if err != nil || page.Total != 1 || page.Items[0].Id != own.Id {
			t.Fatalf("expected only alice's schedule: %+v %v", page, err)
		}
	})
}

func TestScheduleOfAnotherUserCannotBeUpdated(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		s := dailySchedule(t, c, alice)
		_, err := c.UpdateSchedule(ctx, bob, ns, s.Id, model.ScheduleInput{DeploymentId: s.DeploymentId, Cron: "@hourly", Disabled: true})
		if !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("expected not found, got %v", err)
		}
	})
}

func TestScheduleOfAnotherUserCannotBeDeleted(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		s := dailySchedule(t, c, alice)
		if err := c.DeleteSchedule(ctx, bob, ns, s.Id); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("expected not found, got %v", err)
		}
	})
}

func TestScheduleMovedToAnotherDeploymentIsListedThere(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		s := dailySchedule(t, c, alice)
		other, err := c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: waitingModel()})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = c.UpdateSchedule(ctx, alice, ns, s.Id, model.ScheduleInput{DeploymentId: other.Id, Cron: s.Cron, Disabled: true}); err != nil {
			t.Fatal(err)
		}
		page, err := c.ListSchedules(ctx, alice, ns, model.ScheduleListOptions{DeploymentId: other.Id})
		if err != nil || page.Total != 1 || page.Items[0].Id != s.Id {
			t.Fatalf("expected the schedule at the other deployment: %+v %v", page, err)
		}
	})
}
