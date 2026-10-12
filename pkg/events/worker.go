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
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/tracing"
	"go.opentelemetry.io/otel/attribute"
)

type WorkerConfig struct {
	// messages older than this are dropped (EVT-06)
	MaxMessageAge time.Duration
	// how often changed descriptions are read, and how often all of them
	SyncInterval       time.Duration
	FullReloadInterval time.Duration
	// at most one notification per description in this window (EVT-07)
	ErrorNotificationWindow time.Duration
	// concurrent evaluations of qos 0 descriptions
	AsyncWorkers int
}

func DefaultWorkerConfig() WorkerConfig {
	return WorkerConfig{MaxMessageAge: time.Hour, SyncInterval: time.Second, FullReloadInterval: 10 * time.Minute, ErrorNotificationWindow: time.Hour, AsyncWorkers: 20}
}

// Message is a record of a device or import data topic.
type Message struct {
	Topic     string
	Partition int
	Offset    int64
	Value     []byte
	Time      time.Time
}

// Consumer reads topics until ctx is done; handle returning an error means the message is not
// acknowledged and read again.
type Consumer func(ctx context.Context, topics []string, handle func(Message) error) error

type Correlator interface {
	Correlate(ctx context.Context, request engine.CorrelationRequest) (model.CorrelationResult, error)
	QueueNotification(ctx context.Context, userId string, title string, message string) error
}

// Worker is the event worker role: it holds the descriptions in memory, subscribes to exactly
// the topics they name, evaluates every message against the descriptions of its device and
// service or import, and correlates triggered events in the process (EVT-01 to EVT-08).
type Worker struct {
	config     WorkerConfig
	store      *store.Store
	evaluator  *Evaluator
	correlator Correlator
	consume    Consumer
	logger     *slog.Logger
	now        func() time.Time

	mux          sync.RWMutex
	byId         map[string]model.EventDescription
	index        map[string][]string
	importTopics map[string]bool
	revision     int64
	lastFull     time.Time

	notifyMux sync.Mutex
	notified  map[string]time.Time
	async     chan struct{}
}

func NewWorker(config WorkerConfig, s *store.Store, evaluator *Evaluator, correlator Correlator, consume Consumer, logger *slog.Logger) *Worker {
	return &Worker{
		config: config, store: s, evaluator: evaluator, correlator: correlator, consume: consume, logger: logger, now: time.Now,
		byId: map[string]model.EventDescription{}, index: map[string][]string{}, importTopics: map[string]bool{},
		notified: map[string]time.Time{}, async: make(chan struct{}, max(config.AsyncWorkers, 1)),
	}
}

func serviceKey(deviceId string, serviceId string) string {
	return "s|" + deviceId + "|" + serviceId
}

func importKey(importId string) string {
	return "i|" + importId
}

func keyOf(d model.EventDescription) string {
	if d.ImportId != "" {
		return importKey(d.ImportId)
	}
	return serviceKey(d.DeviceId, d.ServiceId)
}

// Sync reads descriptions changed since the last revision, and all of them periodically, so
// that a worker that missed purged tombstones recovers; it returns the topics to read.
func (this *Worker) Sync(ctx context.Context) ([]string, error) {
	this.mux.RLock()
	since := this.revision
	full := this.now().Sub(this.lastFull) >= this.config.FullReloadInterval
	this.mux.RUnlock()
	if full {
		since = 0
	}
	var changed []model.EventDescription
	var removed []string
	var latest int64
	err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		changed, removed, latest, err = tx.EventDescriptionsSince(since)
		return err
	})
	if err != nil {
		return nil, err
	}
	this.mux.Lock()
	defer this.mux.Unlock()
	if full {
		this.byId = map[string]model.EventDescription{}
		this.lastFull = this.now()
	}
	for _, id := range removed {
		delete(this.byId, id)
	}
	for _, d := range changed {
		this.byId[d.Id] = d
	}
	this.revision = latest
	this.index = map[string][]string{}
	this.importTopics = map[string]bool{}
	topics := map[string]bool{}
	for id, d := range this.byId {
		this.index[keyOf(d)] = append(this.index[keyOf(d)], id)
		topics[d.Topic] = true
		if d.ImportId != "" {
			this.importTopics[d.Topic] = true
		}
	}
	result := make([]string, 0, len(topics))
	for t := range topics {
		result = append(result, t)
	}
	sort.Strings(result)
	return result, nil
}

// Run syncs the descriptions and restarts the consumer when the set of topics changes.
func (this *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(this.config.SyncInterval)
	defer ticker.Stop()
	var current []string
	var stop context.CancelFunc
	defer func() {
		if stop != nil {
			stop()
		}
	}()
	for {
		topics, err := this.Sync(ctx)
		if err != nil {
			if ctx.Err() == nil {
				this.logger.Warn("unable to read event descriptions", "error", err)
			}
		} else if !slices.Equal(topics, current) {
			if stop != nil {
				stop()
			}
			current = topics
			stop, err = this.startConsumer(ctx, topics)
			if err != nil {
				this.logger.Error("unable to start event consumer", "error", err)
				current = nil
			}
			this.logger.Info("event worker topics", "count", len(topics))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// startConsumer reads the topics until the returned function is called
func (this *Worker) startConsumer(ctx context.Context, topics []string) (context.CancelFunc, error) {
	consumerCtx, cancel := context.WithCancel(ctx)
	if len(topics) == 0 {
		return cancel, nil
	}
	err := this.consume(consumerCtx, topics, func(m Message) error { return this.Handle(consumerCtx, m) })
	return cancel, err
}

type envelope struct {
	DeviceId  string         `json:"device_id"`
	ServiceId string         `json:"service_id"`
	Value     map[string]any `json:"value"`
}

// Handle evaluates a message against its descriptions; qos 1 descriptions are evaluated and
// correlated before the message is acknowledged, qos 0 ones afterwards (EVT-05).
func (this *Worker) Handle(ctx context.Context, m Message) error {
	age := this.now().Sub(m.Time)
	if !m.Time.IsZero() && age > this.config.MaxMessageAge {
		return nil
	}
	this.mux.RLock()
	isImport := this.importTopics[m.Topic]
	this.mux.RUnlock()
	var key string
	var payload map[string]any
	if isImport {
		if err := json.Unmarshal(m.Value, &payload); err != nil {
			return nil
		}
		importId, _ := payload["import_id"].(string)
		key = importKey(importId)
	} else {
		var e envelope
		if err := json.Unmarshal(m.Value, &e); err != nil {
			return nil
		}
		key = serviceKey(e.DeviceId, e.ServiceId)
		payload = e.Value
	}
	this.mux.RLock()
	descriptions := make([]model.EventDescription, 0, len(this.index[key]))
	for _, id := range this.index[key] {
		descriptions = append(descriptions, this.byId[id])
	}
	this.mux.RUnlock()
	// partition and offset alone are not unique across topics (EVT-05)
	messageId := fmt.Sprintf("%s:%d:%d", m.Topic, m.Partition, m.Offset)
	ageSeconds := float64(int64(max(age, 0).Seconds()))
	var errs []error
	for _, d := range descriptions {
		if d.Qos == 1 {
			errs = append(errs, this.process(ctx, d, payload, messageId, ageSeconds))
			continue
		}
		this.async <- struct{}{}
		go func() {
			defer func() { <-this.async }()
			if err := this.process(ctx, d, payload, messageId, ageSeconds); err != nil {
				this.logger.WarnContext(ctx, "event correlation failed", "event", d.MessageName, "error", err)
			}
		}()
	}
	return errors.Join(errs...)
}

// process returns only errors worth reading the message again for
func (this *Worker) process(ctx context.Context, d model.EventDescription, payload map[string]any, messageId string, ageSeconds float64) (err error) {
	ctx, span := tracing.Start(ctx, "event "+d.MessageName, tracing.Instance{Namespace: d.Namespace, DeploymentId: d.DeploymentId, Owner: d.Owner},
		attribute.String("element_id", d.ElementId), attribute.String("message_id", messageId))
	defer func() { tracing.End(span, err) }()
	evaluation, err := this.evaluator.Evaluate(d, payload)
	if errors.Is(err, ErrUnavailable) {
		return err
	}
	if err != nil {
		this.notifyError(ctx, d, err)
		return nil
	}
	if !evaluation.Triggered {
		return nil
	}
	_, err = this.correlator.Correlate(ctx, engine.CorrelationRequest{
		MessageInput: model.MessageInput{
			Name: d.MessageName, All: true, MessageId: messageId + "|" + d.MessageName,
			LocalVariables: map[string]any{"event": evaluation.Value, "age": ageSeconds, "msg_id": messageId},
		},
		OwnerFilter: d.Owner,
	})
	var modelErr *model.Error
	if errors.As(err, &modelErr) && modelErr.Kind != model.KindInternal && modelErr.Kind != model.KindUnavailable {
		this.logger.WarnContext(ctx, "event correlation rejected", "event", d.MessageName, "error", err)
		return nil
	}
	return err
}

// notifyError tells the owner about a failing evaluation, at most once per window, and does not
// block the consumption (EVT-07)
func (this *Worker) notifyError(ctx context.Context, d model.EventDescription, err error) {
	this.notifyMux.Lock()
	last, ok := this.notified[d.Id]
	if ok && this.now().Sub(last) < this.config.ErrorNotificationWindow {
		this.notifyMux.Unlock()
		return
	}
	this.notified[d.Id] = this.now()
	this.notifyMux.Unlock()
	source := d.DeviceId + " " + d.ServiceId
	if d.ImportId != "" {
		source = "import " + d.ImportId
	}
	message := fmt.Sprintf("The event %v of deployment %v could not be evaluated for %v: %v", d.ElementId, d.DeploymentId, strings.TrimSpace(source), err)
	if notifyErr := this.correlator.QueueNotification(ctx, d.Owner, "Event evaluation failed", message); notifyErr != nil {
		this.logger.WarnContext(ctx, "unable to queue event error notification", "error", notifyErr)
	}
}
