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
	"embed"
	"slices"
	"strings"
	"text/template"
)

//go:embed templates/*.tmpl
var templateFiles embed.FS

var templates = template.Must(template.New("").Option("missingkey=zero").Funcs(template.FuncMap{
	"join": strings.Join,
}).ParseFS(templateFiles, "templates/*.tmpl"))

func render(name string, data map[string]any) (string, error) {
	builder := strings.Builder{}
	err := templates.ExecuteTemplate(&builder, name, data)
	return builder.String(), err
}

// CommentLines splits the comment for a block comment
func (this Method) CommentLines() []string {
	if this.Comment == "" {
		return nil
	}
	return strings.Split(this.Comment, "\n")
}

func namespacesOf(methods []Method) (result []string) {
	for _, m := range methods {
		if !slices.Contains(result, m.Namespace) {
			result = append(result, m.Namespace)
		}
	}
	return result
}

var moduleNotes = []string{
	"",
	"the script API of pre- and postscripts of module tasks. property names are the names the",
	"script runtime exposes, the json tag names of the underlying go structs.",
}

func jsDoc(methods []Method, typeDefs []TypeDef) (string, error) {
	return render("jsdoc.tmpl", map[string]any{"notes": moduleNotes, "namespaces": namespacesOf(methods), "methods": methods, "typedefs": typeDefs})
}

func aceCompleter(methods []Method) (string, error) {
	assignments, statements := []Method{}, []Method{}
	for _, m := range methods {
		if m.Result != nil {
			assignments = append(assignments, m)
		} else {
			statements = append(statements, m)
		}
	}
	// a statement position offers every method, results last
	statements = append(statements, assignments...)
	return render("ace.tmpl", map[string]any{"notes": moduleNotes, "assignments": assignments, "statements": statements})
}

type Interface struct {
	Name       string
	Properties []Property
}

type Property struct {
	Name            string
	Type            string
	Deprecated      bool
	DeprecationNote string
}

type Namespace struct {
	Name    string
	Methods []TsMethod
	// set if the typescript standard library declares the global already, e.g.
	// "declare var console: Console" in lib.dom; a "declare const" would collide with it
	Interface string
}

type TsMethod struct {
	Name    string
	Params  string
	Result  string
	Comment []string
}

var builtinGlobals = map[string]string{"console": "Console"}

func Interfaces(typeDefs []TypeDef) (result []Interface) {
	for _, def := range typeDefs {
		element := Interface{Name: def.Name}
		for _, field := range def.Fields {
			element.Properties = append(element.Properties, Property{Name: field.Name, Type: ToTsType(field.Type), Deprecated: field.Deprecated, DeprecationNote: field.DeprecationNote})
		}
		result = append(result, element)
	}
	return result
}

func Namespaces(methods []Method) (result []Namespace) {
	for _, m := range methods {
		i := slices.IndexFunc(result, func(n Namespace) bool { return n.Name == m.Namespace })
		if i < 0 {
			result = append(result, Namespace{Name: m.Namespace, Interface: builtinGlobals[m.Namespace]})
			i = len(result) - 1
		}
		result[i].Methods = append(result[i].Methods, tsMethod(m))
	}
	return result
}

func tsMethod(m Method) TsMethod {
	result := TsMethod{Name: m.Name, Result: "void", Comment: m.CommentLines()}
	if m.Result != nil {
		result.Result = ToTsType(m.Result.Type)
	}
	params := []string{}
	for _, p := range m.Params {
		if element, rest := strings.CutPrefix(p.Type, "..."); rest {
			params = append(params, "..."+p.Name+": "+ToTsType(element)+"[]")
			continue
		}
		params = append(params, p.Name+": "+ToTsType(p.Type))
	}
	result.Params = strings.Join(params, ", ")
	return result
}

// ToTsType translates a jsdoc type; a go map reaches scripts as a plain object, not as a Map
func ToTsType(jsDocType string) string {
	switch {
	case jsDocType == "":
		return "void"
	case strings.HasSuffix(jsDocType, "[]"):
		return ToTsType(strings.TrimSuffix(jsDocType, "[]")) + "[]"
	case strings.HasSuffix(jsDocType, "|null"):
		element := ToTsType(strings.TrimSuffix(jsDocType, "|null"))
		if element == "any" {
			return element
		}
		return element + " | null"
	case strings.HasPrefix(jsDocType, "Map<string,") && strings.HasSuffix(jsDocType, ">"):
		return "Record<string, " + ToTsType(strings.TrimSuffix(strings.TrimPrefix(jsDocType, "Map<string,"), ">")) + ">"
	case jsDocType == "Object":
		return "any"
	}
	return jsDocType
}

func typescriptDeclarations(methods []Method, typeDefs []TypeDef) (string, error) {
	return render("typescript.tmpl", map[string]any{"notes": moduleNotes, "interfaces": Interfaces(typeDefs), "namespaces": Namespaces(methods)})
}
