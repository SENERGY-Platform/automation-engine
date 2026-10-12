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

package protocol_test

import (
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/fogsync/protocol"
	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
)

// The connector maps processes/{network}/cmd/# of the cloud to processes/cmd/# of the gateway and
// processes/state/# of the gateway to processes/{network}/state/# of the cloud; the topics of the
// protocol have to survive that mapping and stay apart from those of process-sync.
func TestTopicsSurviveTheBridgeOfTheConnector(t *testing.T) {
	cloudCommand := protocol.CloudCommandTopic("hub-1", protocol.KindDesired)
	gatewayCommand := "processes/cmd/" + cloudCommand[len("processes/hub-1/cmd/"):]
	if !mqtt.Matches(protocol.GatewayCommandFilter, gatewayCommand) {
		t.Errorf("the gateway receives %v", gatewayCommand)
	}
	gatewayState := protocol.GatewayStateTopic(protocol.KindChanges)
	cloudState := "processes/hub-1/state/" + gatewayState[len("processes/state/"):]
	if !mqtt.Matches(protocol.CloudStateFilter("engine"), cloudState) || !mqtt.Matches(protocol.CloudStateFilter(""), cloudState) {
		t.Errorf("the cloud receives %v", cloudState)
	}
	for _, processSync := range []string{"processes/hub-1/state/deployment", "processes/hub-1/state/process-instance/known"} {
		if mqtt.Matches(protocol.CloudStateFilter(""), processSync) {
			t.Errorf("a message of process-sync is not one of the engine: %v", processSync)
		}
	}
	cases := map[string][2]string{
		cloudCommand:   {"hub-1", protocol.KindDesired},
		gatewayCommand: {"", protocol.KindDesired},
		gatewayState:   {"", protocol.KindChanges},
		cloudState:     {"hub-1", protocol.KindChanges},
	}
	for topic, expected := range cases {
		network, kind, ok := protocol.KindOf(topic)
		if !ok || network != expected[0] || kind != expected[1] {
			t.Errorf("%v: %v %v %v", topic, network, kind, ok)
		}
	}
	if _, _, ok := protocol.KindOf("processes/hub-1/state/deployment"); ok {
		t.Errorf("topics of process-sync are not read")
	}
}

func TestCompatibilityWindow(t *testing.T) {
	if !protocol.Compatible(protocol.Version, 1) || !protocol.Compatible(protocol.Version-1, 1) || protocol.Compatible(protocol.Version-2, 1) || protocol.Compatible(protocol.Version+1, 1) {
		t.Errorf("the cloud serves its version and the window before it, never a newer one (SYNC-11)")
	}
}
