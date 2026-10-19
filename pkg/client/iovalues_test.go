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
	"testing"
	// the zones of the computed keys do not depend on the tzdata of the system, as in main.go
	_ "time/tzdata"

	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

func TestDeletingAnUnknownIoKeySucceeds(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		if err := c.DeleteIoValue(ctx, alice, "unknown"); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})
}

func TestIoValuesAreListedByDeployment(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		d, err := c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: waitingModel()})
		if err != nil {
			t.Fatal(err)
		}
		for key, deploymentId := range map[string]string{"bound": d.Id, "free": ""} {
			if err = c.SetIoValue(ctx, alice, key, model.IoValueInput{Value: key, DeploymentId: deploymentId}); err != nil {
				t.Fatal(err)
			}
		}
		page, err := c.ListIoValues(ctx, alice, model.IoListOptions{DeploymentId: d.Id})
		if err != nil || page.Total != 1 || page.Items[0].Key != "bound" || page.Items[0].DeploymentId != d.Id {
			t.Fatalf("expected only the value bound to the deployment: %+v %v", page, err)
		}
	})
}

func TestIoValueBoundToADeploymentGoesWithTheDeployment(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		d, err := c.CreateDeployment(ctx, alice, ns, model.DeploymentInput{Xml: waitingModel()})
		if err != nil {
			t.Fatal(err)
		}
		if err = c.SetIoValue(ctx, alice, "bound", model.IoValueInput{Value: 1, DeploymentId: d.Id}); err != nil {
			t.Fatal(err)
		}
		if err = c.DeleteDeployment(ctx, alice, ns, d.Id); err != nil {
			t.Fatal(err)
		}
		if page, err := c.ListIoValues(ctx, alice, model.IoListOptions{}); err != nil || page.Total != 0 {
			t.Fatalf("expected no values after the deletion of the deployment (IO-03): %+v %v", page, err)
		}
	})
}

func TestComputedUtcOffsetFollowsTheTimeZone(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		// neither zone has daylight saving time
		for key, expected := range map[string]float64{"calculate_UtcOffset_Asia/Hong_Kong": 8 * 60, "calculate_UtcOffset_US/Hawaii": -10 * 60} {
			v, err := c.GetIoValue(ctx, alice, key)
			if err != nil || v.Value != expected {
				t.Errorf("%v: expected %v, got %+v %v", key, expected, v, err)
			}
		}
	})
}
