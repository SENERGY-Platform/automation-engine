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
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

var logger = slog.New(slog.NewTextHandler(io.Discard, nil))

// change writes an instance until the signal arrives; the listener connects asynchronously
func expectWake(t *testing.T, s *store.Store, instanceId string, signal <-chan struct{}) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		err := s.Tx(context.Background(), func(tx *store.Tx) error {
			// an update without row still enters the change stream
			return tx.UpdateInstance(store.Instance{Instance: model.Instance{Id: instanceId, Namespace: "processes"}})
		})
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-signal:
			return
		case <-time.After(100 * time.Millisecond):
		case <-deadline:
			t.Fatal("no wake-up for a change of the instance")
		}
	}
}

func TestPostgresNotifiesWithoutPolling(t *testing.T) {
	s, dsn := storetest.OpenPostgresDsn(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := NewHub(s, dsn, logger)
	hub.pollInterval = time.Hour
	signal, unsubscribe := hub.Subscribe("watched")
	defer unsubscribe()
	go hub.Run(ctx)
	expectWake(t, s, "watched", signal)
}

func TestPollingWakesWatchersOfChangedInstancesOnly(t *testing.T) {
	storetest.Run(t, func(t *testing.T, s *store.Store) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		hub := NewHub(s, "", logger)
		hub.pollInterval = 20 * time.Millisecond
		signal, unsubscribe := hub.Subscribe("watched")
		other, unsubscribeOther := hub.Subscribe("other")
		go hub.Run(ctx)
		time.Sleep(100 * time.Millisecond)
		// the first poll wakes everyone once
		drain(signal)
		drain(other)
		expectWake(t, s, "watched", signal)
		select {
		case <-other:
			t.Fatal("a change wakes only the watchers of its instance")
		case <-time.After(100 * time.Millisecond):
		}
		unsubscribe()
		unsubscribeOther()
		if hub.watching() {
			t.Fatal("unsubscribed watchers are removed")
		}
	})
}

func drain(signal <-chan struct{}) {
	select {
	case <-signal:
	default:
	}
}
