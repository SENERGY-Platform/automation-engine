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

package mqtt

import (
	"context"
	"maps"
	"slices"
	"sync"
)

// Memory is a broker in the process for tests: it delivers synchronously to every matching
// subscription, and to one member of a shared group. Published messages are recorded. A message
// published with PublishRetained is kept, last per topic, and delivered with the retain flag to
// every subscription that matches, when it is made and again on Reconnect.
type Memory struct {
	mux           sync.Mutex
	subscriptions map[string]subscription
	published     []Published
	retained      map[string][]byte
	// FailPublish makes publishing fail, as with a lost connection
	FailPublish bool
	// Discard keeps no record of published messages, for long runs
	Discard bool
}

type Published struct {
	Topic   string
	Qos     byte
	Payload []byte
}

func NewMemory() *Memory {
	return &Memory{subscriptions: map[string]subscription{}, retained: map[string][]byte{}}
}

func (this *Memory) Publish(ctx context.Context, topic string, qos byte, payload []byte) error {
	return this.publish(ctx, topic, qos, payload, false)
}

// PublishRetained publishes with the retain flag: the broker keeps the payload for later
// subscriptions, an empty payload clears it. Live subscribers get it without the flag.
func (this *Memory) PublishRetained(ctx context.Context, topic string, qos byte, payload []byte) error {
	return this.publish(ctx, topic, qos, payload, true)
}

func (this *Memory) publish(_ context.Context, topic string, qos byte, payload []byte, retain bool) error {
	this.mux.Lock()
	if this.FailPublish {
		this.mux.Unlock()
		return errNotConnected
	}
	if !this.Discard {
		this.published = append(this.published, Published{Topic: topic, Qos: qos, Payload: append([]byte{}, payload...)})
	}
	if retain {
		if len(payload) == 0 {
			delete(this.retained, topic)
		} else {
			this.retained[topic] = append([]byte{}, payload...)
		}
	}
	handlers := []RetainHandler{}
	for filter, s := range this.subscriptions {
		if Matches(filter, topic) {
			handlers = append(handlers, s.handler)
		}
	}
	this.mux.Unlock()
	for _, handler := range handlers {
		_ = handler(topic, payload, false)
	}
	return nil
}

func (this *Memory) Subscribe(topic string, qos byte, handler Handler) error {
	return this.SubscribeRetain(topic, qos, ignoreRetain(handler))
}

// SubscribeRetain delivers the retained messages that match, with the flag, before it returns.
func (this *Memory) SubscribeRetain(topic string, qos byte, handler RetainHandler) error {
	this.mux.Lock()
	this.subscriptions[topic] = subscription{qos: qos, handler: handler}
	replay := this.retainedFor(topic)
	this.mux.Unlock()
	deliver(replay, handler)
	return nil
}

// Reconnect models a lost and restored connection: the broker replays the retained messages to
// every subscription, as it does for the subscriptions a client renews after a connect.
func (this *Memory) Reconnect() {
	this.mux.Lock()
	type replay struct {
		messages []Published
		handler  RetainHandler
	}
	all := []replay{}
	for filter, s := range this.subscriptions {
		all = append(all, replay{this.retainedFor(filter), s.handler})
	}
	this.mux.Unlock()
	for _, r := range all {
		deliver(r.messages, r.handler)
	}
}

// retainedFor lists the retained messages for a filter, in the order of the topics; the caller
// holds the lock
func (this *Memory) retainedFor(filter string) (result []Published) {
	for _, topic := range slices.Sorted(maps.Keys(this.retained)) {
		if Matches(filter, topic) {
			result = append(result, Published{Topic: topic, Payload: append([]byte{}, this.retained[topic]...)})
		}
	}
	return result
}

func deliver(messages []Published, handler RetainHandler) {
	for _, m := range messages {
		_ = handler(m.Topic, m.Payload, true)
	}
}

func (this *Memory) Unsubscribe(topic string) error {
	this.mux.Lock()
	defer this.mux.Unlock()
	delete(this.subscriptions, topic)
	return nil
}

// Published returns the messages published so far on topics matching filter.
func (this *Memory) Published(filter string) (result []Published) {
	this.mux.Lock()
	defer this.mux.Unlock()
	for _, p := range this.published {
		if Matches(filter, p.Topic) {
			result = append(result, p)
		}
	}
	return result
}

type memoryError string

func (this memoryError) Error() string { return string(this) }

const errNotConnected = memoryError("not connected")
