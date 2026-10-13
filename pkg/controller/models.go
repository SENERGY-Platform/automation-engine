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
	"github.com/SENERGY-Platform/automation-engine/pkg/migrate/rewrite"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/google/uuid"
)

func (this *Controller) Validate(ctx context.Context, token string, namespace string, xml string) ([]model.Issue, error) {
	_, ns, err := this.begin(token, namespace)
	if err != nil {
		return nil, err
	}
	_, issues, err := bpmn.ParseAndValidate(xml, this.engine.Rules(ns))
	if err != nil {
		return nil, model.ValidationFailed([]model.Issue{{Message: err.Error()}})
	}
	if issues == nil {
		issues = []model.Issue{}
	}
	return issues, nil
}

// Convert rewrites a model written for Camunda 7 and validates the result against the rules of
// the namespace, without storing it (MIG-04).
func (this *Controller) Convert(ctx context.Context, token string, namespace string, xml string) (model.Conversion, error) {
	_, ns, err := this.begin(token, namespace)
	if err != nil {
		return model.Conversion{}, err
	}
	result, report, err := rewrite.Rewrite(xml)
	if err != nil {
		return model.Conversion{}, model.ValidationFailed([]model.Issue{{Message: err.Error()}})
	}
	conversion := model.Conversion{Xml: result, Rewritten: nonNil(report.Rewritten), Check: nonNil(report.Check), Unsupported: nonNil(report.Unsupported), Issues: []model.Issue{}}
	if _, issues, err := bpmn.ParseAndValidate(result, this.engine.Rules(ns)); err == nil && issues != nil {
		conversion.Issues = issues
	}
	return conversion, nil
}

func nonNil(issues []model.Issue) []model.Issue {
	if issues == nil {
		return []model.Issue{}
	}
	return issues
}

// modelIssues validates a model on save; issues are hints, the model is stored anyway (MOD-04)
func (this *Controller) modelIssues(ns model.Namespace, xml string) (processName string, issues []model.Issue, err error) {
	if strings.TrimSpace(xml) == "" {
		return "", nil, model.BadRequest("xml is required")
	}
	process, issues, err := bpmn.ParseAndValidate(xml, this.engine.Rules(ns))
	if err != nil {
		// unreadable XML is rejected even on save
		return "", nil, model.ValidationFailed([]model.Issue{{Message: err.Error()}})
	}
	if issues == nil {
		issues = []model.Issue{}
	}
	return firstNonEmpty(process.Name, process.Id), issues, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (this *Controller) CreateModel(ctx context.Context, token string, namespace string, input model.ModelInput) (result model.Model, err error) {
	user, ns, err := this.begin(token, namespace)
	if err != nil {
		return result, err
	}
	modelOwner, err := owner(user, input.Owner)
	if err != nil {
		return result, err
	}
	processName, issues, err := this.modelIssues(ns, input.Xml)
	if err != nil {
		return result, err
	}
	now := time.Now().UTC()
	result = model.Model{
		Id: uuid.NewString(), Namespace: ns.Name, Name: firstNonEmpty(input.Name, processName), Description: input.Description,
		Owner: modelOwner, Version: 1, CreatedAt: now, UpdatedAt: now, Issues: issues,
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		if err := tx.InsertModel(result); err != nil {
			return err
		}
		return tx.InsertModelVersion(model.ModelVersion{ModelId: result.Id, Version: 1, Xml: input.Xml, Svg: input.Svg, CreatedAt: now, Issues: issues})
	})
	return result, err
}

func (this *Controller) UpdateModel(ctx context.Context, token string, namespace string, id string, input model.ModelInput) (result model.Model, err error) {
	user, ns, err := this.begin(token, namespace)
	if err != nil {
		return result, err
	}
	processName, issues, err := this.modelIssues(ns, input.Xml)
	if err != nil {
		return result, err
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		result, err = tx.GetModel(id, true)
		if err != nil {
			return err
		}
		if err = inNamespace(result.Namespace, namespace, KindModel, id); err != nil {
			return err
		}
		if err = this.check(ctx, user, namespace, KindModel, id, result.Owner, Write); err != nil {
			return err
		}
		if input.Owner != "" && input.Owner != result.Owner {
			return model.BadRequest("the owner cannot be changed by an update")
		}
		now := time.Now().UTC()
		result.Version++
		result.Name = firstNonEmpty(input.Name, processName)
		result.Description = input.Description
		result.UpdatedAt = now
		result.Issues = issues
		if err = tx.UpdateModel(result); err != nil {
			return err
		}
		return tx.InsertModelVersion(model.ModelVersion{ModelId: id, Version: result.Version, Xml: input.Xml, Svg: input.Svg, CreatedAt: now, Issues: issues})
	})
	return result, err
}

// readModel loads a model with its latest issues after checking a right
func (this *Controller) readModel(ctx context.Context, tx *store.Tx, token string, namespace string, id string, right Right, lock bool) (model.Model, error) {
	user, _, err := this.begin(token, namespace)
	if err != nil {
		return model.Model{}, err
	}
	m, err := tx.GetModel(id, lock)
	if err != nil {
		return m, err
	}
	if err = inNamespace(m.Namespace, namespace, KindModel, id); err != nil {
		return m, err
	}
	if err = this.check(ctx, user, namespace, KindModel, id, m.Owner, right); err != nil {
		return m, err
	}
	latest, err := tx.GetModelVersion(id, m.Version)
	if err != nil {
		return m, err
	}
	m.Issues = latest.Issues
	return m, nil
}

func (this *Controller) GetModel(ctx context.Context, token string, namespace string, id string) (result model.Model, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		result, err = this.readModel(ctx, tx, token, namespace, id, Read, false)
		return err
	})
	return result, err
}

func (this *Controller) ListModels(ctx context.Context, token string, namespace string, options model.ModelListOptions) (result model.Page[model.Model], err error) {
	user, _, err := this.begin(token, namespace)
	if err != nil {
		return result, err
	}
	access, err := this.auth.Access(ctx, user, namespace, KindModel, Read)
	if err != nil {
		return result, err
	}
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		result, err = tx.ListModels(namespace, options, access)
		return err
	})
	return result, err
}

// DeleteModel deletes a model unless deployments of module tasks use it; force deletes it
// anyway and is for admins (SMS-15)
func (this *Controller) DeleteModel(ctx context.Context, token string, namespace string, id string, force bool) error {
	user, _, err := this.begin(token, namespace)
	if err != nil {
		return err
	}
	if force && !user.IsAdmin() {
		return model.Forbidden("only admins may force the delete of a model in use")
	}
	return this.store.Tx(ctx, func(tx *store.Tx) error {
		if _, err := this.readModel(ctx, tx, token, namespace, id, Administrate, true); err != nil {
			return err
		}
		if !force {
			if err := this.modelInUse(ctx, tx, user, id); err != nil {
				return err
			}
		}
		return tx.DeleteModel(id)
	})
}

func (this *Controller) PublishModel(ctx context.Context, token string, namespace string, id string, input model.PublishInput) (result model.Model, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		result, err = this.readModel(ctx, tx, token, namespace, id, Administrate, true)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		result.Published = input.Published
		result.PublishDescription = input.Description
		result.PublishedAt = nil
		if input.Published {
			result.PublishedAt = &now
		}
		return tx.UpdateModel(result)
	})
	return result, err
}

func (this *Controller) ListModelVersions(ctx context.Context, token string, namespace string, id string, options model.PageOptions) (result model.Page[model.ModelVersion], err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		if _, err := this.readModel(ctx, tx, token, namespace, id, Read, false); err != nil {
			return err
		}
		result, err = tx.ListModelVersions(id, options)
		return err
	})
	return result, err
}

func (this *Controller) GetModelVersion(ctx context.Context, token string, namespace string, id string, version int) (result model.ModelVersion, err error) {
	err = this.store.Tx(ctx, func(tx *store.Tx) error {
		if _, err := this.readModel(ctx, tx, token, namespace, id, Read, false); err != nil {
			return err
		}
		result, err = tx.GetModelVersion(id, version)
		return err
	})
	return result, err
}
