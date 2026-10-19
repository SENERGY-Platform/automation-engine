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

package compat_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/modules/compat"
	"github.com/SENERGY-Platform/automation-engine/pkg/tracing"
	"github.com/SENERGY-Platform/go-service-base/struct-logger/handlers"
)

// runConsole runs the prescript inputs with a logger built like configuration.Config.Logger, in
// a context that carries an instance in its baggage, and returns the log records
func runConsole(t *testing.T, inputs map[string]any, variables map[string]any) []map[string]any {
	t.Helper()
	ctx := tracing.WithBaggage(context.Background(), tracing.KeyInstanceId, "test-instance-id")
	buf := &bytes.Buffer{}
	logger := slog.New(handlers.NewOpenTelemetryHandler(slog.NewJSONHandler(buf, nil)))
	if _, _, err := scripts(nil, logger).Run(ctx, "user", compat.PreScriptPrefix, inputs, nil, variables); err != nil {
		t.Fatal(err)
	}
	records := []map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		record := map[string]any{}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err, line)
		}
		records = append(records, record)
	}
	return records
}

func TestConsoleLogWritesTheArgumentsAsOneInfoRecordOfTheScript(t *testing.T) {
	records := runConsole(t, map[string]any{"prescript": `console.log("foo", 42, true, null, {"bar": [1, "batz"]});`}, map[string]any{})
	if len(records) != 1 {
		t.Fatalf("expected 1 log record, got %#v", records)
	}
	record := records[0]
	if record["msg"] != `foo 42 true null {"bar":[1,"batz"]}` || record["level"] != "INFO" || record["script"] != compat.PreScriptPrefix {
		t.Errorf("%#v", record)
	}
}

func TestConsoleLogCarriesTheInstanceOfTheBaggage(t *testing.T) {
	records := runConsole(t, map[string]any{"prescript": `console.log("foo");`}, map[string]any{})
	if len(records) != 1 || records[0][tracing.KeyInstanceId] != "test-instance-id" {
		t.Errorf("the record can be traced back to its instance: %#v", records)
	}
}

func TestConsoleDumpShowsTheStateAtTheCall(t *testing.T) {
	records := runConsole(t, map[string]any{
		"in1":        "foo",
		"prescript":  `variables.write("v3", {"foo": "bar"}); outputs.set("out1", 13); console.dump();`,
		"postscript": "console.log('post');",
	}, map[string]any{"v1": "str", "v2": float64(42)})
	if len(records) != 1 {
		t.Fatalf("expected 1 log record, got %#v", records)
	}
	record := records[0]
	// changes made earlier in the same script are part of the dump
	if expected := map[string]any{"v1": "str", "v2": float64(42), "v3": map[string]any{"foo": "bar"}}; !reflect.DeepEqual(record["variables"], expected) {
		t.Errorf("variables %#v", record["variables"])
	}
	// only the changes of this script, although they are part of the variables as well
	if expected := map[string]any{"v3": map[string]any{"foo": "bar"}}; !reflect.DeepEqual(record["variablesUpdates"], expected) {
		t.Errorf("variablesUpdates %#v", record["variablesUpdates"])
	}
	// script sources are no inputs of the script
	if expected := map[string]any{"in1": "foo"}; !reflect.DeepEqual(record["inputs"], expected) {
		t.Errorf("inputs %#v", record["inputs"])
	}
	if expected := map[string]any{"out1": float64(13)}; !reflect.DeepEqual(record["outputs"], expected) {
		t.Errorf("outputs %#v", record["outputs"])
	}
}
