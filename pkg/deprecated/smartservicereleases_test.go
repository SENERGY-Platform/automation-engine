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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/deprecated"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
)

// newEnvWith is newEnv with a changed configuration; it also returns the dependencies, among
// them permissions-v2
func newEnvWith(t *testing.T, configure func(config *configuration.Config)) (*env, server.Dependencies) {
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	var deps server.Dependencies
	srv, err := client.NewTestServer(ctx, func(config *configuration.Config, d *server.Dependencies) {
		if configure != nil {
			configure(config)
		}
		deps = *d
	})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(srv.Handler)
	t.Cleanup(httpServer.Close)
	return &env{t: t, srv: srv, url: httpServer.URL}, deps
}

// deviceSelection answers like device-selection with the selectables of a fixture, filtered
// by the kinds of entities the request includes, as the mock of smart-service-repository did
func deviceSelection(t *testing.T, selectables string) *httptest.Server {
	var all []map[string]any
	if err := json.Unmarshal([]byte(selectables), &all); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		result := []map[string]any{}
		for _, s := range all {
			switch {
			case s["device"] != nil && q.Get("include_devices") != "true":
			case s["device_group"] != nil && q.Get("include_groups") != "true":
			case s["import"] != nil && q.Get("include_imports") != "true":
			default:
				result = append(result, s)
			}
		}
		_ = json.NewEncoder(w).Encode(result)
	}))
	t.Cleanup(server.Close)
	return server
}

func (this *env) createRelease(token string, designId string, name string) deprecated.SmartServiceRelease {
	this.t.Helper()
	var release deprecated.SmartServiceRelease
	if status, _ := this.call(token, http.MethodPost, "/smart-services/releases", map[string]any{"design_id": designId, "name": name, "description": "test description"}, &release); status != http.StatusOK {
		this.t.Fatalf("release of %v: %v", designId, status)
	}
	return release
}

func releaseIds(releases []deprecated.SmartServiceRelease) []string {
	result := []string{}
	for _, r := range releases {
		result = append(result, r.Id)
	}
	return result
}

// Without latest, the releases are all versions of a deployment, newest first; limit and offset
// page over the versions, the total counts all of them.
func TestReleaseListPagesOverAllVersions(t *testing.T) {
	e := newEnv(t)
	design := e.createDesign(alice, "Room info", smartServiceDesign("widget"))
	first := e.createRelease(alice, design.Id, "v1")
	e.createRelease(alice, design.Id, "v2")
	e.createRelease(alice, design.Id, "v3")
	var page []deprecated.SmartServiceRelease
	status, header := e.call(alice, http.MethodGet, "/smart-services/releases?limit=2&offset=1", nil, &page)
	if expected := []string{first.Id + ":v2", first.Id + ":v1"}; status != http.StatusOK || !slices.Equal(releaseIds(page), expected) || header.Get("X-Total-Count") != "3" {
		t.Fatalf("page: %v %v total %v, want %v", status, releaseIds(page), header.Get("X-Total-Count"), expected)
	}
	for _, r := range page {
		if r.NewReleaseId != first.Id {
			t.Errorf("an older version points to the current one: %+v", r)
		}
	}
}

// The search of releases matches a part of the name. smart-service-repository also matched the
// description; the engine searches the name only.
func TestReleaseSearchMatchesPartOfTheName(t *testing.T) {
	e := newEnv(t)
	for _, name := range []string{"foo bar batz", "foo", "42", "bar"} {
		design := e.createDesign(alice, name, smartServiceDesign("widget"))
		e.createRelease(alice, design.Id, name)
	}
	for search, expected := range map[string]int{"foo": 2, "foo bar": 1, "bar": 2, "42": 1, "batz": 1} {
		for _, sort := range []string{"name.asc", "name.desc"} {
			var releases []deprecated.SmartServiceRelease
			e.call(alice, http.MethodGet, "/smart-services/releases?sort="+sort+"&search="+url.QueryEscape(search), nil, &releases)
			if len(releases) != expected {
				t.Errorf("search %q sorted by %v: %+v, want %v", search, sort, releases, expected)
			}
		}
	}
}

const unused = "http://unused.invalid"

// The extended release names the analytics flows its analytics tasks create, for designs of
// both module worker versions.
func TestExtendedReleaseNamesItsAnalyticsModules(t *testing.T) {
	for _, name := range []string{"v2_smart_service_analytics_process_example.bpmn", "v2_smart_service_analytics_export_example.bpmn"} {
		t.Run(name, func(t *testing.T) {
			// the task types analytics and export need their services configured; a release does not call them
			e, _ := newEnvWith(t, func(c *configuration.Config) {
				c.FlowEngineUrl, c.FlowParserUrl, c.DeviceRepositoryUrl, c.ImportDeployUrl, c.ServingServiceUrl = unused, unused, unused, unused, unused
			})
			release := e.createRelease(alice, e.createDesign(alice, "Analytics", fixture(t, name)).Id, "release name")
			var extended deprecated.SmartServiceReleaseExtended
			if status, _ := e.call(alice, http.MethodGet, "/smart-services/extended-releases/"+url.PathEscape(release.Id), nil, &extended); status != http.StatusOK {
				t.Fatal(status)
			}
			expected := []deprecated.AnalyticsInfo{{FlowId: "62bd43205e353260a94ad6cd", Name: "smart-service cycle analytics", Desc: "cycle analytics"}}
			if !reflect.DeepEqual(extended.ParsedInfo.ModuleInfo.Analytics, expected) {
				t.Fatalf("analytics %+v, want %+v", extended.ParsedInfo.ModuleInfo.Analytics, expected)
			}
		})
	}
}

// The extended release lists the start parameters, including those selected automatically,
// and the maintenance procedures with their own parameters. The internal event id of
// smart-service-repository (<release id>_<message>) is gone with the stable message names
// (SMS-02).
func TestExtendedReleaseListsParametersAndMaintenanceProcedures(t *testing.T) {
	e := newEnv(t)
	release := e.createRelease(alice, e.createDesign(alice, "Maintenance", fixture(t, "maintenance.bpmn")).Id, "release name")
	var extended deprecated.SmartServiceReleaseExtended
	if status, _ := e.call(alice, http.MethodGet, "/smart-services/extended-releases/"+url.PathEscape(release.Id), nil, &extended); status != http.StatusOK {
		t.Fatal(status)
	}
	type parameter struct {
		Id, Label, Type         string
		Multiple, AutoSelectAll bool
	}
	parameters := []parameter{}
	for _, p := range extended.ParsedInfo.ParameterDescriptions {
		parameters = append(parameters, parameter{p.Id, p.Label, p.Type, p.Multiple, p.AutoSelectAll})
	}
	expected := []parameter{
		{"value_input", "value_input", "string", false, false},
		{"iot_input", "iot_input", "string", false, false},
		{"auto_input", "auto_input", "string", true, true},
	}
	if !reflect.DeepEqual(parameters, expected) {
		t.Errorf("start parameters %+v, want %+v", parameters, expected)
	}
	procedures := extended.ParsedInfo.MaintenanceProcedures
	if len(procedures) != 1 || procedures[0].BpmnId != "StartEvent_0at1j8v" || procedures[0].PublicEventId != "update" ||
		len(procedures[0].ParameterDescriptions) != 1 || procedures[0].ParameterDescriptions[0].Id != "pipeline_id" || procedures[0].ParameterDescriptions[0].Type != "string" {
		t.Errorf("maintenance procedures %+v", procedures)
	}
}

// The parameters of a release describe the start form with the options device-selection
// offers, in the format of smart-service-repository: expected files and device-selection
// answers are those of its tests.
func TestReleaseParametersMatchTheFormatOfSmartServiceRepository(t *testing.T) {
	for name, files := range map[string][3]string{
		"device, group and import options": {"params.bpmn", "selections_response_1.json", "expected_params_1.json"},
		"entity only and same entity":      {"complex_selection.bpmn", "selections_response_2.json", "expected_params_2.json"},
		"paths with characteristics":       {"params.bpmn", "selections_response_3.json", "expected_params_3.json"},
	} {
		t.Run(name, func(t *testing.T) {
			sameReleaseParameters(t, files[0], fixture(t, files[1]), files[2])
		})
	}
}

// sameReleaseParameters releases the design of a fixture with device-selection answering
// selectables and compares the parameters of the release with an expected fixture
func sameReleaseParameters(t *testing.T, design string, selectables string, expectedFile string) {
	t.Helper()
	selection := deviceSelection(t, selectables)
	e, _ := newEnvWith(t, func(config *configuration.Config) { config.DeviceSelectionUrl = selection.URL })
	release := e.createRelease(alice, e.createDesign(alice, "Options", fixture(t, design)).Id, "release name")
	var actual, expected []deprecated.SmartServiceExtendedParameter
	if status, _ := e.call(alice, http.MethodGet, "/smart-services/releases/"+url.PathEscape(release.Id)+"/parameters", nil, &actual); status != http.StatusOK {
		t.Fatal(status)
	}
	if err := json.Unmarshal([]byte(fixture(t, expectedFile)), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		a, _ := json.Marshal(actual)
		b, _ := json.Marshal(expected)
		t.Fatalf("\nactual:   %s\nexpected: %s", a, b)
	}
}

// smart-services allows a design to start only with a plain start event and maintenance
// procedures with a message name (NS-06); a design with a timer, signal or conditional start, a
// message start without message or without plain start is not released.
func TestOnlyValidDesignsAreReleased(t *testing.T) {
	for _, name := range []string{"valid_default_start", "valid_message_start", "invalid_conditional_start", "invalid_missing_default",
		"invalid_missing_msg_event_name", "invalid_signal_start", "invalid_time_event"} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			var design deprecated.SmartServiceDesign
			status, _ := e.call(alice, http.MethodPost, "/smart-services/designs", map[string]any{"name": name, "bpmn_xml": fixture(t, "validation/"+name+".bpmn"), "svg_xml": "<svg/>"}, &design)
			if status == http.StatusOK {
				status, _ = e.call(alice, http.MethodPost, "/smart-services/releases", map[string]any{"design_id": design.Id, "name": name}, nil)
			}
			if valid := strings.HasPrefix(name, "valid_"); valid != (status == http.StatusOK) {
				t.Fatalf("status %v", status)
			}
			if status != http.StatusOK && status >= 500 {
				t.Fatalf("an invalid design is a client error: %v", status)
			}
		})
	}
}

// A release shared with another user stays shared when its owner releases the design again:
// the new release is a version of the same deployment (SMS-03, SMS-07).
func TestSharedReleaseStaysSharedForTheNextRelease(t *testing.T) {
	e, deps := newEnvWith(t, nil)
	design := e.createDesign(alice, "Room info", smartServiceDesign("widget"))
	release := e.createRelease(alice, design.Id, "v1")
	if err := e.srv.Permissions.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	resource, err, _ := deps.Permissions.GetResource(alice, "smart-services_deployments", release.Id)
	if err != nil {
		t.Fatal(err)
	}
	resource.UserPermissions["bob"] = permmodel.PermissionsMap{Read: true, Execute: true}
	if _, err, _ = deps.Permissions.SetPermission(alice, "smart-services_deployments", release.Id, resource.ResourcePermissions); err != nil {
		t.Fatal(err)
	}
	if status, _ := e.call(bob, http.MethodGet, "/smart-services/releases/"+url.PathEscape(release.Id), nil, nil); status != http.StatusOK {
		t.Fatalf("bob reads the shared release: %v", status)
	}

	e.createRelease(alice, design.Id, "v2")
	var read deprecated.SmartServiceRelease
	if status, _ := e.call(bob, http.MethodGet, "/smart-services/releases/"+url.PathEscape(release.Id), nil, &read); status != http.StatusOK || read.NewReleaseId != "" {
		t.Fatalf("bob reads the next release: %v %+v", status, read)
	}
}
