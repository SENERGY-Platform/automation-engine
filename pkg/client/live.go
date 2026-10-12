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

package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/debug"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// Watch opens the live debug stream over a WebSocket (DBG-01). An error before the first
// message is returned typed (CLI-03); later errors arrive as message of type error.
func (this *ClientImpl) Watch(ctx context.Context, token string, namespace string, id string, options model.WatchOptions) (<-chan model.DebugMessage, error) {
	target, err := url.Parse(this.baseUrl + path("namespaces", namespace, "instances", id, "live"))
	if err != nil {
		return nil, model.BadRequest("%v", err)
	}
	target.Scheme = strings.Replace(target.Scheme, "http", "ws", 1)
	q := url.Values{}
	setInt(q, "after", options.After)
	if options.EvaluateEvents {
		q.Set("evaluate_events", "true")
	}
	target.RawQuery = q.Encode()
	conn, resp, err := websocket.Dial(ctx, target.String(), &websocket.DialOptions{HTTPClient: this.http, HTTPHeader: http.Header{"Authorization": {bearer(token)}}})
	if err != nil {
		if resp != nil && resp.StatusCode >= 300 {
			return nil, &model.Error{Kind: model.KindFromHttpStatus(resp.StatusCode), Message: err.Error()}
		}
		return nil, model.Unavailable("live stream of %v: %v", id, err)
	}
	// snapshots carry all variables of an instance
	conn.SetReadLimit(64 << 20)
	var first model.DebugMessage
	if err = wsjson.Read(ctx, conn, &first); err != nil {
		conn.CloseNow()
		return nil, closeError(err)
	}
	if first.Type == model.DebugError {
		conn.CloseNow()
		return nil, &model.Error{Kind: first.ErrorKind, Message: first.Error}
	}
	out := make(chan model.DebugMessage, 16)
	go func() {
		defer close(out)
		defer conn.CloseNow()
		message := first
		for {
			select {
			case out <- message:
			case <-ctx.Done():
				return
			}
			if message.Type == model.DebugEnd || message.Type == model.DebugError {
				return
			}
			message = model.DebugMessage{}
			if err := wsjson.Read(ctx, conn, &message); err != nil {
				if e := closeError(err); errors.Is(e, model.ErrForbidden) {
					// the token expired; Follow reconnects with a new one
					message = model.DebugMessage{Type: model.DebugError, Error: e.Error(), ErrorKind: model.KindForbidden}
					continue
				}
				return
			}
		}
	}()
	return out, nil
}

// closeError types an error from a closed stream by its close code, 4000 plus HTTP status
func closeError(err error) error {
	if status := websocket.CloseStatus(err); status >= 4000 && status < 5000 {
		return &model.Error{Kind: model.KindFromHttpStatus(int(status) - 4000), Message: err.Error()}
	}
	return model.Unavailable("live stream: %v", err)
}

// TokenSource returns a current token, e.g. NewTokenProvider; streams ask again after the
// token expired.
type TokenSource func() (string, error)

// Follower keeps the debug state of an instance current from the live stream: it merges
// snapshot and deltas and resumes after an interruption from the last sequence (CLI-05).
type Follower struct {
	c        Client
	token    TokenSource
	ns, id   string
	options  model.WatchOptions
	messages <-chan model.DebugMessage
	state    model.DebugState
	started  bool
	ended    bool
	retry    time.Duration
}

// Follow connects to the live stream of an instance.
func Follow(ctx context.Context, c Client, token TokenSource, namespace string, id string, options model.WatchOptions) (*Follower, error) {
	f := &Follower{c: c, token: token, ns: namespace, id: id, options: options, retry: 500 * time.Millisecond}
	return f, f.connect(ctx)
}

func (this *Follower) connect(ctx context.Context) error {
	token, err := this.token()
	if err != nil {
		return model.Unavailable("no token: %v", err)
	}
	options := this.options
	if this.started {
		options.After = this.state.Seq
	}
	this.messages, err = this.c.Watch(ctx, token, this.ns, this.id, options)
	return err
}

// State is the merged state after the messages read so far.
func (this *Follower) State() model.DebugState {
	return this.state
}

// Next waits for the next message and merges it into State. It returns io.EOF after the end
// message; an interrupted connection is resumed, errors of the instance are returned.
func (this *Follower) Next(ctx context.Context) (model.DebugMessage, error) {
	for {
		if this.ended {
			return model.DebugMessage{}, io.EOF
		}
		if this.messages == nil {
			if err := this.connect(ctx); err != nil {
				if errors.Is(err, model.ErrNotFound) || errors.Is(err, model.ErrForbidden) || errors.Is(err, model.ErrTooMany) || ctx.Err() != nil {
					return model.DebugMessage{}, err
				}
				select {
				case <-ctx.Done():
					return model.DebugMessage{}, ctx.Err()
				case <-time.After(this.retry):
				}
				this.retry = min(2*this.retry, 30*time.Second)
				continue
			}
		}
		select {
		case <-ctx.Done():
			return model.DebugMessage{}, ctx.Err()
		case message, ok := <-this.messages:
			if !ok {
				this.messages = nil
				continue
			}
			switch message.Type {
			case model.DebugSnapshot:
				this.state, this.started = *message.State, true
			case model.DebugDelta:
				this.state = debug.Apply(this.state, message)
			case model.DebugEnd:
				this.state, this.ended = *message.State, true
			case model.DebugError:
				if message.ErrorKind == model.KindForbidden {
					// expired token: reconnect with a new one
					this.messages = nil
					continue
				}
				return message, &model.Error{Kind: message.ErrorKind, Message: message.Error}
			}
			this.retry = 500 * time.Millisecond
			return message, nil
		}
	}
}

// History steps through the reconstructed states of an instance (DBG-13, CLI-05).
type History struct {
	c      Client
	token  TokenSource
	ns, id string
	state  model.DebugState
}

// OpenHistory loads a step, without seq and time the latest.
func OpenHistory(ctx context.Context, c Client, token TokenSource, namespace string, id string, options model.HistoryOptions) (*History, error) {
	h := &History{c: c, token: token, ns: namespace, id: id}
	return h, h.load(ctx, options)
}

func (this *History) load(ctx context.Context, options model.HistoryOptions) error {
	token, err := this.token()
	if err != nil {
		return model.Unavailable("no token: %v", err)
	}
	state, err := this.c.HistoryState(ctx, token, this.ns, this.id, options)
	if err != nil {
		return err
	}
	this.state = state
	return nil
}

func (this *History) State() model.DebugState {
	return this.state
}

// Forward moves one step on; false at the latest step.
func (this *History) Forward(ctx context.Context) (bool, error) {
	before := this.state.Seq
	if err := this.load(ctx, model.HistoryOptions{Seq: before + 1}); err != nil {
		return false, err
	}
	return this.state.Seq > before, nil
}

// Back moves one step back; false at the earliest step that can be reconstructed.
func (this *History) Back(ctx context.Context) (bool, error) {
	if this.state.Seq <= max(this.state.FirstSeq, 1) {
		return false, nil
	}
	return true, this.load(ctx, model.HistoryOptions{Seq: this.state.Seq - 1})
}

// Jump moves to a sequence or to the last step at or before a time.
func (this *History) Jump(ctx context.Context, options model.HistoryOptions) error {
	return this.load(ctx, options)
}
