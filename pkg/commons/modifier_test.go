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

package commons_test

import (
	"reflect"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/commons"
)

func TestSplitModifierSeparatesIdAndParameters(t *testing.T) {
	cases := []struct {
		id       string
		pure     string
		modifier map[string][]string
	}{
		{"urn:infai:ses:device:1", "urn:infai:ses:device:1", nil},
		{"urn:infai:ses:device:1$service_group_selection=sg1", "urn:infai:ses:device:1", map[string][]string{"service_group_selection": {"sg1"}}},
		{"urn:infai:ses:device:1$%zz", "urn:infai:ses:device:1", nil},
	}
	for _, c := range cases {
		pure, modifier := commons.SplitModifier(c.id)
		if pure != c.pure || !reflect.DeepEqual(modifier, c.modifier) {
			t.Errorf("%v: %v %v", c.id, pure, modifier)
		}
	}
}
