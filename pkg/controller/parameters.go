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
	"maps"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/parameters"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// SetParameters enables start parameters with options (SMS-08); userToken serves starts without
// a caller, e.g. by the watcher.
func (this *Controller) SetParameters(describer *parameters.Describer, userToken auth.UserTokenSource) {
	this.parameters = describer
	this.parametersUserToken = userToken
}

// DeploymentParameters describes the start parameters of a deployment with their options; it
// needs execute on the deployment, as before for releases (SMS-08).
func (this *Controller) DeploymentParameters(ctx context.Context, token string, namespace string, id string) ([]model.ParameterDescription, error) {
	var start *bpmn.Element
	err := this.store.Tx(ctx, func(tx *store.Tx) error {
		d, err := this.readDeployment(ctx, tx, token, namespace, id, Execute, false)
		if err != nil {
			return err
		}
		proc, err := this.engine.Process(tx, d.Id, d.Version)
		start = proc.NoneStartEvent()
		return err
	})
	if err != nil {
		return nil, err
	}
	return this.describe(ctx, token, start)
}

// MaintenanceParameters describes the parameters of a maintenance procedure (SMS-13).
func (this *Controller) MaintenanceParameters(ctx context.Context, token string, namespace string, id string, procedure string) ([]model.ParameterDescription, error) {
	var start *bpmn.Element
	err := this.store.Tx(ctx, func(tx *store.Tx) error {
		inst, err := this.readInstance(ctx, tx, token, namespace, id, Read)
		if err != nil {
			return err
		}
		proc, err := this.engine.Process(tx, inst.DeploymentId, inst.DeploymentVersion)
		if err != nil {
			return err
		}
		for _, p := range procedures(proc) {
			if p.Id == procedure {
				start = proc.Element(p.ElementId)
			}
		}
		if start == nil {
			return model.NotFound("instance %v has no maintenance procedure %v", id, procedure)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return this.describe(ctx, token, start)
}

func (this *Controller) describe(ctx context.Context, token string, start *bpmn.Element) ([]model.ParameterDescription, error) {
	if this.parameters == nil {
		return parameters.New("", nil).Describe(ctx, token, start)
	}
	return this.parameters.Describe(ctx, token, start)
}

// withAutoSelected adds the values of auto_select_all fields of a start event: every option
// the token may select; given values stay
func (this *Controller) withAutoSelected(ctx context.Context, token string, deploymentId string, version int, elementId string, variables map[string]any) (map[string]any, error) {
	if this.parameters == nil {
		return variables, nil
	}
	var start *bpmn.Element
	err := this.store.Tx(ctx, func(tx *store.Tx) error {
		if version == 0 {
			d, err := tx.GetDeployment(deploymentId, false)
			if err != nil {
				return err
			}
			version = d.Version
		}
		proc, err := this.engine.Process(tx, deploymentId, version)
		if err != nil {
			return err
		}
		if elementId == "" {
			start = proc.NoneStartEvent()
		} else {
			start = proc.Element(elementId)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	auto, err := this.parameters.AutoSelected(ctx, token, start)
	if err != nil || len(auto) == 0 {
		return variables, err
	}
	result := maps.Clone(auto)
	maps.Copy(result, variables)
	return result, nil
}
