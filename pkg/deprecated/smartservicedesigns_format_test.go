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

import (
	"net/http"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/deprecated"
)

// smart-service-repository took the description of a design saved without one from the
// senergy:description attribute of its process, as it took the name from the process name.
func TestDesignWithoutDescriptionTakesTheDescriptionOfTheProcess(t *testing.T) {
	e := newEnv(t)
	var created deprecated.SmartServiceDesign
	if status, _ := e.call(alice, http.MethodPost, "/smart-services/designs", map[string]any{"bpmn_xml": fixture(t, "nameanddesc.bpmn"), "svg_xml": "<svg/>"}, &created); status != http.StatusOK {
		t.Fatal(status)
	}
	if created.Description != "test description" {
		t.Fatalf("description %q, want the senergy:description of the process", created.Description)
	}
}
