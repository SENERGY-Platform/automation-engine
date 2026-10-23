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
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/migrate/rewrite"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/prepare"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/service-commons/pkg/jwt"
	"github.com/google/uuid"
)

// resolvedDeployment is the BPMN a deployment input refers to, validated for the namespace
type resolvedDeployment struct {
	xml          string
	svg          string
	name         string
	modelId      string
	modelVersion int
	// what was converted from the format of Camunda 7 or the old designer
	conversion model.ConversionReport
}

// resolve takes the BPMN from a model version or from the input, converts it where it is in
// the format of Camunda 7 or the old designer (MIG-04) and validates it; a model that does not
// pass, or has parts the conversion cannot rewrite, is rejected naming the elements (BPMN-18)
func (this *Controller) resolve(ctx context.Context, tx *store.Tx, user jwt.Token, ns model.Namespace, input model.DeploymentInput) (result resolvedDeployment, err error) {
	result, err = this.source(ctx, tx, user, ns, input.ModelId, input.ModelVersion, input.Xml, input.Svg)
	if err != nil {
		return result, err
	}
	checked, err := rewrite.Check(result.xml, this.engine.Rules(ns))
	if err != nil {
		return result, model.ValidationFailed([]model.Issue{{Message: err.Error()}})
	}
	issues := append(checked.Issues, prepare.ValidateSelections(checked.Process, input.Selections)...)
	if len(issues) > 0 {
		return result, model.ValidationFailed(issues)
	}
	result.xml, result.conversion = checked.Xml, checked.Report
	result.name = firstNonEmpty(input.Name, result.name, checked.Process.Name, checked.Process.Id)
	return result, nil
}

// conversionOf is the conversion for an answer; nil if there was nothing to convert
func conversionOf(report model.ConversionReport) *model.ConversionReport {
	if report.Empty() {
		return nil
	}
	return &report
}

// source reads the BPMN of a model version or takes the given BPMN
func (this *Controller) source(ctx context.Context, tx *store.Tx, user jwt.Token, ns model.Namespace, modelId string, modelVersion int, xml string, svg string) (result resolvedDeployment, err error) {
	input := model.DeploymentInput{ModelId: modelId, ModelVersion: modelVersion, Xml: xml, Svg: svg}
	switch {
	case input.ModelId != "":
		m, err := tx.GetModel(input.ModelId, false)
		if err != nil {
			return result, err
		}
		if err = inNamespace(m.Namespace, ns.Name, KindModel, m.Id); err != nil {
			return result, err
		}
		// deploying needs the execute right on the model, as process-deployment checks today (MOD-02)
		if err = this.check(ctx, user, ns.Name, KindModel, m.Id, m.Owner, Execute); err != nil {
			return result, err
		}
		version := input.ModelVersion
		if version == 0 {
			version = m.Version
		}
		v, err := tx.GetModelVersion(m.Id, version)
		if err != nil {
			return result, err
		}
		result = resolvedDeployment{xml: v.Xml, svg: v.Svg, name: m.Name, modelId: m.Id, modelVersion: v.Version}
		if input.Xml != "" {
			// the content of the model in another form, e.g. rewritten for this engine (MIG-04);
			// the deployment keeps its reference to the model
			result.xml = input.Xml
			if input.Svg != "" {
				result.svg = input.Svg
			}
		}
	case input.Xml != "":
		result = resolvedDeployment{xml: input.Xml, svg: input.Svg}
	default:
		return result, model.BadRequest("model_id or xml is required")
	}
	return result, nil
}

func (this *Controller) CreateDeployment(ctx context.Context, token string, namespace string, input model.DeploymentInput) (model.Deployment, error) {
	return this.createDeployment(ctx, token, namespace, uuid.NewString(), input)
}

// checkRetention rejects a retention above the limits of namespace, platform and owner (HIST-04)
func (this *Controller) checkRetention(tx *store.Tx, namespace string, deploymentOwner string, days int) error {
	if days < 0 {
		return model.BadRequest("retention_days must not be negative")
	}
	if days == 0 {
		return nil
	}
	maximum, err := this.engine.MaxRetention(tx, namespace, deploymentOwner)
	if err != nil {
		return err
	}
	if maximum > 0 && days > maximum {
		return model.BadRequest("retention_days %v exceeds the limit of %v days", days, maximum)
	}
	return nil
}

func sameSelections(a, b map[string]model.Selection) bool {
	return sameAsJson(a, b)
}

// sameParameters compares deploy parameters by effect: nil and empty maps are equal, and an
// element without any parameter is the same as no entry for it
func sameParameters(a, b map[string]model.ElementParameters) bool {
	effective := func(p map[string]model.ElementParameters) map[string]model.ElementParameters {
		result := map[string]model.ElementParameters{}
		for id, params := range p {
			if len(params.Inputs) > 0 || len(params.Variables) > 0 || params.Timer != "" || len(params.Defaults) > 0 {
				result[id] = params
			}
		}
		return result
	}
	return sameAsJson(effective(a), effective(b))
}

// sameAsJson compares values as they are stored: nil and empty maps are equal, and numbers
// compare by value whatever their Go type
func sameAsJson[T any](a, b map[string]T) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	ra, errA := json.Marshal(a)
	rb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return reflect.DeepEqual(a, b)
	}
	var na, nb any
	if json.Unmarshal(ra, &na) != nil || json.Unmarshal(rb, &nb) != nil {
		return reflect.DeepEqual(a, b)
	}
	return reflect.DeepEqual(na, nb)
}

// unchanged reports whether an update would change nothing, so that repeating it is harmless.
// Every field of the input is considered:
//   - counted: name, description, model id and version, xml and svg (as resolved), incident
//     handling (nil is the namespace default), retention, selections, parameters, and source if
//     given (an empty source keeps the stored one);
//   - not counted: update_strategy (how a change is applied, not a change), owner and network
//     id (an update must not change them and is refused if it tries), managed_by (set by the
//     engine at creation only).
func unchanged(d model.Deployment, current model.DeploymentVersion, resolved resolvedDeployment, input model.DeploymentInput, handling model.IncidentHandling) bool {
	return current.Xml == resolved.xml && current.Svg == resolved.svg && d.Name == resolved.name && d.Description == input.Description &&
		d.ModelId == resolved.modelId && d.ModelVersion == resolved.modelVersion && current.IncidentHandling == handling &&
		d.RetentionDays == input.RetentionDays && (input.Source == "" || input.Source == d.Source) &&
		sameSelections(current.Selections, input.Selections) && sameParameters(current.Parameters, input.Parameters)
}

func incidentHandling(ns model.Namespace, input *model.IncidentHandling) model.IncidentHandling {
	if input != nil {
		return *input
	}
	return ns.IncidentHandling
}

func (this *Controller) createDeployment(ctx context.Context, token string, namespace string, id string, input model.DeploymentInput) (result model.Deployment, err error) {
	user, ns, err := this.begin(token, namespace)
	if err != nil {
		return result, err
	}
	deploymentOwner, err := owner(user, input.Owner)
	if err != nil {
		return result, err
	}
	if err = ns.CheckGateway(input.NetworkId); err != nil {
		return result, err
	}
	if input.NetworkId != "" {
		if err = this.checkNetwork(ctx, user, input.NetworkId, Administrate); err != nil {
			return result, err
		}
	}
	plan, err := this.planSelections(ctx, user, ns, input, model.Deployment{Id: id, Namespace: ns.Name, Owner: deploymentOwner, NetworkId: input.NetworkId}, 1)
	if err != nil {
		return result, err
	}
	var conversion model.ConversionReport
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		resolved, err := this.resolve(ctx, tx, user, ns, input)
		if err != nil {
			return err
		}
		conversion = resolved.conversion
		if err = this.checkRetention(tx, namespace, deploymentOwner, input.RetentionDays); err != nil {
			return err
		}
		now := time.Now().UTC()
		result = model.Deployment{
			Id: id, Namespace: ns.Name, Name: resolved.name, Description: input.Description, Owner: deploymentOwner,
			ModelId: resolved.modelId, ModelVersion: resolved.modelVersion, Version: 1, Source: input.Source, RetentionDays: input.RetentionDays,
			ManagedBy: input.ManagedBy, NetworkId: input.NetworkId, CreatedAt: now, UpdatedAt: now,
		}
		if err = tx.InsertDeployment(result); err != nil {
			return err
		}
		version := model.DeploymentVersion{
			DeploymentId: id, Version: 1, ModelVersion: resolved.modelVersion, Xml: resolved.xml, Svg: resolved.svg,
			IncidentHandling: incidentHandling(ns, input.IncidentHandling), Selections: input.Selections, Parameters: input.Parameters, CreatedAt: now,
		}
		if err = tx.InsertDeploymentVersion(version); err != nil {
			return err
		}
		if len(plan.descriptions) > 0 || len(plan.groups) > 0 {
			if err = tx.ReplaceEventDescriptions(id, plan.descriptions, plan.groups, now); err != nil {
				return err
			}
		}
		if result.NetworkId != "" {
			// runs on the gateway, not here
			return this.desireDeployment(tx, result)
		}
		// deployment, version, event descriptions and start events are written in one transaction (DEP-03)
		return this.engine.ActivateVersion(tx, id, 1)
	})
	if err == nil && result.NetworkId != "" {
		this.networks.Wake()
	}
	if err == nil {
		result.Conversion = conversionOf(conversion)
	}
	return result, err
}

// PutDeployment creates a deployment with the given id or updates it. An update with the same
// BPMN and metadata as the current version changes nothing, so that repeating it is harmless.
func (this *Controller) PutDeployment(ctx context.Context, token string, namespace string, id string, input model.DeploymentInput) (result model.Deployment, err error) {
	if err = validId(id); err != nil {
		return result, err
	}
	user, ns, err := this.begin(token, namespace)
	if err != nil {
		return result, err
	}
	var existing model.Deployment
	err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		existing, err = tx.GetDeployment(id, false)
		return err
	})
	if err != nil && !isNotFound(err) {
		return result, err
	}
	if err == nil && input.NetworkId != "" && input.NetworkId != existing.NetworkId {
		return result, model.BadRequest("the network of a deployment cannot change")
	}
	// a gateway deployment from before the namespace became cloud only keeps running, but is
	// not updated; a new deployment is checked by createDeployment
	if err == nil && existing.Namespace == namespace {
		if checkErr := ns.CheckGateway(existing.NetworkId); checkErr != nil {
			return result, checkErr
		}
	}
	if err == nil && existing.NetworkId != "" {
		if err = this.checkNetwork(ctx, user, existing.NetworkId, Administrate); err != nil {
			return result, err
		}
	}
	var plan eventPlan
	if err == nil && existing.Namespace == namespace {
		if plan, err = this.planSelections(ctx, user, ns, input, existing, existing.Version+1); err != nil {
			return result, err
		}
	}
	created := false
	var conversion model.ConversionReport
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		result, err = tx.GetDeployment(id, true)
		if isNotFound(err) {
			created = true
			return nil
		}
		if err != nil {
			return err
		}
		if err = inNamespace(result.Namespace, namespace, KindDeployment, id); err != nil {
			// the id is taken in another namespace
			return model.Conflict("deployment id %v is already used", id)
		}
		if err = this.check(ctx, user, namespace, KindDeployment, id, result.Owner, Write); err != nil {
			return err
		}
		if input.Owner != "" && input.Owner != result.Owner {
			return model.BadRequest("the owner cannot be changed by an update")
		}
		resolved, err := this.resolve(ctx, tx, user, ns, input)
		if err != nil {
			return err
		}
		conversion = resolved.conversion
		if err = this.checkRetention(tx, namespace, result.Owner, input.RetentionDays); err != nil {
			return err
		}
		current, err := tx.GetDeploymentVersion(id, result.Version)
		if err != nil {
			return err
		}
		handling := incidentHandling(ns, input.IncidentHandling)
		if unchanged(result, current, resolved, input, handling) {
			return nil
		}
		if !input.UpdateStrategy.Valid() {
			return model.BadRequest("update_strategy must be one of stop, drain or restart")
		}
		now := time.Now().UTC()
		result.Version++
		result.Name = resolved.name
		result.Description = input.Description
		result.ModelId = resolved.modelId
		result.ModelVersion = resolved.modelVersion
		result.ModelDeleted = false
		result.Source = firstNonEmpty(input.Source, result.Source)
		result.RetentionDays = input.RetentionDays
		result.UpdatedAt = now
		// an update ends a restart loop, the user has acted (INC-04)
		result.FaultySince = nil
		if err = tx.UpdateDeployment(result); err != nil {
			return err
		}
		if err = this.engine.ResetRestarts(tx, id); err != nil {
			return err
		}
		err = tx.InsertDeploymentVersion(model.DeploymentVersion{
			DeploymentId: id, Version: result.Version, ModelVersion: resolved.modelVersion, Xml: resolved.xml, Svg: resolved.svg,
			UpdateStrategy: input.UpdateStrategy, IncidentHandling: handling, Selections: input.Selections, Parameters: input.Parameters, CreatedAt: now,
		})
		if err != nil {
			return err
		}
		for i := range plan.descriptions {
			plan.descriptions[i].Version = result.Version
		}
		if err = tx.ReplaceEventDescriptions(id, plan.descriptions, plan.groups, now); err != nil {
			return err
		}
		if result.NetworkId != "" {
			return this.desireDeployment(tx, result)
		}
		if err = this.engine.ActivateVersion(tx, id, result.Version); err != nil {
			return err
		}
		return this.engine.ApplyUpdate(tx, id, input.UpdateStrategy)
	})
	if err == nil && result.NetworkId != "" {
		this.networks.Wake()
	}
	if err == nil && created {
		return this.createDeployment(ctx, token, namespace, id, input)
	}
	if err == nil {
		result.Conversion = conversionOf(conversion)
	}
	return result, err
}

func (this *Controller) readDeployment(ctx context.Context, tx *store.Tx, token string, namespace string, id string, right Right, lock bool) (model.Deployment, error) {
	user, _, err := this.begin(token, namespace)
	if err != nil {
		return model.Deployment{}, err
	}
	d, err := tx.GetDeployment(id, lock)
	if err != nil {
		return d, err
	}
	if err = inNamespace(d.Namespace, namespace, KindDeployment, id); err != nil {
		return d, err
	}
	return d, this.check(ctx, user, namespace, KindDeployment, id, d.Owner, right)
}

func (this *Controller) GetDeployment(ctx context.Context, token string, namespace string, id string) (result model.Deployment, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		result, err = this.readDeployment(ctx, tx, token, namespace, id, Read, false)
		return err
	})
	return result, err
}

func (this *Controller) ListDeployments(ctx context.Context, token string, namespace string, options model.DeploymentListOptions) (result model.Page[model.Deployment], err error) {
	user, _, err := this.begin(token, namespace)
	if err != nil {
		return result, err
	}
	access, err := this.auth.Access(ctx, user, namespace, KindDeployment, Read)
	if err != nil {
		return result, err
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		result, err = tx.ListDeployments(namespace, options, access)
		return err
	})
	return result, err
}

// DeleteDeployment removes all versions with instances, history, incidents and start events
// in one transaction (DEP-11).
func (this *Controller) DeleteDeployment(ctx context.Context, token string, namespace string, id string) error {
	err := this.store.Tx(ctx, func(tx *store.Tx) error {
		d, err := this.readDeployment(ctx, tx, token, namespace, id, Administrate, true)
		if err != nil {
			return err
		}
		if d.NetworkId != "" {
			user, _, err := this.begin(token, namespace)
			if err != nil {
				return err
			}
			if err = this.checkNetwork(ctx, user, d.NetworkId, Administrate); err != nil {
				return err
			}
			if err = this.desireDeletedDeployment(tx, d); err != nil {
				return err
			}
		}
		return tx.DeleteDeployment(id)
	})
	if err == nil {
		this.wake()
	}
	return err
}

func (this *Controller) ListDeploymentVersions(ctx context.Context, token string, namespace string, id string, options model.PageOptions) (result model.Page[model.DeploymentVersion], err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		if _, err := this.readDeployment(ctx, tx, token, namespace, id, Read, false); err != nil {
			return err
		}
		result, err = tx.ListDeploymentVersions(id, options)
		return err
	})
	return result, err
}

func (this *Controller) GetDeploymentVersion(ctx context.Context, token string, namespace string, id string, version int) (result model.DeploymentVersion, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		if _, err := this.readDeployment(ctx, tx, token, namespace, id, Read, false); err != nil {
			return err
		}
		if result, err = tx.GetDeploymentVersion(id, version); err != nil {
			return err
		}
		return this.addResources(tx, &result)
	})
	return result, err
}

// GetDeploymentSource returns the model version a version of a deployment was created from, as
// it was saved, before the conversion for the engine. Reading the deployment is enough: whoever
// sees a release sees what it was made of, as with the copy smart-service-repository kept.
// ErrNotFound if the version names no model or the model version is gone.
func (this *Controller) GetDeploymentSource(ctx context.Context, token string, namespace string, id string, version int) (result model.ModelVersion, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		d, err := this.readDeployment(ctx, tx, token, namespace, id, Read, false)
		if err != nil {
			return err
		}
		v, err := tx.GetDeploymentVersion(id, version)
		if err != nil {
			return err
		}
		if d.ModelId == "" || v.ModelVersion == 0 {
			return model.NotFound("version %v of deployment %v names no model", version, id)
		}
		result, err = tx.GetModelVersion(d.ModelId, v.ModelVersion)
		return err
	})
	return result, err
}

func (this *Controller) GetStartParameters(ctx context.Context, token string, namespace string, deploymentId string) (result []model.StartParameter, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		d, err := this.readDeployment(ctx, tx, token, namespace, deploymentId, Read, false)
		if err != nil {
			return err
		}
		v, err := tx.GetDeploymentVersion(d.Id, d.Version)
		if err != nil {
			return err
		}
		process, _, err := bpmn.Parse(v.Xml)
		if err != nil {
			return err
		}
		bpmn.ApplyParameters(process, v.Parameters)
		result = bpmn.StartParameters(process.NoneStartEvent())
		return nil
	})
	return result, err
}

// RecomputeEvents derives the event descriptions of the current version again, e.g. after a
// device group changed (DEP-08); only this deployment's descriptions change.
func (this *Controller) RecomputeEvents(ctx context.Context, deploymentId string) error {
	if this.describer == nil {
		return nil
	}
	var deployment model.Deployment
	var version model.DeploymentVersion
	err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		if deployment, err = tx.GetDeployment(deploymentId, false); err != nil {
			return err
		}
		version, err = tx.GetDeploymentVersion(deploymentId, deployment.Version)
		return err
	})
	if err != nil {
		return ignoreNotFound(err)
	}
	process, _, err := bpmn.Parse(version.Xml)
	if err != nil {
		return err
	}
	bpmn.ApplyParameters(process, version.Parameters)
	describer := this.describer
	if deployment.NetworkId != "" {
		describer = describer.Local()
	}
	descriptions, groups, err := describer.Describe(deployment, version.Version, process, version.Selections)
	if err != nil {
		return err
	}
	return this.store.Tx(ctx, func(tx *store.Tx) error {
		current, err := tx.GetDeployment(deploymentId, true)
		if err != nil {
			return ignoreNotFound(err)
		}
		if current.Version != version.Version {
			// an update replaced the descriptions meanwhile
			return nil
		}
		if err = tx.ReplaceEventDescriptions(deploymentId, descriptions, groups, time.Now()); err != nil {
			return err
		}
		if current.NetworkId != "" && this.networks != nil {
			// a gateway gets changed descriptions with its deployment (SYNC-06)
			return this.desireDeployment(tx, current)
		}
		return nil
	})
}

// RecomputeDeviceGroup recomputes the deployments whose events depend on a device group.
func (this *Controller) RecomputeDeviceGroup(ctx context.Context, groupId string) error {
	var ids []string
	err := this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		ids, err = tx.DeploymentsOfDeviceGroup(groupId)
		return err
	})
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range ids {
		errs = append(errs, this.RecomputeEvents(ctx, id))
	}
	return errors.Join(errs...)
}

func ignoreNotFound(err error) error {
	if isNotFound(err) {
		return nil
	}
	return err
}

func (this *Controller) ListEventDescriptions(ctx context.Context, token string, namespace string, deploymentId string) (result []model.EventDescription, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		if _, err := this.readDeployment(ctx, tx, token, namespace, deploymentId, Read, false); err != nil {
			return err
		}
		result, err = tx.ListEventDescriptions(deploymentId)
		return err
	})
	return result, err
}

// SetDeploymentDisabled switches a deployment off or on (DEP-12). Off stops its running
// instances; while it is off, starts through the API are refused and schedules, timer and
// message starts are skipped. Updates keep the state. Deployments of gateways cannot be
// switched off, as with the engine wrapper.
func (this *Controller) SetDeploymentDisabled(ctx context.Context, token string, namespace string, id string, input model.DisabledInput) (result model.Deployment, err error) {
	var running []string
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		result, err = this.readDeployment(ctx, tx, token, namespace, id, Write, true)
		if err != nil {
			return err
		}
		if result.NetworkId != "" {
			return model.BadRequest("deployments of gateways cannot be switched off")
		}
		if err = tx.SetDeploymentDisabled(id, input.Disabled); err != nil {
			return err
		}
		result.Disabled = input.Disabled
		if !input.Disabled {
			return nil
		}
		instances, err := tx.ListInstances(namespace, model.InstanceListOptions{DeploymentId: id, State: "running", PageOptions: model.PageOptions{Limit: 100000}}, store.Access{Admin: true})
		for _, inst := range instances.Items {
			running = append(running, inst.Id)
		}
		return err
	})
	if err != nil {
		return result, err
	}
	var errs []error
	for _, instanceId := range running {
		if err := this.engine.StopInstance(ctx, instanceId, "deployment disabled"); err != nil && !errors.Is(err, model.ErrConflict) {
			errs = append(errs, err)
		}
	}
	return result, errors.Join(errs...)
}
