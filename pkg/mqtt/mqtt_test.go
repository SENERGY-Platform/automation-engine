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

package mqtt_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestMatchesWildcardsAndSharedSubscriptions(t *testing.T) {
	cases := []struct {
		filter, topic string
		match         bool
	}{
		{"event/#", "event/d/s", true},
		{"event/+/s", "event/d/s", true},
		{"event/+/s", "event/d/x", false},
		{"event/+", "event/d/s", false},
		{"$share/engine/processes/+/state/#", "processes/n1/state/engine/v1/x", true},
		{"processes/+/state/#", "processes/n1/cmd/x", false},
	}
	for _, c := range cases {
		if mqtt.Matches(c.filter, c.topic) != c.match {
			t.Errorf("%v %v: expected %v", c.filter, c.topic, c.match)
		}
	}
}

// broker starts a Mosquitto container; skipped without container runtime or with AE_TEST_MQTT=off
func broker(t *testing.T) string {
	t.Helper()
	if os.Getenv("AE_TEST_MQTT") == "off" {
		t.Skip("AE_TEST_MQTT=off")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "eclipse-mosquitto:2.0.20",
			Cmd:          []string{"mosquitto", "-c", "/mosquitto-no-auth.conf"},
			ExposedPorts: []string{"1883/tcp"},
			WaitingFor:   wait.ForListeningPort("1883/tcp").WithStartupTimeout(time.Minute),
		},
		Started: true,
	})
	if err != nil {
		t.Skip("mqtt container not available:", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	endpoint, err := container.PortEndpoint(ctx, "1883/tcp", "tcp")
	if err != nil {
		t.Fatal(err)
	}
	return endpoint
}

// The client delivers a message of a matching wildcard subscription to the handler, which
// acknowledges it by returning without error.
func TestClientDeliversMessagesOfWildcardSubscriptions(t *testing.T) {
	url := broker(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	receiver, err := mqtt.Connect(ctx, mqtt.Config{Broker: url, ClientId: "receiver"}, logger)
	if err != nil {
		t.Fatal(err)
	}
	var mux sync.Mutex
	received := []string{}
	if err = receiver.Subscribe("event/+/+", 1, func(topic string, payload []byte) error {
		mux.Lock()
		defer mux.Unlock()
		received = append(received, topic+" "+string(payload))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sender, err := mqtt.Connect(ctx, mqtt.Config{Broker: url, ClientId: "sender", CleanSession: true}, logger)
	if err != nil {
		t.Fatal(err)
	}
	if err = sender.Publish(ctx, "event/d1/s1", 1, []byte("21.5")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mux.Lock()
		n := len(received)
		mux.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mux.Lock()
	defer mux.Unlock()
	if len(received) != 1 || received[0] != "event/d1/s1 21.5" {
		t.Fatalf("the subscriber receives the message: %v", received)
	}
}
