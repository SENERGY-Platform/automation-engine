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

package tracing

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// record installs a tracer provider that keeps finished spans, and the propagators otelx sets
func record(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previousProvider, previousPropagator := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})
	return recorder
}

func TestStartPutsTheInstanceIntoSpanAndBaggage(t *testing.T) {
	recorder := record(t)
	ctx, span := Start(context.Background(), "task http", Instance{Namespace: "processes", DeploymentId: "d", InstanceId: "i", Owner: "u"}, attribute.String("element_id", "e"))
	bag := baggage.FromContext(ctx)
	if bag.Member(KeyInstanceId).Value() != "i" || bag.Member(KeyDeploymentId).Value() != "d" || bag.Member(KeyNamespace).Value() != "processes" || bag.Member(KeyUserId).Value() != "u" {
		t.Fatalf("baggage %v", bag)
	}
	End(span, errors.New("boom"))
	ended := recorder.Ended()
	if len(ended) != 1 || ended[0].Name() != "task http" || ended[0].Status().Code != codes.Error {
		t.Fatalf("spans %+v", ended)
	}
	attributes := map[attribute.Key]string{}
	for _, a := range ended[0].Attributes() {
		attributes[a.Key] = a.Value.AsString()
	}
	if attributes[KeyInstanceId] != "i" || attributes["element_id"] != "e" {
		t.Fatalf("attributes %v", attributes)
	}
}

func TestWithBaggageSkipsValuesWithSpaces(t *testing.T) {
	ctx := WithBaggage(context.Background(), KeyInstanceId, "a b")
	if baggage.FromContext(ctx).Len() != 0 {
		t.Fatalf("a value with spaces is left out instead of failing: %v", baggage.FromContext(ctx))
	}
}

func TestInjectWritesTraceContextAndBaggage(t *testing.T) {
	record(t)
	ctx, span := Start(context.Background(), "work", Instance{InstanceId: "i"})
	defer span.End()
	req, _ := http.NewRequest(http.MethodGet, "http://example.invalid", nil)
	if err := Inject(ctx, req); err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("traceparent") == "" || req.Header.Get("baggage") != KeyInstanceId+"=i" {
		t.Fatalf("headers %v", req.Header)
	}
}

type plain struct{}

func (*plain) ServeHTTP(http.ResponseWriter, *http.Request) {}

func TestOffNeitherWrapsNorInitializes(t *testing.T) {
	handler := &plain{}
	if got := Handler(Off, handler); got != handler {
		t.Fatalf("handler wrapped: %T", got)
	}
	if err := Init(context.Background(), Off); err != nil {
		t.Fatal(err)
	}
}
