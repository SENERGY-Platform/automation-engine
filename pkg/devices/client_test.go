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

package devices_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/devices"
	"github.com/SENERGY-Platform/service-commons/pkg/signal"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// recordingRepository answers every read with a resource named after the last path segment and
// records the path and trace parent of every request
type recordingRepository struct {
	mux      sync.Mutex
	requests []recordedRequest
}

type recordedRequest struct {
	path        string
	traceparent string
}

func (this *recordingRepository) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	this.mux.Lock()
	this.requests = append(this.requests, recordedRequest{path: r.URL.Path, traceparent: r.Header.Get("traceparent")})
	this.mux.Unlock()
	segments := strings.Split(r.URL.Path, "/")
	id := segments[len(segments)-1]
	json.NewEncoder(w).Encode(map[string]any{"id": id, "device_type_id": "type-1", "owner_id": "owner-1"})
}

func (this *recordingRepository) count(path string) int {
	this.mux.Lock()
	defer this.mux.Unlock()
	result := 0
	for _, r := range this.requests {
		if r.path == path {
			result++
		}
	}
	return result
}

func (this *recordingRepository) all() []recordedRequest {
	this.mux.Lock()
	defer this.mux.Unlock()
	return append([]recordedRequest{}, this.requests...)
}

// userToken is an unsigned jwt; device-repository and the client cache parse tokens without validation
func userToken(user string) devices.Token {
	encode := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return devices.Token("Bearer " + encode(map[string]string{"alg": "HS256", "typ": "JWT"}) + "." + encode(map[string]any{"sub": user, "realm_access": map[string][]string{"roles": {"user"}}}) + ".sig")
}

// publish sends a cache invalidation signal and waits until every subscriber handled it
func publish(t *testing.T, sig signal.Signal, value string) {
	done := make(chan struct{})
	id := signal.Sub("", sig, func(_ string, wg *sync.WaitGroup) {
		go func() {
			wg.Wait()
			close(done)
		}()
	})
	defer signal.Unsub(id)
	signal.Pub(sig, value)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("signal not handled")
	}
}

func TestReadsCarryTheTraceOfTheCaller(t *testing.T) {
	previousProvider, previousPropagator := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(tracetest.NewSpanRecorder()))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	server := &recordingRepository{}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	repo, err := devices.NewRepository(httpServer.URL, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token := userToken("alice")

	ctx, span := provider.Tracer("test").Start(context.Background(), "task")
	defer span.End()
	reads := map[string]func() error{
		"device":       func() error { _, err := repo.GetDevice(ctx, token, "d-trace"); return err },
		"device type":  func() error { _, err := repo.GetDeviceType(ctx, token, "t-trace"); return err },
		"device group": func() error { _, err := repo.GetDeviceGroup(ctx, token, "g-trace"); return err },
		"protocol":     func() error { _, err := repo.GetProtocol(ctx, token, "p-trace"); return err },
		"hub owner": func() error {
			_, err := devices.NewHubs(httpServer.URL).HubOwner(ctx, string(token), "h-trace")
			return err
		},
		"aspect node": func() error {
			_, err := devices.NewAspectNodes(httpServer.URL).ResolveIds(ctx, token, []string{"a-trace"})
			return err
		},
	}
	for name, read := range reads {
		if err := read(); err != nil {
			t.Errorf("%v: %v", name, err)
		}
	}
	traceId := span.SpanContext().TraceID().String()
	requests := server.all()
	if len(requests) != len(reads) {
		t.Errorf("expected %v requests, got %v", len(reads), len(requests))
	}
	for _, r := range requests {
		if !strings.Contains(r.traceparent, traceId) {
			t.Errorf("%v: traceparent %q does not carry the trace %v", r.path, r.traceparent, traceId)
		}
	}
}

func TestReadsAreCachedPerUserAndInvalidatedBySignals(t *testing.T) {
	server := &recordingRepository{}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	repo, err := devices.NewRepository(httpServer.URL, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	alice, bob := userToken("alice"), userToken("bob")

	for _, token := range []devices.Token{alice, alice, bob} {
		if _, err := repo.GetDevice(ctx, token, "d-cache"); err != nil {
			t.Fatal(err)
		}
	}
	if got := server.count("/devices/d-cache"); got != 2 {
		t.Errorf("expected one request per user, got %v", got)
	}

	publish(t, signal.Known.DeviceCacheInvalidation, "d-other")
	repo.GetDevice(ctx, alice, "d-cache")
	if got := server.count("/devices/d-cache"); got != 2 {
		t.Errorf("a signal for another device must keep the entry, got %v requests", got)
	}

	publish(t, signal.Known.DeviceCacheInvalidation, "d-cache")
	repo.GetDevice(ctx, alice, "d-cache")
	repo.GetDevice(ctx, bob, "d-cache")
	if got := server.count("/devices/d-cache"); got != 4 {
		t.Errorf("a signal for the device must clear it for every user, got %v requests", got)
	}

	//device types and protocols arrive as CacheInvalidationAll, see CacheInvalidationAllTopics
	repo.GetDeviceType(ctx, alice, "t-cache")
	publish(t, signal.Known.CacheInvalidationAll, "")
	repo.GetDeviceType(ctx, alice, "t-cache")
	if got := server.count("/device-types/t-cache"); got != 2 {
		t.Errorf("CacheInvalidationAll must clear the device type, got %v requests", got)
	}
}
