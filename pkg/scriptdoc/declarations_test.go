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

package scriptdoc

import (
	"reflect"
	"strings"
	"testing"
)

// Further tests of the generator of smart-service-module-worker-lib.

type bareDeprecationFixture struct {
	//Deprecated
	BareAndCapitalised string `json:"bare_and_capitalised"`

	Untouched string `json:"untouched"`
}

func TestBareCapitalisedDeprecationMarksTheField(t *testing.T) {
	fields := TypeDefOf(bareDeprecationFixture{})[0].Fields
	expected := []TypeDefField{
		{Name: "bare_and_capitalised", Type: "string", Deprecated: true},
		{Name: "untouched", Type: "string"},
	}
	if !reflect.DeepEqual(fields, expected) {
		t.Errorf("%#v", fields)
	}
}

// The type of a documented embedded struct needs a typedef of its own.
func TestEmbeddedStructOfADocumentedFieldHasATypeDef(t *testing.T) {
	defs := TypeDefOf(visibilityFixture{})
	if _, found := findTypeDef(defs, "VisibilityPromote"); !found {
		t.Errorf("%#v", defs)
	}
}

func TestModuleDeclarationsAreTypescriptOfTheScriptApi(t *testing.T) {
	files, err := Generate(compatSource)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"interface Aspect {\n    id: string;\n    name: string;\n    aspect_class_id: string;\n    sub_aspects: Aspect[];\n}",
		"declare const deviceRepo: {",
		"    getAspect(id: string): Aspect;",
		"    setJson(name: string, value: any): void;",
	} {
		if !strings.Contains(files[ModuleDeclarations], expected) {
			t.Errorf("missing %q", expected)
		}
	}
}

// A Go variadic parameter becomes a rest parameter.
func TestModuleDeclarationsDeclareVariadicParametersAsRestParameters(t *testing.T) {
	files, err := Generate(compatSource)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(files[ModuleDeclarations], "    log(...args: any[]): void;") {
		t.Error(files[ModuleDeclarations])
	}
}

// lib.dom declares "declare var console: Console;", a "declare const console" would collide.
func TestModuleDeclarationsDeclareConsoleAsTheDomDoes(t *testing.T) {
	files, err := Generate(compatSource)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"interface Console {", "declare var console: Console;"} {
		if !strings.Contains(files[ModuleDeclarations], expected) {
			t.Errorf("missing %q", expected)
		}
	}
}
