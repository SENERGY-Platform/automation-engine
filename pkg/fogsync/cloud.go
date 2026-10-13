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
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/google/uuid"
)

type CloudSettings struct {
	EngineVersion string
	// the shared subscription of the replicas; empty subscribes without sharing
	Group string
	// a desired version without acknowledgement is sent again after this time (SYNC-08)
	ResendAfter time.Duration
	// a network without status since this time counts as offline and is not sent to
	OfflineAfter time.Duration
	// a network without contact since this time is removed with its resources (SYNC-07)
	CleanupMaxAge time.Duration
	// protocol versions before the own one the cloud still serves (SYNC-11)
	ProtocolWindow int
	// answers of gateways to the requests of this replica come to it alone
	ReplicaId string
	// how long a request waits for the gateway (DBG-15)
	CallTimeout time.Duration
}

func DefaultCloudSettings() CloudSettings {
	return CloudSettings{Group: "automation-engine", ResendAfter: time.Minute, OfflineAfter: 15 * time.Minute, CleanupMaxAge: 730 * time.Hour, ProtocolWindow: 1, CallTimeout: 10 * time.Second}
}

// Cloud is the side of the synchronisation that runs in the engine replicas of the cloud
// (SYNC-09, SYNC-10): it takes status and change stream of the gateways and sends them their
// desired state, see protocol.go. It replaces process-sync.
type Cloud struct {
	settings CloudSettings
	store    *store.Store
	broker   mqtt.Broker
	logger   *slog.Logger
	now      func() time.Time
	wake     chan struct{}

	mux     sync.Mutex
	waiting map[string]chan Response
}

func NewCloud(settings CloudSettings, s *store.Store, broker mqtt.Broker, logger *slog.Logger) *Cloud {
	if settings.ReplicaId == "" {
		settings.ReplicaId = uuid.NewString()
	}
	return &Cloud{settings: settings, store: s, broker: broker, logger: logger, now: time.Now, wake: make(chan struct{}, 1), waiting: map[string]chan Response{}}
}

// SetClock replaces the clock, for tests.
func (this *Cloud) SetClock(now func() time.Time) {
	this.now = now
}

func (this *Cloud) envelope() Envelope {
	return Envelope{Protocol: Version, Engine: this.settings.EngineVersion, SentAt: this.now().UTC()}
}

func (this *Cloud) publish(ctx context.Context, networkId string, kind string, message any) error {
	raw, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return this.broker.Publish(ctx, CloudCommandTopic(networkId, kind), 1, raw)
}

// Start subscribes to the messages of all gateways and runs reconciler and cleanup.
func (this *Cloud) Start(ctx context.Context) error {
	err := this.broker.Subscribe(CloudStateFilter(this.settings.Group), 1, func(topic string, payload []byte) error {
		return this.Handle(ctx, topic, payload)
	})
	if err != nil {
		return err
	}
	err = this.broker.Subscribe(CloudResponseFilter(this.settings.ReplicaId), 1, func(topic string, payload []byte) error {
		this.response(payload)
		return nil
	})
	if err != nil {
		return err
	}
	go this.run(ctx)
	return nil
}

func (this *Cloud) run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastCleanup := time.Time{}
	for {
		if err := this.Reconcile(ctx); err != nil && ctx.Err() == nil {
			this.logger.Warn("unable to send the desired state to gateways", "error", err)
		}
		if this.now().Sub(lastCleanup) >= time.Hour {
			lastCleanup = this.now()
			if err := this.Cleanup(ctx); err != nil && ctx.Err() == nil {
				this.logger.Warn("unable to remove networks without contact", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-this.wake:
		}
	}
}

// Wake lets the reconciler send new desired state now, e.g. after a deployment.
func (this *Cloud) Wake() {
	select {
	case this.wake <- struct{}{}:
	default:
	}
}

// Desire sets the desired state of a resource of a gateway within the transaction of the change
// that causes it; the reconciler sends it (SYNC-01).
func (this *Cloud) Desire(tx *store.Tx, networkId string, resource Resource) error {
	spec := Resource{Namespace: resource.Namespace, Deployment: resource.Deployment, Instance: resource.Instance}
	_, err := tx.Desire(networkId, resource.Kind, resource.Id, resource.Deleted, spec, this.now())
	return err
}

// Handle processes a message of a gateway; it returns an error only when the message should be
// delivered again, which a gateway does by itself anyway.
func (this *Cloud) Handle(ctx context.Context, topic string, payload []byte) error {
	networkId, kind, ok := KindOf(topic)
	if !ok || networkId == "" {
		return nil
	}
	var envelope Envelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		this.logger.Warn("unreadable message of a gateway", "network_id", networkId, "error", err)
		return nil
	}
	if !Compatible(envelope.Protocol, this.settings.ProtocolWindow) {
		return this.incompatible(ctx, networkId, envelope)
	}
	var err error
	switch kind {
	case KindStatus:
		var status Status
		if err = json.Unmarshal(payload, &status); err == nil {
			err = this.status(ctx, networkId, status)
		}
	case KindDesiredAck:
		var applied Applied
		if err = json.Unmarshal(payload, &applied); err == nil {
			err = this.applied(ctx, networkId, applied.Results)
		}
	case KindChanges:
		var changes Changes
		if err = json.Unmarshal(payload, &changes); err == nil {
			err = this.changes(ctx, networkId, changes)
		}
	}
	if err != nil {
		this.logger.Warn("message of a gateway not taken", "network_id", networkId, "kind", kind, "error", err)
	}
	return err
}

// incompatible marks a gateway whose protocol the cloud does not serve; it gets nothing until
// it is updated, instead of being served wrongly (SYNC-11)
func (this *Cloud) incompatible(ctx context.Context, networkId string, envelope Envelope) error {
	this.logger.Error("gateway with an unsupported protocol version", "network_id", networkId, "protocol", envelope.Protocol, "supported", Version, "window", this.settings.ProtocolWindow)
	return this.store.Tx(ctx, func(tx *store.Tx) error {
		n, err := tx.LockNetwork(networkId, this.now())
		if err != nil {
			return err
		}
		n.Protocol, n.EngineVersion, n.Compatible, n.LastContact = envelope.Protocol, envelope.Engine, false, this.now()
		return tx.UpdateNetwork(n)
	})
}

// status records the sign of life of a gateway and what it applied; after a restart or a
// reconnect, everything it has not applied goes out again at once
func (this *Cloud) status(ctx context.Context, networkId string, status Status) error {
	var acked int64
	err := this.store.Tx(ctx, func(tx *store.Tx) error {
		n, err := tx.LockNetwork(networkId, this.now())
		if err != nil {
			return err
		}
		storage, err := json.Marshal(status.Storage)
		if err != nil {
			return err
		}
		n.EngineVersion, n.Protocol, n.Compatible, n.StartedAt, n.LastContact, n.Latest, n.Storage = status.Engine, status.Protocol, true, status.StartedAt, this.now(), status.Latest, storage
		if err = tx.UpdateNetwork(n); err != nil {
			return err
		}
		acked = n.Acked
		reported := map[string]Result{}
		for _, r := range status.Applied {
			reported[r.Kind+"|"+r.Id] = r
			if err = tx.SetDesiredApplied(networkId, r.Kind, r.Id, r.Version, r.Error, r.At); err != nil {
				return err
			}
		}
		desired, err := tx.ListDesired(networkId, "")
		if err != nil {
			return err
		}
		for _, d := range desired {
			if r, ok := reported[d.Kind+"|"+d.Id]; !ok || r.Version < d.Version {
				if err = tx.MarkDesiredSent(networkId, d.Kind, d.Id, d.Version, time.Time{}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	this.Wake()
	return this.publish(ctx, networkId, KindUpAck, UpAck{Envelope: this.envelope(), Acked: acked})
}

func (this *Cloud) applied(ctx context.Context, networkId string, results []Result) error {
	return this.store.Tx(ctx, func(tx *store.Tx) error {
		n, err := tx.LockNetwork(networkId, this.now())
		if err != nil {
			return err
		}
		n.LastContact = this.now()
		if err = tx.UpdateNetwork(n); err != nil {
			return err
		}
		for _, r := range results {
			if err = tx.SetDesiredApplied(networkId, r.Kind, r.Id, r.Version, r.Error, r.At); err != nil {
				return err
			}
		}
		return nil
	})
}

// changes takes a batch of the change stream if it continues the acknowledged position, and
// acknowledges after the commit (SYNC-02, SYNC-10)
func (this *Cloud) changes(ctx context.Context, networkId string, changes Changes) error {
	var acked int64
	err := this.store.Tx(ctx, func(tx *store.Tx) error {
		n, err := tx.LockNetwork(networkId, this.now())
		if err != nil {
			return err
		}
		n.LastContact = this.now()
		acked = n.Acked
		if changes.After != n.Acked {
			// a duplicate or a gap: the gateway continues from the acknowledged position
			return tx.UpdateNetwork(n)
		}
		if err = this.take(tx, networkId, changes); err != nil {
			return err
		}
		n.Acked, n.Latest = changes.Upto, max(n.Latest, changes.Upto)
		acked = n.Acked
		return tx.UpdateNetwork(n)
	})
	if err != nil {
		return err
	}
	return this.publish(ctx, networkId, KindUpAck, UpAck{Envelope: this.envelope(), Acked: acked})
}

func (this *Cloud) take(tx *store.Tx, networkId string, changes Changes) error {
	reported := map[string]bool{}
	for _, state := range changes.Instances {
		inst := state.Instance
		reported[inst.Id] = true
		if err := tx.UpsertReplicatedInstance(networkId, inst); err != nil {
			return err
		}
		if err := tx.ReplaceReplicatedIncidents(inst.Id, inst.Run, state.Incidents); err != nil {
			return err
		}
		if inst.State != model.StateRunning && inst.State != model.StatePending {
			if err := tx.ForgetEndedInstance(networkId, inst.Id); err != nil {
				return err
			}
		}
	}
	absent := map[string]bool{}
	deleted := slices.Clone(changes.DeletedInstances)
	if changes.Snapshot {
		// a snapshot holds every instance; what it lacks is gone on the gateway (LONG-10)
		ids, err := tx.NetworkInstanceIds(networkId)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if !reported[id] {
				deleted = append(deleted, id)
				absent[id] = true
			}
		}
	}
	for _, id := range deleted {
		inst, err := tx.GetInstance(id, true)
		if errors.Is(err, model.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if inst.NetworkId != networkId {
			continue
		}
		// the gateway removed an ended instance for its retention or storage budget; the
		// cloud keeps its copy for its own retention
		if absent[id] && inst.State != model.StateRunning && inst.State != model.StatePending {
			continue
		}
		if inst.State == model.StatePending {
			// an instance the cloud just started and the gateway has not seen yet stays;
			// one the gateway applied and no longer has is gone
			desired, found, err := tx.GetDesired(networkId, ResourceInstance, id)
			if err != nil {
				return err
			}
			if !found || desired.Deleted || desired.AppliedVersion < desired.Version || desired.Error != "" {
				continue
			}
		}
		if err = tx.DeleteInstance(id); err != nil {
			return err
		}
	}
	return nil
}

// Reconcile sends every desired version a gateway has not applied, once and then after the
// resend time, to networks in contact (SYNC-08).
func (this *Cloud) Reconcile(ctx context.Context) error {
	now := this.now()
	var due []store.Desired
	err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		due, err = tx.DueDesired(now.Add(-this.settings.ResendAfter), now.Add(-this.settings.OfflineAfter), 500)
		return err
	})
	if err != nil || len(due) == 0 {
		return err
	}
	byNetwork := map[string][]store.Desired{}
	order := []string{}
	for _, d := range due {
		if _, ok := byNetwork[d.NetworkId]; !ok {
			order = append(order, d.NetworkId)
		}
		byNetwork[d.NetworkId] = append(byNetwork[d.NetworkId], d)
	}
	var errs []error
	for _, networkId := range order {
		message := Desired{Envelope: this.envelope()}
		// namespaces before deployments before instances, so that a gateway can apply in order
		list := byNetwork[networkId]
		slices.SortStableFunc(list, func(a, b store.Desired) int { return kindOrder(a.Kind) - kindOrder(b.Kind) })
		for _, d := range list {
			var resource Resource
			if len(d.Spec) > 0 {
				if err = json.Unmarshal(d.Spec, &resource); err != nil {
					errs = append(errs, err)
					continue
				}
			}
			resource.Kind, resource.Id, resource.Version, resource.Deleted = d.Kind, d.Id, d.Version, d.Deleted
			message.Resources = append(message.Resources, resource)
		}
		if err = this.publish(ctx, networkId, KindDesired, message); err != nil {
			errs = append(errs, err)
			continue
		}
		err = this.store.Tx(ctx, func(tx *store.Tx) error {
			for _, d := range list {
				if err := tx.MarkDesiredSent(d.NetworkId, d.Kind, d.Id, d.Version, now); err != nil {
					return err
				}
			}
			return nil
		})
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func kindOrder(kind string) int {
	switch kind {
	case ResourceNamespace:
		return 0
	case ResourceDeployment:
		return 1
	}
	return 2
}

// Cleanup removes networks without contact for longer than the maximum age with their
// deployments and instances (SYNC-07).
func (this *Cloud) Cleanup(ctx context.Context) error {
	if this.settings.CleanupMaxAge <= 0 {
		return nil
	}
	var stale []string
	err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		stale, err = tx.StaleNetworks(this.now().Add(-this.settings.CleanupMaxAge))
		return err
	})
	if err != nil {
		return err
	}
	for _, id := range stale {
		this.logger.Info("removing a network without contact", "network_id", id)
		if err = this.store.Tx(ctx, func(tx *store.Tx) error { return tx.DeleteNetwork(id) }); err != nil {
			return err
		}
	}
	return nil
}

// Overview reads the networks with the given ids, all if ids is nil; detailed adds the desired
// state per resource (SYNC-12, SYNC-13).
func (this *Cloud) Overview(ctx context.Context, ids []string, detailed bool) (result []model.Network, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		networks, err := tx.ListNetworks(ids)
		if err != nil {
			return err
		}
		backlog, err := tx.DesiredBacklog()
		if err != nil {
			return err
		}
		result = make([]model.Network, 0, len(networks))
		for _, n := range networks {
			view := model.Network{Id: n.Id, EngineVersion: n.EngineVersion, Protocol: n.Protocol, Compatible: n.Compatible, StartedAt: optional(n.StartedAt), LastContact: optional(n.LastContact),
				Online: !n.LastContact.IsZero() && this.now().Sub(n.LastContact) < this.settings.OfflineAfter, UpBacklog: max(n.Latest-n.Acked, 0),
				DownBacklog: backlog[n.Id].Pending, Failed: backlog[n.Id].Failed, Storage: n.Storage}
			if detailed {
				desired, err := tx.ListDesired(n.Id, "")
				if err != nil {
					return err
				}
				for _, d := range desired {
					state := model.ResourcePending
					if d.AppliedVersion >= d.Version {
						state = model.ResourceApplied
						if d.Error != "" {
							state = model.ResourceFailed
						}
					}
					view.Resources = append(view.Resources, model.NetworkResource{Kind: d.Kind, Id: d.Id, Deleted: d.Deleted, Version: d.Version, AppliedVersion: d.AppliedVersion,
						State: state, Error: d.Error, AppliedAt: optional(d.AppliedAt), SentAt: optional(d.SentAt)})
				}
			}
			result = append(result, view)
		}
		return nil
	})
	return result, err
}

func optional(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// response hands an answer of a gateway to the request waiting for it
func (this *Cloud) response(payload []byte) {
	var response Response
	if err := json.Unmarshal(payload, &response); err != nil {
		return
	}
	this.mux.Lock()
	ch, ok := this.waiting[response.RequestId]
	this.mux.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- response:
	default:
		this.logger.Warn("answer of a gateway dropped, the receiver is too slow", "request_id", response.RequestId)
	}
}

func (this *Cloud) register(id string, size int) chan Response {
	ch := make(chan Response, size)
	this.mux.Lock()
	this.waiting[id] = ch
	this.mux.Unlock()
	return ch
}

func (this *Cloud) unregister(id string) {
	this.mux.Lock()
	delete(this.waiting, id)
	this.mux.Unlock()
}

// reachable rejects requests to gateways that are offline or need an update
func (this *Cloud) reachable(ctx context.Context, networkId string) error {
	var network store.Network
	var found bool
	err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		network, found, err = tx.GetNetwork(networkId)
		return err
	})
	if err != nil {
		return err
	}
	if !found || this.now().Sub(network.LastContact) >= this.settings.OfflineAfter {
		return model.Unavailable("gateway %v is offline", networkId)
	}
	if !network.Compatible {
		return model.Unavailable("gateway %v needs an update of its engine", networkId)
	}
	return nil
}

func (this *Cloud) request(ctx context.Context, networkId string, id string, method string, namespace string, instance string, args RequestArgs) error {
	return this.publish(ctx, networkId, KindRequest, Request{Envelope: this.envelope(), Id: id, ReplyTo: this.settings.ReplicaId,
		Method: method, Namespace: namespace, Instance: instance, Args: args})
}

// Call reads from a gateway: the state or history of an instance, which stay there (DBG-15).
// An offline gateway or one that does not answer in time is unavailable.
func (this *Cloud) Call(ctx context.Context, networkId string, method string, namespace string, instance string, args RequestArgs, result any) error {
	if err := this.reachable(ctx, networkId); err != nil {
		return err
	}
	id := uuid.NewString()
	ch := this.register(id, 1)
	defer this.unregister(id)
	if err := this.request(ctx, networkId, id, method, namespace, instance, args); err != nil {
		return model.Unavailable("unable to reach gateway %v: %v", networkId, err)
	}
	timer := time.NewTimer(this.settings.CallTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return model.Unavailable("gateway %v did not answer in %v", networkId, this.settings.CallTimeout)
	case response := <-ch:
		if response.Error != nil {
			return response.Error
		}
		if result == nil || len(response.Result) == 0 {
			return nil
		}
		return json.Unmarshal(response.Result, result)
	}
}

// Watch streams the debug messages of an instance of a gateway; the gateway streams only while
// the request is renewed, so the stream ends with ctx or a lost gateway (DBG-12).
func (this *Cloud) Watch(ctx context.Context, networkId string, namespace string, instance string, options model.WatchOptions) (<-chan model.DebugMessage, error) {
	if err := this.reachable(ctx, networkId); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(options)
	if err != nil {
		return nil, err
	}
	args := RequestArgs{Options: raw}
	id := uuid.NewString()
	ch := this.register(id, 256)
	if err = this.request(ctx, networkId, id, MethodWatch, namespace, instance, args); err != nil {
		this.unregister(id)
		return nil, model.Unavailable("unable to reach gateway %v: %v", networkId, err)
	}
	out := make(chan model.DebugMessage, 16)
	go func() {
		defer close(out)
		defer this.unregister(id)
		renew := time.NewTicker(WatchLease / 3)
		defer renew.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = this.request(context.Background(), networkId, id, MethodUnwatch, namespace, instance, RequestArgs{})
				return
			case <-renew.C:
				if err := this.request(ctx, networkId, id, MethodWatch, namespace, instance, args); err != nil {
					this.logger.Warn("unable to renew a live stream of a gateway", "network_id", networkId, "error", err)
				}
			case response := <-ch:
				if response.Final || response.Error != nil {
					return
				}
				var message model.DebugMessage
				if json.Unmarshal(response.Result, &message) != nil {
					continue
				}
				select {
				case out <- message:
				case <-ctx.Done():
				}
			}
		}
	}()
	return out, nil
}
