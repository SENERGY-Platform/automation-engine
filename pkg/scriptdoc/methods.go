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
	"go/parser"
	"go/token"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// Method is a function of a script namespace, read from the Go source of the type behind it.
type Method struct {
	Namespace string
	Name      string
	Params    []Parameter
	// example arguments, e.g. "id_as_string, limit_as_number"
	ParamsStr string
	Result    *Parameter
	Comment   string
}

// Parameter has its type in jsdoc notation.
type Parameter struct {
	Name         string
	NameWithType string
	Type         string
}

// Methods reads the exported methods of the types behind the namespaces from the source in dir,
// sorted by namespace and name. An unexported method is not callable from a script.
func Methods(dir string, namespaces map[string]any) (result []Method, err error) {
	parsed, err := parser.ParseDir(token.NewFileSet(), dir, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	for namespace, value := range namespaces {
		t := reflect.TypeOf(value)
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		for _, decl := range methodDecls(parsed, t.Name()) {
			method := Method{Namespace: namespace, Name: uncapitalize(decl.Name.Name), Comment: strings.TrimSpace(decl.Doc.Text())}
			examples := []string{}
			index := 0
			for _, field := range decl.Type.Params.List {
				names := field.Names
				if len(names) == 0 {
					names = []*ast.Ident{nil}
				}
				for _, name := range names {
					param := parameter(field.Type, name, "param"+strconv.Itoa(index))
					method.Params = append(method.Params, param)
					examples = append(examples, param.NameWithType)
					index++
				}
			}
			if decl.Type.Results != nil && len(decl.Type.Results.List) > 0 {
				first := decl.Type.Results.List[0]
				var name *ast.Ident
				if len(first.Names) > 0 {
					name = first.Names[0]
				}
				result := parameter(first.Type, name, "result")
				method.Result = &result
			}
			method.ParamsStr = strings.Join(examples, ", ")
			result = append(result, method)
		}
	}
	slices.SortFunc(result, func(a, b Method) int {
		if c := strings.Compare(a.Namespace, b.Namespace); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return result, nil
}

func methodDecls(parsed map[string]*ast.Package, typeName string) (result []*ast.FuncDecl) {
	for _, pkg := range parsed {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				f, ok := decl.(*ast.FuncDecl)
				if !ok || !f.Name.IsExported() || f.Recv == nil || len(f.Recv.List) == 0 {
					continue
				}
				receiver := f.Recv.List[0].Type
				if star, isStar := receiver.(*ast.StarExpr); isStar {
					receiver = star.X
				}
				if ident, isIdent := receiver.(*ast.Ident); isIdent && ident.Name == typeName {
					result = append(result, f)
				}
			}
		}
	}
	return result
}

func uncapitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func parameter(expr ast.Expr, name *ast.Ident, defaultName string) Parameter {
	result := Parameter{Name: defaultName, Type: jsDocTypeOf(expr)}
	if name != nil {
		result.Name = name.Name
	}
	result.NameWithType = result.Name
	if result.Type != "" {
		result.NameWithType = result.Name + "_as_" + typeNameOf(expr)
	}
	return result
}

// typeNameOf is the type as part of an example argument name
func typeNameOf(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return jsDocName(t.Name)
	case *ast.SelectorExpr:
		return jsDocName(t.Sel.Name)
	case *ast.InterfaceType:
		return "any"
	case *ast.MapType:
		return typeNameOf(t.Value) + "_map"
	case *ast.ArrayType:
		return typeNameOf(t.Elt) + "_list"
	case *ast.Ellipsis:
		return typeNameOf(t.Elt)
	}
	return ""
}

func jsDocTypeOf(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return jsDocName(t.Name)
	case *ast.SelectorExpr:
		return jsDocName(t.Sel.Name)
	case *ast.InterfaceType:
		return "Object"
	case *ast.MapType:
		return "Map<string," + jsDocTypeOf(t.Value) + ">"
	case *ast.ArrayType:
		return jsDocTypeOf(t.Elt) + "[]"
	case *ast.Ellipsis:
		// a rest parameter: any number of arguments of the element type
		return "..." + jsDocTypeOf(t.Elt)
	}
	return ""
}

// jsDocName translates Go type names; "Function" would collide with the type of JavaScript
func jsDocName(name string) string {
	switch name {
	case "Function":
		return "FunctionType"
	case "bool":
		return "boolean"
	case "int64", "int", "int32", "float", "float64", "float32":
		return "number"
	case "any":
		return "Object"
	}
	return name
}
