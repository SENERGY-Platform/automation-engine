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

package engine

import (
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

// applyEffects writes what a task changed besides its output, in the transaction that completes
// it: modules and shared variables at the root instance, messages for the instance itself
func (this *runner) applyEffects(job store.Job, effects Effects) error {
	root := *this.inst
	if this.inst.ParentId != "" {
		var err error
		// locked like the instance itself, so that its variables change serialized
		if root, err = this.tx.GetInstance(this.inst.ParentId, true); err != nil {
			return err
		}
	}
	for _, m := range effects.Modules {
		m.InstanceId, m.Namespace, m.Owner, m.CreatedBy, m.ElementId = root.Id, root.Namespace, root.Owner, this.inst.Id, job.ElementId
		if m.CreatedAt.IsZero() {
			m.CreatedAt = this.now
		}
		m.UpdatedAt = this.now
		if m.Data == nil {
			m.Data = map[string]any{}
		}
		if err := this.tx.UpsertModule(m); err != nil {
			return err
		}
	}
	element := this.proc.Element(job.ElementId)
	for _, name := range sortedKeys(effects.Variables) {
		value := effects.Variables[name]
		if root.Id == this.inst.Id {
			if err := this.setProcessVariable(element, name, value); err != nil {
				return err
			}
			continue
		}
		if _, err := this.checkSize(job.ElementId, name, value); err != nil {
			return err
		}
		if err := this.tx.SetVariable(root.Id, name, value, job.ElementId, this.now); err != nil {
			return err
		}
	}
	for _, name := range effects.Messages {
		if err := this.tx.BufferMessage(this.inst.Id, name, nil, this.now); err != nil {
			return err
		}
	}
	if len(effects.Modules) > 0 {
		return this.log(model.ActivityModulesChanged, element, map[string]any{"modules": moduleIds(effects.Modules)})
	}
	return nil
}

func moduleIds(modules []model.Module) []string {
	result := make([]string, 0, len(modules))
	for _, m := range modules {
		result = append(result, m.Id)
	}
	return result
}
