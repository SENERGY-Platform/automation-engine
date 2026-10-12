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
	"reflect"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
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
}

// resolve takes the BPMN from a model version or from the input and validates it; a model
// that does not pass is rejected naming the elements (BPMN-18)
func (this *Controller) resolve(ctx context.Context, tx *store.Tx, user jwt.Token, ns model.Namespace, input model.DeploymentInput) (result resolvedDeployment, err error) {
	result, err = this.source(ctx, tx, user, ns, input.ModelId, input.ModelVersion, input.Xml, input.Svg)
	if err != nil {
		return result, err
	}
	process, issues, err := bpmn.ParseAndValidate(result.xml, this.engine.Rules(ns))
	if err != nil {
		return result, model.ValidationFailed([]model.Issue{{Message: err.Error()}})
	}
	issues = append(issues, prepare.ValidateSelections(process, input.Selections)...)
	if len(issues) > 0 {
		return result, model.ValidationFailed(issues)
	}
	result.name = firstNonEmpty(input.Name, result.name, process.Name, process.Id)
	return result, nil
}

// source reads the BPMN of a model version or takes the given BPMN
func (this *Controller) source(ctx context.Context, tx *store.Tx, user jwt.Token, ns model.Namespace, modelId string, modelVersion int, xml string, svg string) (result resolvedDeployment, err error) {
	input := model.DeploymentInput{ModelId: modelId, ModelVersion: modelVersion, Xml: xml, Svg: svg}
	switch {
	case input.ModelId != "" && input.Xml != "":
		return result, model.BadRequest("either model_id or xml, not both")
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
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
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
	plan, err := this.planSelections(ctx, user, ns, input, model.Deployment{Id: id, Namespace: ns.Name, Owner: deploymentOwner}, 1)
	if err != nil {
		return result, err
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		resolved, err := this.resolve(ctx, tx, user, ns, input)
		if err != nil {
			return err
		}
		if err = this.checkRetention(tx, namespace, deploymentOwner, input.RetentionDays); err != nil {
			return err
		}
		now := time.Now().UTC()
		result = model.Deployment{
			Id: id, Namespace: ns.Name, Name: resolved.name, Description: input.Description, Owner: deploymentOwner,
			ModelId: resolved.modelId, ModelVersion: resolved.modelVersion, Version: 1, Source: input.Source, RetentionDays: input.RetentionDays,
			CreatedAt: now, UpdatedAt: now,
		}
		if err = tx.InsertDeployment(result); err != nil {
			return err
		}
		version := model.DeploymentVersion{
			DeploymentId: id, Version: 1, ModelVersion: resolved.modelVersion, Xml: resolved.xml, Svg: resolved.svg,
			IncidentHandling: incidentHandling(ns, input.IncidentHandling), Selections: input.Selections, CreatedAt: now,
		}
		if err = tx.InsertDeploymentVersion(version); err != nil {
			return err
		}
		if len(plan.descriptions) > 0 || len(plan.groups) > 0 {
			if err = tx.ReplaceEventDescriptions(id, plan.descriptions, plan.groups, now); err != nil {
				return err
			}
		}
		// deployment, version, event descriptions and start events are written in one transaction (DEP-03)
		return this.engine.ActivateVersion(tx, id, 1)
	})
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
	var plan eventPlan
	if err == nil && existing.Namespace == namespace {
		if plan, err = this.planSelections(ctx, user, ns, input, existing, existing.Version+1); err != nil {
			return result, err
		}
	}
	created := false
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
		if err = this.checkRetention(tx, namespace, result.Owner, input.RetentionDays); err != nil {
			return err
		}
		current, err := tx.GetDeploymentVersion(id, result.Version)
		if err != nil {
			return err
		}
		handling := incidentHandling(ns, input.IncidentHandling)
		if current.Xml == resolved.xml && current.Svg == resolved.svg && result.Name == resolved.name && result.Description == input.Description &&
			result.ModelVersion == resolved.modelVersion && current.IncidentHandling == handling && result.RetentionDays == input.RetentionDays &&
			sameSelections(current.Selections, input.Selections) {
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
			UpdateStrategy: input.UpdateStrategy, IncidentHandling: handling, Selections: input.Selections, CreatedAt: now,
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
		if err = this.engine.ActivateVersion(tx, id, result.Version); err != nil {
			return err
		}
		return this.engine.ApplyUpdate(tx, id, input.UpdateStrategy)
	})
	if err == nil && created {
		return this.createDeployment(ctx, token, namespace, id, input)
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
	return this.store.Tx(ctx, func(tx *store.Tx) error {
		if _, err := this.readDeployment(ctx, tx, token, namespace, id, Administrate, true); err != nil {
			return err
		}
		return tx.DeleteDeployment(id)
	})
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
		result, err = tx.GetDeploymentVersion(id, version)
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
	descriptions, groups, err := this.describer.Describe(deployment, version.Version, process, version.Selections)
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
		return tx.ReplaceEventDescriptions(deploymentId, descriptions, groups, time.Now())
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
