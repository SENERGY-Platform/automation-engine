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
	"sync"
)

// Memory is a broker in the process for tests: it delivers synchronously to every matching
// subscription, and to one member of a shared group. Published messages are recorded.
type Memory struct {
	mux           sync.Mutex
	subscriptions map[string]subscription
	published     []Published
	// FailPublish makes publishing fail, as with a lost connection
	FailPublish bool
}

type Published struct {
	Topic   string
	Qos     byte
	Payload []byte
}

func NewMemory() *Memory {
	return &Memory{subscriptions: map[string]subscription{}}
}

func (this *Memory) Publish(_ context.Context, topic string, qos byte, payload []byte) error {
	this.mux.Lock()
	if this.FailPublish {
		this.mux.Unlock()
		return errNotConnected
	}
	this.published = append(this.published, Published{Topic: topic, Qos: qos, Payload: append([]byte{}, payload...)})
	handlers := []Handler{}
	for filter, s := range this.subscriptions {
		if Matches(filter, topic) {
			handlers = append(handlers, s.handler)
		}
	}
	this.mux.Unlock()
	for _, handler := range handlers {
		_ = handler(topic, payload)
	}
	return nil
}

func (this *Memory) Subscribe(topic string, qos byte, handler Handler) error {
	this.mux.Lock()
	defer this.mux.Unlock()
	this.subscriptions[topic] = subscription{qos: qos, handler: handler}
	return nil
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
