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

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/migrate/rewrite"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/prepare"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// PrepareDeployment returns what a deploy dialog needs: start parameters, validation findings
// and the selectable devices, groups and imports per device task and device event (DEP-01).
func (this *Controller) PrepareDeployment(ctx context.Context, token string, namespace string, input model.PrepareInput) (result model.PreparedDeployment, err error) {
	user, ns, err := this.begin(token, namespace)
	if err != nil {
		return result, err
	}
	if err = ns.CheckGateway(input.NetworkId); err != nil {
		return result, err
	}
	var source resolvedDeployment
	err = this.store.Tx(ctx, func(tx *store.Tx) (err error) {
		source, err = this.source(ctx, tx, user, ns, input.ModelId, input.ModelVersion, input.Xml, "")
		return err
	})
	if err != nil {
		return result, err
	}
	// options, criteria and issues are those of the model as it would be deployed (MIG-04)
	checked, err := rewrite.Check(source.xml, this.engine.Rules(ns))
	if err != nil {
		return result, model.ValidationFailed([]model.Issue{{Message: err.Error()}})
	}
	process := checked.Process
	result = model.PreparedDeployment{
		Name: firstNonEmpty(source.name, process.Name, process.Id), ModelId: source.modelId, ModelVersion: source.modelVersion,
		StartParameters: bpmn.StartParameters(process.NoneStartEvent()), Issues: checked.Issues, Conversion: conversionOf(checked.Report),
	}
	preparer := this.preparer
	if preparer == nil {
		// without device-selection the elements are listed without options
		preparer = prepare.New(nil, prepare.Config{})
	}
	if input.NetworkId == "" {
		result.Elements, err = preparer.Prepare(user.Jwt(), process)
		return result, err
	}
	if _, err = this.GetNetwork(ctx, token, input.NetworkId); err != nil {
		return result, err
	}
	result.Elements, err = preparer.PrepareForHub(user.Jwt(), process, input.NetworkId)
	return result, err
}
