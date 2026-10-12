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

// Package metrics exposes Prometheus metrics of the engine (OPS-05, LONG-13, LONG-14).
package metrics

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const prefix = "automation_engine_"

type Metrics struct {
	registry       *prometheus.Registry
	started        *prometheus.CounterVec
	ended          *prometheus.CounterVec
	incidents      *prometheus.CounterVec
	restarts       *prometheus.CounterVec
	correlations   *prometheus.CounterVec
	tasks          *prometheus.CounterVec
	taskDuration   *prometheus.HistogramVec
	timerDelay     prometheus.Histogram
	storageRows    *prometheus.GaugeVec
	databaseBytes  prometheus.Gauge
	stalled        *prometheus.GaugeVec
	topActivities  *prometheus.GaugeVec
	networkContact *prometheus.GaugeVec
	networkBacklog *prometheus.GaugeVec
	stalledAfter   time.Duration
	// names and labels of the external-task-worker, read by the cost-calculator (TASK-12)
	commandSent        *prometheus.CounterVec
	marshallingLatency *prometheus.HistogramVec
	instanceId         string
}

var _ engine.Metrics = (*Metrics)(nil)

// New registers the metrics; stalledAfter is the time without element change after which a
// running instance counts as stalled.
func New(stalledAfter time.Duration) *Metrics {
	m := &Metrics{
		registry:     prometheus.NewRegistry(),
		stalledAfter: stalledAfter,
		started: prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "runs_started_total", Help: "started runs of instances, including restarts"},
			[]string{"namespace", "deployment_id", "user_id"}),
		ended: prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "runs_ended_total", Help: "ended runs by end state"},
			[]string{"namespace", "deployment_id", "user_id", "state"}),
		incidents: prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "incidents_total", Help: "incidents"},
			[]string{"namespace", "deployment_id", "user_id"}),
		restarts: prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "automatic_restarts_total", Help: "automatic restarts after incidents; performed=false when the restart policy prevented them"},
			[]string{"namespace", "deployment_id", "performed"}),
		correlations: prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "correlations_total", Help: "message correlations by result: matched, unmatched, duplicate"},
			[]string{"result"}),
		tasks: prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "task_executions_total", Help: "task executions by task type and result"},
			[]string{"task_type", "result"}),
		taskDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: prefix + "task_execution_seconds", Help: "duration of task executions", Buckets: prometheus.DefBuckets},
			[]string{"task_type"}),
		timerDelay: prometheus.NewHistogram(prometheus.HistogramOpts{Name: prefix + "timer_delay_seconds", Help: "delay between due time and firing of timers",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 300, 3600}}),
		storageRows: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "storage_rows", Help: "rows per table of runtime state, history, outboxes and IO"},
			[]string{"table"}),
		databaseBytes: prometheus.NewGauge(prometheus.GaugeOpts{Name: prefix + "database_bytes", Help: "size of the database"}),
		stalled: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "stalled_instances", Help: "running instances without element change for the configured time"},
			[]string{"namespace"}),
		networkContact: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "network_last_contact_seconds", Help: "seconds since the last message of a gateway; alarm on large values (SYNC-12)"},
			[]string{"network_id"}),
		networkBacklog: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "network_backlog", Help: "unacknowledged synchronisation of a gateway: up in positions of its change stream, down in resources not applied, failed in resources it could not apply"},
			[]string{"network_id", "direction"}),
		topActivities: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "instance_activities_top", Help: "activity entries of the instances with the most entries"},
			[]string{"namespace", "instance_id"}),
		commandSent: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "external_task_worker_task_command_send_count_vec", Help: "device commands sent; process_definition_id carries the deployment id"},
			[]string{"instance_id", "user_id", "process_definition_id"}),
		marshallingLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "external_task_worker_task_marshalling_latency", Help: "latency of marshaller calls",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5}}, []string{"instance_id", "user_id", "endpoint", "service_id", "function_id"}),
	}
	m.instanceId, _ = os.Hostname()
	m.registry.MustRegister(m.started, m.ended, m.incidents, m.restarts, m.correlations, m.tasks, m.taskDuration, m.timerDelay,
		m.storageRows, m.databaseBytes, m.stalled, m.topActivities, m.networkContact, m.networkBacklog, m.commandSent, m.marshallingLatency,
		prometheus.NewGoCollector(), prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	return m
}

func (this *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(this.registry, promhttp.HandlerOpts{})
}

func (this *Metrics) InstanceStarted(namespace, deploymentId, owner string) {
	this.started.WithLabelValues(namespace, deploymentId, owner).Inc()
}

func (this *Metrics) InstanceEnded(namespace, deploymentId, owner string, state model.InstanceState) {
	this.ended.WithLabelValues(namespace, deploymentId, owner, string(state)).Inc()
}

func (this *Metrics) Incident(namespace, deploymentId, owner string) {
	this.incidents.WithLabelValues(namespace, deploymentId, owner).Inc()
}

func (this *Metrics) Restart(namespace, deploymentId string, performed bool) {
	label := "false"
	if performed {
		label = "true"
	}
	this.restarts.WithLabelValues(namespace, deploymentId, label).Inc()
}

func (this *Metrics) Correlation(result string) {
	this.correlations.WithLabelValues(result).Inc()
}

func (this *Metrics) Task(taskType string, result string, duration time.Duration) {
	this.tasks.WithLabelValues(taskType, result).Inc()
	this.taskDuration.WithLabelValues(taskType).Observe(duration.Seconds())
}

// CommandSent counts a device command per owner and deployment (TASK-12).
func (this *Metrics) CommandSent(userId string, deploymentId string) {
	this.commandSent.WithLabelValues(this.instanceId, userId, deploymentId).Inc()
}

func (this *Metrics) MarshallingLatency(endpoint string, userId string, serviceId string, functionIds string, duration time.Duration) {
	this.marshallingLatency.WithLabelValues(this.instanceId, userId, endpoint, serviceId, functionIds).Observe(duration.Seconds())
}

func (this *Metrics) TimerDelay(delay time.Duration) {
	this.timerDelay.Observe(delay.Seconds())
}

// Collect updates the gauges from the database.
func (this *Metrics) Collect(ctx context.Context, s *store.Store) error {
	return s.Tx(ctx, func(tx *store.Tx) error {
		rows, err := tx.StorageRows()
		if err != nil {
			return err
		}
		for table, n := range rows {
			this.storageRows.WithLabelValues(table).Set(float64(n))
		}
		size, err := tx.DatabaseBytes()
		if err != nil {
			return err
		}
		this.databaseBytes.Set(float64(size))
		stalled, err := tx.StalledInstances(time.Now().Add(-this.stalledAfter))
		if err != nil {
			return err
		}
		this.stalled.Reset()
		for ns, n := range stalled {
			this.stalled.WithLabelValues(ns).Set(float64(n))
		}
		top, err := tx.TopActivityInstances(10)
		if err != nil {
			return err
		}
		this.topActivities.Reset()
		for _, c := range top {
			this.topActivities.WithLabelValues(c.Namespace, c.InstanceId).Set(float64(c.Count))
		}
		return this.collectNetworks(tx)
	})
}

// Run collects the gauges periodically until ctx is done.
func (this *Metrics) Run(ctx context.Context, s *store.Store, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := this.Collect(ctx, s); err != nil && ctx.Err() == nil {
			logger.Warn("unable to collect metrics", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// collectNetworks reports the synchronisation with each gateway (SYNC-12)
func (this *Metrics) collectNetworks(tx *store.Tx) error {
	networks, err := tx.ListNetworks(nil)
	if err != nil {
		return err
	}
	backlog, err := tx.DesiredBacklog()
	if err != nil {
		return err
	}
	this.networkContact.Reset()
	this.networkBacklog.Reset()
	for _, n := range networks {
		if !n.LastContact.IsZero() {
			this.networkContact.WithLabelValues(n.Id).Set(time.Since(n.LastContact).Seconds())
		}
		this.networkBacklog.WithLabelValues(n.Id, "up").Set(float64(max(n.Latest-n.Acked, 0)))
		this.networkBacklog.WithLabelValues(n.Id, "down").Set(float64(backlog[n.Id].Pending))
		this.networkBacklog.WithLabelValues(n.Id, "failed").Set(float64(backlog[n.Id].Failed))
	}
	return nil
}
