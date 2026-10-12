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

package controller

import (
	"context"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/debug"
	"github.com/SENERGY-Platform/automation-engine/pkg/live"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// LiveSettings bound the live debug streams (DBG-10).
type LiveSettings struct {
	// streams per user and replica
	MaxPerUser int
	// deltas are merged when changes come faster than this
	MinInterval time.Duration
	// a stream checks its instance at least this often, in case a wake-up got lost
	Fallback time.Duration
	// a resume or a delta covering more activity entries sends a new snapshot instead
	MaxDeltaActivities int
	// interval of the evaluation of the current value of waiting events (DBG-18)
	EventInterval time.Duration
}

func DefaultLiveSettings() LiveSettings {
	return LiveSettings{MaxPerUser: 5, MinInterval: 200 * time.Millisecond, Fallback: 5 * time.Second, MaxDeltaActivities: 500, EventInterval: 30 * time.Second}
}

// SetLive enables live debug streams.
func (this *Controller) SetLive(hub *live.Hub, settings LiveSettings) {
	this.live = hub
	this.liveSettings = settings
	this.liveCount = map[string]int{}
}

// Watch streams the debug state of an instance: a snapshot, then deltas, and the end state
// when the instance ends (DBG-01). The channel closes when the stream ends or ctx is done; a
// stream that ends without end message was interrupted and can be resumed with After.
func (this *Controller) Watch(ctx context.Context, token string, namespace string, id string, options model.WatchOptions) (<-chan model.DebugMessage, error) {
	if this.live == nil {
		return nil, model.Unavailable("live debugging is not available")
	}
	user, _, err := this.begin(token, namespace)
	if err != nil {
		return nil, err
	}
	networkId, _, err := this.onGateway(ctx, token, namespace, id)
	if err != nil {
		return nil, err
	}
	userId := user.GetUserId()
	this.liveMux.Lock()
	if this.liveCount[userId] >= this.liveSettings.MaxPerUser {
		this.liveMux.Unlock()
		return nil, model.TooMany("at most %v live debug connections per user", this.liveSettings.MaxPerUser)
	}
	this.liveCount[userId]++
	this.liveMux.Unlock()
	if networkId != "" {
		// the stream of the gateway runs only while somebody watches (DBG-12)
		remote, err := this.networks.Watch(ctx, networkId, namespace, id, options)
		if err != nil {
			this.liveMux.Lock()
			if this.liveCount[userId]--; this.liveCount[userId] <= 0 {
				delete(this.liveCount, userId)
			}
			this.liveMux.Unlock()
			return nil, err
		}
		out := make(chan model.DebugMessage, 16)
		go func() {
			defer func() {
				close(out)
				this.liveMux.Lock()
				if this.liveCount[userId]--; this.liveCount[userId] <= 0 {
					delete(this.liveCount, userId)
				}
				this.liveMux.Unlock()
			}()
			for message := range remote {
				out <- message
			}
		}()
		return out, nil
	}
	wake, unsubscribe := this.live.Subscribe(id)
	out := make(chan model.DebugMessage, 16)
	go func() {
		defer func() {
			close(out)
			unsubscribe()
			this.liveMux.Lock()
			if this.liveCount[userId]--; this.liveCount[userId] <= 0 {
				delete(this.liveCount, userId)
			}
			this.liveMux.Unlock()
		}()
		s := &stream{c: this, id: id, options: options, out: out, needSnapshot: true}
		s.run(ctx, wake)
	}()
	return out, nil
}

type stream struct {
	c            *Controller
	id           string
	options      model.WatchOptions
	out          chan model.DebugMessage
	sent         model.DebugState
	needSnapshot bool
	// evaluations of waiting events, kept between their intervals (DBG-18)
	events      map[string]*model.EventEvaluation
	evaluatedAt time.Time
}

func (this *stream) run(ctx context.Context, wake <-chan struct{}) {
	settings := this.c.liveSettings
	if this.options.After > 0 {
		this.resume(ctx)
	}
	fallback := time.NewTicker(settings.Fallback)
	defer fallback.Stop()
	var lastSend time.Time
	for {
		if wait := settings.MinInterval - time.Since(lastSend); wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
		done, err := this.step(ctx)
		if err != nil {
			if ctx.Err() == nil {
				e := model.AsError(err)
				this.deliver(ctx, model.DebugMessage{Type: model.DebugError, Seq: this.sent.Seq, Error: e.Message, ErrorKind: e.Kind}, true)
			}
			return
		}
		if done {
			return
		}
		lastSend = time.Now()
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-fallback.C:
		}
	}
}

// resume starts from the reconstructed state the client already has, if the gap allows it
func (this *stream) resume(ctx context.Context) {
	_ = this.c.store.Tx(ctx, func(tx *store.Tx) error {
		inst, err := tx.GetInstance(this.id, false)
		if err != nil || inst.ActivitySeq-this.options.After > int64(this.c.liveSettings.MaxDeltaActivities) {
			return err
		}
		state, err := debug.At(tx, inst, this.options.After)
		if err != nil {
			// evicted or unknown: a snapshot follows
			return nil
		}
		this.sent = debug.Cut(state, this.c.engine.ActivityValueLimit())
		this.needSnapshot = false
		return nil
	})
}

// step sends what changed; done reports the end of the stream
func (this *stream) step(ctx context.Context) (done bool, err error) {
	var current model.DebugState
	var activities []model.Activity
	var ended, snapshot bool
	err = this.c.store.Tx(ctx, func(tx *store.Tx) error {
		inst, err := tx.GetInstance(this.id, false)
		if err != nil {
			return err
		}
		if current, err = this.c.engine.DebugState(tx, inst); err != nil {
			return err
		}
		ended = finished(inst.Instance)
		snapshot = this.needSnapshot || this.sent.Seq < inst.ActivityMinSeq-1 || inst.ActivitySeq-this.sent.Seq > int64(this.c.liveSettings.MaxDeltaActivities)
		if snapshot || inst.ActivitySeq == this.sent.Seq {
			return nil
		}
		activities, err = tx.ListActivities(this.id, model.ActivityListOptions{AfterSeq: this.sent.Seq, Limit: this.c.liveSettings.MaxDeltaActivities})
		return err
	})
	if err != nil {
		return false, err
	}
	this.withEvents(ctx, &current)
	current = debug.Cut(current, this.c.engine.ActivityValueLimit())
	if snapshot {
		state := current
		if this.deliver(ctx, model.DebugMessage{Type: model.DebugSnapshot, Seq: current.Seq, State: &state}, false) {
			this.sent, this.needSnapshot = current, false
		}
	} else if delta := debug.Delta(this.sent, current, activities); !debug.Empty(delta) {
		if this.deliver(ctx, delta, false) {
			this.sent = current
		} else {
			// a slow client gets a new snapshot instead of the missed deltas (DBG-10)
			this.needSnapshot = true
		}
	}
	if ended && !this.needSnapshot {
		state := current
		this.deliver(ctx, model.DebugMessage{Type: model.DebugEnd, Seq: current.Seq, State: &state}, true)
		return true, nil
	}
	return false, nil
}

// deliver sends a message; without wait it gives up when the client does not keep up
func (this *stream) deliver(ctx context.Context, message model.DebugMessage, wait bool) bool {
	if wait {
		select {
		case this.out <- message:
			return true
		case <-ctx.Done():
			return false
		}
	}
	select {
	case this.out <- message:
		return true
	default:
		return false
	}
}

// withEvents adds the evaluation of waiting events in the configured interval and keeps the
// previous evaluation in between, so that no source is queried without need (DBG-18)
func (this *stream) withEvents(ctx context.Context, state *model.DebugState) {
	if !this.options.EvaluateEvents {
		return
	}
	if time.Since(this.evaluatedAt) >= this.c.liveSettings.EventInterval {
		this.c.evaluateEvents(ctx, state)
		this.events = map[string]*model.EventEvaluation{}
		for _, w := range state.Waits {
			if w.Message != nil {
				for _, e := range w.Message.Events {
					this.events[e.EventDescriptionId] = e.Current
				}
			}
		}
		this.evaluatedAt = time.Now()
		return
	}
	for i, w := range state.Waits {
		if w.Message == nil {
			continue
		}
		message := *w.Message
		message.Events = append([]model.EventSource(nil), message.Events...)
		for j, e := range message.Events {
			message.Events[j].Current = this.events[e.EventDescriptionId]
		}
		state.Waits[i].Message = &message
	}
}

// finished reports whether an instance ended for good: completed, terminated, or in an incident
// without a pending automatic restart
func finished(inst model.Instance) bool {
	switch inst.State {
	case model.StateCompleted, model.StateTerminated:
		return true
	case model.StateIncident:
		return inst.RestartAt == nil
	}
	return false
}
