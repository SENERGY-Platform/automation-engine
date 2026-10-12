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

package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/debug"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/script"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/google/uuid"
)

type Config struct {
	// identifies this replica in job locks
	ReplicaId string
	// time zone for timer dates without offset and cron expressions
	Location *time.Location
	// concurrent tokens per instance (LONG-16)
	MaxTokens int
	// JSON size of a single variable and of all variables of an instance (LONG-08)
	MaxVariableBytes          int
	MaxInstanceVariablesBytes int
	// activity log entries per instance, as ring buffer (LONG-03)
	MaxActivities int
	// values in the activity log are truncated to this JSON size
	MaxActivityValueBytes int
	ScriptTimeout         time.Duration
	// task defaults if the element sets no senergy:retries and senergy:timeout (TASK-07)
	TaskTimeout time.Duration
	TaskRetries int
	// delay before a failed task is retried, multiplied by the attempt
	TaskRetryBackoff time.Duration
	PollInterval     time.Duration
	BatchSize        int
	// how long message ids are remembered for idempotent correlation (EVT-05)
	ReceiptRetention time.Duration
	// retention of history in days when neither deployment nor namespace set one, and the global
	// maximum, 0 for none (HIST-04)
	RetentionDefaultDays int
	RetentionMaxDays     int
	// newest model versions kept besides those a deployment refers to (MOD-07)
	ModelVersionsKept    int
	HousekeepingInterval time.Duration
	// false in roles that advance processes but do not execute jobs, e.g. the event worker:
	// jobs are created without checking that the task type runs here
	ExecutesTasks bool
	// full states for the history are written every MaxActivities/SnapshotsPerLog activity
	// entries, at least every SnapshotMaxAge, and at most SnapshotsKept are kept (DBG-14)
	SnapshotsPerLog int
	SnapshotMaxAge  time.Duration
	SnapshotsKept   int
	// how long the change stream keeps entries (SYNC-03)
	ChangeRetention time.Duration
}

func DefaultConfig() Config {
	return Config{
		ReplicaId:                 uuid.NewString(),
		Location:                  time.Local,
		MaxTokens:                 100,
		MaxVariableBytes:          1 << 20,
		MaxInstanceVariablesBytes: 4 << 20,
		MaxActivities:             10000,
		MaxActivityValueBytes:     4096,
		ScriptTimeout:             2 * time.Second,
		TaskTimeout:               2 * time.Minute,
		TaskRetries:               0,
		TaskRetryBackoff:          5 * time.Second,
		PollInterval:              time.Second,
		BatchSize:                 100,
		ReceiptRetention:          24 * time.Hour,
		RetentionDefaultDays:      7,
		ModelVersionsKept:         50,
		HousekeepingInterval:      time.Minute,
		ExecutesTasks:             true,
		SnapshotsPerLog:           20,
		SnapshotMaxAge:            24 * time.Hour,
		SnapshotsKept:             30,
		ChangeRetention:           time.Hour,
	}
}

// Namespaces gives the engine the namespace configuration for checks at execution time (NS-03).
type Namespaces interface {
	Get(name string) (model.Namespace, bool)
}

// Notifier sends notifications to users; it is called after commit from the outbox (INC-02).
type Notifier interface {
	Notify(ctx context.Context, n store.Notification) error
}

type Engine struct {
	store      *store.Store
	config     Config
	sandbox    *script.Sandbox
	namespaces Namespaces
	logger     *slog.Logger
	metrics    Metrics
	notifier   Notifier

	tasksMux sync.RWMutex
	tasks    map[string]TaskType

	cacheMux sync.Mutex
	cache    map[string]*cachedVersion

	wake chan struct{}

	housekeepingMux  sync.Mutex
	lastHousekeeping time.Time
	// cursors of the retention passes, which proceed in small steps
	retentionCursor string
	modelCursor     string

	// replaced in tests
	now func() time.Time
}

const maxCachedProcesses = 1000

func New(s *store.Store, config Config, namespaces Namespaces, logger *slog.Logger) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	if config.Location == nil {
		config.Location = time.Local
	}
	sandbox := script.NewSandbox()
	sandbox.Timeout = config.ScriptTimeout
	sandbox.MaxResultBytes = config.MaxVariableBytes
	return &Engine{
		store:      s,
		config:     config,
		sandbox:    sandbox,
		namespaces: namespaces,
		logger:     logger,
		metrics:    NoMetrics{},
		tasks:      map[string]TaskType{},
		cache:      map[string]*cachedVersion{},
		wake:       make(chan struct{}, 1),
		now:        func() time.Time { return time.Now().UTC() },
	}
}

func (this *Engine) SetMetrics(metrics Metrics) {
	this.metrics = metrics
}

func (this *Engine) SetNotifier(notifier Notifier) {
	this.notifier = notifier
}

// limits of a namespace, falling back to the engine configuration (NS-07)
func (this *Engine) limits(namespace string) model.Limits {
	result := model.Limits{
		MaxTokens:                 this.config.MaxTokens,
		MaxVariableBytes:          this.config.MaxVariableBytes,
		MaxInstanceVariablesBytes: this.config.MaxInstanceVariablesBytes,
		MaxActivities:             this.config.MaxActivities,
	}
	ns, ok := this.namespaces.Get(namespace)
	if !ok {
		return result
	}
	if ns.Limits.MaxTokens > 0 {
		result.MaxTokens = ns.Limits.MaxTokens
	}
	if ns.Limits.MaxVariableBytes > 0 {
		result.MaxVariableBytes = ns.Limits.MaxVariableBytes
	}
	if ns.Limits.MaxInstanceVariablesBytes > 0 {
		result.MaxInstanceVariablesBytes = ns.Limits.MaxInstanceVariablesBytes
	}
	if ns.Limits.MaxActivities > 0 {
		result.MaxActivities = ns.Limits.MaxActivities
	}
	return result
}

func (this *Engine) Now() time.Time {
	return this.now()
}

// SetClock replaces the time source; for tests.
func (this *Engine) SetClock(now func() time.Time) {
	this.now = now
}

func (this *Engine) Store() *store.Store {
	return this.store
}

func (this *Engine) RegisterTaskType(name string, taskType TaskType) {
	this.tasksMux.Lock()
	defer this.tasksMux.Unlock()
	this.tasks[name] = taskType
}

func (this *Engine) TaskTypeAvailable(name string) bool {
	_, ok := this.taskType(name)
	return ok
}

func (this *Engine) taskType(name string) (TaskType, bool) {
	this.tasksMux.RLock()
	defer this.tasksMux.RUnlock()
	t, ok := this.tasks[name]
	return t, ok
}

// Rules returns the validation rules of a namespace, including the available task types.
func (this *Engine) Rules(namespace model.Namespace) bpmn.Rules {
	return bpmn.Rules{
		Elements:          namespace.Elements,
		TaskTypes:         namespace.TaskTypes,
		TaskTypeAvailable: this.TaskTypeAvailable,
		ForbidCycles:      namespace.ForbidCycles,
	}
}

// signal wakes the scheduler, e.g. after a new job or timer has been created
func (this *Engine) signal() {
	select {
	case this.wake <- struct{}{}:
	default:
	}
}

// cachedVersion is a parsed deployment version
type cachedVersion struct {
	process    *bpmn.Process
	selections map[string]model.Selection
}

// process returns the parsed process of a deployment version, cached.
func (this *Engine) process(tx *store.Tx, deploymentId string, version int) (*bpmn.Process, error) {
	v, err := this.version(tx, deploymentId, version)
	if err != nil {
		return nil, err
	}
	return v.process, nil
}

// version returns a parsed deployment version, cached. The creation time is part of the key,
// because a deleted deployment id can be used again with other content.
func (this *Engine) version(tx *store.Tx, deploymentId string, version int) (*cachedVersion, error) {
	created, err := tx.DeploymentVersionCreated(deploymentId, version)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%v/%v/%v", deploymentId, version, created.UnixMilli())
	this.cacheMux.Lock()
	cached, ok := this.cache[key]
	this.cacheMux.Unlock()
	if ok {
		return cached, nil
	}
	v, err := tx.GetDeploymentVersion(deploymentId, version)
	if err != nil {
		return nil, err
	}
	p, _, err := bpmn.Parse(v.Xml)
	if err != nil {
		return nil, fmt.Errorf("deployment %v version %v: %w", deploymentId, version, err)
	}
	// the deploy validated them; what does not fit anymore is left out
	bpmn.ApplyParameters(p, v.Parameters)
	cached = &cachedVersion{process: p, selections: v.Selections}
	this.cacheMux.Lock()
	if len(this.cache) >= maxCachedProcesses {
		for k := range this.cache {
			delete(this.cache, k)
			break
		}
	}
	this.cache[key] = cached
	this.cacheMux.Unlock()
	return cached, nil
}

// EventMessageName is the message name of a device or import event; it is stable across
// updates of the deployment (DEP-04).
func EventMessageName(deploymentId string, elementId string) string {
	return deploymentId + "_" + elementId
}

func messageName(deploymentId string, e *bpmn.Element) string {
	if e.Message == nil || e.Message.DeviceEvent {
		return EventMessageName(deploymentId, e.Id)
	}
	return e.Message.Name
}

// Run executes due timers and jobs until ctx is done.
func (this *Engine) Run(ctx context.Context) {
	ticker := time.NewTicker(this.config.PollInterval)
	defer ticker.Stop()
	for {
		if err := this.ProcessDue(ctx); err != nil && ctx.Err() == nil {
			this.logger.Warn("processing due timers and jobs failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-this.wake:
		}
	}
}

// DebugState builds the live state of an instance for the debugger (DBG-02).
func (this *Engine) DebugState(tx *store.Tx, inst store.Instance) (model.DebugState, error) {
	proc, err := this.process(tx, inst.DeploymentId, inst.DeploymentVersion)
	if err != nil {
		return model.DebugState{}, err
	}
	return debug.Live(tx, inst, proc)
}

// ActivityValueLimit is the size above which values are cut in the activity log and in
// transfers of the debugger (DBG-04).
func (this *Engine) ActivityValueLimit() int {
	return this.config.MaxActivityValueBytes
}

// Process returns the parsed BPMN of a deployment version from the cache.
func (this *Engine) Process(tx *store.Tx, deploymentId string, version int) (*bpmn.Process, error) {
	return this.process(tx, deploymentId, version)
}
