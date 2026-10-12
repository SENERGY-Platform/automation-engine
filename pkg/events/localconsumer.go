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

package events

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
)

// LocalConsumer reads the device events of the broker of a gateway (FOG-06). It subscribes to
// all events once, like the event-worker in fog mode; messages without a description are
// dropped by the worker. A restart of the consumer only replaces the handler, so that no
// unsubscribe of the previous consumer can race with the subscription of the next.
//
// MQTT has no offsets: a message gets a unique number, so that message ids stay apart
// (EVT-05). A message that cannot be processed is logged and acknowledged anyway, because an
// unacknowledged message would block the delivery of later ones.
func LocalConsumer(broker mqtt.Broker, qos byte, logger *slog.Logger) Consumer {
	var once sync.Once
	var subscribeErr error
	var current atomic.Pointer[func(Message) error]
	var sequence atomic.Int64
	return func(ctx context.Context, topics []string, handle func(Message) error) error {
		current.Store(&handle)
		once.Do(func() {
			subscribeErr = broker.Subscribe(LocalEventPrefix+"#", qos, func(topic string, payload []byte) error {
				now := time.Now()
				m := Message{Topic: topic, Value: payload, Time: now, Offset: now.UnixNano() + sequence.Add(1)}
				if err := (*current.Load())(m); err != nil {
					logger.Warn("local event not processed", "topic", topic, "error", err)
				}
				return nil
			})
		})
		return subscribeErr
	}
}
