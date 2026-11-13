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

package notification

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/tracing"
	devnotifications "github.com/SENERGY-Platform/developer-notifications/pkg/client"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestNotifyPostsTheNotifierFormatWithDuplicateWindow(t *testing.T) {
	var received map[string]string
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_ = json.NewDecoder(r.Body).Decode(&received)
	}))
	defer server.Close()
	n := New(server.URL, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := n.Notify(context.Background(), store.Notification{UserId: "u", Title: "Process-Incident in x", Message: "boom", Topic: "incident", DuplicateWindow: 3600})
	if err != nil {
		t.Fatal(err)
	}
	if received["userId"] != "u" || received["title"] != "Process-Incident in x" || received["message"] != "boom" || received["topic"] != "incident" {
		t.Errorf("unexpected body %v", received)
	}
	if query != "ignore_duplicates_within_seconds=3600" {
		t.Errorf("unexpected query %v", query)
	}

	// SNRGY-3911: without a window the notifier delivers every notification
	query = "unset"
	if err = n.Notify(context.Background(), store.Notification{UserId: "u", Title: "hot", Topic: Topic}); err != nil {
		t.Fatal(err)
	}
	if query != "" {
		t.Errorf("a notification without a window is sent without one: %q", query)
	}
}

func TestNotifyReportsRejectedMessagesForRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	n := New(server.URL, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := n.Notify(context.Background(), store.Notification{UserId: "u"}); err == nil {
		t.Fatal("expected error")
	}
}

// Without answer the engine postpones the other due notifications as well (SNRGY-5064); an
// answer, even an error status, concerns this message only.
func TestNotifyTellsAMissingAnswerFromARefusal(t *testing.T) {
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer refusing.Close()
	closed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closed.Close()
	// the server notices the client giving up only once it has read the body
	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer hanging.Close()
	timeout, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	for name, test := range map[string]struct {
		url         string
		ctx         context.Context
		unreachable bool
	}{
		"refused":   {refusing.URL, context.Background(), false},
		"closed":    {closed.URL, context.Background(), true},
		"no answer": {hanging.URL, timeout, true},
	} {
		err := New(test.url, "", slog.New(slog.NewTextHandler(io.Discard, nil))).Notify(test.ctx, store.Notification{UserId: "u"})
		var unreachable *engine.NotifierUnreachableError
		if err == nil || errors.As(err, &unreachable) != test.unreachable {
			t.Errorf("%v: %v", name, err)
		}
	}
}

func TestNotifyWithoutTargetsDoesNothing(t *testing.T) {
	if err := New("", "", slog.Default()).Notify(context.Background(), store.Notification{}); err != nil {
		t.Fatal(err)
	}
}

func TestNotifyCarriesTraceContextAndBaggage(t *testing.T) {
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })
	var header http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Clone()
	}))
	defer server.Close()
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled})
	ctx := tracing.WithBaggage(trace.ContextWithSpanContext(context.Background(), spanContext), tracing.KeyInstanceId, "i")
	n := New(server.URL, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := n.Notify(ctx, store.Notification{UserId: "u", Title: "t", Message: "m"}); err != nil {
		t.Fatal(err)
	}
	if header.Get("traceparent") == "" || header.Get("baggage") != tracing.KeyInstanceId+"=i" {
		t.Fatalf("the notifier is a platform service and gets trace context and baggage: %v", header)
	}
}

// The copy to developer-notifications names the namespace, so that a process and a smart
// service of the same name can be told apart.
func TestDeveloperCopyNamesTheNamespace(t *testing.T) {
	var received devnotifications.Message
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&received)
	}))
	defer server.Close()
	n := New("", server.URL, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := n.Notify(context.Background(), store.Notification{UserId: "u", Title: "Process-Incident in x", Message: "boom", Namespace: "smart-services"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(received.Tags, "smart-services") || !strings.Contains(received.Body, "Namespace: smart-services\n") {
		t.Fatalf("%+v", received)
	}
}

// SNRGY-5141: a developer-notifications without answer holds the sending of a notification only
// for the timeout, not beyond the claim of the notification, after which another replica would
// send it again
func TestAHangingDeveloperCopyEndsWithTheTimeout(t *testing.T) {
	var notified atomic.Int32
	notifier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { notified.Add(1) }))
	defer notifier.Close()
	release := make(chan struct{})
	developers := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer developers.Close()
	defer close(release)
	n := New(notifier.URL, developers.URL, slog.New(slog.NewTextHandler(io.Discard, nil)))
	n.http.Timeout = 100 * time.Millisecond
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		done <- n.Notify(context.Background(), store.Notification{UserId: "u", Title: "Process-Incident in x", Message: "boom", Topic: "incident"})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a failed copy is no failure of the notification: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the copy to developer-notifications is still waiting")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("the copy took %v", elapsed)
	}
	if notified.Load() != 1 {
		t.Errorf("the notifier gets the notification once: %v", notified.Load())
	}
}
