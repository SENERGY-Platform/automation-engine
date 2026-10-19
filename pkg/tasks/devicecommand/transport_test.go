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

package devicecommand

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
)

// The HTTP part of external-task-worker TestComswitch and TestComswitchProduceWithKey; the
// Kafka part needs a broker and is the producer of service-commons.

func TestTransportPostsToAHandlerGivenAsUrl(t *testing.T) {
	var mux sync.Mutex
	received := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mux.Lock()
		received = append(received, r.Method+" "+r.URL.Path+" "+r.Header.Get("Content-Type")+" "+string(body))
		mux.Unlock()
	}))
	defer server.Close()
	transport := NewTransport(ctx, "")
	for _, msg := range []string{`"msg1"`, `"msg2"`} {
		if err := transport.Produce(ctx, server.URL+"/commands", "key", []byte(msg)); err != nil {
			t.Fatal(err)
		}
	}
	expected := []string{`POST /commands application/json "msg1"`, `POST /commands application/json "msg2"`}
	if !reflect.DeepEqual(received, expected) {
		t.Errorf("each message is posted to the url (TASK-04): %v", received)
	}
}

func TestTransportReportsAHandlerThatRejectsTheMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusBadGateway)
	}))
	defer server.Close()
	if err := NewTransport(ctx, "").Produce(ctx, server.URL+"/err", "key", []byte(`"msg"`)); err == nil {
		t.Error("an answer other than 200 is an error, so that the command is retried")
	}
}

func TestTransportWithoutKafkaRejectsATopic(t *testing.T) {
	if err := NewTransport(ctx, "").Produce(ctx, "connector", "key", []byte(`"msg"`)); err == nil {
		t.Error("a topic needs Kafka")
	}
}
