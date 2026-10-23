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
	"slices"
	"sync"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/prepare"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// SetParameters enables start parameters with options (SMS-08); userToken serves starts without
// a caller, e.g. by the watcher.
func (this *Controller) SetParameters(describer *prepare.Describer, userToken auth.UserTokenSource) {
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

// usableConcurrency bounds the deployments DeploymentsUsable describes at once; each asks
// device-selection and device-repository
const usableConcurrency = 16

// DeploymentsUsable reports per deployment whether every start parameter of its current
// version has an option for the caller, the usable flag of release lists (SMS-08). Deployments
// without the execute right, or unknown ones, are not usable. Unlike DeploymentParameters per
// deployment it checks the rights once and holds no database connection while it asks other
// services: a list of 46 releases one by one took longer than the mobile app waits.
func (this *Controller) DeploymentsUsable(ctx context.Context, token string, namespace string, ids []string) (map[string]bool, error) {
	user, _, err := this.begin(token, namespace)
	if err != nil {
		return nil, err
	}
	access, err := this.auth.Access(ctx, user, namespace, KindDeployment, Execute)
	if err != nil {
		return nil, err
	}
	result := map[string]bool{}
	starts := map[string]*bpmn.Element{}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		for _, id := range ids {
			result[id] = false
			d, err := tx.GetDeployment(id, false)
			if err != nil || d.Namespace != namespace {
				continue
			}
			if !access.Admin && d.Owner != access.Owner && !slices.Contains(access.Ids, d.Id) {
				continue
			}
			proc, err := this.engine.Process(tx, d.Id, d.Version)
			if err != nil {
				return err
			}
			starts[id] = proc.NoneStartEvent()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	shared := prepare.WithSelectablesCache(ctx)
	var mux sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, usableConcurrency)
	for id, start := range starts {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			parameters, err := this.describe(shared, token, start)
			usable := err == nil && !slices.ContainsFunc(parameters, func(p model.ParameterDescription) bool { return p.HasNoValidOption })
			mux.Lock()
			result[id] = usable
			mux.Unlock()
		}()
	}
	wg.Wait()
	return result, nil
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
		return prepare.NewDescriber("", nil).Describe(ctx, token, start)
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
