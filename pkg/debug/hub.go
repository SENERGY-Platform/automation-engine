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

package debug

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/jackc/pgx/v5"
)

// Hub tells live debug streams of a replica when their instance changed (DBG-09). The
// source is the change stream (SYNC-03): Postgres announces changes on commit through
// LISTEN/NOTIFY, and the outbox is polled as fallback and for SQLite. A wake-up is only a hint;
// the stream reads the state itself, lost or repeated wake-ups do no harm.
type Hub struct {
	store  *store.Store
	logger *slog.Logger
	// Postgres connection string for LISTEN; empty for SQLite
	dsn          string
	pollInterval time.Duration
	mux          sync.Mutex
	watchers     map[string]map[chan struct{}]struct{}
}

// NewHub creates a hub; dsn is the Postgres connection string, empty for SQLite.
func NewHub(s *store.Store, dsn string, logger *slog.Logger) *Hub {
	interval := 250 * time.Millisecond
	if dsn != "" {
		// LISTEN delivers changes at once; polling only covers a lost listener connection
		interval = 2 * time.Second
	}
	return &Hub{store: s, dsn: dsn, logger: logger, pollInterval: interval, watchers: map[string]map[chan struct{}]struct{}{}}
}

// Subscribe returns a channel that receives a signal when the instance changed; signals
// coalesce while unread. The returned function ends the subscription.
func (this *Hub) Subscribe(instanceId string) (<-chan struct{}, func()) {
	signal := make(chan struct{}, 1)
	this.mux.Lock()
	if this.watchers[instanceId] == nil {
		this.watchers[instanceId] = map[chan struct{}]struct{}{}
	}
	this.watchers[instanceId][signal] = struct{}{}
	this.mux.Unlock()
	return signal, func() {
		this.mux.Lock()
		defer this.mux.Unlock()
		delete(this.watchers[instanceId], signal)
		if len(this.watchers[instanceId]) == 0 {
			delete(this.watchers, instanceId)
		}
	}
}

// Wake signals the watchers of an instance.
func (this *Hub) Wake(instanceId string) {
	this.mux.Lock()
	defer this.mux.Unlock()
	for signal := range this.watchers[instanceId] {
		select {
		case signal <- struct{}{}:
		default:
		}
	}
}

func (this *Hub) wakeAll() {
	this.mux.Lock()
	ids := make([]string, 0, len(this.watchers))
	for id := range this.watchers {
		ids = append(ids, id)
	}
	this.mux.Unlock()
	for _, id := range ids {
		this.Wake(id)
	}
}

func (this *Hub) watching() bool {
	this.mux.Lock()
	defer this.mux.Unlock()
	return len(this.watchers) > 0
}

// Run listens and polls until ctx is done.
func (this *Hub) Run(ctx context.Context) {
	if this.dsn != "" {
		go this.listen(ctx)
	}
	this.poll(ctx)
}

func (this *Hub) poll(ctx context.Context) {
	var cursor int64
	started := false
	ticker := time.NewTicker(this.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if !this.watching() {
			// nobody watches, nothing is read (DBG-18 needs the same for its source)
			started = false
			continue
		}
		err := this.store.Tx(ctx, func(tx *store.Tx) error {
			if !started {
				var err error
				if cursor, err = tx.LatestChangeSeq(); err != nil {
					return err
				}
				// changes between the subscription and this point have no entry after the
				// cursor; waking every watcher once covers them
				started = true
				this.wakeAll()
				return nil
			}
			changes, err := tx.ChangesAfter(cursor, 1000)
			for _, c := range changes {
				this.Wake(c.InstanceId)
				cursor = max(cursor, c.Seq)
			}
			return err
		})
		if err != nil && ctx.Err() == nil {
			this.logger.Warn("unable to read the change stream", "error", err)
		}
	}
}

func (this *Hub) listen(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		err := this.listenOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		this.logger.Warn("change notifications interrupted, polling meanwhile", "error", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(2*backoff, 30*time.Second)
	}
}

func (this *Hub) listenOnce(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, this.dsn)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	if _, err = conn.Exec(ctx, "LISTEN "+store.ChangesChannel); err != nil {
		return err
	}
	for {
		notification, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		this.Wake(notification.Payload)
	}
}
