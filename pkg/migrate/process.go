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

package migrate

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/deprecated"
	"github.com/SENERGY-Platform/automation-engine/pkg/migrate/rewrite"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/google/uuid"
)

const processes = "processes"

// the topic of process-model-repository in permissions-v2
const processModelTopic = "processmodel"

// instanceNamespace makes instance ids for instances without usable business key; the same
// Camunda instance gives the same id in every run
var instanceNamespace = uuid.MustParse("4b3f7c64-5a2e-4e47-9d8e-2c1f6a7b9e10")

var underscoredUuid = regexp.MustCompile(`^[0-9a-f]{8}_[0-9a-f]{4}_[0-9a-f]{4}_[0-9a-f]{4}_[0-9a-f]{12}`)

// vidOf reads the deployment id of process-deployment from a process definition key or id of
// Camunda (deplid_<vid with _>[_<n>][:version:id]); empty for other keys
func vidOf(key string) string {
	key, _, _ = strings.Cut(key, ":")
	rest, ok := strings.CutPrefix(key, "deplid_")
	if !ok {
		return ""
	}
	match := underscoredUuid.FindString(rest)
	return strings.ReplaceAll(match, "_", "-")
}

func dateOf(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.Format(time.DateOnly)
}

func contentKey(owner string, xml string) string {
	return owner + "\x00" + strings.TrimSpace(xml)
}

func parseTime(millis int64) time.Time {
	if millis <= 0 {
		return time.Now().UTC()
	}
	if millis < 1e11 {
		// seconds
		return time.Unix(millis, 0).UTC()
	}
	return time.UnixMilli(millis).UTC()
}

// processModels takes the models of process-model-repository with their ids into the namespace
// processes, as version 1 with the BPMN as it is; the designer keeps working on them (MIG-01)
func (this *migration) processModels(ctx context.Context) error {
	const step = "process-models"
	models, err := this.sources.ProcessModels(ctx)
	if err != nil {
		return err
	}
	ns, _ := this.target.Namespaces.Get(processes)
	this.modelsByContent = map[string]string{}
	for _, pm := range models {
		owner := this.options.owner(pm.Owner)
		if owner == "" {
			this.result(step, "model", pm.Id, pm.Owner, Manual, "no valid owner; map it with -owner")
			continue
		}
		if _, ok := this.modelsByContent[contentKey(owner, pm.BpmnXml)]; !ok {
			this.modelsByContent[contentKey(owner, pm.BpmnXml)] = pm.Id
		}
		exists, err := this.target.modelExists(ctx, pm.Id)
		if err != nil {
			this.result(step, "model", pm.Id, owner, Failed, "%v", err)
			continue
		}
		if exists {
			this.result(step, "model", pm.Id, owner, Existing, "")
			continue
		}
		if this.options.Apply {
			_, issues, _ := bpmn.ParseAndValidate(pm.BpmnXml, this.target.Engine.Rules(ns))
			created, updated := parseTime(pm.Date), parseTime(pm.LastUpdatedUnix)
			m := model.Model{Id: pm.Id, Namespace: processes, Name: pm.Name, Description: pm.Description, Owner: owner, Version: 1,
				Published: pm.Publish, CreatedAt: created, UpdatedAt: updated, Issues: issues}
			if pm.Publish {
				m.PublishDescription = pm.Description
				m.PublishedAt = &updated
			}
			err = this.target.Store.Tx(ctx, func(tx *store.Tx) error {
				if err := tx.InsertModel(m); err != nil {
					return err
				}
				return tx.InsertModelVersion(model.ModelVersion{ModelId: pm.Id, Version: 1, Xml: pm.BpmnXml, Svg: pm.SvgXml, CreatedAt: updated, Issues: issues})
			})
			if err == nil {
				err = this.copyRights(ctx, processModelTopic, processes, model.KindModel, pm.Id, owner)
			}
			if err != nil {
				this.result(step, "model", pm.Id, owner, Failed, "%v", err)
				continue
			}
		}
		this.result(step, "model", pm.Id, owner, Migrated, "")
	}
	return nil
}

// processDeployments takes the deployments of process-deployment with their ids; the BPMN is
// rewritten, the model is found by owner and identical BPMN where it is unique (MIG-01)
func (this *migration) processDeployments(ctx context.Context) error {
	const step = "process-deployments"
	deployments, err := this.sources.ProcessDeployments(ctx)
	if err != nil {
		return err
	}
	this.deployments = map[string]ProcessDeployment{}
	for _, pd := range deployments {
		this.deployments[pd.Id] = pd
		owner := this.options.owner(pd.Owner)
		if owner == "" {
			this.result(step, "deployment", pd.Id, pd.Owner, Manual, "no valid owner; map it with -owner")
			continue
		}
		exists, err := this.target.deploymentExists(ctx, pd.Id)
		if err != nil {
			this.result(step, "deployment", pd.Id, owner, Failed, "%v", err)
			continue
		}
		if exists {
			this.result(step, "deployment", pd.Id, owner, Existing, "")
			continue
		}
		input, err := deprecated.DeploymentInput(ctx, this.target.Controller, this.target.token(), processes, pd.Deployment, pd.Source)
		if err != nil {
			this.result(step, "deployment", pd.Id, owner, Manual, "%v", describe(err))
			continue
		}
		input.Owner = owner
		if modelId, ok := this.modelsByContent[contentKey(owner, pd.Deployment.Diagram.XmlRaw)]; ok {
			input.ModelId, input.ModelVersion = modelId, 1
		}
		if !this.options.Apply {
			issues, err := this.target.Controller.Validate(ctx, this.target.token(), processes, input.Xml)
			if err == nil && len(issues) > 0 {
				err = model.ValidationFailed(issues)
			}
			if err != nil {
				this.result(step, "deployment", pd.Id, owner, Manual, "%v", describe(err))
				continue
			}
			this.planned[pd.Id] = input.Xml
		} else if _, err = this.target.Controller.PutDeployment(ctx, this.target.token(), processes, pd.Id, input); err != nil {
			this.result(step, "deployment", pd.Id, owner, Failed, "%v", describe(err))
			continue
		}
		this.result(step, "deployment", pd.Id, owner, Migrated, "model %v", input.ModelId)
	}
	return nil
}

// describe names the issues of a validation error
func describe(err error) string {
	e := model.AsError(err)
	if len(e.Issues) == 0 {
		return err.Error()
	}
	var parts []string
	for _, issue := range e.Issues {
		parts = append(parts, issue.String())
	}
	return strings.Join(parts, "; ")
}

// camundaDeployments lists deployments that exist in Camunda without process-deployment, e.g.
// older ones and those of canaries, for a decision; those named in the options are migrated
// with the BPMN from Camunda (MIG-03)
func (this *migration) camundaDeployments(ctx context.Context) error {
	const step = "camunda-deployments"
	deployments, err := this.sources.CamundaDeployments(ctx)
	if err != nil {
		return err
	}
	this.camunda = deployments
	for _, cd := range deployments {
		vid := vidOf(cd.Key)
		if _, known := this.deployments[vid]; known && vid != "" {
			continue
		}
		id := firstNonEmpty(vid, cd.Id)
		if !this.options.CamundaOnly[cd.Id] && !this.options.CamundaOnly[id] {
			this.result(step, "deployment", cd.Id, cd.TenantId, Manual, "%q (%v, deployed %v) exists only in Camunda; name it with -camunda-only to migrate it",
				cd.Name, cd.Engine, dateOf(cd.Time))
			continue
		}
		owner := this.options.owner(cd.TenantId)
		if owner == "" {
			this.result(step, "deployment", cd.Id, cd.TenantId, Manual, "no valid owner; map it with -owner")
			continue
		}
		exists, err := this.target.deploymentExists(ctx, id)
		if err != nil {
			this.result(step, "deployment", cd.Id, owner, Failed, "%v", err)
			continue
		}
		if exists {
			this.result(step, "deployment", cd.Id, owner, Existing, "")
			continue
		}
		xml, report, err := rewrite.Rewrite(cd.Bpmn)
		if err == nil && !report.Complete() {
			err = model.ValidationFailed(report.Unsupported)
		}
		if err != nil {
			this.result(step, "deployment", cd.Id, owner, Manual, "%v", describe(err))
			continue
		}
		if !this.options.Apply {
			this.planned[id] = xml
		} else {
			if _, err = this.target.Controller.PutDeployment(ctx, this.target.token(), processes, id, model.DeploymentInput{Name: cd.Name, Xml: xml, Source: cd.Source, Owner: owner}); err != nil {
				this.result(step, "deployment", cd.Id, owner, Failed, "%v", describe(err))
				continue
			}
		}
		this.result(step, "deployment", cd.Id, owner, Migrated, "as %v", id)
	}
	return nil
}

// known reports a deployment in the engine, or one the dry run would create
func (this *migration) known(ctx context.Context, deploymentId string) bool {
	if _, planned := this.planned[deploymentId]; planned {
		return true
	}
	exists, err := this.target.deploymentExists(ctx, deploymentId)
	return err == nil && exists
}

// startParameters reads the start form of a deployment, of a planned one from its BPMN
func (this *migration) startParameters(ctx context.Context, deploymentId string) ([]model.StartParameter, error) {
	if xml, planned := this.planned[deploymentId]; planned {
		process, _, err := bpmn.Parse(xml)
		if err != nil {
			return nil, err
		}
		return bpmn.StartParameters(process.NoneStartEvent()), nil
	}
	return this.target.Controller.GetStartParameters(ctx, this.target.token(), processes, deploymentId)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// runningInstances starts the instances that run in Camunda anew, with the current values of
// the variables named like the start form fields; the business key is the instance id where
// it is unique (MIG-02)
func (this *migration) runningInstances(ctx context.Context) error {
	const step = "running-instances"
	for _, cd := range this.camunda {
		deploymentId := firstNonEmpty(vidOf(cd.Key), cd.Id)
		if !this.known(ctx, deploymentId) {
			continue
		}
		instances, err := this.sources.RunningInstances(ctx, cd)
		if err != nil {
			this.result(step, "instance", cd.Id, cd.TenantId, Failed, "unable to read the instances: %v", err)
			continue
		}
		parameters, err := this.startParameters(ctx, deploymentId)
		if err != nil {
			this.result(step, "instance", cd.Id, cd.TenantId, Failed, "unable to read the start parameters: %v", err)
			continue
		}
		keys := map[string]int{}
		for _, ci := range instances {
			keys[strings.TrimPrefix(ci.BusinessKey, "wardened:")]++
		}
		for _, ci := range instances {
			id := strings.TrimPrefix(ci.BusinessKey, "wardened:")
			if id == "" || keys[id] > 1 || len(id) > 64 || strings.ContainsAny(id, "/?# ") {
				id = uuid.NewSHA1(instanceNamespace, []byte(cd.Engine+"/"+ci.Id)).String()
			}
			this.instanceIds[ci.Id] = id
			variables := map[string]any{}
			var names []string
			for _, p := range parameters {
				if value, ok := ci.Variables[p.Id]; ok {
					variables[p.Id] = value
					names = append(names, p.Id)
				}
			}
			sort.Strings(names)
			owner := this.options.owner(ci.TenantId)
			if owner == "" {
				this.result(step, "instance", id, ci.TenantId, Manual, "no valid owner; map it with -owner")
				continue
			}
			if this.options.NoRestart {
				this.result(step, "instance", id, owner, Manual, "runs in Camunda as %v; not restarted", ci.Id)
				continue
			}
			exists, err := this.target.instanceExists(ctx, id)
			if err != nil {
				this.result(step, "instance", id, owner, Failed, "%v", err)
				continue
			}
			if exists {
				this.result(step, "instance", id, owner, Existing, "")
				continue
			}
			if this.options.Apply {
				_, err = this.target.Controller.PutInstance(ctx, this.target.token(), processes, id, model.StartInstanceInput{DeploymentId: deploymentId, Variables: variables, Owner: owner})
				if err != nil {
					this.result(step, "instance", id, owner, Failed, "%v", describe(err))
					continue
				}
			}
			message := fmt.Sprintf("restarted from Camunda instance %v", ci.Id)
			if len(names) > 0 {
				message += "; start values are the current values of " + strings.Join(names, ", ") + ", check them if the process changes them"
			}
			this.result(step, "instance", id, owner, Migrated, "%v", message)
		}
	}
	return nil
}

func (this *migration) schedules(ctx context.Context) error {
	const step = "schedules"
	schedules, err := this.sources.Schedules(ctx)
	if err != nil {
		return err
	}
	for _, s := range schedules {
		owner := this.options.owner(s.User)
		exists, err := this.target.exists(ctx, func(tx *store.Tx) error { _, err := tx.GetSchedule(s.Id); return err })
		switch {
		case err != nil:
			this.result(step, "schedule", s.Id, owner, Failed, "%v", err)
			continue
		case exists:
			this.result(step, "schedule", s.Id, owner, Existing, "")
			continue
		case owner == "":
			this.result(step, "schedule", s.Id, s.User, Manual, "no valid owner; map it with -owner")
			continue
		}
		if !this.known(ctx, s.DeploymentId) {
			this.result(step, "schedule", s.Id, owner, Skipped, "deployment %v is not migrated", s.DeploymentId)
			continue
		}
		now := time.Now().UTC()
		schedule := model.Schedule{Id: s.Id, Namespace: processes, DeploymentId: s.DeploymentId, Owner: owner, Cron: s.Cron, Alias: s.Alias,
			Disabled: s.Disabled, CreatedBy: s.CreatedBy, CreatedAt: now, UpdatedAt: now}
		next, err := this.target.Engine.NextScheduleDue(schedule, now)
		if err != nil {
			this.result(step, "schedule", s.Id, owner, Manual, "%v", err)
			continue
		}
		if !s.Disabled {
			schedule.NextDue = next
		}
		if this.options.Apply {
			if err = this.target.Store.Tx(ctx, func(tx *store.Tx) error { return tx.SaveSchedule(schedule) }); err != nil {
				this.result(step, "schedule", s.Id, owner, Failed, "%v", err)
				continue
			}
		}
		this.result(step, "schedule", s.Id, owner, Migrated, "")
	}
	return nil
}

// ioValues takes the values of process-io; ids of Camunda in keys and bindings become those of
// the engine, values of instances that ended in Camunda are left out (IO-03)
func (this *migration) ioValues(ctx context.Context) error {
	const step = "process-io"
	values, err := this.sources.IoValues(ctx)
	if err != nil {
		return err
	}
	for _, v := range values {
		id := v.UserId + "/" + v.Key
		key := v.Key
		value := model.IoValue{Value: v.Value, UpdatedAt: time.Unix(v.Timestamp, 0).UTC()}
		if v.InstanceId != "" {
			instanceId, ok := this.instanceIds[v.InstanceId]
			if !ok {
				this.result(step, "value", id, v.UserId, Skipped, "the instance ended in Camunda")
				continue
			}
			value.InstanceId = instanceId
			key = strings.ReplaceAll(key, v.InstanceId, instanceId)
		}
		if v.DefinitionId != "" {
			deploymentId := vidOf(v.DefinitionId)
			if deploymentId == "" || !this.known(ctx, deploymentId) {
				this.result(step, "value", id, v.UserId, Skipped, "the deployment is not migrated")
				continue
			}
			value.DeploymentId = deploymentId
			key = strings.ReplaceAll(key, v.DefinitionId, deploymentId)
		}
		value.Key = key
		exists := false
		err := this.target.Store.Tx(ctx, func(tx *store.Tx) (err error) {
			_, exists, err = tx.GetIoValue(v.UserId, key)
			return err
		})
		switch {
		case err != nil:
			this.result(step, "value", id, v.UserId, Failed, "%v", err)
			continue
		case exists:
			this.result(step, "value", id, v.UserId, Existing, "")
			continue
		}
		if this.options.Apply {
			if err = this.target.Store.Tx(ctx, func(tx *store.Tx) error { return tx.SetIoValue(v.UserId, value) }); err != nil {
				this.result(step, "value", id, v.UserId, Failed, "%v", err)
				continue
			}
		}
		message := ""
		if key != v.Key {
			message = "key " + key
		}
		this.result(step, "value", id, v.UserId, Migrated, "%v", message)
	}
	return nil
}
