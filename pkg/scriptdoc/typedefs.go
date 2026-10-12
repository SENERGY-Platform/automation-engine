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
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"reflect"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/SENERGY-Platform/automation-engine/pkg/modules/compat"
	devicemodel "github.com/SENERGY-Platform/device-repository/v3/lib/model"
	"github.com/SENERGY-Platform/models/go/models"
	"github.com/dop251/goja"
)

// fieldNameMapper is the one the script runtimes install: scripts see struct fields under their
// json tag name, and fields without a tag usable as identifier not at all.
var fieldNameMapper = goja.TagFieldNameMapper("json", true)

type TypeDefField struct {
	Name string
	// jsdoc notation
	Type string
	// read from the Go comment of the declaring struct; the note may be empty
	Deprecated      bool
	DeprecationNote string
}

type TypeDef struct {
	Name   string
	Fields []TypeDefField
}

// typeDefSources are the values module scripts read and write through deviceRepo and util.
func typeDefSources() []any {
	return []any{
		models.AspectNode{},
		models.Aspect{},
		models.Characteristic{},
		models.ConceptWithCharacteristics{},
		models.Concept{},
		models.DeviceClass{},
		models.Function{},
		devicemodel.FilterCriteria{},
		devicemodel.DeviceTypeSelectable{},
		models.Service{},
		models.DeviceType{},
		models.Device{},
		models.DeviceGroup{},
		models.Hub{},
		models.Location{},
		compat.IotOption{},
	}
}

// TypeDefs are the typedefs of all sources and the structs they reference, sorted by name.
func TypeDefs() (result []TypeDef) {
	for _, source := range typeDefSources() {
		for _, def := range TypeDefOf(source) {
			if !slices.ContainsFunc(result, func(d TypeDef) bool { return d.Name == def.Name }) {
				result = append(result, def)
			}
		}
	}
	slices.SortFunc(result, func(a, b TypeDef) int { return strings.Compare(a.Name, b.Name) })
	return result
}

// TypeDefOf is the typedef of a struct value and of the structs its fields reference.
func TypeDefOf(value any) []TypeDef {
	return typeDefOf(reflect.TypeOf(value), map[string]bool{})
}

func typeDefOf(t reflect.Type, done map[string]bool) (result []TypeDef) {
	def := TypeDef{Name: jsDocName(t.Name())}
	if done[def.Name] {
		return nil
	}
	done[def.Name] = true
	sub := []TypeDef{}
	for _, field := range reflect.VisibleFields(t) {
		if !field.IsExported() {
			continue
		}
		name := fieldNameMapper.FieldName(t, field)
		if name == "" || slices.ContainsFunc(def.Fields, func(f TypeDefField) bool { return f.Name == name }) {
			// invisible to scripts, or shadowed by a field closer to the surface
			continue
		}
		note, deprecated := deprecationOf(declaringStruct(t, field), field.Name)
		def.Fields = append(def.Fields, TypeDefField{Name: name, Type: typeName(field.Type), Deprecated: deprecated, DeprecationNote: note})
		if nested := structOf(field.Type); nested != nil {
			sub = append(sub, typeDefOf(nested, done)...)
		}
	}
	return append([]TypeDef{def}, sub...)
}

func structOf(t reflect.Type) reflect.Type {
	switch t.Kind() {
	case reflect.Struct:
		return t
	case reflect.Map, reflect.Slice, reflect.Pointer:
		return structOf(t.Elem())
	}
	return nil
}

func typeName(t reflect.Type) string {
	if t.Kind() == reflect.Struct && t.Name() != "" {
		return jsDocName(t.Name())
	}
	// a named type like models.Interaction is its underlying value for scripts
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice:
		return typeName(t.Elem()) + "[]"
	case reflect.Map:
		return "Map<string," + typeName(t.Elem()) + ">"
	case reflect.Pointer:
		return typeName(t.Elem()) + "|null"
	case reflect.Interface:
		return "Object|null"
	}
	return ""
}

// declaringStruct is the struct that declares the field, which differs for promoted fields
func declaringStruct(t reflect.Type, field reflect.StructField) reflect.Type {
	for _, i := range field.Index[:len(field.Index)-1] {
		t = t.Field(i).Type
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
	}
	return t
}

// A field is deprecated when a line of the comment above or beside it in the Go source opens
// with the word "deprecated", as the platform models mark them; what follows is the note. The
// marking stays where it is decided, in the model, instead of a list kept here.
const deprecationMarker = "deprecated"

var deprecations = struct {
	sync.Mutex
	byPackage map[string]map[string]string
}{byPackage: map[string]map[string]string{}}

func deprecationOf(declaring reflect.Type, fieldName string) (note string, deprecated bool) {
	if declaring == nil || declaring.PkgPath() == "" || declaring.Name() == "" {
		return "", false
	}
	note, deprecated = deprecationsOfPackage(declaring.PkgPath())[declaring.Name()+"."+fieldName]
	return note, deprecated
}

func deprecationsOfPackage(pkgPath string) map[string]string {
	deprecations.Lock()
	defer deprecations.Unlock()
	if known, ok := deprecations.byPackage[pkgPath]; ok {
		return known
	}
	result := map[string]string{}
	deprecations.byPackage[pkgPath] = result
	pkg, err := build.Import(pkgPath, ".", build.FindOnly)
	if err != nil {
		return result
	}
	parsed, err := parser.ParseDir(token.NewFileSet(), pkg.Dir, nil, parser.ParseComments)
	if err != nil {
		return result
	}
	for _, astPackage := range parsed {
		for _, file := range astPackage.Files {
			ast.Inspect(file, func(node ast.Node) bool {
				spec, ok := node.(*ast.TypeSpec)
				if !ok {
					return true
				}
				structType, ok := spec.Type.(*ast.StructType)
				if !ok {
					return true
				}
				for _, field := range structType.Fields.List {
					if note, deprecated := deprecationComment(field); deprecated {
						for _, name := range field.Names {
							result[spec.Name.Name+"."+name.Name] = note
						}
					}
				}
				return true
			})
		}
	}
	return result
}

func deprecationComment(field *ast.Field) (string, bool) {
	for _, comment := range []string{field.Doc.Text(), field.Comment.Text()} {
		for _, line := range strings.Split(comment, "\n") {
			if note, deprecated := deprecationOfLine(line); deprecated {
				return note, true
			}
		}
	}
	return "", false
}

// deprecationOfLine: the word has to stand alone, a "deprecatedName" says nothing about the field
func deprecationOfLine(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if len(line) < len(deprecationMarker) || !strings.EqualFold(line[:len(deprecationMarker)], deprecationMarker) {
		return "", false
	}
	rest := line[len(deprecationMarker):]
	if first, _ := utf8.DecodeRuneInString(rest); rest != "" && (unicode.IsLetter(first) || unicode.IsDigit(first) || first == '_') {
		return "", false
	}
	return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(rest), ":,-")), true
}
