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

import "encoding/json"

type ScriptEnvOutputs struct {
	env *ScriptEnv
}

func NewOutputsScriptEnv(env *ScriptEnv) *ScriptEnvOutputs {
	return &ScriptEnvOutputs{env: env}
}

// Set a process worker output
func (this *ScriptEnvOutputs) Set(name string, value interface{}) {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	this.env.Outputs[name] = value
}

// Get a process worker output
func (this *ScriptEnvOutputs) Get(name string) interface{} {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	return this.env.Outputs[name]
}

// SetJson marshals the given value to json and sets it as a process worker output
func (this *ScriptEnvOutputs) SetJson(name string, value interface{}) {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	temp, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	this.env.Outputs[name] = string(temp)
}
