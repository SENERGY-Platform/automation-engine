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
	"regexp"
	"sort"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/migrate/rewrite"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
)

const smartServices = "smart-services"

// the topics of smart-service-repository in permissions-v2
const (
	releaseTopic  = "smart_service_releases"
	instanceTopic = "smart_service_instances"
)

// releaseRef is where a release went: the deployment of its design and the version
type releaseRef struct {
	deploymentId string
	version      int
}

func unix(seconds int64) time.Time {
	if seconds <= 0 {
		return time.Now().UTC()
	}
	return time.Unix(seconds, 0).UTC()
}

// designs takes the designs as models of the namespace smart-services, in the format of the
// old designer (MIG-01); designs had no rights of their own
func (this *migration) designs(ctx context.Context) error {
	const step = "smart-service-designs"
	designs, err := this.sources.Designs(ctx)
	if err != nil {
		return err
	}
	ns, _ := this.target.Namespaces.Get(smartServices)
	this.migratedDesigns = map[string]bool{}
	for _, d := range designs {
		owner := this.options.owner(d.UserId)
		if owner == "" {
			this.result(step, "design", d.Id, d.UserId, Manual, "no valid owner; map it with -owner")
			continue
		}
		this.migratedDesigns[d.Id] = true
		exists, err := this.target.modelExists(ctx, d.Id)
		switch {
		case err != nil:
			this.result(step, "design", d.Id, owner, Failed, "%v", err)
			continue
		case exists:
			this.result(step, "design", d.Id, owner, Existing, "")
			continue
		}
		if this.options.Apply {
			// the issues of a save; the conversion is reported with the releases
			var issues []model.Issue
			if checked, err := rewrite.Check(d.BpmnXml, this.target.Engine.Rules(ns)); err == nil {
				issues = append(checked.Issues, checked.Hints()...)
			}
			updated := unix(d.UpdatedAt)
			err = this.target.Store.Tx(ctx, func(tx *store.Tx) error {
				if err := tx.InsertModel(model.Model{Id: d.Id, Namespace: smartServices, Name: d.Name, Description: d.Description, Owner: owner, Version: 1,
					CreatedAt: updated, UpdatedAt: updated, Issues: issues}); err != nil {
					return err
				}
				return tx.InsertModelVersion(model.ModelVersion{ModelId: d.Id, Version: 1, Xml: d.BpmnXml, Svg: d.SvgXml, CreatedAt: updated, Issues: issues})
			})
			if err != nil {
				this.result(step, "design", d.Id, owner, Failed, "%v", err)
				continue
			}
		}
		this.result(step, "design", d.Id, owner, Migrated, "")
	}
	return nil
}

// releases takes the releases of a design as the versions of one deployment, oldest first;
// the deployment has the id of the newest release (SMS-03, decided for M7)
func (this *migration) releases(ctx context.Context) error {
	const step = "smart-service-releases"
	releases, err := this.sources.Releases(ctx)
	if err != nil {
		return err
	}
	chains := map[string][]Release{}
	var order []string
	for _, r := range releases {
		key := r.DesignId
		if key == "" {
			key = "release:" + r.Id
		}
		if _, ok := chains[key]; !ok {
			order = append(order, key)
		}
		chains[key] = append(chains[key], r)
	}
	this.releaseRefs = map[string]releaseRef{}
	for _, key := range order {
		chain := chains[key]
		sort.SliceStable(chain, func(a, b int) bool { return chain[a].CreatedAt < chain[b].CreatedAt })
		newest := chain[len(chain)-1]
		deploymentId := newest.Id
		owner := this.options.owner(newest.Creator)
		if owner == "" {
			for _, r := range chain {
				this.result(step, "release", r.Id, r.Creator, Manual, "the newest release of the design has no valid creator; map it with -owner")
			}
			continue
		}
		exists, err := this.target.deploymentExists(ctx, deploymentId)
		if err != nil {
			this.result(step, "release", deploymentId, owner, Failed, "%v", err)
			continue
		}
		for i, r := range chain {
			this.releaseRefs[r.Id] = releaseRef{deploymentId: deploymentId, version: i + 1}
		}
		if exists {
			for _, r := range chain {
				this.result(step, "release", r.Id, owner, Existing, "")
			}
			continue
		}
		for i, r := range chain {
			conversion, err := this.target.Controller.Convert(ctx, this.target.token(), smartServices, r.BpmnXml)
			if err == nil && len(conversion.Unsupported) > 0 {
				err = model.ValidationFailed(conversion.Unsupported)
			}
			if err != nil {
				this.result(step, "release", r.Id, owner, Manual, "%v; the later releases of the design are left out", describe(err))
				this.findings(step, r.Id, owner, conversion.Rewritten, conversion.Check, conversion.Unsupported)
				break
			}
			input := model.DeploymentInput{Name: r.Name, Description: r.Description, Xml: conversion.Xml, Svg: r.SvgXml, Owner: owner}
			if this.migratedDesigns[r.DesignId] {
				input.ModelId, input.ModelVersion = r.DesignId, 1
			}
			if i > 0 {
				// instances stay on their release until a parameter change moves them (SMS-09)
				input.UpdateStrategy = model.UpdateDrain
			}
			if !this.options.Apply {
				this.planned[deploymentId] = conversion.Xml
			} else {
				if _, err = this.target.Controller.PutDeployment(ctx, this.target.token(), smartServices, deploymentId, input); err != nil {
					this.result(step, "release", r.Id, owner, Failed, "%v; the later releases of the design are left out", describe(err))
					this.findings(step, r.Id, owner, conversion.Rewritten, conversion.Check, conversion.Unsupported)
					break
				}
				if err = this.copyRights(ctx, releaseTopic, r.Id, smartServices, model.KindDeployment, deploymentId, owner); err != nil {
					this.result(step, "release", r.Id, owner, Failed, "%v", err)
					this.findings(step, r.Id, owner, conversion.Rewritten, conversion.Check, conversion.Unsupported)
					continue
				}
			}
			this.result(step, "release", r.Id, owner, Migrated, "version %v of %v", i+1, deploymentId)
			this.findings(step, r.Id, owner, conversion.Rewritten, conversion.Check, conversion.Unsupported)
		}
	}
	return nil
}

// smartServiceInstances takes the instances: those whose setup ended keep their modules and
// are not run again, which would create every module once more; an instance in setup starts
// anew (MIG-02)
func (this *migration) smartServiceInstances(ctx context.Context) error {
	const step = "smart-service-instances"
	instances, err := this.sources.SmartServiceInstances(ctx)
	if err != nil {
		return err
	}
	modules, err := this.sources.Modules(ctx)
	if err != nil {
		return err
	}
	modulesOf := map[string][]Module{}
	for _, m := range modules {
		modulesOf[m.InstanceId] = append(modulesOf[m.InstanceId], m)
	}
	this.smartInstances = map[string]string{}
	this.insertedModules = map[string]bool{}
	for _, si := range instances {
		owner := this.options.owner(si.UserId)
		ref, ok := this.releaseRefs[si.ReleaseId]
		switch {
		case owner == "":
			this.result(step, "instance", si.Id, si.UserId, Manual, "no valid owner; map it with -owner")
			continue
		case !ok:
			this.result(step, "instance", si.Id, owner, Manual, "release %v is not migrated", si.ReleaseId)
			continue
		}
		this.smartInstances[si.Id] = owner
		exists, err := this.target.instanceExists(ctx, si.Id)
		switch {
		case err != nil:
			this.result(step, "instance", si.Id, owner, Failed, "%v", err)
			continue
		case exists:
			this.result(step, "instance", si.Id, owner, Existing, "")
			continue
		}
		variables := map[string]any{}
		for _, p := range si.Parameters {
			variables[p.Id] = p.Value
		}
		finished := si.Ready || si.Error != ""
		if this.options.Apply {
			if finished {
				err = this.insertFinished(ctx, si, ref, owner, variables, modulesOf[si.Id])
			} else {
				_, err = this.target.Controller.PutInstance(ctx, this.target.token(), smartServices, si.Id, model.StartInstanceInput{DeploymentId: ref.deploymentId,
					DeploymentVersion: ref.version, Variables: variables, Owner: owner, Name: si.Name, Description: si.Description})
			}
			if err == nil {
				err = this.copyRights(ctx, instanceTopic, si.Id, smartServices, model.KindInstance, si.Id, owner)
			}
			if err != nil {
				this.result(step, "instance", si.Id, owner, Failed, "%v", describe(err))
				continue
			}
		}
		if finished {
			this.result(step, "instance", si.Id, owner, Migrated, "")
		} else {
			this.result(step, "instance", si.Id, owner, Migrated, "its setup was running and starts anew; modules it had created already may exist twice")
		}
	}
	return nil
}

// insertFinished writes an instance whose run ended, with the error of the old service as the
// reason, so that its status shows it (SMS-11), together with its modules: an instance with
// modules is kept by the retention, one without would go at once. It ended at the migration,
// since the old service knew no end time.
func (this *migration) insertFinished(ctx context.Context, si SmartServiceInstance, ref releaseRef, owner string, variables map[string]any, modules []Module) error {
	return this.target.Store.Tx(ctx, func(tx *store.Tx) error {
		d, err := tx.GetDeployment(ref.deploymentId, false)
		if err != nil {
			return err
		}
		started, ended := unix(si.CreatedAt), time.Now().UTC()
		state := model.StateCompleted
		if si.Error != "" {
			state = model.StateTerminated
		}
		inst := store.Instance{Instance: model.Instance{Id: si.Id, Namespace: smartServices, Owner: owner, DeploymentId: d.Id, DeploymentName: d.Name,
			DeploymentVersion: ref.version, ModelId: d.ModelId, ModelVersion: d.ModelVersion, Run: 1, State: state, EndReason: si.Error, StartVariables: variables,
			CreatedAt: started, StartedAt: started, EndedAt: &ended, LastActivityAt: ended, Name: si.Name, Description: si.Description}, ActivityMinSeq: 1}
		if err = tx.InsertInstance(inst); err != nil {
			return err
		}
		if err = tx.InsertRun(model.Run{InstanceId: si.Id, Run: 1, DeploymentVersion: ref.version, Cause: model.CauseStart, State: state, EndReason: si.Error,
			StartedAt: started, EndedAt: &ended}); err != nil {
			return err
		}
		for _, m := range modules {
			if err = this.insertModule(tx, m, owner); err != nil {
				return err
			}
			this.insertedModules[m.Id] = true
		}
		return nil
	})
}

func (this *migration) insertModule(tx *store.Tx, m Module, owner string) error {
	updated := unix(m.LastUpdate)
	info, _ := this.deleteInfo(m)
	module := model.Module{Id: m.Id, Namespace: smartServices, InstanceId: m.InstanceId, CreatedBy: m.InstanceId, Owner: owner, ModuleType: m.Type,
		Data: m.Data, Keys: m.Keys, DeleteInfo: info, CreatedAt: updated, UpdatedAt: updated}
	if err := tx.UpsertModule(module); err != nil {
		return err
	}
	if m.Error == "" {
		return nil
	}
	return tx.SetModuleCheck(m.Id, m.Error, updated)
}

// delete info of the old workers that points at services the engine replaces
var (
	deploymentUrl = regexp.MustCompile(`/(?:v3/deployments|deployments/[^/]+)/([^/?]+)$`)
	// with the network for process-sync
	businessKeyUrl = regexp.MustCompile(`/process-instances-by-business-key/(?:([^/]+)/)?(?:wardened:)?([^/?]+)$`)
	watcherUrl     = regexp.MustCompile(`/watcher/[^/?]+$`)
)

// deleteInfo rewrites the delete info of a module onto resources of the engine where the old
// URL points at a service the engine replaces; other URLs stay (MIG-01). A business key names
// the instance that had it in this run; where that is ambiguous, the URL stays and the problem
// is returned.
func (this *migration) deleteInfo(m Module) (*model.ModuleDeleteInfo, string) {
	switch {
	case m.DeleteUrl == "":
		return nil, ""
	case businessKeyUrl.MatchString(m.DeleteUrl):
		match := businessKeyUrl.FindStringSubmatch(m.DeleteUrl)
		id, problem := this.instanceOfKey(match[1], match[2])
		if problem != "" {
			return &model.ModuleDeleteInfo{Url: m.DeleteUrl, UserId: m.DeleteUser}, "the delete info keeps its URL: " + problem
		}
		return &model.ModuleDeleteInfo{Resource: &model.ResourceRef{Kind: model.ResourceInstance, Namespace: processes, Id: id}}, ""
	case deploymentUrl.MatchString(m.DeleteUrl) && (m.Type == "process_deployment" || m.Data["process_deployment_id"] != nil):
		return &model.ModuleDeleteInfo{Resource: &model.ResourceRef{Kind: model.ResourceDeployment, Namespace: processes, Id: deploymentUrl.FindStringSubmatch(m.DeleteUrl)[1]}}, ""
	}
	return &model.ModuleDeleteInfo{Url: m.DeleteUrl, UserId: m.DeleteUser}, ""
}

// modules takes the modules of the migrated instances with their ids
func (this *migration) modules(ctx context.Context) error {
	const step = "smart-service-modules"
	modules, err := this.sources.Modules(ctx)
	if err != nil {
		return err
	}
	for _, m := range modules {
		owner, ok := this.smartInstances[m.InstanceId]
		if !ok {
			this.result(step, "module", m.Id, m.UserId, Skipped, "instance %v is not migrated", m.InstanceId)
			continue
		}
		exists, err := this.target.exists(ctx, func(tx *store.Tx) error { _, err := tx.GetModule(m.Id); return err })
		switch {
		case err != nil:
			this.result(step, "module", m.Id, owner, Failed, "%v", err)
			continue
		case exists && !this.insertedModules[m.Id]:
			this.result(step, "module", m.Id, owner, Existing, "")
			continue
		}
		if this.options.Apply && !this.insertedModules[m.Id] {
			err = this.target.Store.Tx(ctx, func(tx *store.Tx) error { return this.insertModule(tx, m, owner) })
			if err != nil {
				this.result(step, "module", m.Id, owner, Failed, "%v", err)
				continue
			}
		}
		if _, problem := this.deleteInfo(m); problem != "" {
			this.result(step, "module", m.Id, owner, Manual, "%v", problem)
			continue
		}
		if watcherUrl.MatchString(m.DeleteUrl) {
			this.result(step, "module", m.Id, owner, Manual, "the watch of the old watcher is not taken over; the module keeps its URL")
			continue
		}
		this.result(step, "module", m.Id, owner, Migrated, "")
	}
	return nil
}

// smartServiceVariables takes the variables of the repository as process variables of the
// instances (SMS-10)
func (this *migration) smartServiceVariables(ctx context.Context) error {
	const step = "smart-service-variables"
	variables, err := this.sources.SmartServiceVariables(ctx)
	if err != nil {
		return err
	}
	for _, v := range variables {
		id := v.InstanceId + "/" + v.Name
		owner, ok := this.smartInstances[v.InstanceId]
		if !ok {
			this.result(step, "variable", id, "", Skipped, "instance %v is not migrated", v.InstanceId)
			continue
		}
		var existing []model.Variable
		err := this.target.Store.Tx(ctx, func(tx *store.Tx) (err error) { existing, err = tx.GetVariables(v.InstanceId); return err })
		if err != nil {
			this.result(step, "variable", id, owner, Failed, "%v", err)
			continue
		}
		if containsVariable(existing, v.Name) {
			this.result(step, "variable", id, owner, Existing, "")
			continue
		}
		if this.options.Apply {
			if err = this.target.Store.Tx(ctx, func(tx *store.Tx) error { return tx.SetVariable(v.InstanceId, v.Name, v.Value, "", time.Now().UTC()) }); err != nil {
				this.result(step, "variable", id, owner, Failed, "%v", err)
				continue
			}
		}
		this.result(step, "variable", id, owner, Migrated, "")
	}
	return nil
}

func containsVariable(variables []model.Variable, name string) bool {
	for _, v := range variables {
		if v.Name == name {
			return true
		}
	}
	return false
}
