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

	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// An update that changes only the deploy parameters is a change like any other (DEP-04, DEP-01);
// nil and empty parameters are the same.
func TestUpdateOfParametersOnlyCreatesAVersion(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		d, err := c.PutDeployment(ctx, alice, ns, "params", model.DeploymentInput{Xml: waitingModel()})
		if err != nil || d.Version != 1 {
			t.Fatalf("create: %+v %v", d, err)
		}
		d, err = c.PutDeployment(ctx, alice, ns, "params", model.DeploymentInput{Xml: waitingModel(), Parameters: map[string]model.ElementParameters{}, Selections: map[string]model.Selection{}})
		if err != nil || d.Version != 1 {
			t.Fatalf("empty parameters are no change: %+v %v", d, err)
		}
		inst, err := c.StartInstance(ctx, alice, ns, model.StartInstanceInput{DeploymentId: d.Id})
		if err != nil {
			t.Fatal(err)
		}
		parameters := map[string]model.ElementParameters{"start": {Defaults: map[string]string{"level": "7"}}}
		d, err = c.PutDeployment(ctx, alice, ns, "params", model.DeploymentInput{Xml: waitingModel(), Parameters: parameters, UpdateStrategy: model.UpdateRestart})
		if err != nil || d.Version != 2 {
			t.Fatalf("changed parameters create a version: %+v %v", d, err)
		}
		v2, err := c.GetDeploymentVersion(ctx, alice, ns, d.Id, 2)
		if err != nil || v2.Parameters["start"].Defaults["level"] != "7" {
			t.Fatalf("version 2 keeps the parameters: %+v %v", v2.Parameters, err)
		}
		params, err := c.GetStartParameters(ctx, alice, ns, d.Id)
		if err != nil || len(params) != 1 || params[0].Default != "7" {
			t.Fatalf("start parameters of the new version: %+v %v", params, err)
		}
		if inst, err = c.GetInstance(ctx, alice, ns, inst.Id); err != nil || inst.Run != 2 || inst.DeploymentVersion != 2 {
			t.Fatalf("the update strategy applies: %+v %v", inst, err)
		}
		same := map[string]model.ElementParameters{"start": {Defaults: map[string]string{"level": "7"}, Inputs: map[string]string{}}}
		if d, err = c.PutDeployment(ctx, alice, ns, "params", model.DeploymentInput{Xml: waitingModel(), Parameters: same}); err != nil || d.Version != 2 {
			t.Fatalf("the same parameters with empty maps are no change: %+v %v", d, err)
		}
		if d, err = c.PutDeployment(ctx, alice, ns, "params", model.DeploymentInput{Xml: waitingModel(), Parameters: same, UpdateStrategy: model.UpdateDrain}); err != nil || d.Version != 2 {
			t.Fatalf("a strategy alone is no change: %+v %v", d, err)
		}
		if d, err = c.PutDeployment(ctx, alice, ns, "params", model.DeploymentInput{Xml: waitingModel(), UpdateStrategy: model.UpdateDrain}); err != nil || d.Version != 3 {
			t.Fatalf("removing the parameters is a change: %+v %v", d, err)
		}
		if d, err = c.PutDeployment(ctx, alice, ns, "params", model.DeploymentInput{Xml: waitingModel(), Source: "generated", UpdateStrategy: model.UpdateDrain}); err != nil || d.Version != 4 || d.Source != "generated" {
			t.Fatalf("a changed source is a change: %+v %v", d, err)
		}
		if d, err = c.PutDeployment(ctx, alice, ns, "params", model.DeploymentInput{Xml: waitingModel()}); err != nil || d.Version != 4 || d.Source != "generated" {
			t.Fatalf("an empty source keeps the stored one: %+v %v", d, err)
		}
	})
}

// Moving a deployment to another model with the same BPMN and version number is a change.
func TestUpdateToAnotherModelCreatesAVersion(t *testing.T) {
	each(t, func(t *testing.T, c client.Client) {
		a, err := c.CreateModel(ctx, alice, ns, model.ModelInput{Name: "a", Xml: waitingModel()})
		if err != nil {
			t.Fatal(err)
		}
		other, err := c.CreateModel(ctx, alice, ns, model.ModelInput{Name: "b", Xml: waitingModel()})
		if err != nil {
			t.Fatal(err)
		}
		d, err := c.PutDeployment(ctx, alice, ns, "moved", model.DeploymentInput{Name: "moved", ModelId: a.Id})
		if err != nil || d.Version != 1 || d.ModelId != a.Id {
			t.Fatalf("create: %+v %v", d, err)
		}
		d, err = c.PutDeployment(ctx, alice, ns, "moved", model.DeploymentInput{Name: "moved", ModelId: other.Id, UpdateStrategy: model.UpdateDrain})
		if err != nil || d.Version != 2 || d.ModelId != other.Id {
			t.Fatalf("the model reference changed: %+v %v", d, err)
		}
	})
}
