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

// Package permissions connects the engine to permissions-v2 (NS-05, SEC-01, SEC-07, SEC-10).
//
// The owner of a resource is kept by the engine and always has every right, independent of
// permissions-v2. Rights of others count only on shareable levels. Resources are registered in
// permissions-v2 asynchronously: a new resource is marked unregistered in its transaction, and
// Sync writes it; deletions queue a removal in their transaction. permissions-v2 being
// unavailable therefore delays sharing but blocks nothing, and no HTTP call runs inside a
// database transaction.
package permissions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"reflect"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	permclient "github.com/SENERGY-Platform/permissions-v2/pkg/client"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
	"github.com/SENERGY-Platform/service-commons/pkg/jwt"
)

// Right follows permissions-v2.
type Right = permmodel.Permission

const (
	Read         = permmodel.Read
	Write        = permmodel.Write
	Execute      = permmodel.Execute
	Administrate = permmodel.Administrate
)

// Authorizer decides access to resources of the engine.
type Authorizer interface {
	Check(ctx context.Context, token jwt.Token, namespace string, kind model.Kind, id string, owner string, right Right) (bool, error)
	// Access restricts list queries to what the token may see
	Access(ctx context.Context, token jwt.Token, namespace string, kind model.Kind, right Right) (store.Access, error)
}

// OwnerOnly grants access to owners and to the admin role; used without permissions-v2.
type OwnerOnly struct{}

func (OwnerOnly) Check(_ context.Context, token jwt.Token, _ string, _ model.Kind, _ string, owner string, _ Right) (bool, error) {
	return token.IsAdmin() || token.GetUserId() == owner, nil
}

func (OwnerOnly) Access(_ context.Context, token jwt.Token, _ string, _ model.Kind, _ Right) (store.Access, error) {
	return store.Access{Admin: token.IsAdmin(), Owner: token.GetUserId()}, nil
}

type Namespaces interface {
	Get(name string) (model.Namespace, bool)
	List() []model.Namespace
}

type Service struct {
	// written into the topics; permissions-v2 publishes rights changes there (SEC-10)
	kafkaTopic string
	client     permclient.Client
	adminToken auth.TokenSource
	// tokens of other users, for checks of their rights in permissions-v2
	userToken  auth.UserTokenSource
	namespaces Namespaces
	store      *store.Store
	logger     *slog.Logger
	batchSize  int
	now        func() time.Time
}

var _ Authorizer = (*Service)(nil)

func New(client permclient.Client, adminToken auth.TokenSource, userToken auth.UserTokenSource, namespaces Namespaces, s *store.Store, logger *slog.Logger) *Service {
	return &Service{client: client, adminToken: adminToken, userToken: userToken, namespaces: namespaces, store: s, logger: logger, batchSize: 100, now: time.Now}
}

// PublishTo makes permissions-v2 publish rights changes of the engine topics to a Kafka topic.
func (this *Service) PublishTo(kafkaTopic string) {
	this.kafkaTopic = kafkaTopic
}

func (this *Service) SetNamespaces(namespaces Namespaces) {
	this.namespaces = namespaces
}

func codeErr(err error, code int) error {
	if err == nil {
		return nil
	}
	if code == http.StatusNotFound {
		return model.NotFound("permissions-v2: %v", err)
	}
	if code == http.StatusBadRequest {
		return model.BadRequest("permissions-v2: %v", err)
	}
	return model.Unavailable("permissions-v2: %v", err)
}

func (this *Service) level(namespace string, kind model.Kind) model.LevelSettings {
	ns, _ := this.namespaces.Get(namespace)
	return ns.Level(kind)
}

func (this *Service) Check(ctx context.Context, token jwt.Token, namespace string, kind model.Kind, id string, owner string, right Right) (bool, error) {
	if token.IsAdmin() || token.GetUserId() == owner {
		return true, nil
	}
	if !this.level(namespace, kind).Shareable {
		return false, nil
	}
	access, err, code := this.client.CheckPermissionContext(ctx, token.Jwt(), model.Topic(namespace, kind), id, right)
	if code == http.StatusNotFound {
		// the topic is not written yet, so nothing is shared
		return false, nil
	}
	return access, codeErr(err, code)
}

func (this *Service) Access(ctx context.Context, token jwt.Token, namespace string, kind model.Kind, right Right) (store.Access, error) {
	result := store.Access{Admin: token.IsAdmin(), Owner: token.GetUserId()}
	if result.Admin || !this.level(namespace, kind).Shareable {
		return result, nil
	}
	ids, err, code := this.client.ListAccessibleResourceIdsContext(ctx, token.Jwt(), model.Topic(namespace, kind), permmodel.ListOptions{}, right)
	if code == http.StatusNotFound {
		// the topic is not written yet, so nothing is shared
		return result, nil
	}
	if err != nil {
		return result, codeErr(err, code)
	}
	result.Ids = ids
	return result, nil
}

// HasAdministrate reports whether user holds administrate on a resource in permissions-v2,
// directly or through a group or role (SEC-11). permissions-v2 evaluates the rights with a
// token of that user.
// HubsTopic is the permissions-v2 topic of gateways, written by device-repository (SEC-05).
const HubsTopic = "hubs"

// CheckHub checks a right of the user on a gateway: rx to read and start, a to deploy and
// delete (SEC-05).
func (this *Service) CheckHub(ctx context.Context, token jwt.Token, networkId string, right Right) (bool, error) {
	if token.IsAdmin() {
		return true, nil
	}
	access, err, code := this.client.CheckPermissionContext(ctx, token.Jwt(), HubsTopic, networkId, right)
	if code == http.StatusNotFound {
		return false, nil
	}
	return access, codeErr(err, code)
}

// ReadableHubs lists the gateways the user may read.
func (this *Service) ReadableHubs(ctx context.Context, token jwt.Token) ([]string, error) {
	ids, err, code := this.client.ListAccessibleResourceIdsContext(ctx, token.Jwt(), HubsTopic, permmodel.ListOptions{}, Read)
	if code == http.StatusNotFound {
		return []string{}, nil
	}
	return ids, codeErr(err, code)
}

func (this *Service) HasAdministrate(ctx context.Context, namespace string, kind model.Kind, id string, user string) (bool, error) {
	token, err := this.userToken(user)
	if err != nil {
		return false, model.Unavailable("no token of the user: %v", err)
	}
	allowed, err, code := this.client.CheckPermissionContext(ctx, token, model.Topic(namespace, kind), id, Administrate)
	if code == http.StatusNotFound {
		return false, nil
	}
	if err != nil {
		return false, codeErr(err, code)
	}
	return allowed, nil
}

func toPermissions(p model.Principals) permmodel.ResourcePermissions {
	convert := func(in map[string]model.Rights) map[string]permmodel.PermissionsMap {
		result := map[string]permmodel.PermissionsMap{}
		for key, r := range in {
			result[key] = permmodel.PermissionsMap{Read: r.Read, Write: r.Write, Execute: r.Execute, Administrate: r.Administrate}
		}
		return result
	}
	return permmodel.ResourcePermissions{UserPermissions: convert(p.Users), GroupPermissions: convert(p.Groups), RolePermissions: convert(p.Roles)}
}

// EnsureTopics writes the three topics of a namespace with their topic-wide default permissions.
func (this *Service) EnsureTopics(ctx context.Context, ns model.Namespace) error {
	token, err := this.adminToken()
	if err != nil {
		return err
	}
	for _, kind := range model.Kinds {
		topic := permmodel.Topic{Id: ns.Topic(kind), DefaultPermissions: toPermissions(ns.Level(kind).TopicDefaultPermissions), PublishToKafkaTopic: this.kafkaTopic}
		if _, err, code := this.client.SetTopicContext(ctx, token, topic); err != nil {
			return codeErr(err, code)
		}
	}
	return nil
}

func (this *Service) RemoveTopics(ctx context.Context, name string) error {
	token, err := this.adminToken()
	if err != nil {
		return err
	}
	for _, kind := range model.Kinds {
		if err, code := this.client.RemoveTopicContext(ctx, token, model.Topic(name, kind)); err != nil && code != http.StatusNotFound {
			return codeErr(err, code)
		}
	}
	return nil
}

// registerAttempts bounds how often register reads again because the resource changed while it
// was working
const registerAttempts = 3

// register makes sure the resource exists in permissions-v2 and the owner administrates it;
// shares made meanwhile are kept. permissions-v2 takes only whole resources and knows no
// versions, so register reads again right before writing and starts over if someone changed
// the resource meanwhile; a change between that read and the write can still be lost.
func (this *Service) register(ctx context.Context, token string, r store.Registration) error {
	ns, ok := this.namespaces.Get(r.Namespace)
	if !ok {
		return fmt.Errorf("unknown namespace %v", r.Namespace)
	}
	level := ns.Level(r.Kind)
	if !level.Registered() {
		return nil
	}
	topic := ns.Topic(r.Kind)
	read := func() (permmodel.ResourcePermissions, bool, error) {
		resource, err, code := this.client.GetResourceContext(ctx, token, topic, r.Id)
		switch {
		case code == http.StatusNotFound:
			return permmodel.ResourcePermissions{}, false, nil
		case err != nil:
			return permmodel.ResourcePermissions{}, false, codeErr(err, code)
		}
		return resource.ResourcePermissions, true, nil
	}
	for attempt := 1; ; attempt++ {
		current, exists, err := read()
		if err != nil {
			return err
		}
		var permissions permmodel.ResourcePermissions
		if exists {
			if current.UserPermissions[r.Owner].Administrate {
				return nil
			}
			permissions = clonePermissions(current)
		} else {
			permissions = toPermissions(level.InitialRights)
		}
		permissions.UserPermissions[r.Owner] = permmodel.PermissionsMap{Read: true, Write: true, Execute: true, Administrate: true}
		again, existsAgain, err := read()
		if err != nil {
			return err
		}
		if existsAgain != exists || !reflect.DeepEqual(again, current) {
			if attempt < registerAttempts {
				continue
			}
			return model.Conflict("the permissions of %v %v keep changing", r.Kind, r.Id)
		}
		_, err, code := this.client.SetPermissionContext(ctx, token, topic, r.Id, permissions)
		return codeErr(err, code)
	}
}

func clonePermissions(p permmodel.ResourcePermissions) permmodel.ResourcePermissions {
	return permmodel.ResourcePermissions{
		UserPermissions:  orEmpty(maps.Clone(p.UserPermissions)),
		GroupPermissions: maps.Clone(p.GroupPermissions),
		RolePermissions:  maps.Clone(p.RolePermissions),
	}
}

func orEmpty(m map[string]permmodel.PermissionsMap) map[string]permmodel.PermissionsMap {
	if m == nil {
		return map[string]permmodel.PermissionsMap{}
	}
	return m
}

// Register makes the owner of a resource administrate it now, as the sync would later, and
// marks the resource registered; an owner change calls it so that the new owner's rights exist
// when the call returns (SEC-11). On an error the sync remains, the resource stays pending.
func (this *Service) Register(ctx context.Context, namespace string, kind model.Kind, id string, owner string) error {
	token, err := this.adminToken()
	if err != nil {
		return model.Unavailable("no admin token: %v", err)
	}
	if err = this.register(ctx, token, store.Registration{Kind: kind, Id: id, Namespace: namespace, Owner: owner}); err != nil {
		return err
	}
	return this.store.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.MarkRegistered(kind, id, owner)
		return err
	})
}

// Sync registers pending resources and processes queued removals, a batch per call.
func (this *Service) Sync(ctx context.Context) error {
	token, err := this.adminToken()
	if err != nil {
		return model.Unavailable("no admin token: %v", err)
	}
	var errs []error
	for _, kind := range model.Kinds {
		var pending []store.Registration
		err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
			pending, err = tx.PendingRegistrations(kind, this.batchSize)
			return err
		})
		if err != nil {
			return err
		}
		for _, r := range pending {
			if err = this.register(ctx, token, r); err != nil {
				errs = append(errs, fmt.Errorf("register %v %v: %w", kind, r.Id, err))
				continue
			}
			// a resource deleted meanwhile has queued its removal, which runs after this
			err = this.store.Tx(ctx, func(tx *store.Tx) error {
				_, err := tx.MarkRegistered(kind, r.Id, r.Owner)
				return err
			})
			errs = append(errs, err)
		}
	}
	var removals []store.Removal
	err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		removals, err = tx.DueRemovals(this.now(), this.batchSize)
		return err
	})
	if err != nil {
		return err
	}
	for _, r := range removals {
		removeErr, code := this.client.RemoveResourceContext(ctx, token, r.Topic, r.ResourceId)
		err = this.store.Tx(ctx, func(tx *store.Tx) error {
			if removeErr == nil || code == http.StatusNotFound {
				return tx.DeleteRemoval(r.Topic, r.ResourceId)
			}
			backoff := time.Duration(min(1<<min(r.Attempts, 10), 3600)) * time.Second
			return tx.RetryRemoval(r.Topic, r.ResourceId, this.now().Add(backoff), removeErr.Error())
		})
		errs = append(errs, err)
		if removeErr != nil && code != http.StatusNotFound {
			errs = append(errs, fmt.Errorf("remove %v %v: %w", r.Topic, r.ResourceId, removeErr))
		}
	}
	return errors.Join(errs...)
}

// Run calls Sync until ctx is done.
func (this *Service) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := this.Sync(ctx); err != nil && ctx.Err() == nil {
			this.logger.Warn("permissions sync incomplete", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Owners resolves the owner of a resource of the engine; ok is false if it does not exist.
type Owners func(kind model.Kind, id string) (owner string, ok bool, err error)

// RemoveUser removes the rights a user holds on resources of others in every topic of the
// engine (SEC-08). Resources the user owns are skipped, they are deleted with their removal.
// The user's resources are listed with an unsigned token of the user, which permissions-v2
// accepts because it does not verify signatures; device-repository deletes users the same way.
func (this *Service) RemoveUser(ctx context.Context, userId string, owners Owners) error {
	token, err := this.adminToken()
	if err != nil {
		return model.Unavailable("no admin token: %v", err)
	}
	userToken := auth.UnsignedToken(userId)
	for _, ns := range this.namespaces.List() {
		for _, kind := range model.Kinds {
			topic := ns.Topic(kind)
			ids, err, code := this.client.ListAccessibleResourceIdsContext(ctx, userToken, topic, permmodel.ListOptions{}, Read)
			if code == http.StatusNotFound {
				continue
			}
			if err != nil {
				return codeErr(err, code)
			}
			for _, id := range ids {
				owner, exists, err := owners(kind, id)
				if err != nil {
					return err
				}
				if exists && owner == userId {
					continue
				}
				resource, err, code := this.client.GetResourceContext(ctx, token, topic, id)
				if code == http.StatusNotFound {
					continue
				}
				if err != nil {
					return codeErr(err, code)
				}
				if _, ok := resource.UserPermissions[userId]; !ok {
					// the access comes from a group or role
					continue
				}
				delete(resource.UserPermissions, userId)
				if exists {
					resource.UserPermissions[owner] = permmodel.PermissionsMap{Read: true, Write: true, Execute: true, Administrate: true}
				}
				if !resource.ResourcePermissions.Valid() {
					// no administrating user is left and the resource is unknown to the engine
					if err, code = this.client.RemoveResourceContext(ctx, token, topic, id); err != nil && code != http.StatusNotFound {
						return codeErr(err, code)
					}
					continue
				}
				if _, err, code = this.client.SetPermissionContext(ctx, token, topic, id, resource.ResourcePermissions); err != nil {
					return codeErr(err, code)
				}
			}
		}
	}
	return nil
}

// RightsMessage is what permissions-v2 publishes on a rights change
type RightsMessage struct {
	Command string `json:"command"`
	Id      string `json:"id"`
	Rights  struct {
		UserRights map[string]permmodel.PermissionsMap `json:"user_rights"`
	} `json:"rights"`
}

// HandleRightsMessage restores the owner's administrate right when it was withdrawn directly in
// permissions-v2 (SEC-10): the resource is registered again, which adds the owner.
func (this *Service) HandleRightsMessage(ctx context.Context, raw []byte) error {
	var msg RightsMessage
	if err := json.Unmarshal(raw, &msg); err != nil || msg.Command != "RIGHTS" || msg.Id == "" {
		return nil
	}
	return this.store.Tx(ctx, func(tx *store.Tx) error {
		for _, kind := range model.Kinds {
			owner, _, exists, err := tx.Owner(kind, msg.Id)
			if err != nil {
				return err
			}
			if exists && !msg.Rights.UserRights[owner].Administrate {
				this.logger.Warn("owner lost administrate in permissions-v2, restoring it", "kind", kind, "id", msg.Id)
				return tx.MarkUnregistered(kind, msg.Id)
			}
		}
		return nil
	})
}
