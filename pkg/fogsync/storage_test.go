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
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/fogsync"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// A gateway reports the size of runtime state, history, outboxes and IO values and the instances
// with the most history entries; the cloud shows them in the network overview (LONG-14).
func TestTheStorageReportOfAGatewayReachesTheCloud(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	d := deployOnGateway(t, e)
	inst, err := c.PutInstance(ctx, alice, "processes", "i-1", model.StartInstanceInput{DeploymentId: d.Id})
	if err != nil {
		t.Fatal(err)
	}
	e.waitFor("the running instance on the gateway", func() bool {
		gi, err := e.gatewayInstance(inst.Id)
		return err == nil && gi.State == model.StateRunning
	})
	if err = e.gateway.Sync.SendStatus(ctx); err != nil {
		t.Fatal(err)
	}
	var storage fogsync.Storage
	e.waitFor("the storage report in the cloud", func() bool {
		n, err := c.GetNetwork(ctx, alice, network)
		if err != nil || len(n.Storage) == 0 {
			return false
		}
		storage = fogsync.Storage{}
		return json.Unmarshal(n.Storage, &storage) == nil && len(storage.Top) > 0
	})
	for _, category := range []string{"runtime", "history", "outbox", "io"} {
		if _, ok := storage.Bytes[category]; !ok {
			t.Errorf("size of %v missing: %v", category, storage.Bytes)
		}
	}
	if storage.Bytes["runtime"] == 0 || storage.Bytes["history"] == 0 {
		t.Errorf("a running instance has runtime state and history: %v", storage.Bytes)
	}
	if storage.Top[0].Id != inst.Id || storage.Top[0].Namespace != "processes" || storage.Top[0].Entries < 1 {
		t.Errorf("the instance with the most history entries: %+v", storage.Top)
	}
	if _, ok := storage.Rows["io_values"]; !ok {
		t.Errorf("rows of every table: %v", storage.Rows)
	}
}
