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

package processio_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
	"github.com/SENERGY-Platform/automation-engine/pkg/tasks/processio"
)

var ctx = context.Background()
var t0 = time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)

const (
	owner        = "alice"
	deploymentId = "d1"
	instanceId   = "i1"
)

// newService stores a deployment with a running instance of owner, so that values can be
// bound to them
func newService(t *testing.T, s *store.Store) *processio.Service {
	t.Helper()
	err := s.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.InsertDeployment(model.Deployment{Id: deploymentId, Namespace: "processes", Name: "d", Owner: owner, Version: 1, CreatedAt: t0, UpdatedAt: t0}); err != nil {
			return err
		}
		if err := tx.InsertDeploymentVersion(model.DeploymentVersion{DeploymentId: deploymentId, Version: 1, Xml: "<x/>", CreatedAt: t0}); err != nil {
			return err
		}
		return tx.InsertInstance(store.Instance{Instance: model.Instance{Id: instanceId, Namespace: "processes", Owner: owner, DeploymentId: deploymentId, DeploymentVersion: 1,
			Run: 1, State: model.StateRunning, CreatedAt: t0, StartedAt: t0, LastActivityAt: t0}, ActivityMinSeq: 1})
	})
	if err != nil {
		t.Fatal(err)
	}
	return processio.New(s, processio.Limits{})
}

// execute runs a process_io task of the instance with the given inputs
func execute(t *testing.T, service *processio.Service, input map[string]any) map[string]any {
	t.Helper()
	result, err := processio.NewType(service).Execute(ctx, engine.Task{InstanceId: instanceId, DeploymentId: deploymentId, Namespace: "processes", Owner: owner, TaskType: processio.TaskType, Input: input})
	if err != nil {
		t.Fatal(err)
	}
	return result.Output
}

type binding struct{ deploymentId, instanceId string }

func TestWrittenKeysAreBoundByTheirPlaceholders(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		service := newService(t, s)
		execute(t, service, map[string]any{
			"ignored":                                13.0,
			"io.write.a":                             "a",
			"io.write.instance_{{InstanceId}}_b":     "b",
			"io.write.deployment_{{DeploymentId}}_c": "c",
		})
		page, err := service.List(ctx, owner, model.IoListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]binding{}
		for _, v := range page.Items {
			got[v.Key] = binding{v.DeploymentId, v.InstanceId}
		}
		expected := map[string]binding{"a": {}, "instance_i1_b": {deploymentId, instanceId}, "deployment_d1_c": {deploymentId, ""}}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("expected %v, got %v", expected, got)
		}
	})
}

func TestWrittenValuesReadBackWithTheirJsonType(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		service := newService(t, s)
		execute(t, service, map[string]any{
			"io.write.number":  42.0,
			"io.write.jsonStr": `"json-string"`,
			"io.write.jsonObj": `{"foo": true}`,
			"io.write.bool":    true,
			"io.write.text":    "plain text",
		})
		output := execute(t, service, map[string]any{
			"io.read.o_number":  "number",
			"io.read.o_jsonStr": "jsonStr",
			"io.read.o_jsonObj": "jsonObj",
			"io.read.o_bool":    "bool",
			"io.read.o_text":    "text",
		})
		expected := map[string]any{"o_number": 42.0, "o_jsonStr": "json-string", "o_jsonObj": map[string]any{"foo": true}, "o_bool": true, "o_text": "plain text"}
		if !reflect.DeepEqual(output, expected) {
			t.Fatalf("expected %#v, got %#v", expected, output)
		}
	})
}

func TestReadOfAMissingKeyFallsBackToItsDefaultParsedAsJson(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		service := newService(t, s)
		output := execute(t, service, map[string]any{
			"io.read.text": "instance_{{InstanceId}}_text",
			"io.default.instance_{{InstanceId}}_text": "defaultstringvalue",
			"io.read.number": "instance_{{InstanceId}}_number",
			"io.default.instance_{{InstanceId}}_number":     42.0,
			"io.read.jsonNumber":                            "instance_{{InstanceId}}_jsonNumber",
			"io.default.instance_{{InstanceId}}_jsonNumber": "42",
			"io.read.jsonStr":                               "instance_{{InstanceId}}_jsonStr",
			"io.default.instance_{{InstanceId}}_jsonStr":    `"defaultstringvalue"`,
		})
		expected := map[string]any{"text": "defaultstringvalue", "number": 42.0, "jsonNumber": 42.0, "jsonStr": "defaultstringvalue"}
		if !reflect.DeepEqual(output, expected) {
			t.Fatalf("expected %#v, got %#v", expected, output)
		}
	})
}

func TestReadOfAnExistingKeyIgnoresTheDefault(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		service := newService(t, s)
		execute(t, service, map[string]any{"io.write.instance_{{InstanceId}}_value": "foobar"})
		output := execute(t, service, map[string]any{
			"io.read.value": "instance_{{InstanceId}}_value",
			"io.default.instance_{{InstanceId}}_value": "default",
		})
		if output["value"] != "foobar" {
			t.Fatalf("expected the stored value foobar, got %#v", output["value"])
		}
	})
}

func TestReadOfAnUnknownKeyWithoutDefaultIsNull(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		service := newService(t, s)
		output := execute(t, service, map[string]any{"io.read.unknown": "unknown_key"})
		if value, ok := output["unknown"]; !ok || value != nil {
			t.Fatalf("expected the output unknown with null, got %#v", output)
		}
	})
}
