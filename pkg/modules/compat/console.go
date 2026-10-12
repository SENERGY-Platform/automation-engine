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
	"encoding/json"
	"fmt"
	"strings"
)

type ScriptEnvConsole struct {
	env *ScriptEnv
}

func NewConsoleScriptEnv(env *ScriptEnv) *ScriptEnvConsole {
	return &ScriptEnvConsole{env: env}
}

// Log writes the arguments, separated by spaces, as info message to the worker log; strings are written as they are, everything else as json
func (this *ScriptEnvConsole) Log(args ...interface{}) {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, formatLogArg(arg))
	}
	//the context carries the trace and the baggage (smart-service-instance-id), the log record picks both up
	this.env.logger.InfoContext(this.env.ctx, strings.Join(parts, " "))
}

// Dump writes all smart-service instance variables, the variables changed by this script, process worker inputs and process worker outputs known at the time of the call to the worker log
func (this *ScriptEnvConsole) Dump() {
	defer func() {
		if caught := recover(); caught != nil {
			panic(this.env.GetVm().ToValue(caught))
		}
	}()
	this.env.logger.InfoContext(this.env.ctx, "script dump", "variables", this.env.Variables, "variablesUpdates", this.env.VariablesUpdates, "inputs", this.env.Inputs, "outputs", this.env.Outputs)
}

func formatLogArg(arg interface{}) string {
	if str, ok := arg.(string); ok {
		return str
	}
	temp, err := json.Marshal(arg)
	if err != nil {
		//e.g. a js function, which has no json representation
		return fmt.Sprint(arg)
	}
	return string(temp)
}
