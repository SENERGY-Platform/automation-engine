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

package httpcall

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type namespaces map[string]model.Namespace

func (this namespaces) Get(name string) (model.Namespace, bool) {
	ns, ok := this[name]
	return ns, ok
}

func TestCallsDoNotCarryTraceContextOrBaggage(t *testing.T) {
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })
	var header http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Clone()
	}))
	defer server.Close()
	lab := model.Namespace{Http: model.HttpSettings{Cloud: model.HttpPolicy{Enabled: true, AllowNetworks: []string{"127.0.0.0/8"}}}}
	call := New(namespaces{"lab": lab}, "cloud")
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled})
	ctx := tracing.WithBaggage(trace.ContextWithSpanContext(context.Background(), spanContext), tracing.KeyUserId, "u")
	result, err := call.Execute(ctx, engine.Task{Namespace: "lab", Input: map[string]any{"url": server.URL}})
	if err != nil || result.Output["statusCode"] != 200.0 {
		t.Fatalf("call: %+v %v", result, err)
	}
	if header.Get("traceparent") != "" || header.Get("baggage") != "" {
		t.Fatalf("targets outside the platform get neither trace context nor the user id: %v", header)
	}
}

// The policy a gateway sets for itself replaces the fog policy that came with the namespace.
func TestLocalPolicyReplacesNamespacePolicy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()
	processes := model.Namespace{Http: model.HttpSettings{Fog: model.HttpPolicy{Enabled: true, AllowNetworks: []string{"10.0.0.0/8"}}}}
	task := engine.Task{Namespace: "processes", Input: map[string]any{"url": server.URL}}

	call := New(namespaces{"processes": processes}, Fog)
	if _, err := call.Execute(context.Background(), task); err == nil || !strings.Contains(err.Error(), "target not allowed") {
		t.Fatalf("the namespace allows only 10.0.0.0/8: %v", err)
	}

	call.SetLocalPolicy(model.HttpPolicy{Enabled: true, AllowNetworks: []string{"127.0.0.0/8"}})
	if result, err := call.Execute(context.Background(), task); err != nil || result.Output["statusCode"] != 200.0 {
		t.Fatalf("the local policy allows loopback: %+v %v", result, err)
	}

	call.SetLocalPolicy(model.HttpPolicy{Enabled: false})
	if _, err := call.Execute(context.Background(), task); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("the local policy disables calls: %v", err)
	}
}
