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

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// ListBlockedScripts lists the scripts that ended the engine; admins only (EXPR-03).
func (this *Controller) ListBlockedScripts(ctx context.Context, token string) (result []model.BlockedScript, err error) {
	if _, err = this.admin(token); err != nil {
		return nil, err
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		result, err = tx.ListBlockedScripts()
		return err
	})
	return result, err
}

// UnblockScript removes a script from the blocklist; admins only (EXPR-03). This replica applies
// it at once, the others when they see the new revision.
func (this *Controller) UnblockScript(ctx context.Context, token string, fingerprint string) error {
	user, err := this.admin(token)
	if err != nil {
		return err
	}
	return this.engine.UnblockScript(ctx, fingerprint, user.GetUserId())
}
