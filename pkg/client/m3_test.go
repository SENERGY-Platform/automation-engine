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

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
)

func deviceEventModel() string {
	return b.Process(
		b.Start("start"),
		`<bpmn:intermediateCatchEvent id="reading" senergy:function="urn:infai:ses:measuring-function:t" senergy:aspect="a" senergy:characteristic="c" senergy:script="x &gt; 20" senergy:value_variable_name="x"><bpmn:messageEventDefinition/></bpmn:intermediateCatchEvent>`,
		b.End("end"), b.Flow("start", "reading"), b.Flow("reading", "end"),
	)
}

func TestPreparedDeploymentAndCheckedSelections(t *testing.T) {
	eachEnv(t, func(t *testing.T, e env) {
		prepared, err := e.PrepareDeployment(ctx, alice, ns, model.PrepareInput{Xml: deviceEventModel()})
		if err != nil {
			t.Fatal(err)
		}
		if len(prepared.Elements) != 1 || prepared.Elements[0].ElementId != "reading" || prepared.Elements[0].Kind != model.PreparedEvent || prepared.Elements[0].Criteria.FunctionId == "" {
			t.Fatalf("unexpected preparation %+v", prepared)
		}
		input := model.DeploymentInput{Xml: deviceEventModel(), Selections: map[string]model.Selection{"start": {DeviceId: "d1"}}}
		if _, err = e.CreateDeployment(ctx, alice, ns, input); !errors.Is(err, model.ErrValidation) {
			t.Fatalf("a selection on an element without devices is invalid (DEP-05), got %v", err)
		}
		input.Selections = map[string]model.Selection{"reading": {DeviceId: "d1", ServiceId: "s1"}}
		if _, err = e.CreateDeployment(ctx, alice, ns, input); !errors.Is(err, model.ErrForbidden) {
			t.Fatalf("selecting a device needs execute on it (DEP-06), got %v", err)
		}
		adminToken := client.TestToken("x", "admin")
		_, err, _ = e.permissions.SetPermission(adminToken, "devices", "d1", permmodel.ResourcePermissions{
			UserPermissions: map[string]permmodel.PermissionsMap{"alice": {Read: true, Execute: true, Administrate: true}},
		})
		if err != nil {
			t.Fatal(err)
		}
		d, err := e.CreateDeployment(ctx, alice, ns, input)
		if err != nil {
			t.Fatal(err)
		}
		v, err := e.GetDeploymentVersion(ctx, alice, ns, d.Id, 1)
		if err != nil || v.Selections["reading"].DeviceId != "d1" || v.Selections["reading"].ServiceId != "s1" {
			t.Fatalf("the selection is stored typed at the version: %+v %v", v.Selections, err)
		}
		same, err := e.PutDeployment(ctx, alice, ns, d.Id, input)
		if err != nil || same.Version != 1 {
			t.Fatalf("the same selection again changes nothing: %+v %v", same, err)
		}
		input.Selections = map[string]model.Selection{"reading": {DeviceId: "d1"}}
		input.UpdateStrategy = model.UpdateDrain
		changed, err := e.PutDeployment(ctx, alice, ns, d.Id, input)
		if err != nil || changed.Version != 2 {
			t.Fatalf("another selection is a new version: %+v %v", changed, err)
		}
	})
}
