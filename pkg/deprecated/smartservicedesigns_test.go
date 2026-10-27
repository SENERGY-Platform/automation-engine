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
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/deprecated"
)

// fixture reads a file of testdata/smartservices, taken over from smart-service-repository
func fixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "smartservices", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func (this *env) createDesign(token string, name string, xml string) deprecated.SmartServiceDesign {
	this.t.Helper()
	var design deprecated.SmartServiceDesign
	if status, _ := this.call(token, http.MethodPost, "/smart-services/designs", map[string]any{"name": name, "bpmn_xml": xml, "svg_xml": "<svg/>"}, &design); status != http.StatusOK {
		this.t.Fatalf("create design %v: %v", name, status)
	}
	return design
}

func designNames(designs []deprecated.SmartServiceDesign) []string {
	result := []string{}
	for _, d := range designs {
		result = append(result, d.Name)
	}
	return result
}

// A design saved without a name is named after its process; it reads back as saved, also
// after a change of its name.
func TestDesignIsCreatedReadAndRenamed(t *testing.T) {
	e := newEnv(t)
	xml := fixture(t, "nameanddesc.bpmn")
	var created deprecated.SmartServiceDesign
	if status, _ := e.call(alice, http.MethodPost, "/smart-services/designs", map[string]any{"bpmn_xml": xml, "svg_xml": "<svg/>"}, &created); status != http.StatusOK {
		t.Fatal(status)
	}
	if created.Id == "" || created.Name != "test name" || created.BpmnXml != xml || created.SvgXml != "<svg/>" {
		t.Fatalf("the design keeps its BPMN and takes the name of the process: %+v", created)
	}
	var read deprecated.SmartServiceDesign
	if e.call(alice, http.MethodGet, "/smart-services/designs/"+url.PathEscape(created.Id), nil, &read); read != created {
		t.Fatalf("read after create:\n%+v\n%+v", read, created)
	}
	changed := created
	changed.Name = "changed name"
	var updated deprecated.SmartServiceDesign
	if status, _ := e.call(alice, http.MethodPut, "/smart-services/designs/"+url.PathEscape(created.Id), changed, &updated); status != http.StatusOK || updated.Id != created.Id || updated.Name != "changed name" || updated.BpmnXml != xml {
		t.Fatalf("update: %v %+v", status, updated)
	}
	if e.call(alice, http.MethodGet, "/smart-services/designs/"+url.PathEscape(created.Id), nil, &read); read != updated {
		t.Fatalf("read after update:\n%+v\n%+v", read, updated)
	}
}

// The list of designs is sorted by name, ascending unless asked otherwise, pages with limit and
// offset and leaves out a deleted design.
func TestDesignsAreListedSortedAndPaged(t *testing.T) {
	e := newEnv(t)
	xml := fixture(t, "nameanddesc.bpmn")
	deleted := e.createDesign(alice, "deleted", xml)
	names := []string{"a", "b", "c", "d", "e", "f"}
	for _, name := range names {
		e.createDesign(alice, name, xml)
	}
	if status, _ := e.call(alice, http.MethodDelete, "/smart-services/designs/"+url.PathEscape(deleted.Id), nil, nil); status != http.StatusOK {
		t.Fatalf("delete: %v", status)
	}
	reversed := slices.Clone(names)
	slices.Reverse(reversed)
	for query, expected := range map[string][]string{
		"":                  names,
		"?sort=name.asc":    names,
		"?sort=name.desc":   reversed,
		"?limit=2&offset=1": names[1:3],
	} {
		var designs []deprecated.SmartServiceDesign
		if status, _ := e.call(alice, http.MethodGet, "/smart-services/designs"+query, nil, &designs); status != http.StatusOK || !slices.Equal(designNames(designs), expected) {
			t.Errorf("designs%v: %v %v, want %v", query, status, designNames(designs), expected)
		}
	}
}

// The search of designs matches a part of the name, in both sort orders. smart-service-repository
// also matched the description and single words; the engine searches the name only.
func TestDesignSearchMatchesPartOfTheName(t *testing.T) {
	e := newEnv(t)
	xml := fixture(t, "nameanddesc.bpmn")
	for _, name := range []string{"foo bar batz", "foo", "42", "bar"} {
		e.createDesign(alice, name, xml)
	}
	for search, expected := range map[string]int{"foo": 2, "foo bar": 1, "bar": 2, "42": 1, "batz": 1} {
		for _, sort := range []string{"name.asc", "name.desc"} {
			var designs []deprecated.SmartServiceDesign
			e.call(alice, http.MethodGet, "/smart-services/designs?sort="+sort+"&search="+url.QueryEscape(search), nil, &designs)
			if len(designs) != expected {
				t.Errorf("search %q sorted by %v: %v, want %v", search, sort, designNames(designs), expected)
			}
		}
	}
}

// A design is created with the id of its request, by PUT as platform-content does and by POST
// with an id, as smart-service-repository did (SNRGY-5035).
func TestADesignIsCreatedWithTheIdOfItsRequest(t *testing.T) {
	e := newEnv(t)
	xml := fixture(t, "nameanddesc.bpmn")
	const put, posted = "cda74dea-0000-4000-8000-000000000001", "cda74dea-0000-4000-8000-000000000002"
	var created deprecated.SmartServiceDesign
	if status, _ := e.call(alice, http.MethodPut, "/smart-services/designs/"+put, map[string]any{"id": put, "name": "Anomaly-Detection", "bpmn_xml": xml, "svg_xml": "<svg/>"}, &created); status != http.StatusOK || created.Id != put || created.UserId != "alice" {
		t.Fatalf("PUT of an unknown id creates the design: %v %+v", status, created)
	}
	var updated deprecated.SmartServiceDesign
	if status, _ := e.call(alice, http.MethodPut, "/smart-services/designs/"+put, map[string]any{"name": "renamed", "bpmn_xml": xml, "svg_xml": "<svg/>"}, &updated); status != http.StatusOK || updated.Id != put || updated.Name != "renamed" {
		t.Errorf("the next PUT updates it: %v %+v", status, updated)
	}
	var viaPost deprecated.SmartServiceDesign
	if status, _ := e.call(alice, http.MethodPost, "/smart-services/designs", map[string]any{"id": posted, "name": "posted", "bpmn_xml": xml, "svg_xml": "<svg/>"}, &viaPost); status != http.StatusOK || viaPost.Id != posted {
		t.Errorf("POST takes the id of its body: %v %+v", status, viaPost)
	}
	var read deprecated.SmartServiceDesign
	if status, _ := e.call(alice, http.MethodGet, "/smart-services/designs/"+posted, nil, &read); status != http.StatusOK || read.Name != "posted" {
		t.Errorf("the design reads back under its id: %v %+v", status, read)
	}
	if status, _ := e.call(alice, http.MethodPut, "/smart-services/designs/"+put, map[string]any{"id": posted, "name": "x", "bpmn_xml": xml, "svg_xml": "<svg/>"}, nil); status != http.StatusBadRequest {
		t.Errorf("a body naming another id is refused: %v", status)
	}
}

// The id of a design of another user is refused, by PUT and by POST, and changes nothing.
func TestTheIdOfADesignOfAnotherUserIsRefused(t *testing.T) {
	e := newEnv(t)
	xml := fixture(t, "nameanddesc.bpmn")
	design := e.createDesign(alice, "Room info", xml)
	if status, _ := e.call(bob, http.MethodPut, "/smart-services/designs/"+design.Id, map[string]any{"name": "taken", "bpmn_xml": xml, "svg_xml": "<svg/>"}, nil); status != http.StatusConflict {
		t.Errorf("PUT: expected 409, got %v", status)
	}
	if status, _ := e.call(bob, http.MethodPost, "/smart-services/designs", map[string]any{"id": design.Id, "name": "taken", "bpmn_xml": xml, "svg_xml": "<svg/>"}, nil); status != http.StatusConflict {
		t.Errorf("POST: expected 409, got %v", status)
	}
	var unchanged deprecated.SmartServiceDesign
	if e.call(alice, http.MethodGet, "/smart-services/designs/"+design.Id, nil, &unchanged); unchanged.Name != "Room info" || unchanged.UserId != "alice" {
		t.Errorf("the design of alice stays as it was: %+v", unchanged)
	}
}

// The id of a deleted design whose releases another user still holds is refused, so that nobody
// takes over releases of others under it (SNRGY-4993).
func TestTheIdOfReleasesOfAnotherUserIsRefused(t *testing.T) {
	e := newEnv(t)
	design := e.createDesign(alice, "Room info", smartServiceDesign("widget"))
	e.createRelease(alice, design.Id, "first")
	if status, _ := e.call(alice, http.MethodDelete, "/smart-services/designs/"+design.Id, nil, nil); status != http.StatusOK {
		t.Fatalf("delete the design: %v", status)
	}
	body := map[string]any{"name": "taken", "bpmn_xml": smartServiceDesign("widget"), "svg_xml": "<svg/>"}
	if status, _ := e.call(bob, http.MethodPut, "/smart-services/designs/"+design.Id, body, nil); status != http.StatusConflict {
		t.Errorf("expected 409, got %v", status)
	}
	var again deprecated.SmartServiceDesign
	if status, _ := e.call(alice, http.MethodPut, "/smart-services/designs/"+design.Id, body, &again); status != http.StatusOK || again.Id != design.Id {
		t.Errorf("the owner of the releases takes the id again: %v %+v", status, again)
	}
}
