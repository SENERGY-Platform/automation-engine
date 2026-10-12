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

// Package modules runs the module task types of smart services and other namespaces (TASK-20
// to TASK-29) and keeps their resources: removal through the outbox and health checks.
package modules

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/tracing"
)

// Remover removes the resources of deleted modules from the outbox, with growing pauses
// between attempts, so that nothing is left behind when a service is down (SMS-06).
type Remover struct {
	store     *store.Store
	userToken auth.UserTokenSource
	http      *http.Client
	logger    *slog.Logger
	now       func() time.Time
}

func NewRemover(s *store.Store, userToken auth.UserTokenSource, logger *slog.Logger) *Remover {
	return &Remover{store: s, userToken: userToken, http: &http.Client{Timeout: 30 * time.Second}, logger: logger, now: time.Now}
}

// Run processes due removals until ctx is done.
func (this *Remover) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := this.Process(ctx); err != nil && ctx.Err() == nil {
			this.logger.WarnContext(ctx, "module removal incomplete", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Process works off the due removals; failed ones are retried later.
func (this *Remover) Process(ctx context.Context) error {
	var due []store.ModuleRemoval
	err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		due, err = tx.DueModuleRemovals(this.now(), 50)
		return err
	})
	if err != nil {
		return err
	}
	for _, r := range due {
		removeErr := this.remove(ctx, r)
		err = this.store.Tx(ctx, func(tx *store.Tx) error {
			if removeErr == nil {
				return tx.FinishModuleRemoval(r.ModuleId)
			}
			if r.Attempts >= 2 {
				this.logger.WarnContext(ctx, "unable to remove the resource of a module", "module_id", r.ModuleId, "module_type", r.ModuleType, "attempts", r.Attempts+1, "error", removeErr)
			}
			backoff := min(time.Minute<<min(r.Attempts, 6), time.Hour)
			return tx.RetryModuleRemoval(r.ModuleId, this.now().Add(backoff), removeErr.Error())
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (this *Remover) remove(ctx context.Context, r store.ModuleRemoval) error {
	info := r.DeleteInfo
	if info.Resource != nil {
		ref := *info.Resource
		if ref.Namespace == "" {
			ref.Namespace = r.Namespace
		}
		return this.store.Tx(ctx, func(tx *store.Tx) error {
			return tx.RemoveResource(ref)
		})
	}
	if info.Url == "" {
		return nil
	}
	return Delete(ctx, this.http, this.userToken, info)
}

// Delete calls the delete URL of a module with the token of its user; 2xx and 404 count as
// removed, as with the module workers.
func Delete(ctx context.Context, client *http.Client, userToken auth.UserTokenSource, info model.ModuleDeleteInfo) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, info.Url, nil)
	if err != nil {
		return err
	}
	if info.UserId != "" {
		if userToken == nil {
			return fmt.Errorf("no token source for user %v", info.UserId)
		}
		token, err := userToken(info.UserId)
		if err != nil {
			return fmt.Errorf("token of user %v: %w", info.UserId, err)
		}
		req.Header.Set("Authorization", token)
	}
	if err = tracing.Inject(ctx, req); err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("DELETE %v: %v %v", info.Url, resp.Status, strings.TrimSpace(string(body)))
}
