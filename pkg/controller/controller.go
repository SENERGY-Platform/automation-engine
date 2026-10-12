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
	"errors"
	"regexp"
	"sync"

	"github.com/SENERGY-Platform/automation-engine/pkg/api"
	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/live"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/parameters"
	"github.com/SENERGY-Platform/automation-engine/pkg/permissions"
	"github.com/SENERGY-Platform/automation-engine/pkg/prepare"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/tasks/processio"
	"github.com/SENERGY-Platform/service-commons/pkg/jwt"
)

const (
	KindModel      = model.KindModel
	KindDeployment = model.KindDeployment
	KindInstance   = model.KindInstance
)

type Right = permissions.Right

const (
	Read         = permissions.Read
	Write        = permissions.Write
	Execute      = permissions.Execute
	Administrate = permissions.Administrate
)

// Namespaces is the namespace registry (NS-02).
type Namespaces interface {
	Get(name string) (model.Namespace, bool)
	List() []model.Namespace
	Put(ctx context.Context, ns model.Namespace) (model.Namespace, bool, error)
	Delete(ctx context.Context, name string) error
}

// AdministrateChecker is implemented by authorizers that know shared rights (SEC-11).
type AdministrateChecker interface {
	HasAdministrate(ctx context.Context, namespace string, kind model.Kind, id string, user string) (bool, error)
}

// UserRemover removes a deleted user's rights on resources of others (SEC-08).
type UserRemover interface {
	RemoveUser(ctx context.Context, userId string, owners permissions.Owners) error
}

type Controller struct {
	store      *store.Store
	engine     *engine.Engine
	namespaces Namespaces
	auth       permissions.Authorizer
	// nil: tokens are not verified here, Kong has verified them
	certs *jwt.KeycloakCertProvider
	// device selection options for deploy dialogs and access checks of selections (DEP-01, DEP-06)
	preparer        *prepare.Preparer
	selectionAccess *prepare.Access
	io              *processio.Service
	describer       *events.Describer
	// live debug streams (DBG-01) and their number per user (DBG-10)
	live         *live.Hub
	liveSettings LiveSettings
	liveMux      sync.Mutex
	liveCount    map[string]int
	// start parameters with options (SMS-08)
	parameters          *parameters.Describer
	parametersUserToken auth.UserTokenSource
	// event debugging; nil without timescale-wrapper (EVT-10, DBG-18)
	series    events.Series
	evaluator *events.Evaluator
	userToken auth.UserTokenSource
}

// SetIo enables the process-io API (IO-02).
func (this *Controller) SetIo(io *processio.Service) {
	this.io = io
}

// SetPreparation enables selection options and access checks of selected devices.
func (this *Controller) SetPreparation(preparer *prepare.Preparer, access *prepare.Access) {
	this.preparer = preparer
	this.selectionAccess = access
}

// eventPlan holds the event descriptions of a deployment version, computed before its transaction
type eventPlan struct {
	descriptions []model.EventDescription
	groups       []string
}

// planSelections validates the deployment input first, so that a malformed selection is
// reported as such, then checks the rights on the selected devices and derives the event
// descriptions (DEP-06, DEP-07). Both call other services and run outside any transaction.
func (this *Controller) planSelections(ctx context.Context, user jwt.Token, ns model.Namespace, input model.DeploymentInput, deployment model.Deployment, version int) (plan eventPlan, err error) {
	if len(input.Selections) == 0 && len(input.Parameters) == 0 {
		return plan, nil
	}
	var resolved resolvedDeployment
	err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		resolved, err = this.resolve(ctx, tx, user, ns, input)
		return err
	})
	if err != nil {
		return plan, err
	}
	if this.selectionAccess != nil && !user.IsAdmin() {
		if err = this.selectionAccess.Check(ctx, user.Jwt(), input.Selections); err != nil {
			return plan, err
		}
	}
	process, _, err := bpmn.Parse(resolved.xml)
	if err != nil {
		return plan, err
	}
	if issues := bpmn.ApplyParameters(process, input.Parameters); len(issues) > 0 {
		return plan, model.ValidationFailed(issues)
	}
	if this.describer == nil {
		return plan, nil
	}
	plan.descriptions, plan.groups, err = this.describer.Describe(deployment, version, process, input.Selections)
	return plan, err
}

// SetDescriber enables event descriptions for event workers (DEP-07).
func (this *Controller) SetDescriber(describer *events.Describer) {
	this.describer = describer
}

var _ api.Controller = (*Controller)(nil)

func New(s *store.Store, e *engine.Engine, namespaces Namespaces, auth permissions.Authorizer, certs *jwt.KeycloakCertProvider) *Controller {
	if auth == nil {
		auth = permissions.OwnerOnly{}
	}
	return &Controller{store: s, engine: e, namespaces: namespaces, auth: auth, certs: certs}
}

func (this *Controller) parseToken(token string) (jwt.Token, error) {
	var parsed jwt.Token
	var err error
	if this.certs != nil {
		parsed, err = jwt.ParseWithValidation(this.certs, token)
	} else {
		parsed, err = jwt.Parse(token)
	}
	if err != nil {
		return parsed, model.Forbidden("invalid token: %v", err)
	}
	if parsed.GetUserId() == "" {
		return parsed, model.Forbidden("token without subject")
	}
	return parsed, nil
}

func (this *Controller) admin(token string) (jwt.Token, error) {
	parsed, err := this.parseToken(token)
	if err != nil {
		return parsed, err
	}
	if !parsed.IsAdmin() {
		return parsed, model.Forbidden("admin role required")
	}
	return parsed, nil
}

// begin parses the token and resolves the namespace of a request
func (this *Controller) begin(token string, namespace string) (jwt.Token, model.Namespace, error) {
	parsed, err := this.parseToken(token)
	if err != nil {
		return parsed, model.Namespace{}, err
	}
	ns, ok := this.namespaces.Get(namespace)
	if !ok {
		return parsed, ns, model.NotFound("namespace %v not found", namespace)
	}
	return parsed, ns, nil
}

// check verifies a right on a resource; a resource the user may not read is reported as not
// found, so that its existence does not leak
func (this *Controller) check(ctx context.Context, token jwt.Token, namespace string, kind model.Kind, id string, owner string, right Right) error {
	ok, err := this.auth.Check(ctx, token, namespace, kind, id, owner, right)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	if right != Read {
		if readable, err := this.auth.Check(ctx, token, namespace, kind, id, owner, Read); err == nil && readable {
			return model.Forbidden("missing right %c on %v %v", right, kind, id)
		}
	}
	return model.NotFound("%v %v not found", kind, id)
}

// owner resolves the owner of a new resource: the caller, or for admins a given owner (SEC-11)
func owner(token jwt.Token, requested string) (string, error) {
	if requested == "" || requested == token.GetUserId() {
		return token.GetUserId(), nil
	}
	if !token.IsAdmin() {
		return "", model.Forbidden("only admins may create resources for another owner")
	}
	return requested, nil
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// validId checks caller provided ids, which become part of URLs and message names
func validId(id string) error {
	if !idPattern.MatchString(id) {
		return model.BadRequest("invalid id %q: 1 to 128 characters of letters, digits, '.', '_', ':' and '-'", id)
	}
	return nil
}

func inNamespace(resourceNamespace string, namespace string, kind model.Kind, id string) error {
	if resourceNamespace != namespace {
		return model.NotFound("%v %v not found", kind, id)
	}
	return nil
}

func isNotFound(err error) bool {
	return errors.Is(err, model.ErrNotFound)
}

func (this *Controller) ListNamespaces(ctx context.Context, token string) ([]model.Namespace, error) {
	if _, err := this.parseToken(token); err != nil {
		return nil, err
	}
	return this.namespaces.List(), nil
}

func (this *Controller) GetNamespace(ctx context.Context, token string, namespace string) (model.Namespace, error) {
	_, ns, err := this.begin(token, namespace)
	return ns, err
}

// PutNamespace creates or changes a namespace with its permissions-v2 topics (NS-02, NS-05).
func (this *Controller) PutNamespace(ctx context.Context, token string, name string, ns model.Namespace) (model.Namespace, error) {
	if _, err := this.admin(token); err != nil {
		return ns, err
	}
	if ns.Name != "" && ns.Name != name {
		return ns, model.BadRequest("name in body and path differ")
	}
	ns.Name = name
	result, _, err := this.namespaces.Put(ctx, ns)
	return result, err
}

func (this *Controller) DeleteNamespace(ctx context.Context, token string, name string) error {
	if _, err := this.admin(token); err != nil {
		return err
	}
	return this.namespaces.Delete(ctx, name)
}

// SetOwner changes the owner of a resource. Allowed are platform admins and users who
// administrate the resource; the new owner must already administrate it (SEC-11).
func (this *Controller) SetOwner(ctx context.Context, token string, namespace string, kind model.Kind, id string, input model.OwnerInput) error {
	user, ns, err := this.begin(token, namespace)
	if err != nil {
		return err
	}
	if err = kind.Valid(); err != nil {
		return err
	}
	if input.Owner == "" {
		return model.BadRequest("owner is required")
	}
	var current, resourceNamespace string
	var exists bool
	err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		current, resourceNamespace, exists, err = tx.Owner(kind, id)
		return err
	})
	if err != nil {
		return err
	}
	if !exists || resourceNamespace != namespace {
		return model.NotFound("%v %v not found", kind, id)
	}
	if err = this.check(ctx, user, namespace, kind, id, current, Administrate); err != nil {
		return err
	}
	if input.Owner == current {
		return nil
	}
	if !user.IsAdmin() {
		checker, ok := this.auth.(AdministrateChecker)
		if !ok || !ns.Level(kind).Registered() {
			return model.Forbidden("only admins may change the owner here")
		}
		allowed, err := checker.HasAdministrate(ctx, namespace, kind, id, input.Owner)
		if err != nil {
			return err
		}
		if !allowed {
			return model.BadRequest("the new owner must administrate the resource already")
		}
	}
	return this.store.Tx(ctx, func(tx *store.Tx) error {
		return tx.SetOwner(kind, id, input.Owner)
	})
}

// ResetRestarts clears the faulty mark of a deployment after a restart loop (INC-04).
func (this *Controller) ResetRestarts(ctx context.Context, token string, namespace string, id string) (result model.Deployment, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		if _, err := this.readDeployment(ctx, tx, token, namespace, id, Write, true); err != nil {
			return err
		}
		if err := this.engine.ResetRestarts(tx, id); err != nil {
			return err
		}
		result, err = tx.GetDeployment(id, false)
		return err
	})
	return result, err
}
