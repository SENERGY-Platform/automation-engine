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

// Package messaging sends messages to Kafka topics or, for handlers given as URL, by HTTP.
package messaging

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/tracing"
	"github.com/SENERGY-Platform/service-commons/pkg/kafka"
)

// Producer sends a message to a topic; a topic that is an http(s) URL gets a POST (TASK-04).
type Producer interface {
	Produce(ctx context.Context, topic string, key string, payload []byte) error
}

type Switch struct {
	kafkaUrl string
	ctx      context.Context
	http     *http.Client
	mux      sync.Mutex
	writers  map[string]*kafka.Producer
}

// New creates a producer; without kafkaUrl only HTTP handlers can be reached.
func New(ctx context.Context, kafkaUrl string) *Switch {
	return &Switch{kafkaUrl: kafkaUrl, ctx: ctx, http: &http.Client{Timeout: 30 * time.Second}, writers: map[string]*kafka.Producer{}}
}

func (this *Switch) Produce(ctx context.Context, topic string, key string, payload []byte) error {
	if strings.HasPrefix(topic, "http://") || strings.HasPrefix(topic, "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, topic, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		if err = tracing.Inject(ctx, req); err != nil {
			return err
		}
		resp, err := this.http.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("handler %v answered %v", topic, resp.Status)
		}
		return nil
	}
	if this.kafkaUrl == "" {
		return errors.New("kafka is not configured")
	}
	writer, err := this.writer(topic)
	if err != nil {
		return err
	}
	return writer.Produce(key, payload)
}

func (this *Switch) writer(topic string) (*kafka.Producer, error) {
	this.mux.Lock()
	defer this.mux.Unlock()
	if w, ok := this.writers[topic]; ok {
		return w, nil
	}
	w, err := kafka.NewProducer(this.ctx, kafka.Config{KafkaUrl: this.kafkaUrl}, topic)
	if err != nil {
		return nil, err
	}
	this.writers[topic] = w
	return w, nil
}
