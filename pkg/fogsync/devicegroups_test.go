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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/models/go/models"
)

// process-sync looked up the deployments of a changed device group and sent them again; the
// gateway has to get the event descriptions of a device that joins the group (SYNC-06)
func TestADeviceThatJoinsAGroupGetsItsEventDescriptionOnTheGateway(t *testing.T) {
	output := []models.Content{{ContentVariable: models.ContentVariable{Name: "value", FunctionId: measuring, AspectId: "air"}}}
	var mux sync.Mutex
	answers := map[string]any{
		"/device-groups/g":  models.DeviceGroup{Id: "g", DeviceIds: []string{"d1"}},
		"/devices/d1":       models.Device{Id: "d1", LocalId: "local-d1", DeviceTypeId: "dt"},
		"/devices/d2":       models.Device{Id: "d2", LocalId: "local-d2", DeviceTypeId: "dt"},
		"/device-types/dt":  models.DeviceType{Id: "dt", Services: []models.Service{{Id: "s1", LocalId: "temperature", Interaction: models.EVENT, Outputs: output}}},
		"/aspect-nodes/air": models.AspectNode{Id: "air", RootId: "air"},
	}
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.Lock()
		answer, ok := answers[r.URL.Path]
		mux.Unlock()
		if ok {
			_ = json.NewEncoder(w).Encode(answer)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer repository.Close()
	e := newEnv(t, func(c *configuration.Config) {
		c.DeviceRepositoryUrl = repository.URL
		// the changed group has to be read again
		c.DeviceCacheTimeout = "1ms"
	})
	c := e.cloud.Controller
	// an admin, so that the test needs no rights on the device group
	owner := client.TestToken("alice", "admin")
	d, err := c.CreateDeployment(ctx, owner, "processes", model.DeploymentInput{NetworkId: network, Xml: b.Process(
		b.Start("start"),
		`<bpmn:intermediateCatchEvent id="reading" senergy:function="`+measuring+`" senergy:aspect="air" senergy:characteristic="c" senergy:script="x &gt; 20" senergy:value_variable_name="x"><bpmn:messageEventDefinition/></bpmn:intermediateCatchEvent>`,
		b.End("end"), b.Flow("start", "reading"), b.Flow("reading", "end"),
	), Selections: map[string]model.Selection{"reading": {DeviceGroupId: "g"}}})
	if err != nil {
		t.Fatal(err)
	}
	descriptions := func() (result []model.EventDescription) {
		_ = e.gateway.Store.Tx(ctx, func(tx *store.Tx) (err error) { result, err = tx.ListEventDescriptions(d.Id); return err })
		return result
	}
	e.waitFor("the description of the first device on the gateway", func() bool { return len(descriptions()) == 1 })
	mux.Lock()
	answers["/device-groups/g"] = models.DeviceGroup{Id: "g", DeviceIds: []string{"d1", "d2"}}
	mux.Unlock()
	if err = c.RecomputeDeviceGroup(ctx, "g"); err != nil {
		t.Fatal(err)
	}
	if inCloud, err := c.ListEventDescriptions(ctx, owner, "processes", d.Id); err != nil || len(inCloud) != 2 {
		t.Fatalf("the cloud describes both devices: %+v %v", inCloud, err)
	}
	e.waitFor("the descriptions of both devices on the gateway", func() bool { return len(descriptions()) == 2 })
}
