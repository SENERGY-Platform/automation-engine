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
	"reflect"
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/models/go/models"
)

// listEnv holds two deployments of alice and one of bob, deployed through the old route
func listEnv(t *testing.T) *env {
	e := newEnv(t)
	for token, names := range map[string][]string{alice: {"name1", "name2"}, bob: {"name3"}} {
		for _, name := range names {
			deployment := models.Deployment{Version: 3, Name: name, Diagram: models.Diagram{XmlRaw: b.Process(b.Start("start")), Svg: "<svg/>"}}
			if status, _ := e.call(token, http.MethodPost, "/deployment/v3/deployments", deployment, nil); status != http.StatusOK {
				t.Fatalf("deploy %v: %v", name, status)
			}
		}
	}
	return e
}

func listedNames(t *testing.T, e *env, query string) []string {
	t.Helper()
	var list []models.Deployment
	if status, _ := e.call(alice, http.MethodGet, "/deployment/v3/deployments"+query, nil, &list); status != http.StatusOK {
		t.Fatalf("list: %v", status)
	}
	names := []string{}
	for _, d := range list {
		names = append(names, d.Name)
	}
	return names
}

// The deployment list of process-deployment (TestDeploymentListApi): the own deployments,
// sortable by name, with limit and offset.
func TestDeploymentListOfTheOldApi(t *testing.T) {
	e := listEnv(t)
	cases := map[string]struct {
		query    string
		expected []string
	}{
		"shows only the own deployments": {"?sort=name", []string{"name1", "name2"}},
		"sorts by name descending":       {"?sort=name.desc", []string{"name2", "name1"}},
		"sorts by name ascending":        {"?sort=name.asc", []string{"name1", "name2"}},
		"limits the page":                {"?sort=name&limit=1", []string{"name1"}},
		"skips the offset":               {"?sort=name&offset=1", []string{"name2"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := listedNames(t, e, c.query); !reflect.DeepEqual(got, c.expected) {
				t.Errorf("expected %v, got %v", c.expected, got)
			}
		})
	}
}
