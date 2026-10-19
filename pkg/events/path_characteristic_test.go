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

package events_test

import (
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// The characteristic of a selected path is the one the service sends at that path; the
// condition is evaluated in the characteristic of the event (EVT-04), so the value is
// converted to that one, as event-deployment did.
func TestDescriptionWithSelectedPathKeepsTheCharacteristicOfTheEvent(t *testing.T) {
	describer := events.NewDescriber(&fakeRepo{}, aspects(), fakeImports{})
	selection := model.Selection{DeviceId: "d1", ServiceId: "s-request", Path: &model.SelectedPath{Path: "value.energy.level", CharacteristicId: "service-characteristic"}}
	result := describe(t, describer, eventWith(`senergy:aspect="air"`), selection)
	if len(result) != 1 || result[0].Path != "value.energy.level" {
		t.Fatalf("expected one description with the selected path, got %+v", result)
	}
	if result[0].CharacteristicId != "c" {
		t.Errorf("expected the characteristic of the event c, got %v", result[0].CharacteristicId)
	}
}
