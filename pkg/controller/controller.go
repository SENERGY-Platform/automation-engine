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
	"github.com/SENERGY-Platform/automation-engine/pkg/debug"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
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

// Registrar gives the owner of a resource its rights in permissions-v2 right away instead of
// with the next sync (SEC-11)
type Registrar interface {
	Register(ctx context.Context, namespace string, kind model.Kind, id string, owner string) error
}

// Granter gives users other than the owner rights on resources of gateways and evaluates them
// also where the namespace does not share the kind (FOG-11).
type Granter interface {
	Grant(ctx context.Context, namespace string, kind model.Kind, id string, owner string, user string, rights model.Rights) error
	CheckGranted(ctx context.Context, token jwt.Token, namespace string, kind model.Kind, id string, right Right) (bool, error)
	GrantedIds(ctx context.Context, token jwt.Token, namespace string, kind model.Kind, right Right) ([]string, error)
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
	live         *debug.Hub
	liveSettings LiveSettings
	liveMux      sync.Mutex
	liveCount    map[string]int
	// start parameters with options (SMS-08)
	parameters          *prepare.Describer
	parametersUserToken auth.UserTokenSource
	// event debugging; nil without timescale-wrapper (EVT-10, DBG-18)
	series    events.Series
	evaluator *events.Evaluator
	userToken auth.UserTokenSource
	// nil without synchronisation with gateways (DEP-09)
	networks Networks
	// owners of networks and the token to read them without a caller (FOG-11)
	hubOwners  HubOwners
	adminToken auth.TokenSource
	// usable flags of release lists, computed in the background past the request (SMS-08)
	usable usableFlags
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
	issues := bpmn.ApplyParameters(process, input.Parameters)
	for elementId, params := range input.Parameters {
		if e := process.Element(elementId); e != nil && params.Timer != "" {
			issues = append(issues, bpmn.CheckTimerMinimum(e, this.engine.Rules(ns).MinTimerDuration)...)
		}
	}
	if len(issues) > 0 {
		return plan, model.ValidationFailed(issues)
	}
	if this.describer == nil {
		return plan, nil
	}
	describer := this.describer
	if deployment.NetworkId != "" {
		// the events of a gateway are read on its broker (FOG-06)
		describer = describer.Local()
	}
	plan.descriptions, plan.groups, err = describer.Describe(deployment, version, process, input.Selections)
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
	return this.checkResource(ctx, token, namespace, kind, id, owner, "", right)
}

// checkResource is check for a deployment or instance of networkId; on a gateway the rights
// granted to whoever deployed or started it count as well (FOG-11)
func (this *Controller) checkResource(ctx context.Context, token jwt.Token, namespace string, kind model.Kind, id string, owner string, networkId string, right Right) error {
	allowed := func(right Right) (bool, error) {
		ok, err := this.auth.Check(ctx, token, namespace, kind, id, owner, right)
		if err != nil || ok || networkId == "" {
			return ok, err
		}
		granter, isGranter := this.auth.(Granter)
		if !isGranter {
			return false, nil
		}
		return granter.CheckGranted(ctx, token, namespace, kind, id, right)
	}
	ok, err := allowed(right)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	if right != Read {
		if readable, err := allowed(Read); err == nil && readable {
			return model.Forbidden("missing right %c on %v %v", right, kind, id)
		}
	}
	return model.NotFound("%v %v not found", kind, id)
}

// access restricts lists to what the user may read, with the deployments and instances of
// gateways granted to the user (FOG-11)
func (this *Controller) access(ctx context.Context, token jwt.Token, namespace string, kind model.Kind, right Right) (store.Access, error) {
	access, err := this.auth.Access(ctx, token, namespace, kind, right)
	if err != nil || access.Admin || this.networks == nil || (kind != KindDeployment && kind != KindInstance) {
		return access, err
	}
	if granter, ok := this.auth.(Granter); ok {
		access.Gateway, err = granter.GrantedIds(ctx, token, namespace, kind, right)
	}
	return access, err
}

// grant gives the user who deploys or starts on a gateway the rights of the namespace on the new
// resource, which belongs to the owner of the network (FOG-11)
func (this *Controller) grant(ctx context.Context, token jwt.Token, ns model.Namespace, kind model.Kind, id string, owner string) error {
	granter, ok := this.auth.(Granter)
	if !ok || token.IsAdmin() || token.GetUserId() == owner {
		return nil
	}
	rights := *ns.WithDefaults().GatewayCreatorRights
	if !rights.Any() {
		return nil
	}
	if err := granter.Grant(ctx, ns.Name, kind, id, owner, token.GetUserId(), rights); err != nil {
		return model.Unavailable("rights of %v on %v %v: %v", token.GetUserId(), kind, id, err)
	}
	return nil
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
	var current, resourceNamespace, networkId string
	var exists bool
	err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		current, resourceNamespace, exists, err = tx.Owner(kind, id)
		if err != nil || !exists {
			return err
		}
		networkId, err = networkOf(tx, kind, id)
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
	if networkId != "" {
		// resources of a gateway keep the owner of the network (FOG-11)
		if _, err = this.networkOwner(ctx, token, networkId, input.Owner); err != nil {
			return err
		}
	}
	if input.Owner == current {
		// a repeated change completes the rights a failed registration left pending
		return this.registerOwner(ctx, namespace, kind, id, current)
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
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		return tx.SetOwner(kind, id, input.Owner)
	})
	if err != nil {
		return err
	}
	return this.registerOwner(ctx, namespace, kind, id, input.Owner)
}

// networkOf is the network of a deployment or an instance, empty for resources of the cloud
func networkOf(tx *store.Tx, kind model.Kind, id string) (string, error) {
	switch kind {
	case KindDeployment:
		d, err := tx.GetDeployment(id, false)
		return d.NetworkId, err
	case KindInstance:
		i, err := tx.GetInstance(id, false)
		return i.NetworkId, err
	}
	return "", nil
}

// registerOwner gives the owner its rights before the owner change answers, so that a caller
// can write rights right after it; on an error the sync gives them later, and the caller learns
// that they are not there yet
func (this *Controller) registerOwner(ctx context.Context, namespace string, kind model.Kind, id string, owner string) error {
	registrar, ok := this.auth.(Registrar)
	if !ok {
		return nil
	}
	if err := registrar.Register(ctx, namespace, kind, id, owner); err != nil {
		return model.Unavailable("the owner of %v %v is changed, its rights in permissions-v2 follow with the next sync: %v", kind, id, err)
	}
	return nil
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
