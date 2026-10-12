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
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/modules/compat"
	"github.com/SENERGY-Platform/automation-engine/pkg/script"
	devicemodel "github.com/SENERGY-Platform/device-repository/v3/lib/model"
	"github.com/SENERGY-Platform/models/go/models"
	"github.com/dop251/goja"
)

// Most tests here are those of the generator of smart-service-module-worker-lib.

const compatSource = "../modules/compat"

// The documentation is committed; it has to be regenerated whenever the script API changes.
func TestGeneratedFilesAreCurrent(t *testing.T) {
	files, err := Generate(compatSource)
	if err != nil {
		t.Fatal(err)
	}
	for name, expected := range files {
		actual, err := os.ReadFile(filepath.Join("../../docs/scripts", name))
		if err != nil || string(actual) != expected {
			t.Errorf("docs/scripts/%v is outdated, run: go generate ./pkg/scriptdoc", name)
		}
	}
}

func TestTypeDefUsesJsonTagNames(t *testing.T) {
	defs := TypeDefOf(models.Aspect{})
	expected := []TypeDefField{{Name: "id", Type: "string"}, {Name: "name", Type: "string"}, {Name: "aspect_class_id", Type: "string"}, {Name: "sub_aspects", Type: "Aspect[]"}}
	if len(defs) != 1 || !reflect.DeepEqual(defs[0].Fields, expected) {
		t.Errorf("%#v", defs)
	}
}

type visibilityFixture struct {
	Plain             string `json:"plain"`
	WithOptions       string `json:"with_options,omitempty"`
	Untagged          string
	Excluded          string `json:"-"`
	NoIdentifier      string `json:"no-identifier"`
	EmptyTag          string `json:""`
	unexported        string
	Hidden            visibilityHidden
	VisibilityPromote `json:"emb"`
}

type visibilityHidden struct {
	Foo string `json:"foo"`
}

type VisibilityPromote struct {
	Promoted string `json:"promoted"`
}

// A field a script cannot reach is not documented, and the rule is checked against goja.
func TestTypeDefFieldVisibility(t *testing.T) {
	_ = visibilityFixture{}.unexported
	defs := TypeDefOf(visibilityFixture{})
	expected := []string{"emb", "plain", "promoted", "with_options"}
	if actual := fieldNames(defs[0]); !reflect.DeepEqual(actual, expected) {
		t.Errorf("documented %v", actual)
	}
	if actual := scriptVisibleProperties(t, visibilityFixture{}); !reflect.DeepEqual(actual, expected) {
		t.Errorf("the runtime sees %v", actual)
	}
	if _, found := findTypeDef(defs, "visibilityHidden"); found {
		t.Errorf("the type of a hidden field is documented: %#v", defs)
	}
}

func TestTypeDefsMatchScriptRuntime(t *testing.T) {
	defs := TypeDefs()
	for _, source := range typeDefSources() {
		name := jsDocName(reflect.TypeOf(source).Name())
		t.Run(name, func(t *testing.T) {
			def, found := findTypeDef(defs, name)
			if !found {
				t.Fatalf("no typedef for %v", name)
			}
			if expected, actual := scriptVisibleProperties(t, source), fieldNames(def); !reflect.DeepEqual(actual, expected) {
				t.Errorf("runtime %v\ndocumented %v", expected, actual)
			}
		})
	}
}

// Every method a module script can call is documented, and nothing else.
func TestDocumentedMethodsAreTheCallableOnes(t *testing.T) {
	methods, err := Methods(compatSource, compat.Namespaces())
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range compat.Namespaces() {
		t.Run(name, func(t *testing.T) {
			documented := []string{}
			for _, m := range methods {
				if m.Namespace == name {
					documented = append(documented, m.Name)
				}
			}
			callable := scriptVisibleMethods(value)
			sort.Strings(callable)
			if !reflect.DeepEqual(documented, callable) {
				t.Errorf("callable %v\ndocumented %v", callable, documented)
			}
		})
	}
}

// The globals of the BPMN sandbox besides the standard library are exactly the documented ones.
func TestEngineGlobalsAreDocumented(t *testing.T) {
	names := `Object.getOwnPropertyNames(this)`
	standard, err := goja.New().RunString(names)
	if err != nil {
		t.Fatal(err)
	}
	result, err := script.NewSandbox().Run(names, nil)
	if err != nil {
		t.Fatal(err)
	}
	registered := []string{}
	for _, name := range result.Value.([]any) {
		if !slices.Contains(standard.Export().([]any), name) {
			registered = append(registered, name.(string))
		}
	}
	documented := []string{}
	for _, global := range EngineGlobals {
		documented = append(documented, global.Name)
	}
	sort.Strings(registered)
	sort.Strings(documented)
	if !reflect.DeepEqual(registered, documented) {
		t.Errorf("sandbox %v\ndocumented %v", registered, documented)
	}
}

type deprecationFixture struct {
	DeprecationEmbedded `json:"emb"`

	BesideTheField string `json:"beside_the_field"` //deprecated: use something else

	//deprecated: above the field
	AboveTheField string `json:"above_the_field"`

	//Deprecated: the go convention capitalises it
	Capitalised string `json:"capitalised"`

	//deprecated:
	WithoutANote string `json:"without_a_note"`

	Bare string `json:"bare"` //deprecated

	//deprecated - a dash introduces the note just as well
	WithADash string `json:"with_a_dash"`

	//deprecated use the other field
	WithoutPunctuation string `json:"without_punctuation"`

	//deprecatedName was the old spelling of this field
	NamesADeprecation string `json:"names_a_deprecation"`

	//a comment that is not a deprecation
	Current string `json:"current"`
}

type DeprecationEmbedded struct {
	Promoted string `json:"promoted"` //deprecated: declared by the embedded struct
}

func TestDeprecationIsReadFromTheGoComment(t *testing.T) {
	byName := map[string]TypeDefField{}
	for _, field := range TypeDefOf(deprecationFixture{})[0].Fields {
		byName[field.Name] = field
	}
	expected := map[string]TypeDefField{
		"beside_the_field":    {Name: "beside_the_field", Type: "string", Deprecated: true, DeprecationNote: "use something else"},
		"above_the_field":     {Name: "above_the_field", Type: "string", Deprecated: true, DeprecationNote: "above the field"},
		"capitalised":         {Name: "capitalised", Type: "string", Deprecated: true, DeprecationNote: "the go convention capitalises it"},
		"without_a_note":      {Name: "without_a_note", Type: "string", Deprecated: true},
		"bare":                {Name: "bare", Type: "string", Deprecated: true},
		"with_a_dash":         {Name: "with_a_dash", Type: "string", Deprecated: true, DeprecationNote: "a dash introduces the note just as well"},
		"without_punctuation": {Name: "without_punctuation", Type: "string", Deprecated: true, DeprecationNote: "use the other field"},
		"names_a_deprecation": {Name: "names_a_deprecation", Type: "string"},
		"current":             {Name: "current", Type: "string"},
		"emb":                 {Name: "emb", Type: "DeprecationEmbedded"},
		"promoted":            {Name: "promoted", Type: "string", Deprecated: true, DeprecationNote: "declared by the embedded struct"},
	}
	if !reflect.DeepEqual(byName, expected) {
		t.Errorf("%#v", byName)
	}
}

func TestDeclarationsMarkDeprecatedFieldsOfTheModels(t *testing.T) {
	files, err := Generate(compatSource)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := findTypeDef(TypeDefOf(devicemodel.FilterCriteria{}), "FilterCriteria"); !found {
		t.Fatal("no typedef of FilterCriteria")
	}
	for name, expected := range map[string][]string{
		ModuleDeclarations: {
			"    /** @deprecated alias for a single element AspectIds; normalized into AspectIds at the controller boundary */\n    aspect_id: string;\n    aspect_ids: string[];",
			"    /** @deprecated alias for a single element AspectNodes; holds the node with the alphabetically first id */\n    aspect_node: AspectNode;",
		},
		ModuleJsDoc: {" * @property { string } aspect_id deprecated: alias for a single element AspectIds"},
		Reference:   {"- `aspect_id: string` (abgekündigt: alias for a single element AspectIds"},
	} {
		for _, e := range expected {
			if !strings.Contains(files[name], e) {
				t.Errorf("%v misses %q", name, e)
			}
		}
	}
	if strings.Contains(files[ModuleJsDoc], "function_id deprecated") {
		t.Error("a current field is marked as deprecated")
	}
}

func TestToTsType(t *testing.T) {
	for jsDoc, expected := range map[string]string{
		"": "void", "string": "string", "Aspect[]": "Aspect[]", "Object": "any", "Object|null": "any", "Object|null[]": "any[]",
		"string|null": "string | null", "Map<string,IotOption[]>": "Record<string, IotOption[]>", "Map<string,string>[]": "Record<string, string>[]",
	} {
		if actual := ToTsType(jsDoc); actual != expected {
			t.Errorf("%v: %v", jsDoc, actual)
		}
	}
}

var typeNameExpr = regexp.MustCompile(`[A-Za-z_][A-Za-z_0-9]*`)

// typescript rejects a reference to an undeclared type and a member declared twice, jsdoc not
func TestDeclarationsAreConsistent(t *testing.T) {
	methods, err := Methods(compatSource, compat.Namespaces())
	if err != nil {
		t.Fatal(err)
	}
	interfaces := Interfaces(TypeDefs())
	declared := []string{"string", "number", "boolean", "any", "void", "null", "Record"}
	for _, i := range interfaces {
		declared = append(declared, i.Name)
	}
	check := func(context string, tsType string) {
		for _, part := range strings.Split(tsType, ",") {
			if index := strings.Index(part, ":"); index >= 0 {
				part = part[index+1:]
			}
			for _, name := range typeNameExpr.FindAllString(part, -1) {
				if !slices.Contains(declared, name) {
					t.Errorf("%v references undeclared type %v", context, name)
				}
			}
		}
	}
	unique := func(context string, names []string) {
		for i, name := range names {
			if slices.Contains(names[:i], name) {
				t.Errorf("%v declares %v twice", context, name)
			}
		}
	}
	for _, i := range interfaces {
		names := []string{}
		for _, p := range i.Properties {
			check(i.Name+"."+p.Name, p.Type)
			names = append(names, p.Name)
		}
		unique(i.Name, names)
	}
	for _, n := range Namespaces(methods) {
		names := []string{}
		for _, m := range n.Methods {
			check(n.Name+"."+m.Name, m.Params)
			check(n.Name+"."+m.Name, m.Result)
			names = append(names, m.Name)
		}
		unique(n.Name, names)
	}
}

func scriptVisibleProperties(t *testing.T, value any) []string {
	t.Helper()
	vm := goja.New()
	vm.SetFieldNameMapper(fieldNameMapper)
	if err := vm.Set("value", value); err != nil {
		t.Fatal(err)
	}
	keys, err := vm.RunString(`Object.keys(value)`)
	if err != nil {
		t.Fatal(err)
	}
	methods := scriptVisibleMethods(value)
	result := []string{}
	for _, key := range keys.Export().([]any) {
		if !slices.Contains(methods, key.(string)) {
			result = append(result, key.(string))
		}
	}
	sort.Strings(result)
	return result
}

// goja makes values addressable, so methods of pointer receivers are visible as well
func scriptVisibleMethods(value any) (result []string) {
	t := reflect.TypeOf(value)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	for _, candidate := range []reflect.Type{t, reflect.PointerTo(t)} {
		for i := 0; i < candidate.NumMethod(); i++ {
			if name := fieldNameMapper.MethodName(candidate, candidate.Method(i)); !slices.Contains(result, name) {
				result = append(result, name)
			}
		}
	}
	return result
}

func findTypeDef(list []TypeDef, name string) (TypeDef, bool) {
	i := slices.IndexFunc(list, func(def TypeDef) bool { return def.Name == name })
	if i < 0 {
		return TypeDef{}, false
	}
	return list[i], true
}

func fieldNames(def TypeDef) (result []string) {
	result = []string{}
	for _, field := range def.Fields {
		result = append(result, field.Name)
	}
	sort.Strings(result)
	return result
}
