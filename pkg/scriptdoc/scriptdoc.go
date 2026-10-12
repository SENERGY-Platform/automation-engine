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

// Package scriptdoc generates the documentation of the two JavaScript environments of the
// engine into docs/scripts: the pre- and postscripts of module tasks (pkg/modules/compat), as
// typescript declarations, jsdoc and ace completer like smart-service-module-worker-lib did,
// and the scripts and expressions of BPMN models (pkg/script). A Markdown reference covers
// both. Methods and comments are read from the Go source, types and field names by reflection
// with the name mapping of the script runtime, so the documentation follows the code.
package scriptdoc

//go:generate go run ./generate

import (
	"github.com/SENERGY-Platform/automation-engine/pkg/modules/compat"
)

// Files maps the paths below docs/scripts to their content.
type Files map[string]string

const (
	ModuleDeclarations   = "modules/script-env.d.ts"
	ModuleJsDoc          = "modules/jsdoc.js"
	ModuleAceCompleter   = "modules/ace-code-completer.ts"
	EngineDeclarations   = "engine/script-env.d.ts"
	Reference            = "README.md"
	generatedNoteCommand = "go generate ./pkg/scriptdoc"
)

// Generate creates all files; compatSource is the directory of pkg/modules/compat, whose
// method comments become the descriptions.
func Generate(compatSource string) (Files, error) {
	methods, err := Methods(compatSource, compat.Namespaces())
	if err != nil {
		return nil, err
	}
	typeDefs := TypeDefs()
	result := Files{}
	generators := map[string]func() (string, error){
		ModuleDeclarations: func() (string, error) { return typescriptDeclarations(methods, typeDefs) },
		ModuleJsDoc:        func() (string, error) { return jsDoc(methods, typeDefs) },
		ModuleAceCompleter: func() (string, error) { return aceCompleter(methods) },
		EngineDeclarations: engineDeclarations,
		Reference:          func() (string, error) { return reference(methods, typeDefs) },
	}
	for name, generate := range generators {
		if result[name], err = generate(); err != nil {
			return nil, err
		}
	}
	return result, nil
}
