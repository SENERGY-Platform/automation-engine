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
	"sync/atomic"
	"testing"
	"time"
)

// An attempt that fails while a dependency is starting is repeated until it succeeds.
func TestRetryRepeatsAFailedAttemptUntilItSucceeds(t *testing.T) {
	var calls atomic.Int32
	retry(context.Background(), retrySettings{First: time.Millisecond, Max: 4 * time.Millisecond, Block: time.Second}, func() error {
		if calls.Add(1) < 4 {
			return errors.New("connection refused")
		}
		return nil
	})
	if calls.Load() != 4 {
		t.Fatalf("expected 4 attempts, got %v", calls.Load())
	}
}

// After Block, the start goes on and the attempts continue in the background.
func TestRetryGoesOnInTheBackgroundAfterBlock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	succeed := atomic.Bool{}
	start := time.Now()
	retry(ctx, retrySettings{First: time.Millisecond, Max: 2 * time.Millisecond, Block: 20 * time.Millisecond}, func() error {
		calls.Add(1)
		if succeed.Load() {
			return nil
		}
		return errors.New("connection refused")
	})
	if time.Since(start) > time.Second {
		t.Fatal("the start waits only for Block")
	}
	before := calls.Load()
	succeed.Store(true)
	deadline := time.Now().Add(time.Second)
	for calls.Load() == before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() == before {
		t.Fatal("the attempts go on after Block")
	}
	settled := calls.Load()
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != settled {
		t.Errorf("no attempt after success: %v then %v", settled, calls.Load())
	}
}

// A cancelled context ends the attempts.
func TestRetryStopsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int32
	retry(ctx, retrySettings{First: time.Hour, Max: time.Hour, Block: time.Hour}, func() error {
		calls.Add(1)
		return errors.New("connection refused")
	})
	if calls.Load() != 1 {
		t.Fatalf("one attempt, then the context ends it: %v", calls.Load())
	}
}
