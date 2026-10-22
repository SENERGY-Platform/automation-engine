/*
 * Copyright (c) 2026 InfAI (CC SES)
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
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
)

// refusingBroker speaks just enough MQTT 3.1.1 for the client: it accepts connections and
// answers subscriptions with success or, while refuse is set, with the failure code 0x80, as
// VerneMQ does while its webhooks do not answer.
type refusingBroker struct {
	listener net.Listener
	mux      sync.Mutex
	refuse   bool
	conn     net.Conn
	accepted int
	refused  int
}

func newRefusingBroker(t *testing.T) *refusingBroker {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b := &refusingBroker{listener: listener}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			b.mux.Lock()
			b.conn = conn
			b.mux.Unlock()
			go b.serve(conn)
		}
	}()
	return b
}

func (this *refusingBroker) url() string {
	return "tcp://" + this.listener.Addr().String()
}

func (this *refusingBroker) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	for {
		header, err := r.ReadByte()
		if err != nil {
			return
		}
		length, multiplier := 0, 1
		for {
			b, err := r.ReadByte()
			if err != nil {
				return
			}
			length += int(b&127) * multiplier
			multiplier *= 128
			if b&128 == 0 {
				break
			}
		}
		body := make([]byte, length)
		if _, err = io.ReadFull(r, body); err != nil {
			return
		}
		switch header >> 4 {
		case 1: // CONNECT
			_, _ = conn.Write([]byte{0x20, 2, 0, 0})
		case 8: // SUBSCRIBE: packet id, then topic filters with their QoS
			code := byte(1)
			this.mux.Lock()
			if this.refuse {
				code = 0x80
				this.refused++
			} else {
				this.accepted++
			}
			this.mux.Unlock()
			_, _ = conn.Write([]byte{0x90, 3, body[0], body[1], code})
		case 12: // PINGREQ
			_, _ = conn.Write([]byte{0xD0, 0})
		case 14: // DISCONNECT
			return
		}
	}
}

func (this *refusingBroker) set(refuse bool) {
	this.mux.Lock()
	defer this.mux.Unlock()
	this.refuse = refuse
}

func (this *refusingBroker) counts() (accepted int, refused int) {
	this.mux.Lock()
	defer this.mux.Unlock()
	return this.accepted, this.refused
}

// restart drops the connection like a broker that restarts
func (this *refusingBroker) restart() {
	this.mux.Lock()
	defer this.mux.Unlock()
	if this.conn != nil {
		_ = this.conn.Close()
	}
}

func waitUntil(t *testing.T, what string, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting for " + what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A subscription the broker refuses is an error, not a silent success.
func TestARefusedSubscriptionIsAnError(t *testing.T) {
	b := newRefusingBroker(t)
	b.set(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, err := mqtt.Connect(ctx, mqtt.Config{Broker: b.url(), ClientId: "engine", CleanSession: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Subscribe("processes/+/state/#", 1, func(string, []byte) error { return nil }); err == nil {
		t.Fatal("expected an error for the refused subscription")
	}
}

// After the broker restarts, it may refuse the renewed subscription for a while; the client
// tries again until the broker accepts it, instead of staying connected without it.
func TestARefusedSubscriptionIsRenewedAfterAReconnect(t *testing.T) {
	b := newRefusingBroker(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, err := mqtt.Connect(ctx, mqtt.Config{Broker: b.url(), ClientId: "engine", CleanSession: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Subscribe("processes/+/state/#", 1, func(string, []byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	// the connect handler may subscribe once more right after the first connect
	time.Sleep(200 * time.Millisecond)
	before, _ := b.counts()
	b.set(true)
	b.restart()
	waitUntil(t, "the refused renewal", 20*time.Second, func() bool { _, refused := b.counts(); return refused > 0 })
	b.set(false)
	waitUntil(t, "the accepted renewal", 20*time.Second, func() bool { accepted, _ := b.counts(); return accepted > before })
}
