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

package deprecated_test

import "testing"

// The old designer leaves a timer without definition for the deploy dialog to fill in;
// process-deployment offered it as a duration with an empty time. A device event without
// script was a message event of the analytics pipeline there; the engine offers it as a device
// event that triggers on every value (EVT-03), so it is not compared.
func TestPreparedDeploymentOffersAnEmptyTimerAsDurationToFillIn(t *testing.T) {
	e := newEnv(t)
	cases := map[string][]string{
		"0pools0lane_with_order": {"IntermediateThrowEvent_164ft4m"},
		"0pools0lanes4events":    nil,
		"0pools0lanes5events":    {"StartEvent_1"},
		"conditional_event_2":    nil,
	}
	for name, dropped := range cases {
		t.Run(name, func(t *testing.T) { checkFixture(t, e, name, dropped...) })
	}
}

// process-deployment named an element without name by its id.
func TestPreparedElementWithoutNameIsNamedByItsId(t *testing.T) {
	checkFixture(t, newEnv(t), "0pools0lanes1notification")
}

// process-deployment showed the value variable a device event without
// senergy:value_variable_name is evaluated with: "value".
func TestPreparedDeviceEventWithoutValueVariableShowsTheDefault(t *testing.T) {
	e := newEnv(t)
	for _, name := range []string{"conditional_event_1", "conditional_event_aspect_list"} {
		t.Run(name, func(t *testing.T) { checkFixture(t, e, name) })
	}
}
