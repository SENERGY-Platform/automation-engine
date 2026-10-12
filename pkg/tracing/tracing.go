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

// Package tracing carries OpenTelemetry trace context and baggage through the engine, as the
// other platform services do with gin-middleware/otelx: incoming requests, outgoing requests to
// platform services, log records and the background execution of instances.
package tracing

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/SENERGY-Platform/gin-middleware/otelx"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// ServiceName is the OpenTelemetry service name of the engine.
const ServiceName = "automation-engine"

// Off disables tracing as value of the otel endpoint.
const Off = "off"

// Baggage keys of the engine; the prefix keeps them apart from ids of other services, e.g. the
// smart_service_instance_id of smart-service-repository. user_id and username come from otelx.
const (
	KeyInstanceId   = "automation_instance_id"
	KeyDeploymentId = "automation_deployment_id"
	KeyNamespace    = "automation_namespace"
	KeyUserId       = "user_id"
)

// Init sets up the OpenTelemetry exporter once per process. An empty endpoint selects the
// platform default of otelx; Off leaves tracing disabled, spans are then dropped.
func Init(ctx context.Context, endpoint string) error {
	if endpoint == Off {
		return nil
	}
	// otelx initializes only through its middlewares; the returned handler is not used
	_, err := otelx.HTTPOpenTelemetry(ctx, endpoint, ServiceName, http.NotFoundHandler())
	return err
}

// Handler traces incoming requests and takes over trace context and baggage of the caller;
// otelx adds user_id and username of the token to the baggage. With Off, handler is returned
// unchanged: otelx would set up its exporter on the first call.
func Handler(endpoint string, handler http.Handler) http.Handler {
	if endpoint == Off {
		return handler
	}
	traced, err := otelx.HTTPOpenTelemetry(context.Background(), endpoint, ServiceName, handler)
	if err != nil {
		return handler
	}
	return traced
}

// Inject writes trace context and baggage of ctx into a request to another platform service.
// Requests to targets outside the platform must not carry them, the baggage holds user ids.
func Inject(ctx context.Context, req *http.Request) error {
	return otelx.InjectContextToRequest(ctx, req)
}

// WithBaggage returns ctx with key=value in its baggage and adds it to the active span. Values
// with spaces or invalid characters are left out, the baggage is a diagnostic aid and must not
// fail the work it describes.
func WithBaggage(ctx context.Context, key string, value string) context.Context {
	if key == "" || value == "" || strings.ContainsAny(key+value, " ") {
		return ctx
	}
	member, err := baggage.NewMember(key, value)
	if err != nil {
		return ctx
	}
	bag, err := baggage.FromContext(ctx).SetMember(member)
	if err != nil {
		return ctx
	}
	ctx = baggage.ContextWithBaggage(ctx, bag)
	if span := trace.SpanFromContext(ctx); span.IsRecording() {
		span.SetAttributes(attribute.String(key, value))
	}
	return ctx
}

// Instance identifies the work of an instance in the baggage, so that requests and log records
// caused by it can be traced back to it.
type Instance struct {
	Namespace    string
	DeploymentId string
	InstanceId   string
	Owner        string
}

func (this Instance) attributes() []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String(KeyNamespace, this.Namespace),
		attribute.String(KeyDeploymentId, this.DeploymentId),
		attribute.String(KeyInstanceId, this.InstanceId),
		attribute.String(KeyUserId, this.Owner),
	}
}

// Start begins a span of background work for an instance, e.g. a task execution, and puts the
// instance into the baggage. Background work has no caller, so the span starts a trace of its
// own unless ctx carries one.
func Start(ctx context.Context, name string, instance Instance, attributes ...attribute.KeyValue) (context.Context, trace.Span) {
	ctx, span := otel.Tracer(ServiceName).Start(ctx, name, trace.WithAttributes(append(instance.attributes(), attributes...)...))
	ctx = WithBaggage(ctx, KeyNamespace, instance.Namespace)
	ctx = WithBaggage(ctx, KeyDeploymentId, instance.DeploymentId)
	ctx = WithBaggage(ctx, KeyInstanceId, instance.InstanceId)
	ctx = WithBaggage(ctx, KeyUserId, instance.Owner)
	return ctx, span
}

// End records err on span and ends it.
func End(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, fmt.Sprint(err))
	}
	span.End()
}
