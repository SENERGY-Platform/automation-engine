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

package fogsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// positionAcked is the position of the change stream the cloud has taken.
const positionAcked = store.PositionUpAcked

type GatewaySettings struct {
	EngineVersion string
	// a batch without acknowledgement is sent again after this time, then with backoff up to MaxResendAfter
	ResendAfter    time.Duration
	MaxResendAfter time.Duration
	StatusInterval time.Duration
	// instances per batch of the change stream
	BatchSize     int
	StorageBudget int64
}

func DefaultGatewaySettings() GatewaySettings {
	return GatewaySettings{ResendAfter: 30 * time.Second, MaxResendAfter: 5 * time.Minute, StatusInterval: 5 * time.Minute, BatchSize: 100}
}

// Local is the API of the gateway the cloud reads through requests: the history stays on the
// gateway (DBG-15), the live stream runs while the cloud renews it (DBG-12).
type Local interface {
	DebugState(ctx context.Context, token string, namespace string, id string, options model.DebugOptions) (model.DebugState, error)
	HistoryState(ctx context.Context, token string, namespace string, id string, options model.HistoryOptions) (model.DebugState, error)
	ListRuns(ctx context.Context, token string, namespace string, id string) ([]model.Run, error)
	ListActivities(ctx context.Context, token string, namespace string, id string, options model.ActivityListOptions) (model.ActivityPage, error)
	ListElementStats(ctx context.Context, token string, namespace string, id string, run int) ([]model.ElementStats, error)
	GetVariables(ctx context.Context, token string, namespace string, id string) ([]model.Variable, error)
	GetVariable(ctx context.Context, token string, namespace string, id string, name string, elementId string) (model.Variable, error)
	HistoryVariable(ctx context.Context, token string, namespace string, id string, name string, elementId string, options model.HistoryOptions) (model.HistoryVariable, error)
	Watch(ctx context.Context, token string, namespace string, id string, options model.WatchOptions) (<-chan model.DebugMessage, error)
}

// Namespaces stores namespaces replicated from the cloud (NS-10).
type Namespaces interface {
	Get(name string) (model.Namespace, bool)
	Put(ctx context.Context, ns model.Namespace) (model.Namespace, bool, error)
}

// Gateway is the side of the synchronisation that runs in the engine on a gateway (SYNC-09):
// it applies the desired state of the cloud and sends the change stream of the gateway, see
// protocol.go. It replaces mgw-process-sync-client.
type Gateway struct {
	settings   GatewaySettings
	store      *store.Store
	engine     *engine.Engine
	namespaces Namespaces
	broker     mqtt.Broker
	logger     *slog.Logger
	now        func() time.Time
	started    time.Time

	mux sync.Mutex
	// the batch in flight: sent after the acknowledged position up to upto
	inflightUpto int64
	inflightAt   time.Time
	resendAfter  time.Duration
	lastStatus   time.Time
	wake         chan struct{}

	local   Local
	watches map[string]*watch
}

type watch struct {
	cancel  context.CancelFunc
	renewed time.Time
}

// SetLocal lets the cloud read instances of the gateway.
func (this *Gateway) SetLocal(local Local) {
	this.local = local
}

func NewGateway(settings GatewaySettings, s *store.Store, e *engine.Engine, namespaces Namespaces, broker mqtt.Broker, logger *slog.Logger) *Gateway {
	return &Gateway{settings: settings, store: s, engine: e, namespaces: namespaces, broker: broker, logger: logger, now: time.Now,
		resendAfter: settings.ResendAfter, wake: make(chan struct{}, 1), watches: map[string]*watch{}}
}

// SetClock replaces the clock, for tests.
func (this *Gateway) SetClock(now func() time.Time) {
	this.now = now
}

func (this *Gateway) envelope() Envelope {
	return Envelope{Protocol: Version, Engine: this.settings.EngineVersion, SentAt: this.now().UTC()}
}

func (this *Gateway) publish(ctx context.Context, kind string, message any) error {
	raw, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return this.broker.Publish(ctx, GatewayStateTopic(kind), 1, raw)
}

// Start subscribes to the messages of the cloud and sends status and change stream until ctx is done.
func (this *Gateway) Start(ctx context.Context) error {
	this.started = this.now()
	// from now on, retention and budget keep instances until the cloud has taken their end
	if err := this.store.Tx(ctx, func(tx *store.Tx) error { return tx.InitSyncPosition(positionAcked) }); err != nil {
		return err
	}
	err := this.broker.Subscribe(GatewayCommandFilter, 1, func(topic string, payload []byte) error {
		return this.Handle(ctx, topic, payload)
	})
	if err != nil {
		return err
	}
	go this.run(ctx)
	return nil
}

func (this *Gateway) run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := this.Tick(ctx); err != nil && ctx.Err() == nil {
			this.logger.Warn("synchronisation with the cloud failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-this.wake:
		}
	}
}

// Tick sends the status when it is due and the next batch of the change stream.
func (this *Gateway) Tick(ctx context.Context) error {
	this.mux.Lock()
	statusDue := this.lastStatus.IsZero() || this.now().Sub(this.lastStatus) >= this.settings.StatusInterval
	this.mux.Unlock()
	var errs []error
	if statusDue {
		errs = append(errs, this.SendStatus(ctx))
	}
	this.endExpiredWatches()
	return errors.Join(append(errs, this.SendChanges(ctx))...)
}

// Handle processes a message of the cloud.
func (this *Gateway) Handle(ctx context.Context, topic string, payload []byte) error {
	_, kind, ok := KindOf(topic)
	if !ok {
		return nil
	}
	var envelope Envelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		this.logger.Warn("unreadable message of the cloud", "topic", topic, "error", err)
		return nil
	}
	if envelope.Protocol != Version {
		this.logger.Error("message of the cloud with another protocol version, the engine of the gateway needs an update", "protocol", envelope.Protocol, "supported", Version)
		return nil
	}
	switch kind {
	case KindDesired:
		var desired Desired
		if err := json.Unmarshal(payload, &desired); err != nil {
			return nil
		}
		results := this.ApplyAll(ctx, desired.Resources)
		return this.publish(ctx, KindDesiredAck, Applied{Envelope: this.envelope(), Results: results})
	case KindRequest:
		var request Request
		if err := json.Unmarshal(payload, &request); err != nil {
			return nil
		}
		return this.answer(ctx, request)
	case KindUpAck:
		var ack UpAck
		if err := json.Unmarshal(payload, &ack); err != nil {
			return nil
		}
		return this.acknowledged(ctx, ack.Acked)
	}
	return nil
}

// acknowledged takes the position the cloud reports as taken; it may also go back, when the
// cloud lost the state of the network, and the gateway then sends from there
func (this *Gateway) acknowledged(ctx context.Context, acked int64) error {
	err := this.store.Tx(ctx, func(tx *store.Tx) error { return tx.SetSyncPosition(positionAcked, acked) })
	if err != nil {
		return err
	}
	this.mux.Lock()
	if acked >= this.inflightUpto {
		this.inflightUpto, this.inflightAt, this.resendAfter = 0, time.Time{}, this.settings.ResendAfter
	}
	this.mux.Unlock()
	select {
	case this.wake <- struct{}{}:
	default:
	}
	return nil
}

// SendChanges sends the next batch of the change stream after the acknowledged position, or
// sends the batch in flight again after the resend time.
func (this *Gateway) SendChanges(ctx context.Context) error {
	this.mux.Lock()
	inflight, at, resendAfter := this.inflightUpto, this.inflightAt, this.resendAfter
	this.mux.Unlock()
	now := this.now()
	if inflight > 0 && now.Sub(at) < resendAfter {
		return nil
	}
	var batch Changes
	var empty bool
	err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		batch, empty, err = this.nextBatch(tx)
		return err
	})
	if err != nil || empty {
		return err
	}
	batch.Envelope = this.envelope()
	this.mux.Lock()
	if inflight > 0 {
		// no acknowledgement: the cloud or the way to it is gone, ask less often
		this.resendAfter = min(this.resendAfter*2, this.settings.MaxResendAfter)
	}
	this.inflightUpto, this.inflightAt = batch.Upto, now
	this.mux.Unlock()
	return this.publish(ctx, KindChanges, batch)
}

// nextBatch reads the changes after the acknowledged position, folded per instance; a
// position whose continuation is purged is answered with a snapshot (LONG-10)
func (this *Gateway) nextBatch(tx *store.Tx) (batch Changes, empty bool, err error) {
	acked, err := tx.SyncPosition(positionAcked)
	if err != nil {
		return batch, false, err
	}
	latest, err := tx.LatestChangeSeq()
	if err != nil {
		return batch, false, err
	}
	if latest == acked {
		return batch, true, nil
	}
	oldest, err := tx.OldestChangeSeq()
	if err != nil {
		return batch, false, err
	}
	batch.After = acked
	// the entries after the position are purged, or the cloud knows more than the gateway has,
	// e.g. after its database was replaced
	if acked < oldest-1 || latest < acked {
		return this.snapshot(tx, acked, latest)
	}
	changes, err := tx.ChangesAfter(acked, this.settings.BatchSize*20)
	if err != nil {
		return batch, false, err
	}
	seen := map[string]bool{}
	for _, c := range changes {
		if !seen[c.InstanceId] && len(seen) >= this.settings.BatchSize {
			break
		}
		seen[c.InstanceId] = true
		batch.Upto = c.Seq
	}
	for id := range seen {
		if err = this.addInstance(tx, &batch, id); err != nil {
			return batch, false, err
		}
	}
	return batch, false, nil
}

func (this *Gateway) snapshot(tx *store.Tx, acked int64, latest int64) (Changes, bool, error) {
	batch := Changes{After: acked, Upto: latest, Snapshot: true}
	ids, err := tx.InstanceIds()
	if err != nil {
		return batch, false, err
	}
	for _, id := range ids {
		if err = this.addInstance(tx, &batch, id); err != nil {
			return batch, false, err
		}
	}
	return batch, false, nil
}

func (this *Gateway) addInstance(tx *store.Tx, batch *Changes, id string) error {
	inst, err := tx.GetInstance(id, false)
	if errors.Is(err, model.ErrNotFound) {
		batch.DeletedInstances = append(batch.DeletedInstances, id)
		return nil
	}
	if err != nil {
		return err
	}
	incidents, err := tx.ListIncidents(inst.Namespace, model.IncidentListOptions{InstanceId: id, Run: inst.Run, PageOptions: model.PageOptions{Limit: 10}}, store.Access{Admin: true})
	if err != nil {
		return err
	}
	batch.Instances = append(batch.Instances, InstanceState{Instance: inst.Instance, Incidents: incidents.Items})
	return nil
}

// SendStatus reports versions, positions, applied resources and storage (SYNC-12, LONG-14).
func (this *Gateway) SendStatus(ctx context.Context) error {
	status := Status{Envelope: this.envelope(), StartedAt: this.started.UTC(), Storage: Storage{BudgetBytes: this.settings.StorageBudget, Rows: map[string]int64{}}}
	err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		if status.Latest, err = tx.LatestChangeSeq(); err != nil {
			return err
		}
		if status.Acked, err = tx.SyncPosition(positionAcked); err != nil {
			return err
		}
		if _, err = tx.PruneAppliedInstances(); err != nil {
			return err
		}
		applied, err := tx.ListApplied()
		if err != nil {
			return err
		}
		for _, a := range applied {
			status.Applied = append(status.Applied, Result{Kind: a.Kind, Id: a.Id, Version: a.Version, Error: a.Error, At: a.At})
		}
		if status.Storage.UsedBytes, err = tx.UsedBytes(); err != nil {
			return err
		}
		rows, err := tx.StorageRows()
		for table, n := range rows {
			status.Storage.Rows[table] = int64(n)
		}
		return err
	})
	if err != nil {
		return err
	}
	this.mux.Lock()
	this.lastStatus = this.now()
	this.mux.Unlock()
	return this.publish(ctx, KindStatus, status)
}

// ApplyAll applies desired resources in their order and returns a result for each.
func (this *Gateway) ApplyAll(ctx context.Context, resources []Resource) []Result {
	results := make([]Result, 0, len(resources))
	for _, r := range resources {
		results = append(results, this.Apply(ctx, r))
	}
	return results
}

// Apply applies a desired resource unless the same or a newer version was applied already;
// a failure is recorded with the version, so that the cloud shows it (SYNC-13).
func (this *Gateway) Apply(ctx context.Context, r Resource) Result {
	var previous store.Applied
	var found bool
	err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		previous, found, err = tx.GetApplied(r.Kind, r.Id)
		return err
	})
	if err != nil {
		return Result{Kind: r.Kind, Id: r.Id, Version: r.Version, Error: err.Error(), At: this.now().UTC()}
	}
	if found && previous.Version >= r.Version {
		return Result{Kind: previous.Kind, Id: previous.Id, Version: previous.Version, Error: previous.Error, At: previous.At}
	}
	applyErr := this.apply(ctx, r)
	result := store.Applied{Kind: r.Kind, Id: r.Id, Version: r.Version, At: this.now().UTC()}
	if applyErr != nil {
		result.Error = applyErr.Error()
		this.logger.Warn("unable to apply a resource of the cloud", "kind", r.Kind, "id", r.Id, "version", r.Version, "error", applyErr)
	}
	if err = this.store.Tx(ctx, func(tx *store.Tx) error { return tx.SetApplied(result) }); err != nil {
		result.Error = errors.Join(applyErr, err).Error()
	}
	return Result{Kind: result.Kind, Id: result.Id, Version: result.Version, Error: result.Error, At: result.At}
}

func (this *Gateway) apply(ctx context.Context, r Resource) error {
	switch r.Kind {
	case ResourceNamespace:
		if r.Deleted || r.Namespace == nil {
			// namespaces of a gateway stay; their resources go with them
			return nil
		}
		ns := *r.Namespace
		ns.Name = r.Id
		_, _, err := this.namespaces.Put(ctx, ns)
		return err
	case ResourceDeployment:
		if r.Deleted {
			return this.store.Tx(ctx, func(tx *store.Tx) error {
				return ignoreNotFound(tx.DeleteDeployment(r.Id))
			})
		}
		if r.Deployment == nil {
			return errors.New("deployment without content")
		}
		return this.applyDeployment(ctx, r.Id, *r.Deployment)
	case ResourceInstance:
		return this.applyInstance(ctx, r)
	}
	return fmt.Errorf("unknown kind of resource %v", r.Kind)
}

// applyDeployment takes over a deployment with id and version of the cloud; the event
// descriptions come from the cloud, which reads the device repository (SYNC-06)
func (this *Gateway) applyDeployment(ctx context.Context, id string, spec DeploymentSpec) error {
	d, v := spec.Deployment, spec.Version
	// on the gateway the deployment is local
	d.Id, d.NetworkId, v.DeploymentId = id, "", id
	if _, ok := this.namespaces.Get(d.Namespace); !ok {
		return fmt.Errorf("namespace %v is not known on the gateway", d.Namespace)
	}
	if d.Version < 1 || v.Version != d.Version {
		return fmt.Errorf("deployment %v has no consistent version (%v, %v)", id, d.Version, v.Version)
	}
	for i := range spec.Descriptions {
		spec.Descriptions[i].DeploymentId, spec.Descriptions[i].Version = id, d.Version
	}
	return this.store.Tx(ctx, func(tx *store.Tx) error {
		existing, err := tx.GetDeployment(id, true)
		created := errors.Is(err, model.ErrNotFound)
		if err != nil && !created {
			return err
		}
		now := this.now().UTC()
		if !created && existing.Version > d.Version {
			return nil
		}
		if !created && existing.Version == d.Version {
			// the same version with descriptions derived again, after a device group changed (SYNC-06)
			return tx.ReplaceEventDescriptions(id, spec.Descriptions, spec.Groups, now)
		}
		if created {
			if err = tx.InsertDeployment(d); err != nil {
				return err
			}
		} else {
			d.CreatedAt = existing.CreatedAt
			d.FaultySince = nil
			if err = tx.UpdateDeployment(d); err != nil {
				return err
			}
			if err = this.engine.ResetRestarts(tx, id); err != nil {
				return err
			}
		}
		if v.CreatedAt.IsZero() {
			v.CreatedAt = now
		}
		if err = tx.InsertDeploymentVersion(v); err != nil {
			return err
		}
		if err = tx.ReplaceEventDescriptions(id, spec.Descriptions, spec.Groups, now); err != nil {
			return err
		}
		if err = this.engine.ActivateVersion(tx, id, d.Version); err != nil {
			return err
		}
		if created {
			return nil
		}
		strategy := v.UpdateStrategy
		if !strategy.Valid() {
			strategy = model.UpdateDrain
		}
		return this.engine.ApplyUpdate(tx, id, strategy)
	})
}

// applyInstance starts, stops or deletes an instance; restarts after incidents are the
// gateway's own and need no cloud (FOG-07)
func (this *Gateway) applyInstance(ctx context.Context, r Resource) error {
	if r.Deleted {
		return this.store.Tx(ctx, func(tx *store.Tx) error {
			if _, err := tx.GetInstance(r.Id, true); err != nil {
				return ignoreNotFound(err)
			}
			return tx.DeleteInstance(r.Id)
		})
	}
	if r.Instance == nil {
		return errors.New("instance without content")
	}
	if r.Instance.Stopped {
		var running bool
		err := this.store.Tx(ctx, func(tx *store.Tx) error {
			inst, err := tx.GetInstance(r.Id, false)
			running = err == nil && inst.State == model.StateRunning
			return ignoreNotFound(err)
		})
		if err != nil || !running {
			return err
		}
		return this.engine.StopInstance(ctx, r.Id, "stopped")
	}
	_, _, err := this.engine.StartInstance(ctx, engine.StartRequest{InstanceId: r.Id, DeploymentId: r.Instance.DeploymentId, Owner: r.Instance.Owner, Variables: r.Instance.Variables})
	return err
}

func ignoreNotFound(err error) error {
	if errors.Is(err, model.ErrNotFound) {
		return nil
	}
	return err
}

// gatewayAdmin reads for the cloud; the cloud has checked the rights of the user
var gatewayAdmin = auth.UnsignedToken("gateway", "admin")

// answer reads what the cloud asks for and sends it to the replica that asked
func (this *Gateway) answer(ctx context.Context, request Request) error {
	respond := func(result any, err error, final bool) error {
		response := Response{Envelope: this.envelope(), RequestId: request.Id, Final: final}
		if err != nil {
			response.Error = model.AsError(err)
		} else if result != nil {
			raw, err := json.Marshal(result)
			if err != nil {
				response.Error = model.AsError(err)
			}
			response.Result = raw
		}
		return this.publish(ctx, KindResponse+"/"+request.ReplyTo, response)
	}
	if this.local == nil {
		return respond(nil, model.Unavailable("the gateway answers no requests"), true)
	}
	ns, id, args := request.Namespace, request.Instance, request.Args
	options := func(target any) error {
		if len(args.Options) == 0 {
			return nil
		}
		return json.Unmarshal(args.Options, target)
	}
	var result any
	var err error
	switch request.Method {
	case MethodDebugState:
		var o model.DebugOptions
		if err = options(&o); err == nil {
			result, err = this.local.DebugState(ctx, gatewayAdmin, ns, id, o)
		}
	case MethodHistoryState:
		var o model.HistoryOptions
		if err = options(&o); err == nil {
			result, err = this.local.HistoryState(ctx, gatewayAdmin, ns, id, o)
		}
	case MethodListRuns:
		result, err = this.local.ListRuns(ctx, gatewayAdmin, ns, id)
	case MethodListActivities:
		var o model.ActivityListOptions
		if err = options(&o); err == nil {
			result, err = this.local.ListActivities(ctx, gatewayAdmin, ns, id, o)
		}
	case MethodListElementStats:
		result, err = this.local.ListElementStats(ctx, gatewayAdmin, ns, id, args.Run)
	case MethodGetVariables:
		result, err = this.local.GetVariables(ctx, gatewayAdmin, ns, id)
	case MethodGetVariable:
		result, err = this.local.GetVariable(ctx, gatewayAdmin, ns, id, args.Name, args.ElementId)
	case MethodHistoryVariable:
		var o model.HistoryOptions
		if err = options(&o); err == nil {
			result, err = this.local.HistoryVariable(ctx, gatewayAdmin, ns, id, args.Name, args.ElementId, o)
		}
	case MethodWatch:
		return this.watch(ctx, request, respond)
	case MethodUnwatch:
		this.mux.Lock()
		if w, ok := this.watches[request.Id]; ok {
			w.cancel()
			delete(this.watches, request.Id)
		}
		this.mux.Unlock()
		return nil
	default:
		err = model.BadRequest("unknown method %v", request.Method)
	}
	return respond(result, err, true)
}

// watch streams the debug messages of an instance while the cloud renews the request (DBG-12)
func (this *Gateway) watch(ctx context.Context, request Request, respond func(any, error, bool) error) error {
	this.mux.Lock()
	if w, ok := this.watches[request.Id]; ok {
		w.renewed = this.now()
		this.mux.Unlock()
		return nil
	}
	this.mux.Unlock()
	var o model.WatchOptions
	if len(request.Args.Options) > 0 {
		if err := json.Unmarshal(request.Args.Options, &o); err != nil {
			return respond(nil, model.BadRequest("invalid options: %v", err), true)
		}
	}
	watchCtx, cancel := context.WithCancel(ctx)
	messages, err := this.local.Watch(watchCtx, gatewayAdmin, request.Namespace, request.Instance, o)
	if err != nil {
		cancel()
		return respond(nil, err, true)
	}
	this.mux.Lock()
	this.watches[request.Id] = &watch{cancel: cancel, renewed: this.now()}
	this.mux.Unlock()
	go func() {
		defer cancel()
		for message := range messages {
			if err := respond(message, nil, false); err != nil {
				this.logger.Warn("unable to stream a debug message", "error", err)
			}
		}
		this.mux.Lock()
		delete(this.watches, request.Id)
		this.mux.Unlock()
		_ = respond(nil, nil, true)
	}()
	return nil
}

// endExpiredWatches stops streams nobody renewed within the lease
func (this *Gateway) endExpiredWatches() {
	this.mux.Lock()
	defer this.mux.Unlock()
	for id, w := range this.watches {
		if this.now().Sub(w.renewed) > WatchLease {
			w.cancel()
			delete(this.watches, id)
		}
	}
}
