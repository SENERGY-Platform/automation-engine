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

package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/namespaces"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/service-commons/pkg/cache/invalidator"
	"github.com/SENERGY-Platform/service-commons/pkg/kafka"
)

// EventDependencies replace the unmarshalling and the Kafka consumer of the event role in tests.
type EventDependencies struct {
	Unmarshaller events.Unmarshaller
	Consumer     events.Consumer
}

// EventServer is the event role: it evaluates device and import data and correlates events in
// the process, against the same database as the engine role (section 10).
type EventServer struct {
	Store   *store.Store
	Engine  *engine.Engine
	Worker  *events.Worker
	Metrics *Metrics
	config  configuration.Config
	logger  *slog.Logger
	wg      sync.WaitGroup
}

// newUnmarshaller builds the unmarshalling of events, shared by the event role and the event
// debugging of the engine role (EVT-10)
func newUnmarshaller(config configuration.Config) (events.Unmarshaller, error) {
	duration, err := time.ParseDuration(config.DeviceCacheTimeout)
	if err != nil {
		return nil, fmt.Errorf("invalid device_cache_timeout: %w", err)
	}
	var token auth.TokenSource
	// the device-repository mirror of a gateway needs no token
	if config.Mode != "fog" {
		token = auth.ClientCredentials(config.AuthEndpoint, config.AuthClientId, config.AuthClientSecret.Value())
	}
	repo, err := events.NewCachedDeviceRepo(config.DeviceRepositoryUrl, token, duration)
	if err != nil {
		return nil, err
	}
	return events.NewMarshaller(repo)
}

func NewEventServer(ctx context.Context, config configuration.Config, logger *slog.Logger, deps EventDependencies) (*EventServer, error) {
	s, err := openStore(ctx, config)
	if err != nil {
		return nil, err
	}
	settings, err := engineConfig(config)
	if err != nil {
		s.Close()
		return nil, err
	}
	// jobs are executed by the engine role
	settings.ExecutesTasks = false
	registry, err := namespaces.New(ctx, s, config.Namespaces, nil, logger)
	if err != nil {
		s.Close()
		return nil, err
	}
	e := engine.New(s, settings, registry, logger)
	m := NewMetrics(configuration.Duration(config.StalledAfter))
	e.SetMetrics(m)
	result := &EventServer{Store: s, Engine: e, Metrics: m, config: config, logger: logger}

	unmarshaller := deps.Unmarshaller
	if unmarshaller == nil {
		if unmarshaller, err = newUnmarshaller(config); err != nil {
			s.Close()
			return nil, err
		}
	}
	consumer := deps.Consumer
	if consumer == nil {
		if config.KafkaUrl == "" {
			s.Close()
			return nil, errors.New("the event role needs kafka_url")
		}
		consumer = result.kafkaConsumer
	}
	worker := events.DefaultWorkerConfig()
	result.Worker = events.NewWorker(worker, s, events.NewEvaluator(unmarshaller), e, consumer, logger)
	return result, nil
}

// kafkaConsumer reads the device data topics in one consumer group, so that event workers share
// the partitions; a message is committed after its qos 1 descriptions are handled
func (this *EventServer) kafkaConsumer(ctx context.Context, topics []string, handle func(events.Message) error) error {
	config := kafka.Config{
		KafkaUrl: this.config.KafkaUrl, ConsumerGroup: this.config.EventConsumerGroup, StartOffset: kafka.LastOffset, PartitionWatchInterval: time.Minute,
		OnError: func(err error) { this.logger.Error("event consumer failed", "error", err) },
	}
	return kafka.NewMultiConsumer(ctx, config, topics, func(m kafka.Message) error {
		return handle(events.Message{Topic: m.Topic, Partition: m.Partition, Offset: m.Offset, Value: m.Value, Time: m.Time})
	})
}

// Run reads events until ctx is done and serves metrics and health on the internal port.
func (this *EventServer) Run(ctx context.Context) error {
	if this.config.KafkaUrl != "" {
		all := kafka.Config{KafkaUrl: this.config.KafkaUrl, StartOffset: kafka.LastOffset, OnError: func(err error) { this.logger.Error("kafka consumer failed", "error", err) }}
		if len(this.config.CacheInvalidationAllTopics) > 0 {
			if err := invalidator.StartCacheInvalidatorAll(ctx, all, this.config.CacheInvalidationAllTopics, nil); err != nil {
				return err
			}
		}
		err := invalidator.StartKnownCacheInvalidators(ctx, all, invalidator.KnownTopics{
			ConceptTopic: "concepts", CharacteristicTopic: "characteristics", FunctionTopic: "functions", AspectTopic: "aspects",
		}, nil)
		if err != nil {
			return err
		}
	}
	go this.Worker.Run(ctx)
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", this.Metrics.Handler())
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if err := this.Store.Ping(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := &http.Server{Addr: net.JoinHostPort("", strconv.Itoa(this.config.InternalPort)), Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	this.logger.Info("event worker listening", "internal_port", this.config.InternalPort)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	this.wg.Wait()
	return nil
}

func (this *EventServer) Close() error {
	return this.Store.Close()
}
