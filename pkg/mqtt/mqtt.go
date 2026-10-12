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

// Package mqtt connects to MQTT brokers: the broker of a gateway in fog mode (FOG-01) and the
// cloud broker for the synchronisation (SYNC-04). The client is paho.mqtt.golang, used under
// the Eclipse Distribution License 1.0.
package mqtt

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// Handler processes a message; an error leaves a message of qos 1 or 2 unacknowledged, so that
// the broker delivers it again after a reconnect of a persistent session.
type Handler func(topic string, payload []byte) error

// Broker is what the engine needs of a broker; Client is the network one, Memory the one of tests.
type Broker interface {
	Publish(ctx context.Context, topic string, qos byte, payload []byte) error
	Subscribe(topic string, qos byte, handler Handler) error
	Unsubscribe(topic string) error
}

type Config struct {
	Broker   string
	ClientId string
	User     string
	Password string
	// false keeps subscriptions and unacknowledged messages across reconnects
	CleanSession bool
	Timeout      time.Duration
}

type subscription struct {
	qos     byte
	handler Handler
}

type Client struct {
	client        paho.Client
	timeout       time.Duration
	logger        *slog.Logger
	mux           sync.Mutex
	subscriptions map[string]subscription
}

// Connect connects and keeps reconnecting; subscriptions are renewed after every connect.
func Connect(ctx context.Context, config Config, logger *slog.Logger) (*Client, error) {
	if config.Timeout == 0 {
		config.Timeout = 10 * time.Second
	}
	result := &Client{timeout: config.Timeout, logger: logger, subscriptions: map[string]subscription{}}
	options := paho.NewClientOptions().
		AddBroker(config.Broker).
		SetClientID(config.ClientId).
		SetUsername(config.User).
		SetPassword(config.Password).
		SetCleanSession(config.CleanSession).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second).
		SetAutoAckDisabled(true).
		SetOrderMatters(false).
		SetConnectionLostHandler(func(_ paho.Client, err error) {
			logger.Warn("mqtt connection lost", "broker", config.Broker, "error", err)
		}).
		SetOnConnectHandler(func(_ paho.Client) {
			result.resubscribe()
		})
	result.client = paho.NewClient(options)
	token := result.client.Connect()
	if !token.WaitTimeout(config.Timeout) {
		// connect retries in the background; publishing fails until it succeeds
		logger.Warn("mqtt broker not reachable yet", "broker", config.Broker)
	} else if token.Error() != nil {
		return nil, token.Error()
	}
	go func() {
		<-ctx.Done()
		result.client.Disconnect(250)
	}()
	return result, nil
}

func (this *Client) wait(token paho.Token) error {
	if !token.WaitTimeout(this.timeout) {
		return errors.New("mqtt timeout")
	}
	return token.Error()
}

func (this *Client) Publish(ctx context.Context, topic string, qos byte, payload []byte) error {
	if !this.client.IsConnectionOpen() {
		return fmt.Errorf("mqtt not connected, unable to publish to %v", topic)
	}
	return this.wait(this.client.Publish(topic, qos, false, payload))
}

func (this *Client) Subscribe(topic string, qos byte, handler Handler) error {
	this.mux.Lock()
	this.subscriptions[topic] = subscription{qos: qos, handler: handler}
	this.mux.Unlock()
	if !this.client.IsConnectionOpen() {
		// subscribed by the connect handler
		return nil
	}
	return this.subscribe(topic, qos, handler)
}

func (this *Client) subscribe(topic string, qos byte, handler Handler) error {
	return this.wait(this.client.Subscribe(topic, qos, func(_ paho.Client, message paho.Message) {
		if err := handler(message.Topic(), message.Payload()); err != nil {
			this.logger.Warn("mqtt message not processed", "topic", message.Topic(), "error", err)
			return
		}
		message.Ack()
	}))
}

func (this *Client) resubscribe() {
	this.mux.Lock()
	current := map[string]subscription{}
	for topic, s := range this.subscriptions {
		current[topic] = s
	}
	this.mux.Unlock()
	for topic, s := range current {
		if err := this.subscribe(topic, s.qos, s.handler); err != nil {
			this.logger.Error("unable to subscribe", "topic", topic, "error", err)
		}
	}
}

func (this *Client) Unsubscribe(topic string) error {
	this.mux.Lock()
	delete(this.subscriptions, topic)
	this.mux.Unlock()
	if !this.client.IsConnectionOpen() {
		return nil
	}
	return this.wait(this.client.Unsubscribe(topic))
}

// Matches reports whether topic matches filter with + and # wildcards; a shared subscription
// "$share/<group>/<filter>" matches like its filter.
func Matches(filter string, topic string) bool {
	if strings.HasPrefix(filter, "$share/") {
		parts := strings.SplitN(filter, "/", 3)
		if len(parts) < 3 {
			return false
		}
		filter = parts[2]
	}
	f := strings.Split(filter, "/")
	t := strings.Split(topic, "/")
	for i, part := range f {
		if part == "#" {
			return true
		}
		if i >= len(t) {
			return false
		}
		if part != "+" && part != t[i] {
			return false
		}
	}
	return len(f) == len(t)
}
