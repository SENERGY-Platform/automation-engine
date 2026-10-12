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

package compat

import (
	"strings"
)

type ScriptEnvVariables struct {
	env *ScriptEnv
}

func NewVariablesScriptEnv(env *ScriptEnv) *ScriptEnvVariables {
	return &ScriptEnvVariables{env: env}
}

// Write value as smart-service instance variable
func (this *ScriptEnvVariables) Write(name string, value interface{}) {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	this.env.Variables[name] = value
	this.env.VariablesUpdates[name] = value
}

// Read value of a smart-service instance variable
func (this *ScriptEnvVariables) Read(name string) interface{} {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	return this.env.Variables[name]
}

// Exists checks if a smart-service instance variable exists
func (this *ScriptEnvVariables) Exists(name string) bool {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	_, exists := this.env.Variables[name]
	return exists
}

// Ref creates a reference to a variable (e.g. "my_var_name" --> "{{.my_var_name}}")
// throws exception if variable is unknown
func (this *ScriptEnvVariables) Ref(name string) string {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	_, ok := this.env.Variables[name]
	if !ok {
		panic("unknown variable name")
	}
	return "{{." + name + "}}"
}

var TrowErrorForUnknownDerefName = false

// DerefName returns the name of a smart-service instance variable referenced in parameter ref
func (this *ScriptEnvVariables) DerefName(ref string) string {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	if !strings.HasPrefix(ref, "{{.") {
		panic("derefName input is not valid reference: missing '{{.' prefix")
	}
	if !strings.HasSuffix(ref, "}}") {
		panic("derefName input is not valid reference: missing '}}' suffix")
	}
	name := strings.TrimPrefix(strings.TrimSuffix(ref, "}}"), "{{.")
	if TrowErrorForUnknownDerefName {
		_, ok := this.env.Variables[name]
		if !ok {
			panic("unknown variable name")
		}
	}
	return name
}

// DerefValue returns the value of a smart-service instance variable referenced in parameter ref
func (this *ScriptEnvVariables) DerefValue(ref string) interface{} {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	return this.env.Variables[this.DerefName(ref)]
}

// DerefTemplate replaces variable references in the input string with the corresponding variable values
func (this *ScriptEnvVariables) DerefTemplate(templ string) string {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	placeholder, err := GetPlaceholder(this.env.Variables)
	if err != nil {
		panic(err)
	}
	result, err := Replace(templ, placeholder)
	if err != nil {
		panic(err)
	}
	return result
}
