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
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

// replaceGateway puts a gateway with an empty database in place of the first one, as after a
// reset of the device; the first one is cut off from the cloud
func (this *env) replaceGateway() *server.Server {
	this.t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	this.t.Cleanup(cancel)
	this.online.Store(false)
	broker := mqtt.NewMemory()
	// the same filter replaces the subscription of the bridge to the first gateway
	_ = this.cloudBroker.Subscribe("processes/"+network+"/cmd/#", 1, func(topic string, payload []byte) error {
		return broker.Publish(ctx, "processes/cmd/"+strings.TrimPrefix(topic, "processes/"+network+"/cmd/"), 1, payload)
	})
	_ = broker.Subscribe("processes/state/#", 1, func(topic string, payload []byte) error {
		return this.cloudBroker.Publish(ctx, "processes/"+network+"/state/"+strings.TrimPrefix(topic, "processes/state/"), 1, payload)
	})
	config := configuration.Default()
	config.SqlitePath = storetest.MemoryDsn()
	config.PollInterval = "20ms"
	config.Mode, config.MqttBroker = "fog", "memory"
	srv, err := server.New(ctx, config, slog.New(slog.NewTextHandler(io.Discard, nil)), server.Dependencies{Broker: broker})
	if err != nil {
		this.t.Fatal(err)
	}
	srv.Start(ctx)
	return srv
}

// process-sync redeployed what the mgw reported as missing after a reset of its database and
// restarted the running instances (TestReconcileWithClientReset, TestKnown)
func TestAGatewayThatLostItsDatabaseGetsItsDeploymentsAgain(t *testing.T) {
	e := newEnv(t)
	d := e.runningOnTheGateway("i-1")
	fresh := e.replaceGateway()
	e.waitFor("the deployment on the new gateway", func() bool { _, err := deploymentOn(fresh, d.Id); return err == nil })
}

func TestAGatewayThatLostItsDatabaseRunsItsRunningInstancesAgain(t *testing.T) {
	e := newEnv(t)
	e.runningOnTheGateway("i-1")
	fresh := e.replaceGateway()
	e.waitFor("the running instance on the new gateway", func() bool {
		inst, err := instanceOn(fresh, "i-1")
		return err == nil && inst.State == "running"
	})
}
