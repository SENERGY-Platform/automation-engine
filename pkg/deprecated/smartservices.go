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

package deprecated

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/service-commons/pkg/jwt"
)

// The routes of smart-service-repository (Kong /smart-services/repository) that the web-ui
// uses, in the namespace smart-services. A design is a model. The releases of a design are the
// versions of one deployment (SMS-03): the current version has the id of the deployment, an
// older one <id>:v<version>, so that the mapping needs no table; new_release_id points from an
// older version to the current one, and an instance moves there by a change of its parameters
// (SMS-09). The routes the module workers used are gone with the workers.

const smartServices = "smart-services"

type SmartServiceDesign struct {
	Id          string `json:"id"`
	UserId      string `json:"user_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	BpmnXml     string `json:"bpmn_xml"`
	SvgXml      string `json:"svg_xml"`
	UpdatedAt   int64  `json:"updated_at"`
}

type SmartServiceRelease struct {
	Id               string                  `json:"id"`
	DesignId         string                  `json:"design_id"`
	Name             string                  `json:"name"`
	Description      string                  `json:"description"`
	CreatedAt        int64                   `json:"created_at"`
	NewReleaseId     string                  `json:"new_release_id,omitempty"`
	Creator          string                  `json:"creator"`
	Usable           *bool                   `json:"usable,omitempty"`
	ChangedResources []model.ChangedResource `json:"changed_resources,omitempty"`
}

type ResourceUsage struct {
	Releases  int                    `json:"releases"`
	Instances int                    `json:"instances"`
	Readable  []ResourceUsageRelease `json:"readable"`
}

type ResourceUsageRelease struct {
	Id       string `json:"id"`
	DesignId string `json:"design_id"`
	Name     string `json:"name"`
}

type PermissionsInfo struct {
	Shared      bool            `json:"shared"`
	Permissions map[string]bool `json:"permissions"`
}

type SmartServiceReleaseExtended struct {
	SmartServiceRelease
	BpmnXml         string          `json:"bpmn_xml"`
	SvgXml          string          `json:"svg_xml"`
	ParsedInfo      ReleaseInfo     `json:"parsed_info"`
	PermissionsInfo PermissionsInfo `json:"permissions_info"`
}

type ReleaseInfo struct {
	ParameterDescriptions []ReleaseParameter     `json:"parameter_descriptions"`
	MaintenanceProcedures []MaintenanceProcedure `json:"maintenance_procedures"`
	ModuleInfo            ModuleInfo             `json:"module_info"`
}

type ReleaseParameter struct {
	Id               string         `json:"id"`
	Label            string         `json:"label"`
	Description      string         `json:"description"`
	Type             string         `json:"type"`
	DefaultValue     any            `json:"default_value"`
	Multiple         bool           `json:"multiple"`
	AutoSelectAll    bool           `json:"auto_select_all"`
	Options          map[string]any `json:"options,omitempty"`
	Order            int            `json:"order"`
	CharacteristicId *string        `json:"characteristic_id,omitempty"`
	Optional         bool           `json:"optional"`
}

type MaintenanceProcedure struct {
	BpmnId                string             `json:"bpmn_id"`
	MessageRef            string             `json:"message_ref"`
	PublicEventId         string             `json:"public_event_id"`
	InternalEventId       string             `json:"internal_event_id"`
	ParameterDescriptions []ReleaseParameter `json:"parameter_descriptions"`
}

type ModuleInfo struct {
	Analytics []AnalyticsInfo `json:"analytics"`
}

type AnalyticsInfo struct {
	FlowId string `json:"flow_id"`
	Name   string `json:"name"`
	Desc   string `json:"desc"`
}

type SmartServiceParameter struct {
	Id         string `json:"id"`
	Value      any    `json:"value"`
	Label      string `json:"label"`
	ValueLabel string `json:"value_label,omitempty"`
}

type SmartServiceExtendedParameter struct {
	SmartServiceParameter
	Description      string                  `json:"description"`
	DefaultValue     any                     `json:"default_value"`
	Type             string                  `json:"type"`
	Options          []model.ParameterOption `json:"options"`
	Multiple         bool                    `json:"multiple"`
	Order            int                     `json:"order"`
	CharacteristicId *string                 `json:"characteristic_id,omitempty"`
	Characteristic   any                     `json:"characteristic,omitempty"`
	Optional         bool                    `json:"optional"`
	HasNoValidOption bool                    `json:"has_no_valid_option"`
}

type SmartServiceInstanceInit struct {
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Parameters  []SmartServiceParameter `json:"parameters"`
}

type SmartServiceInstanceInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type SmartServiceInstance struct {
	SmartServiceInstanceInit
	PermissionsInfo       PermissionsInfo `json:"permissions_info"`
	Id                    string          `json:"id"`
	UserId                string          `json:"user_id"`
	DesignId              string          `json:"design_id"`
	ReleaseId             string          `json:"release_id"`
	NewReleaseId          string          `json:"new_release_id,omitempty"`
	RunningMaintenanceIds []string        `json:"running_maintenance_ids,omitempty"`
	Ready                 bool            `json:"ready"`
	Deleting              bool            `json:"deleting,omitempty"`
	Error                 string          `json:"error,omitempty"`
	CreatedAt             int64           `json:"created_at"`
	UpdatedAt             int64           `json:"updated_at"`
}

type ModuleDeleteInfo struct {
	Url    string `json:"url"`
	UserId string `json:"user_id"`
}

type SmartServiceModule struct {
	Id         string            `json:"id"`
	UserId     string            `json:"user_id"`
	InstanceId string            `json:"instance_id"`
	DesignId   string            `json:"design_id"`
	ReleaseId  string            `json:"release_id"`
	LastUpdate int64             `json:"last_update"`
	Error      string            `json:"error,omitempty"`
	DeleteInfo *ModuleDeleteInfo `json:"delete_info"`
	ModuleType string            `json:"module_type"`
	ModuleData map[string]any    `json:"module_data"`
	Keys       []string          `json:"keys"`
}

func (this *Handler) smartServices() {
	this.handle("GET /smart-services/designs", this.listDesigns)
	this.handle("GET /smart-services/designs/{id}", this.getDesign)
	this.handle("POST /smart-services/designs", this.createDesign)
	this.handle("PUT /smart-services/designs/{id}", this.updateDesign)
	this.handle("DELETE /smart-services/designs/{id}", this.deleteDesign)
	this.handle("POST /smart-services/releases", this.createRelease)
	this.handle("DELETE /smart-services/releases/{id}", this.deleteRelease)
	this.handle("GET /smart-services/releases/{id}", this.getRelease)
	this.handle("GET /smart-services/releases", this.listReleases)
	this.handle("GET /smart-services/extended-releases/{id}", this.getExtendedRelease)
	this.handle("GET /smart-services/extended-releases", this.listExtendedReleases)
	this.handle("GET /smart-services/releases/{id}/parameters", this.releaseParameters)
	this.handle("POST /smart-services/releases/{id}/instances", this.createSmartServiceInstance)
	this.handle("GET /smart-services/instances", this.listSmartServiceInstances)
	this.handle("GET /smart-services/instances/{id}", this.getSmartServiceInstance)
	this.handle("DELETE /smart-services/instances/{id}", this.deleteSmartServiceInstance)
	this.handle("PUT /smart-services/instances/{id}/info", this.updateSmartServiceInstanceInfo)
	this.handle("PUT /smart-services/instances/{id}/parameters", this.updateSmartServiceInstanceParameters)
	this.handle("GET /smart-services/modules", this.listSmartServiceModules)
	this.handle("GET /smart-services/releases/{id}/instance-count", this.releaseInstanceCount)
	this.handle("GET /smart-services/resource-usage/{kind}/{id}", this.resourceUsage)
}

// userOf reads the user of a token for display; every call checks the token in the engine
func userOf(r *http.Request) string {
	parsed, err := jwt.Parse(token(r))
	if err != nil {
		return ""
	}
	return parsed.GetUserId()
}

// permissions describes the rights of the user as far as the layer knows them: all for the
// owner, reading otherwise
func permissions(r *http.Request, owner string, keys [4]string) PermissionsInfo {
	own := userOf(r) == owner
	result := PermissionsInfo{Shared: !own, Permissions: map[string]bool{}}
	for i, key := range keys {
		result.Permissions[key] = own || i == 0
	}
	return result
}

var releaseRights = [4]string{"r", "w", "x", "a"}
var instanceRights = [4]string{"read", "write", "execute", "administrate"}

func releaseId(deploymentId string, version int, current int) string {
	if version == 0 || version == current {
		return deploymentId
	}
	return deploymentId + ":v" + strconv.Itoa(version)
}

// parseRelease reads deployment id and version of a release id; version 0 is the current one
func parseRelease(id string) (string, int) {
	deploymentId, suffix, found := strings.Cut(id, ":v")
	if !found {
		return id, 0
	}
	version, err := strconv.Atoi(suffix)
	if err != nil {
		return id, 0
	}
	return deploymentId, version
}

func design(m model.Model, v model.ModelVersion) SmartServiceDesign {
	return SmartServiceDesign{Id: m.Id, UserId: m.Owner, Name: m.Name, Description: m.Description, BpmnXml: v.Xml, SvgXml: v.Svg, UpdatedAt: m.UpdatedAt.Unix()}
}

// listDesigns godoc
//
//	@Summary		designs of smart-service-repository
//	@Tags		deprecated-smart-services
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		limit	query	int	false	"page size"
//	@Param		offset	query	int	false	"offset"
//	@Param		sort	query	string	false	"name or updated_at with .asc or .desc"
//	@Param		search	query	string	false	"part of the name"
//	@Success	200	{array}		SmartServiceDesign
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/smart-services/designs [get]
func (this *Handler) listDesigns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := intParam(r, "limit", 0)
	if err != nil {
		writeError(w, err)
		return
	}
	offset, err := intParam(r, "offset", 0)
	if err != nil {
		writeError(w, err)
		return
	}
	options := model.ModelListOptions{Search: q.Get("search"), Sort: sort(q.Get("sort"), map[string]string{"name": "name", "updated_at": "updated_at"})}
	models, _, err := paged(limit, offset, func(page model.PageOptions) (model.Page[model.Model], error) {
		options.PageOptions = page
		return this.c.ListModels(r.Context(), token(r), smartServices, options)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	result := make([]SmartServiceDesign, 0, len(models))
	for _, m := range models {
		v, err := this.c.GetModelVersion(r.Context(), token(r), smartServices, m.Id, m.Version)
		if err != nil {
			writeError(w, err)
			return
		}
		result = append(result, design(m, v))
	}
	writeJson(w, http.StatusOK, result)
}

// getDesign godoc
//
//	@Summary		design
//	@Tags		deprecated-smart-services
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"design id"
//	@Success	200	{object}	SmartServiceDesign
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/smart-services/designs/{id} [get]
func (this *Handler) getDesign(w http.ResponseWriter, r *http.Request) {
	m, v, err := this.latestModel(r, smartServices, r.PathValue("id"))
	respond(w, design(m, v), err)
}

func (this *Handler) saveDesign(w http.ResponseWriter, r *http.Request, save func(input model.ModelInput) (model.Model, error)) {
	d, err := decode[SmartServiceDesign](r)
	if err != nil {
		writeError(w, err)
		return
	}
	m, err := save(model.ModelInput{Name: d.Name, Description: d.Description, Xml: d.BpmnXml, Svg: d.SvgXml})
	if err != nil {
		writeError(w, err)
		return
	}
	v, err := this.c.GetModelVersion(r.Context(), token(r), smartServices, m.Id, m.Version)
	respond(w, design(m, v), err)
}

// createDesign godoc
//
//	@Summary		create a design
//	@Tags		deprecated-smart-services
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		design	body	SmartServiceDesign	true	"design"
//	@Success	200	{object}	SmartServiceDesign
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/smart-services/designs [post]
func (this *Handler) createDesign(w http.ResponseWriter, r *http.Request) {
	this.saveDesign(w, r, func(input model.ModelInput) (model.Model, error) {
		return this.c.CreateModel(r.Context(), token(r), smartServices, input)
	})
}

// updateDesign godoc
//
//	@Summary		update a design
//	@Tags		deprecated-smart-services
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"design id"
//	@Param		design	body	SmartServiceDesign	true	"design"
//	@Success	200	{object}	SmartServiceDesign
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/smart-services/designs/{id} [put]
func (this *Handler) updateDesign(w http.ResponseWriter, r *http.Request) {
	this.saveDesign(w, r, func(input model.ModelInput) (model.Model, error) {
		return this.c.UpdateModel(r.Context(), token(r), smartServices, r.PathValue("id"), input)
	})
}

// deleteDesign godoc
//
//	@Summary		delete a design
//	@Tags		deprecated-smart-services
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"design id"
//	@Success	200
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/smart-services/designs/{id} [delete]
func (this *Handler) deleteDesign(w http.ResponseWriter, r *http.Request) {
	err := this.c.DeleteModel(r.Context(), token(r), smartServices, r.PathValue("id"), false)
	if errors.Is(err, model.ErrNotFound) {
		err = nil
	}
	if err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func release(d model.Deployment, version int, createdAt int64, changed []model.ChangedResource) SmartServiceRelease {
	result := SmartServiceRelease{Id: releaseId(d.Id, version, d.Version), DesignId: d.ModelId, Name: d.Name, Description: d.Description, CreatedAt: createdAt, Creator: d.Owner,
		ChangedResources: changed}
	if version != 0 && version < d.Version {
		result.NewReleaseId = d.Id
	}
	return result
}

// deploymentOfDesign finds the deployment that holds the releases of a design
func (this *Handler) deploymentOfDesign(r *http.Request, designId string) (model.Deployment, bool, error) {
	page, err := this.c.ListDeployments(r.Context(), token(r), smartServices, model.DeploymentListOptions{ModelId: designId, PageOptions: model.PageOptions{Limit: 1}})
	if err != nil || len(page.Items) == 0 {
		return model.Deployment{}, false, err
	}
	return page.Items[0], true, nil
}

// createRelease releases the current version of a design: the first release creates the
// deployment, every further one a new version of it, which running instances keep until
// their parameters move them (drain, SMS-03)
//
//	@Summary		release a design: a new deployment or a new version of it
//	@Tags		deprecated-smart-services
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		release	body	SmartServiceRelease	true	"design_id, optional name and description"
//	@Success	200	{object}	SmartServiceRelease
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/smart-services/releases [post]
func (this *Handler) createRelease(w http.ResponseWriter, r *http.Request) {
	request, err := decode[SmartServiceRelease](r)
	if err != nil {
		writeError(w, err)
		return
	}
	m, v, err := this.latestModel(r, smartServices, request.DesignId)
	if err != nil {
		writeError(w, model.BadRequest("the design %v cannot be released: %v", request.DesignId, err))
		return
	}
	conversion, _, err := this.convert(r, smartServices, v.Xml)
	if err != nil {
		writeError(w, err)
		return
	}
	input := model.DeploymentInput{ModelId: m.Id, ModelVersion: v.Version, Xml: conversion.Xml, Svg: v.Svg,
		Name: firstNonEmpty(request.Name, m.Name), Description: firstNonEmpty(request.Description, m.Description)}
	existing, found, err := this.deploymentOfDesign(r, m.Id)
	if err != nil {
		writeError(w, err)
		return
	}
	var d model.Deployment
	switch {
	case found:
		input.UpdateStrategy = model.UpdateDrain
		d, err = this.c.PutDeployment(r.Context(), token(r), smartServices, existing.Id, input)
	case request.Id != "":
		// an id in use belongs to another release, possibly of another user
		if _, err := this.c.GetDeployment(r.Context(), token(r), smartServices, request.Id); !errors.Is(err, model.ErrNotFound) {
			writeError(w, model.Conflict("release id %v is already in use", request.Id))
			return
		}
		d, err = this.c.PutDeployment(r.Context(), token(r), smartServices, request.Id, input)
	default:
		d, err = this.c.CreateDeployment(r.Context(), token(r), smartServices, input)
	}
	respond(w, release(d, d.Version, d.UpdatedAt.Unix(), nil), err)
}

// deleteRelease godoc
//
//	@Summary		delete a release without instances
//	@Tags		deprecated-smart-services
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"release id"
//	@Success	200
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/smart-services/releases/{id} [delete]
func (this *Handler) deleteRelease(w http.ResponseWriter, r *http.Request) {
	deploymentId, version := parseRelease(r.PathValue("id"))
	if version != 0 {
		writeError(w, model.BadRequest("%v is an older version of release %v; only the release as a whole can be deleted", r.PathValue("id"), deploymentId))
		return
	}
	instances, err := this.c.ListInstances(r.Context(), token(r), smartServices, model.InstanceListOptions{DeploymentId: deploymentId, PageOptions: model.PageOptions{Limit: 1}})
	if err != nil {
		writeError(w, err)
		return
	}
	if instances.Total > 0 {
		writeError(w, model.BadRequest("a release may only be deleted if it is not referenced by any smart-service instance"))
		return
	}
	if err = this.c.DeleteDeployment(r.Context(), token(r), smartServices, deploymentId); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// readRelease reads the deployment and the version a release id names
func (this *Handler) readRelease(r *http.Request, id string) (model.Deployment, model.DeploymentVersion, error) {
	deploymentId, version := parseRelease(id)
	d, err := this.c.GetDeployment(r.Context(), token(r), smartServices, deploymentId)
	if err != nil {
		return d, model.DeploymentVersion{}, err
	}
	if version == 0 {
		version = d.Version
	}
	v, err := this.c.GetDeploymentVersion(r.Context(), token(r), smartServices, deploymentId, version)
	return d, v, err
}

// getRelease godoc
//
//	@Summary		release
//	@Tags		deprecated-smart-services
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"release id"
//	@Success	200	{object}	SmartServiceRelease
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/smart-services/releases/{id} [get]
func (this *Handler) getRelease(w http.ResponseWriter, r *http.Request) {
	d, v, err := this.readRelease(r, r.PathValue("id"))
	respond(w, release(d, v.Version, v.CreatedAt.Unix(), v.ChangedResources), err)
}

// releaseParameter describes a form field of the start event with its properties, as the
// release info of smart-service-repository did
func releaseParameter(f bpmn.FormField) ReleaseParameter {
	result := ReleaseParameter{Id: f.Id, Label: firstNonEmpty(f.Label, f.Id), Description: f.Properties["description"], Type: f.Type, DefaultValue: f.DefaultValue,
		Multiple: f.Properties["multiple"] == "true", AutoSelectAll: f.Properties["auto_select_all"] == "true", Optional: f.Properties["optional"] == "true",
		CharacteristicId: ptr(f.Properties["characteristic_id"])}
	result.Order, _ = strconv.Atoi(f.Properties["order"])
	if raw := f.Properties["options"]; raw != "" {
		result.Options = via[map[string]any](jsonRaw(raw))
	}
	return result
}

func jsonRaw(text string) any {
	var value any
	_ = json.Unmarshal([]byte(text), &value)
	return value
}

// releaseInfo reads parameters, maintenance procedures and analytics modules of a release
func releaseInfo(xml string) ReleaseInfo {
	result := ReleaseInfo{ParameterDescriptions: []ReleaseParameter{}, MaintenanceProcedures: []MaintenanceProcedure{}, ModuleInfo: ModuleInfo{Analytics: []AnalyticsInfo{}}}
	process, _, err := bpmn.Parse(xml)
	if err != nil {
		return result
	}
	for _, e := range process.Ordered() {
		switch {
		case e.Type == bpmn.StartEvent && e.Kind == bpmn.EventNone:
			for _, f := range e.FormFields {
				result.ParameterDescriptions = append(result.ParameterDescriptions, releaseParameter(f))
			}
		case e.Type == bpmn.StartEvent && e.Kind == bpmn.EventMessage && e.Message != nil:
			procedure := MaintenanceProcedure{BpmnId: e.Id, PublicEventId: e.Message.Name, InternalEventId: e.Message.Name, ParameterDescriptions: []ReleaseParameter{}}
			for _, f := range e.FormFields {
				procedure.ParameterDescriptions = append(procedure.ParameterDescriptions, releaseParameter(f))
			}
			result.MaintenanceProcedures = append(result.MaintenanceProcedures, procedure)
		case e.TaskType == "analytics":
			flow, _ := inputText(e, "analytics.flow_id")
			name, _ := inputText(e, "analytics.name")
			desc, _ := inputText(e, "analytics.desc")
			result.ModuleInfo.Analytics = append(result.ModuleInfo.Analytics, AnalyticsInfo{FlowId: flow, Name: name, Desc: desc})
		}
	}
	return result
}

func (this *Handler) extendedRelease(r *http.Request, d model.Deployment, v model.DeploymentVersion) SmartServiceReleaseExtended {
	return SmartServiceReleaseExtended{SmartServiceRelease: release(d, v.Version, v.CreatedAt.Unix(), v.ChangedResources), BpmnXml: v.Xml, SvgXml: v.Svg,
		ParsedInfo: releaseInfo(v.Xml), PermissionsInfo: permissions(r, d.Owner, releaseRights)}
}

// getExtendedRelease godoc
//
//	@Summary		release with BPMN, parameters and permissions
//	@Tags		deprecated-smart-services
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"release id"
//	@Success	200	{object}	SmartServiceReleaseExtended
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/smart-services/extended-releases/{id} [get]
func (this *Handler) getExtendedRelease(w http.ResponseWriter, r *http.Request) {
	d, v, err := this.readRelease(r, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJson(w, http.StatusOK, this.extendedRelease(r, d, v))
}

// releases lists the releases with the parameters of smart-service-repository; latest=true
// lists the current versions only, otherwise the older versions follow each deployment
func (this *Handler) releases(r *http.Request) ([]model.Deployment, []model.DeploymentVersion, int, error) {
	q := r.URL.Query()
	limit, err := intParam(r, "limit", 100)
	if err != nil {
		return nil, nil, 0, err
	}
	offset, err := intParam(r, "offset", 0)
	if err != nil {
		return nil, nil, 0, err
	}
	options := model.DeploymentListOptions{Search: q.Get("search"), Sort: sort(q.Get("sort"), map[string]string{"name": "name", "created_at": "created_at"})}
	var deployments []model.Deployment
	var total int
	ids := splitList(q.Get("ids"))
	switch {
	case len(ids) > 0:
		for _, id := range ids {
			d, err := this.c.GetDeployment(r.Context(), token(r), smartServices, id)
			if errors.Is(err, model.ErrNotFound) || errors.Is(err, model.ErrForbidden) {
				continue
			}
			if err != nil {
				return nil, nil, 0, err
			}
			deployments = append(deployments, d)
		}
		total = len(deployments)
	case boolParam(r, "latest"):
		deployments, total, err = paged(limit, offset, func(page model.PageOptions) (model.Page[model.Deployment], error) {
			options.PageOptions = page
			return this.c.ListDeployments(r.Context(), token(r), smartServices, options)
		})
	default:
		deployments, err = all(func(page model.PageOptions) (model.Page[model.Deployment], error) {
			options.PageOptions = page
			return this.c.ListDeployments(r.Context(), token(r), smartServices, options)
		})
	}
	if err != nil {
		return nil, nil, 0, err
	}
	var resultDeployments []model.Deployment
	var versions []model.DeploymentVersion
	for _, d := range deployments {
		all := []model.DeploymentVersion{}
		if boolParam(r, "latest") || len(ids) > 0 {
			v, err := this.c.GetDeploymentVersion(r.Context(), token(r), smartServices, d.Id, d.Version)
			if err != nil {
				return nil, nil, 0, err
			}
			all = append(all, v)
		} else {
			for version := d.Version; version >= 1; version-- {
				v, err := this.c.GetDeploymentVersion(r.Context(), token(r), smartServices, d.Id, version)
				if errors.Is(err, model.ErrNotFound) {
					continue
				}
				if err != nil {
					return nil, nil, 0, err
				}
				all = append(all, v)
			}
		}
		for _, v := range all {
			resultDeployments = append(resultDeployments, d)
			versions = append(versions, v)
		}
	}
	if !boolParam(r, "latest") && len(ids) == 0 {
		total = len(versions)
		end := len(versions)
		if limit > 0 {
			end = min(offset+limit, len(versions))
		}
		start := min(offset, len(versions))
		resultDeployments, versions = resultDeployments[start:end], versions[start:end]
	}
	return resultDeployments, versions, total, nil
}

// usable reports whether every parameter of a release has a valid option for the user
func (this *Handler) usable(r *http.Request, deploymentId string) bool {
	parameters, err := this.c.DeploymentParameters(r.Context(), token(r), smartServices, deploymentId)
	if err != nil {
		return false
	}
	return !slices.ContainsFunc(parameters, func(p model.ParameterDescription) bool { return p.HasNoValidOption })
}

// listReleases godoc
//
//	@Summary		releases
//	@Tags		deprecated-smart-services
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		limit	query	int	false	"page size"
//	@Param		offset	query	int	false	"offset"
//	@Param		search	query	string	false	"part of the name"
//	@Param		sort	query	string	false	"name or created_at with .asc or .desc"
//	@Param		latest	query	bool	false	"current versions only"
//	@Param		add-usable-flag	query	bool	false	"whether every parameter has an option"
//	@Success	200	{array}		SmartServiceRelease
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/smart-services/releases [get]
func (this *Handler) listReleases(w http.ResponseWriter, r *http.Request) {
	deployments, versions, total, err := this.releases(r)
	if err != nil {
		writeError(w, err)
		return
	}
	result := make([]SmartServiceRelease, 0, len(versions))
	for i, v := range versions {
		item := release(deployments[i], v.Version, v.CreatedAt.Unix(), v.ChangedResources)
		if boolParam(r, "add-usable-flag") {
			usable := this.usable(r, deployments[i].Id)
			item.Usable = &usable
		}
		result = append(result, item)
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	w.Header().Set("Access-Control-Expose-Headers", "X-Total-Count")
	writeJson(w, http.StatusOK, result)
}

// listExtendedReleases godoc
//
//	@Summary		releases with BPMN, parameters and permissions
//	@Tags		deprecated-smart-services
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		limit	query	int	false	"page size"
//	@Param		offset	query	int	false	"offset"
//	@Param		search	query	string	false	"part of the name"
//	@Param		ids	query	string	false	"comma separated release ids"
//	@Param		latest	query	bool	false	"current versions only"
//	@Param		add-usable-flag	query	bool	false	"whether every parameter has an option"
//	@Success	200	{array}		SmartServiceReleaseExtended
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/smart-services/extended-releases [get]
func (this *Handler) listExtendedReleases(w http.ResponseWriter, r *http.Request) {
	deployments, versions, total, err := this.releases(r)
	if err != nil {
		writeError(w, err)
		return
	}
	result := make([]SmartServiceReleaseExtended, 0, len(versions))
	for i, v := range versions {
		item := this.extendedRelease(r, deployments[i], v)
		if boolParam(r, "add-usable-flag") {
			usable := this.usable(r, deployments[i].Id)
			item.Usable = &usable
		}
		result = append(result, item)
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	w.Header().Set("Access-Control-Expose-Headers", "X-Total-Count")
	writeJson(w, http.StatusOK, result)
}

// releaseParameters godoc
//
//	@Summary		start parameters of a release with options
//	@Tags		deprecated-smart-services
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"release id"
//	@Success	200	{array}		SmartServiceExtendedParameter
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/smart-services/releases/{id}/parameters [get]
func (this *Handler) releaseParameters(w http.ResponseWriter, r *http.Request) {
	deploymentId, _ := parseRelease(r.PathValue("id"))
	parameters, err := this.c.DeploymentParameters(r.Context(), token(r), smartServices, deploymentId)
	if err != nil {
		writeError(w, err)
		return
	}
	result := make([]SmartServiceExtendedParameter, 0, len(parameters))
	for _, p := range parameters {
		result = append(result, SmartServiceExtendedParameter{SmartServiceParameter: SmartServiceParameter{Id: p.Id, Label: p.Label},
			Description: p.Description, DefaultValue: p.DefaultValue, Type: p.Type, Options: p.Options, Multiple: p.Multiple, Order: p.Order,
			CharacteristicId: ptr(p.CharacteristicId), Characteristic: p.Characteristic, Optional: p.Optional, HasNoValidOption: p.HasNoValidOption})
	}
	writeJson(w, http.StatusOK, result)
}

func variables(parameters []SmartServiceParameter) map[string]any {
	result := map[string]any{}
	for _, p := range parameters {
		result[p.Id] = p.Value
	}
	return result
}

// smartServiceInstance shows an instance as smart-service-repository did; the deployment
// gives design and release
func smartServiceInstance(r *http.Request, i model.Instance, d model.Deployment) SmartServiceInstance {
	result := SmartServiceInstance{SmartServiceInstanceInit: SmartServiceInstanceInit{Name: i.Name, Description: i.Description, Parameters: []SmartServiceParameter{}},
		PermissionsInfo: permissions(r, i.Owner, instanceRights), Id: i.Id, UserId: i.Owner, DesignId: d.ModelId,
		ReleaseId: releaseId(i.DeploymentId, i.DeploymentVersion, d.Version), Ready: i.Status == model.StatusReady,
		CreatedAt: i.CreatedAt.Unix(), UpdatedAt: i.LastActivityAt.Unix()}
	if d.Version > i.DeploymentVersion {
		result.NewReleaseId = d.Id
	}
	if i.Status == model.StatusError {
		result.Error = firstNonEmpty(i.StatusMessage, "error")
	}
	for _, name := range sortedKeys(i.StartVariables) {
		result.Parameters = append(result.Parameters, SmartServiceParameter{Id: name, Value: i.StartVariables[name], Label: name})
	}
	return result
}

// deploymentCache reads each deployment of a request once
type deploymentCache struct {
	this *Handler
	r    *http.Request
	byId map[string]model.Deployment
}

func (this *deploymentCache) get(id string) (model.Deployment, error) {
	if d, ok := this.byId[id]; ok {
		return d, nil
	}
	d, err := this.this.c.GetDeployment(this.r.Context(), token(this.r), smartServices, id)
	if err == nil {
		this.byId[id] = d
	}
	return d, err
}

func (this *Handler) newDeploymentCache(r *http.Request) *deploymentCache {
	return &deploymentCache{this: this, r: r, byId: map[string]model.Deployment{}}
}

// createSmartServiceInstance godoc
//
//	@Summary		create an instance of a release
//	@Tags		deprecated-smart-services
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"release id"
//	@Param		instance	body	SmartServiceInstanceInit	true	"name and parameters"
//	@Success	200	{object}	SmartServiceInstance
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/smart-services/releases/{id}/instances [post]
func (this *Handler) createSmartServiceInstance(w http.ResponseWriter, r *http.Request) {
	init, err := decode[SmartServiceInstanceInit](r)
	if err != nil {
		writeError(w, err)
		return
	}
	if init.Name == "" {
		writeError(w, model.BadRequest("missing name"))
		return
	}
	deploymentId, version := parseRelease(r.PathValue("id"))
	i, err := this.c.StartInstance(r.Context(), token(r), smartServices, model.StartInstanceInput{DeploymentId: deploymentId, DeploymentVersion: version,
		Variables: variables(init.Parameters), Name: init.Name, Description: init.Description})
	if err != nil {
		writeError(w, err)
		return
	}
	d, err := this.c.GetDeployment(r.Context(), token(r), smartServices, deploymentId)
	respond(w, smartServiceInstance(r, i, d), err)
}

// listSmartServiceInstances godoc
//
//	@Summary		smart-service instances
//	@Tags		deprecated-smart-services
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		limit	query	int	false	"page size"
//	@Param		offset	query	int	false	"offset"
//	@Param		sort	query	string	false	"name or created_at with .asc or .desc"
//	@Param		release-id	query	string	false	"release id"
//	@Success	200	{array}		SmartServiceInstance
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/smart-services/instances [get]
func (this *Handler) listSmartServiceInstances(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := intParam(r, "limit", 0)
	if err != nil {
		writeError(w, err)
		return
	}
	offset, err := intParam(r, "offset", 0)
	if err != nil {
		writeError(w, err)
		return
	}
	options := model.InstanceListOptions{Sort: sort(firstNonEmpty(q.Get("sort"), "name.asc"), map[string]string{"name": "name", "created_at": "created_at"})}
	if releaseId := q.Get("release-id"); releaseId != "" {
		options.DeploymentId, _ = parseRelease(releaseId)
	}
	instances, total, err := paged(limit, offset, func(page model.PageOptions) (model.Page[model.Instance], error) {
		options.PageOptions = page
		return this.c.ListInstances(r.Context(), token(r), smartServices, options)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	cache := this.newDeploymentCache(r)
	result := make([]SmartServiceInstance, 0, len(instances))
	for _, i := range instances {
		if i.ParentId != "" {
			// a running maintenance procedure belongs to its instance
			continue
		}
		d, err := cache.get(i.DeploymentId)
		if err != nil {
			writeError(w, err)
			return
		}
		result = append(result, smartServiceInstance(r, i, d))
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	w.Header().Set("Access-Control-Expose-Headers", "X-Total-Count")
	writeJson(w, http.StatusOK, result)
}

func (this *Handler) smartServiceInstanceById(w http.ResponseWriter, r *http.Request, i model.Instance, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	d, err := this.c.GetDeployment(r.Context(), token(r), smartServices, i.DeploymentId)
	respond(w, smartServiceInstance(r, i, d), err)
}

// getSmartServiceInstance godoc
//
//	@Summary		smart-service instance
//	@Tags		deprecated-smart-services
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"instance id"
//	@Success	200	{object}	SmartServiceInstance
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/smart-services/instances/{id} [get]
func (this *Handler) getSmartServiceInstance(w http.ResponseWriter, r *http.Request) {
	i, err := this.c.GetInstance(r.Context(), token(r), smartServices, r.PathValue("id"))
	this.smartServiceInstanceById(w, r, i, err)
}

// deleteSmartServiceInstance removes the instance and, through the engine, its modules
//
//	@Summary		delete an instance with its modules
//	@Tags		deprecated-smart-services
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"instance id"
//	@Success	200
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/smart-services/instances/{id} [delete]
func (this *Handler) deleteSmartServiceInstance(w http.ResponseWriter, r *http.Request) {
	err := this.c.DeleteInstances(r.Context(), token(r), smartServices, []string{r.PathValue("id")})
	if errors.Is(err, model.ErrNotFound) {
		err = nil
	}
	if err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// updateSmartServiceInstanceInfo godoc
//
//	@Summary		change name and description of an instance
//	@Tags		deprecated-smart-services
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"instance id"
//	@Param		info	body	SmartServiceInstanceInfo	true	"name and description"
//	@Success	200	{object}	SmartServiceInstance
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/smart-services/instances/{id}/info [put]
func (this *Handler) updateSmartServiceInstanceInfo(w http.ResponseWriter, r *http.Request) {
	info, err := decode[SmartServiceInstanceInfo](r)
	if err != nil {
		writeError(w, err)
		return
	}
	if info.Name == "" {
		writeError(w, model.BadRequest("missing name"))
		return
	}
	i, err := this.c.UpdateInstanceInfo(r.Context(), token(r), smartServices, r.PathValue("id"), model.InstanceInfoInput{Name: info.Name, Description: info.Description})
	this.smartServiceInstanceById(w, r, i, err)
}

// updateSmartServiceInstanceParameters starts a new run with the parameters; release_id moves
// the instance to that version of its deployment (SMS-09)
//
//	@Summary		change the parameters of an instance; release_id moves it to that release
//	@Tags		deprecated-smart-services
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"instance id"
//	@Param		release_id	query	string	false	"release id"
//	@Param		parameters	body	[]SmartServiceParameter	true	"parameters"
//	@Success	200	{object}	SmartServiceInstance
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/smart-services/instances/{id}/parameters [put]
func (this *Handler) updateSmartServiceInstanceParameters(w http.ResponseWriter, r *http.Request) {
	parameters, err := decode[[]SmartServiceParameter](r)
	if err != nil {
		writeError(w, err)
		return
	}
	input := model.ParametersInput{Variables: variables(parameters)}
	if target := r.URL.Query().Get("release_id"); target != "" {
		i, err := this.c.GetInstance(r.Context(), token(r), smartServices, r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		deploymentId, version := parseRelease(target)
		if deploymentId != i.DeploymentId {
			writeError(w, model.BadRequest("release %v belongs to another design", target))
			return
		}
		input.DeploymentVersion = version
	}
	if _, err = this.c.UpdateParameters(r.Context(), token(r), smartServices, r.PathValue("id"), input); err != nil {
		writeError(w, err)
		return
	}
	i, err := this.c.GetInstance(r.Context(), token(r), smartServices, r.PathValue("id"))
	this.smartServiceInstanceById(w, r, i, err)
}

// listSmartServiceModules godoc
//
//	@Summary		modules of smart-service instances
//	@Tags		deprecated-smart-services
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		limit	query	int	false	"page size"
//	@Param		offset	query	int	false	"offset"
//	@Param		instance_id	query	string	false	"instance id"
//	@Param		instance_ids	query	string	false	"comma separated instance ids"
//	@Param		module_type	query	string	false	"module type"
//	@Param		key	query	string	false	"key"
//	@Success	200	{array}		SmartServiceModule
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/smart-services/modules [get]
func (this *Handler) listSmartServiceModules(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := intParam(r, "limit", 0)
	if err != nil {
		writeError(w, err)
		return
	}
	offset, err := intParam(r, "offset", 0)
	if err != nil {
		writeError(w, err)
		return
	}
	instanceIds := splitList(q.Get("instance_ids"))
	if id := q.Get("instance_id"); id != "" {
		instanceIds = []string{id}
	}
	if len(instanceIds) == 0 {
		instanceIds = []string{""}
	}
	var modules []model.Module
	for _, instanceId := range instanceIds {
		options := model.ModuleListOptions{InstanceId: instanceId, ModuleType: q.Get("module_type"), Key: q.Get("key")}
		page, _, err := paged(limit, offset, func(page model.PageOptions) (model.Page[model.Module], error) {
			options.PageOptions = page
			return this.c.ListModules(r.Context(), token(r), smartServices, options)
		})
		if err != nil {
			writeError(w, err)
			return
		}
		modules = append(modules, page...)
	}
	cache := this.newDeploymentCache(r)
	instances := map[string]model.Instance{}
	result := make([]SmartServiceModule, 0, len(modules))
	for _, m := range modules {
		module := SmartServiceModule{Id: m.Id, UserId: m.Owner, InstanceId: m.InstanceId, LastUpdate: m.UpdatedAt.Unix(), Error: m.Error,
			ModuleType: m.ModuleType, ModuleData: m.Data, Keys: m.Keys}
		if m.DeleteInfo != nil && m.DeleteInfo.Url != "" {
			module.DeleteInfo = &ModuleDeleteInfo{Url: m.DeleteInfo.Url, UserId: m.DeleteInfo.UserId}
		}
		i, ok := instances[m.InstanceId]
		if !ok {
			if i, err = this.c.GetInstance(r.Context(), token(r), smartServices, m.InstanceId); err == nil {
				instances[m.InstanceId] = i
			}
		}
		if i.DeploymentId != "" {
			if d, err := cache.get(i.DeploymentId); err == nil {
				module.DesignId, module.ReleaseId = d.ModelId, releaseId(i.DeploymentId, i.DeploymentVersion, d.Version)
			}
		}
		result = append(result, module)
	}
	writeJson(w, http.StatusOK, result)
}

// releaseInstanceCount godoc
//
//	@Summary		count the instances of all users on a release, for its administrators
//	@Tags			deprecated-smart-services
//	@Deprecated
//	@Security		Bearer
//	@Produce		json
//	@Param			id	path	string	true	"release id"
//	@Success		200	{object}	model.Count
//	@Failure		400,403,404	{string}	string
//	@Router			/deprecated/smart-services/releases/{id}/instance-count [get]
func (this *Handler) releaseInstanceCount(w http.ResponseWriter, r *http.Request) {
	deploymentId, version := parseRelease(r.PathValue("id"))
	result, err := this.c.DeploymentInstanceCount(r.Context(), token(r), smartServices, deploymentId, version)
	respond(w, result, err)
}

// resourceUsage godoc
//
//	@Summary		who uses a process model, flow or import type, as smart-service-repository answered it
//	@Description	Asked by process-model-repository, analytics-flow-repo-v2 and import-repository before they delete a resource.
//	@Tags			deprecated-smart-services
//	@Deprecated
//	@Security		Bearer
//	@Produce		json
//	@Param			kind	path	string	true	"process-models, flows or import-types"
//	@Param			id		path	string	true	"resource id"
//	@Success		200	{object}	ResourceUsage
//	@Failure		400,403,404	{string}	string
//	@Router			/deprecated/smart-services/resource-usage/{kind}/{id} [get]
func (this *Handler) resourceUsage(w http.ResponseWriter, r *http.Request) {
	usage, err := this.c.ResourceUsage(r.Context(), token(r), smartServices, r.PathValue("kind"), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	result := ResourceUsage{Releases: usage.Versions, Instances: usage.Instances, Readable: []ResourceUsageRelease{}}
	for _, d := range usage.Readable {
		id := d.Id
		if !d.Current {
			id = d.Id + ":v" + strconv.Itoa(d.Version)
		}
		result.Readable = append(result.Readable, ResourceUsageRelease{Id: id, DesignId: d.ModelId, Name: d.Name})
	}
	writeJson(w, http.StatusOK, result)
}
