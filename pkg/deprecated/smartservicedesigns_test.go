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
