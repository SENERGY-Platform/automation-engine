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

import "sort"

type ScriptEnvInputs struct {
	env *ScriptEnv
}

func NewInputsScriptEnv(env *ScriptEnv) *ScriptEnvInputs {
	return &ScriptEnvInputs{env: env}
}

// Get value of a process worker input
func (this *ScriptEnvInputs) Get(name string) interface{} {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	return this.env.Inputs[name]
}

// Exists checks if a process worker input exists
func (this *ScriptEnvInputs) Exists(name string) bool {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	_, exists := this.env.Inputs[name]
	return exists
}

// List input values sorted by their names
func (this *ScriptEnvInputs) List() []interface{} {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result := []interface{}{}
	for _, name := range this.ListNames() {
		result = append(result, this.Get(name))
	}
	return result
}

// ListNames lists sorted input names
func (this *ScriptEnvInputs) ListNames() []string {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	result := []string{}
	for name, _ := range this.env.Inputs {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}
