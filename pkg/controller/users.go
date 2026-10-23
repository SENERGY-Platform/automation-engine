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
	"fmt"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/google/uuid"
)

// GetUserDependents is the dry run of DeleteUser; user-management calls it before any service
// deletes anything (SEC-14).
func (this *Controller) GetUserDependents(ctx context.Context, token string, userId string) (result model.UserDependents, err error) {
	if _, err = this.admin(token); err != nil {
		return result, err
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		result, err = this.dependents(tx, userId)
		return err
	})
	return result, err
}

func (this *Controller) dependents(tx *store.Tx, userId string) (result model.UserDependents, err error) {
	result.UserId = userId
	if result.Owned, err = tx.OwnedCounts(userId); err != nil {
		return result, err
	}
	if result.Dependents, err = tx.UserDependents(userId); err != nil {
		return result, err
	}
	for _, d := range result.Dependents {
		ns, ok := this.namespaces.Get(d.Namespace)
		if !ok || ns.WithDefaults().UserDeletion != model.UserDeletionCascade {
			result.Blocked = true
		}
	}
	return result, nil
}

// DeleteUser removes everything the user owns in all namespaces and the user's rights on
// resources of others (SEC-08). Resources of others that depend on the user's resources are
// deleted with notification of their owners, or the deletion is rejected, as the namespace
// decides (SEC-13). Rights are removed first: if permissions-v2 fails, nothing is deleted and the
// call can be repeated.
func (this *Controller) DeleteUser(ctx context.Context, token string, userId string) error {
	if _, err := this.admin(token); err != nil {
		return err
	}
	return this.DeleteUserData(ctx, userId)
}

// DeleteUserData deletes a user without a request token, e.g. for the Kafka topic user, which
// remains as fallback for users deleted outside of user-management (SEC-14).
func (this *Controller) DeleteUserData(ctx context.Context, userId string) error {
	if userId == "" {
		return model.BadRequest("user id is required")
	}
	var deps model.UserDependents
	err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		deps, err = this.dependents(tx, userId)
		return err
	})
	if err != nil {
		return err
	}
	if deps.Blocked {
		return blockedError(deps)
	}
	if remover, ok := this.auth.(UserRemover); ok {
		err = remover.RemoveUser(ctx, userId, func(kind model.Kind, id string) (owner string, exists bool, err error) {
			err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
				owner, _, exists, err = tx.Owner(kind, id)
				return err
			})
			return owner, exists, err
		})
		if err != nil {
			return err
		}
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		// dependents may have appeared meanwhile
		deps, err := this.dependents(tx, userId)
		if err != nil {
			return err
		}
		if deps.Blocked {
			return blockedError(deps)
		}
		now := this.engine.Now()
		for _, d := range deps.Dependents {
			message := fmt.Sprintf("Your %v %v in %v was deleted because the account that owned its deployment was deleted.", singular(d.Kind), d.Id, d.Namespace)
			if err = tx.InsertNotification(store.Notification{Id: uuid.NewString(), UserId: d.Owner, Title: "Instance deleted", Message: message, Topic: "deletion", Namespace: d.Namespace, CreatedAt: now}); err != nil {
				return err
			}
		}
		// the cloud rows of resources on gateways go at once, the gateways delete them later
		if this.networks != nil {
			onGateways, err := tx.OwnedOnGateways(userId)
			if err != nil {
				return err
			}
			for _, r := range onGateways {
				if err = this.desireDeleted(tx, r.NetworkId, r.Kind, r.Id); err != nil {
					return err
				}
			}
		}
		return tx.DeleteOwned(userId)
	})
	if err == nil {
		this.wake()
	}
	return err
}

func singular(kind model.Kind) string {
	return string(kind)[:len(kind)-1]
}

func blockedError(deps model.UserDependents) error {
	issues := make([]model.Issue, 0, len(deps.Dependents))
	for _, d := range deps.Dependents {
		issues = append(issues, model.Issue{ElementId: d.Namespace + "/" + string(d.Kind) + "/" + d.Id, Message: "owner " + d.Owner + ": " + d.Reason})
	}
	e := model.Conflict("user %v owns resources that resources of other owners depend on", deps.UserId)
	e.Issues = issues
	return e
}

func (this *Controller) GetUserSettings(ctx context.Context, token string, userId string) (result model.UserSettings, err error) {
	user, err := this.parseToken(token)
	if err != nil {
		return result, err
	}
	if !user.IsAdmin() && user.GetUserId() != userId {
		return result, model.Forbidden("only admins may read the settings of other users")
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		result, err = tx.GetUserSettings(userId)
		return err
	})
	return result, err
}

// SetUserSettings sets the limits of a user; a lower retention applies at once (HIST-04).
func (this *Controller) SetUserSettings(ctx context.Context, token string, userId string, settings model.UserSettings) (model.UserSettings, error) {
	if _, err := this.admin(token); err != nil {
		return settings, err
	}
	if settings.MaxRetentionDays < 0 {
		return settings, model.BadRequest("max_retention_days must not be negative")
	}
	settings.UserId = userId
	return settings, this.store.Tx(ctx, func(tx *store.Tx) error {
		return tx.SetUserSettings(settings)
	})
}
