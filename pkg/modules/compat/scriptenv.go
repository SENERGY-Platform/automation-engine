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

// Package compat keeps the module task types compatible with the former smart service module
// workers (TASK-28): string inputs are templates over the instance variables, and prescript
// and postscript inputs run with the script API of smart-service-module-worker-lib, ported
// from that library. Designs written for the workers run unchanged.
package compat

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SENERGY-Platform/device-repository/v3/lib/client"
	"github.com/dop251/goja"
)

const PreScriptPrefix = "prescript"
const PostScriptPrefix = "postscript"

// TokenSource returns a token of a user including the "Bearer " prefix.
type TokenSource func(userId string) (string, error)

// ScriptEnv is what a script sees: the variables of the instance, the inputs and outputs of the
// task, the device repository and helpers, all with the rights of the user.
type ScriptEnv struct {
	vm               *goja.Runtime
	ctx              context.Context
	logger           *slog.Logger
	Variables        map[string]interface{}
	VariablesUpdates map[string]interface{}
	Inputs           map[string]interface{}
	Outputs          map[string]interface{}
	tokens           TokenSource
	iotClient        client.Interface
	userId           string
	userToken        string
	mux              sync.Mutex
}

func NewScriptEnv(ctx context.Context, logger *slog.Logger, tokens TokenSource, iotClient client.Interface, userId string, variables map[string]interface{}, inputs map[string]interface{}, outputs map[string]interface{}) *ScriptEnv {
	result := &ScriptEnv{ctx: ctx, logger: logger, Variables: variables, VariablesUpdates: map[string]interface{}{}, Inputs: RemoveScriptInputs(inputs),
		Outputs: outputs, tokens: tokens, iotClient: iotClient, userId: userId}
	if result.Outputs == nil {
		result.Outputs = map[string]interface{}{}
	}
	if result.ctx == nil {
		result.ctx = context.Background()
	}
	if result.logger == nil {
		result.logger = slog.Default()
	}
	return result
}

func RemoveScriptInputs(inputs map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{}
	for name, value := range inputs {
		if !strings.HasPrefix(name, PostScriptPrefix) && !strings.HasPrefix(name, PreScriptPrefix) {
			result[name] = value
		}
	}
	return result
}

func (this *ScriptEnv) GetVm() *goja.Runtime {
	return this.vm
}

func (this *ScriptEnv) environment() map[string]interface{} {
	return map[string]interface{}{
		"variables":  NewVariablesScriptEnv(this),
		"inputs":     NewInputsScriptEnv(this),
		"outputs":    NewOutputsScriptEnv(this),
		"deviceRepo": NewDeviceRepoScriptEnv(this),
		"util":       NewScriptEnvUtil(this),
		"console":    NewConsoleScriptEnv(this),
	}
}

func (this *ScriptEnv) getToken() string {
	this.mux.Lock()
	defer this.mux.Unlock()
	if this.userToken != "" {
		return this.userToken
	}
	token, err := this.tokens(this.userId)
	if err != nil {
		panic(err)
	}
	this.userToken = token
	return this.userToken
}

// Scripts runs the prescript or postscript inputs of a task, concatenated in the order of their
// names, within 2 seconds. Variables written by the script change in variables and are returned
// as changes; outputs extend the given ones.
type Scripts struct {
	Logger *slog.Logger
	Tokens TokenSource
	Iot    client.Interface
}

func (this Scripts) Run(ctx context.Context, userId string, prefix string, inputs map[string]interface{}, outputs map[string]interface{}, variables map[string]interface{}) (variableChanges map[string]interface{}, result map[string]interface{}, err error) {
	type keyValue struct{ key, value string }
	parts := []keyValue{}
	for name, value := range inputs {
		if str, ok := value.(string); ok && strings.HasPrefix(name, prefix) {
			parts = append(parts, keyValue{name, str})
		}
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].key < parts[j].key })
	script := strings.Builder{}
	for _, part := range parts {
		script.WriteString(part.value)
	}
	env := NewScriptEnv(ctx, this.Logger.With("script", prefix), this.Tokens, this.Iot, userId, variables, inputs, outputs)
	vm := goja.New()
	timer := time.AfterFunc(2*time.Second, func() {
		vm.Interrupt("script execution timeout")
	})
	defer timer.Stop()
	env.vm = vm
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	for key, value := range env.environment() {
		if err = vm.Set(key, value); err != nil {
			return nil, nil, err
		}
	}
	if _, err = vm.RunString(script.String()); err != nil {
		return nil, nil, err
	}
	return env.VariablesUpdates, env.Outputs, nil
}
