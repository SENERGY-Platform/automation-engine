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
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/prepare"
	"github.com/SENERGY-Platform/models/go/models"
)

// The routes of process-deployment (Kong /process/deployment) that the web-ui uses, with the
// deployment format version 3 of models/go. The BPMN of the old designer is rewritten on the
// way in (MIG-04); device selections become the selections of the deployment, changed inputs,
// timers, notification texts and start defaults its parameters (DEP-01, DEP-05). On the way
// out the elements are rebuilt from the deployed process.

type Dependencies struct {
	DeploymentId string             `json:"deployment_id"`
	Owner        string             `json:"owner"`
	Devices      []DeviceDependency `json:"devices"`
	Events       []EventDependency  `json:"events"`
}

type DeviceDependency struct {
	DeviceId      string         `json:"device_id"`
	Name          string         `json:"name"`
	BpmnResources []BpmnResource `json:"bpmn_resources"`
}

type EventDependency struct {
	EventId       string         `json:"event_id"`
	BpmnResources []BpmnResource `json:"bpmn_resources"`
}

type BpmnResource struct {
	Id string `json:"id"`
}

type PrepareRequest struct {
	Xml string `json:"xml"`
	Svg string `json:"svg"`
}

func (this *Handler) deployments() {
	this.handle("GET /deployment/v3/start-parameters/{modelId}", this.modelStartParameters)
	this.handle("POST /deployment/v3/prepared-deployments", this.prepareXml)
	this.handle("GET /deployment/v3/prepared-deployments/{modelId}", this.prepareModel)
	this.handle("POST /deployment/v3/deployments", this.createDeployment)
	this.handle("PUT /deployment/v3/deployments/{id}", this.updateDeployment)
	this.handle("GET /deployment/v3/deployments", this.listV3Deployments)
	this.handle("GET /deployment/v3/deployments/{id}", this.getV3Deployment)
	this.handle("DELETE /deployment/v3/deployments/{id}", this.deleteV3Deployment)
	this.handle("GET /deployment/dependencies", this.listDependencies)
	this.handle("GET /deployment/dependencies/{id}", this.getDependencies)
}

func ptr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func withOptions(r *http.Request) bool {
	value := r.URL.Query().Get("with_options")
	return value == "" || value == "true"
}

// convert rewrites BPMN of the old designer; what the engine cannot run rejects the request
func (this *Handler) convert(r *http.Request, namespace string, xml string) (model.Conversion, *bpmn.Process, error) {
	conversion, err := this.c.Convert(r.Context(), token(r), namespace, xml)
	if err != nil {
		return conversion, nil, err
	}
	if len(conversion.Unsupported) > 0 {
		return conversion, nil, model.ValidationFailed(conversion.Unsupported)
	}
	process, _, err := bpmn.Parse(conversion.Xml)
	if err != nil {
		return conversion, nil, model.BadRequest("%v", err)
	}
	return conversion, process, nil
}

// options reads the device options of a process from the engine; without them, only the
// criteria are known
func (this *Handler) options(r *http.Request, xml string, with bool) (map[string][]model.SelectionOption, error) {
	result := map[string][]model.SelectionOption{}
	if !with {
		return result, nil
	}
	prepared, err := this.c.PrepareDeployment(r.Context(), token(r), processes, model.PrepareInput{Xml: xml})
	if err != nil {
		return nil, err
	}
	for _, e := range prepared.Elements {
		result[e.ElementId] = e.Options
	}
	return result, nil
}

func oldCriteria(c model.Criteria) models.ProcessFilterCriteria {
	result := models.ProcessFilterCriteria{FunctionId: ptr(c.FunctionId), CharacteristicId: ptr(c.CharacteristicId), DeviceClassId: ptr(c.DeviceClassId), AspectIds: c.AspectIds}
	if len(c.AspectIds) > 0 {
		result.AspectId = ptr(c.AspectIds[0])
	}
	return result
}

func oldPath(p *model.SelectedPath) *models.PathOption {
	if p == nil {
		return nil
	}
	result := &models.PathOption{Path: p.Path, CharacteristicId: p.CharacteristicId}
	for _, c := range p.Configurables {
		configurable := models.Configurable{Path: c.Path, CharacteristicId: c.CharacteristicId, FunctionId: c.FunctionId, Value: c.Value, Type: models.Type(c.Type)}
		for _, id := range c.AspectNodeIds {
			configurable.AspectNodes = append(configurable.AspectNodes, models.AspectNode{Id: id})
		}
		if len(configurable.AspectNodes) > 0 {
			configurable.AspectNode = configurable.AspectNodes[0]
		}
		result.Configurables = append(result.Configurables, configurable)
	}
	return result
}

func newPath(p *models.PathOption) *model.SelectedPath {
	if p == nil || p.Path == "" {
		return nil
	}
	result := &model.SelectedPath{Path: p.Path, CharacteristicId: p.CharacteristicId}
	for _, c := range p.Configurables {
		configurable := model.Configurable{Path: c.Path, CharacteristicId: c.CharacteristicId, FunctionId: c.FunctionId, Value: c.Value, Type: string(c.Type)}
		for _, node := range c.AspectNodes {
			configurable.AspectNodeIds = append(configurable.AspectNodeIds, node.Id)
		}
		if len(configurable.AspectNodeIds) == 0 && c.AspectNode.Id != "" {
			configurable.AspectNodeIds = []string{c.AspectNode.Id}
		}
		result.Configurables = append(result.Configurables, configurable)
	}
	return result
}

// via converts between types with the same JSON form
func via[T any](value any) (result T) {
	raw, _ := json.Marshal(value)
	_ = json.Unmarshal(raw, &result)
	return result
}

func oldOption(o model.SelectionOption) models.SelectionOption {
	result := models.SelectionOption{Services: []models.SelectionService{}}
	if o.Device != nil {
		result.Device = &models.SelectionDevice{Id: o.Device.Id, Name: o.Device.Name}
	}
	for _, s := range o.Services {
		result.Services = append(result.Services, models.SelectionService{Id: s.Id, Name: s.Name})
	}
	if o.DeviceGroup != nil {
		result.DeviceGroup = &models.SelectionDeviceGroup{Id: o.DeviceGroup.Id, Name: o.DeviceGroup.Name}
	}
	if o.Import != nil {
		result.Import = via[*models.Import](o.Import)
	}
	if o.ImportType != nil {
		result.ImportType = via[*models.ImportType](o.ImportType)
	}
	if len(o.PathOptions) > 0 {
		result.PathOptions = map[string][]models.PathOption{}
		for service, raw := range o.PathOptions {
			var paths []models.PathOption
			if json.Unmarshal(raw, &paths) == nil {
				result.PathOptions[service] = paths
			}
		}
	}
	return result
}

func oldSelection(criteria model.Criteria, s model.Selection, options []model.SelectionOption) models.Selection {
	result := models.Selection{FilterCriteria: oldCriteria(criteria), SelectionOptions: []models.SelectionOption{},
		SelectedDeviceId: ptr(s.DeviceId), SelectedServiceId: ptr(s.ServiceId), SelectedDeviceGroupId: ptr(s.DeviceGroupId), SelectedImportId: ptr(s.ImportId),
		SelectedPath: oldPath(s.Path)}
	for _, o := range options {
		result.SelectionOptions = append(result.SelectionOptions, oldOption(o))
	}
	return result
}

func newSelection(s models.Selection) (model.Selection, error) {
	if s.SelectedGenericEventSource != nil {
		return model.Selection{}, model.BadRequest("generic event sources are not supported")
	}
	value := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	return model.Selection{DeviceId: value(s.SelectedDeviceId), ServiceId: value(s.SelectedServiceId), DeviceGroupId: value(s.SelectedDeviceGroupId),
		ImportId: value(s.SelectedImportId), Path: newPath(s.SelectedPath)}, nil
}

// inputText is the text of an input mapping, false if the input is missing or not text
func inputText(e *bpmn.Element, name string) (string, bool) {
	for _, m := range e.Inputs {
		if m.Name == name {
			return m.Value.Text, m.Value.Kind == bpmn.ValueText
		}
	}
	return "", false
}

var timerKinds = map[string]string{"duration": "timeDuration", "date": "timeDate", "cycle": "timeCycle"}

// elements rebuilds the configurable elements of a deployment in the old format: device tasks
// and events with their selections, timers and notifications
func elements(process *bpmn.Process, selections map[string]model.Selection, options map[string][]model.SelectionOption) []models.Element {
	result := []models.Element{}
	for _, e := range process.Ordered() {
		element := models.Element{BpmnId: e.Id, Name: e.Name, Group: ptr(e.Lane)}
		element.Order, _ = strconv.ParseInt(e.Attributes["order"], 10, 64)
		criteria, device := prepare.CriteriaOf(e)
		switch {
		case device && e.Type == bpmn.ServiceTask:
			task := &models.Task{Parameter: map[string]string{}, Selection: oldSelection(criteria, selections[e.Id], options[e.Id])}
			for _, m := range e.Inputs {
				if m.Name == "payload" {
					var payload struct{ Retries int64 }
					if json.Unmarshal([]byte(m.Value.Text), &payload) == nil {
						task.Retries = payload.Retries
					}
				} else if m.Value.Kind == bpmn.ValueText {
					task.Parameter[m.Name] = m.Value.Text
				}
			}
			if prefer := selections[e.Id].PreferEvent; prefer != nil {
				task.PreferEvent = *prefer
			}
			element.Task = task
		case device:
			event := &models.ConditionalEvent{Script: e.Attributes["script"], ValueVariable: e.Attributes["value_variable_name"], Variables: map[string]string{},
				Selection: oldSelection(criteria, selections[e.Id], options[e.Id])}
			_ = json.Unmarshal([]byte(e.Attributes["variables"]), &event.Variables)
			event.Qos, _ = strconv.Atoi(e.Attributes["qos"])
			element.ConditionalEvent = event
		case e.Timer != nil:
			element.TimeEvent = &models.TimeEvent{Type: timerKinds[string(e.Timer.Kind)], Time: e.Timer.Value}
		case e.TaskType == "notification":
			title, _ := inputText(e, "title")
			message, _ := inputText(e, "message")
			element.Notification = &models.Notification{Title: title, Message: message}
		default:
			continue
		}
		result = append(result, element)
	}
	return result
}

func oldStartParameters(process *bpmn.Process) []models.ProcessStartParameter {
	result := []models.ProcessStartParameter{}
	for _, p := range bpmn.StartParameters(process.NoneStartEvent()) {
		result = append(result, models.ProcessStartParameter{Id: p.Id, Label: p.Label, Type: p.Type, Default: p.Default, Properties: p.Properties})
	}
	return result
}

// prepared answers a deployment that is yet to be made, as process-deployment did
func (this *Handler) prepared(r *http.Request, name string, xml string, svg string) (models.Deployment, error) {
	conversion, process, err := this.convert(r, processes, xml)
	if err != nil {
		return models.Deployment{}, err
	}
	options, err := this.options(r, conversion.Xml, withOptions(r))
	if err != nil {
		return models.Deployment{}, err
	}
	if name == "" {
		name = firstNonEmpty(process.Name, process.Id)
	}
	return models.Deployment{Version: 3, Name: name, Diagram: models.Diagram{XmlRaw: xml, XmlDeployed: conversion.Xml, Svg: svg},
		Elements: elements(process, map[string]model.Selection{}, options), Executable: len(conversion.Issues) == 0,
		IncidentHandling: &models.IncidentHandling{Restart: false, Notify: true}, StartParameter: oldStartParameters(process)}, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// prepareXml godoc
//
//	@Summary		prepare a deployment of BPMN of the old designer
//	@Tags		deprecated-deployment
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		with_options	query	bool	false	"device options, default true"
//	@Param		request	body	PrepareRequest	true	"BPMN and SVG"
//	@Success	200	{object}	models.Deployment
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/deployment/v3/prepared-deployments [post]
func (this *Handler) prepareXml(w http.ResponseWriter, r *http.Request) {
	request, err := decode[PrepareRequest](r)
	if err != nil {
		writeError(w, err)
		return
	}
	result, err := this.prepared(r, "", request.Xml, request.Svg)
	respond(w, result, err)
}

// latestModel reads a model with the content of its latest version
func (this *Handler) latestModel(r *http.Request, namespace string, id string) (model.Model, model.ModelVersion, error) {
	m, err := this.c.GetModel(r.Context(), token(r), namespace, id)
	if err != nil {
		return m, model.ModelVersion{}, err
	}
	version, err := this.c.GetModelVersion(r.Context(), token(r), namespace, id, m.Version)
	return m, version, err
}

// prepareModel godoc
//
//	@Summary		prepare a deployment of a model
//	@Tags		deprecated-deployment
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		modelId	path	string	true	"model id"
//	@Param		with_options	query	bool	false	"device options, default true"
//	@Success	200	{object}	models.Deployment
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/deployment/v3/prepared-deployments/{modelId} [get]
func (this *Handler) prepareModel(w http.ResponseWriter, r *http.Request) {
	m, version, err := this.latestModel(r, processes, r.PathValue("modelId"))
	if err != nil {
		writeError(w, err)
		return
	}
	result, err := this.prepared(r, m.Name, version.Xml, version.Svg)
	respond(w, result, err)
}

// modelStartParameters godoc
//
//	@Summary		start parameters of a model
//	@Tags		deprecated-deployment
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		modelId	path	string	true	"model id"
//	@Success	200	{array}		models.ProcessStartParameter
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/deployment/v3/start-parameters/{modelId} [get]
func (this *Handler) modelStartParameters(w http.ResponseWriter, r *http.Request) {
	_, version, err := this.latestModel(r, processes, r.PathValue("modelId"))
	if err != nil {
		writeError(w, err)
		return
	}
	_, process, err := this.convert(r, processes, version.Xml)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJson(w, http.StatusOK, oldStartParameters(process))
}

// changed reports a value the old deploy dialog changed: set, without expression and other
// than in the model
func changed(value string, original string, known bool) bool {
	return value != "" && !strings.Contains(value, "${") && (!known || value != original)
}

// deploymentInput turns a deployment of the old format into the input of the engine
func (this *Handler) deploymentInput(r *http.Request, old models.Deployment) (model.DeploymentInput, error) {
	return DeploymentInput(r.Context(), this.c, token(r), processes, old, r.URL.Query().Get("source"))
}

// Converter rewrites models of Camunda (MIG-04).
type Converter interface {
	Convert(ctx context.Context, token string, namespace string, xml string) (model.Conversion, error)
}

// DeploymentInput turns a deployment of process-deployment (format version 3) into the input
// of the engine: the BPMN rewritten, device selections as selections, changed values as
// parameters. The migration uses it as well (MIG-01).
func DeploymentInput(ctx context.Context, c Converter, token string, namespace string, old models.Deployment, source string) (model.DeploymentInput, error) {
	if old.Version != 3 {
		return model.DeploymentInput{}, model.BadRequest("expected deployment version 3")
	}
	conversion, err := c.Convert(ctx, token, namespace, old.Diagram.XmlRaw)
	if err != nil {
		return model.DeploymentInput{}, err
	}
	if len(conversion.Unsupported) > 0 {
		return model.DeploymentInput{}, model.ValidationFailed(conversion.Unsupported)
	}
	process, _, err := bpmn.Parse(conversion.Xml)
	if err != nil {
		return model.DeploymentInput{}, model.BadRequest("%v", err)
	}
	input := model.DeploymentInput{Name: old.Name, Description: old.Description, Xml: conversion.Xml, Svg: old.Diagram.Svg, Source: source,
		Selections: map[string]model.Selection{}, Parameters: map[string]model.ElementParameters{}}
	if old.IncidentHandling != nil {
		input.IncidentHandling = &model.IncidentHandling{Restart: old.IncidentHandling.Restart, Notify: old.IncidentHandling.Notify}
	}
	for _, element := range old.Elements {
		e := process.Element(element.BpmnId)
		if e == nil {
			continue
		}
		params := model.ElementParameters{Inputs: map[string]string{}, Variables: map[string]string{}}
		var selection *models.Selection
		switch {
		case element.Task != nil:
			selection = &element.Task.Selection
			for name, value := range element.Task.Parameter {
				if original, ok := inputText(e, name); ok && changed(value, original, true) {
					params.Inputs[name] = value
				}
			}
		case element.ConditionalEvent != nil:
			selection = &element.ConditionalEvent.Selection
			for name, value := range element.ConditionalEvent.Variables {
				params.Variables[name] = value
			}
		case element.MessageEvent != nil:
			selection = &element.MessageEvent.Selection
		case element.TimeEvent != nil && e.Timer != nil:
			if changed(element.TimeEvent.Time, e.Timer.Value, !e.Timer.Expression) {
				params.Timer = element.TimeEvent.Time
			}
		case element.Notification != nil:
			for name, value := range map[string]string{"title": element.Notification.Title, "message": element.Notification.Message} {
				if original, ok := inputText(e, name); ok && changed(value, original, true) {
					params.Inputs[name] = value
				}
			}
		}
		if selection != nil {
			s, err := newSelection(*selection)
			if err != nil {
				return input, err
			}
			if element.Task != nil && element.Task.PreferEvent {
				prefer := true
				s.PreferEvent = &prefer
			}
			if !s.Empty() {
				input.Selections[e.Id] = s
			}
		}
		if len(params.Inputs) > 0 || len(params.Variables) > 0 || params.Timer != "" {
			input.Parameters[e.Id] = params
		}
	}
	if start := process.NoneStartEvent(); start != nil {
		defaults := map[string]string{}
		for _, p := range old.StartParameter {
			for _, field := range start.FormFields {
				if field.Id == p.Id && p.Default != field.DefaultValue {
					defaults[p.Id] = p.Default
				}
			}
		}
		if len(defaults) > 0 {
			params := input.Parameters[start.Id]
			params.Defaults = defaults
			input.Parameters[start.Id] = params
		}
	}
	return input, nil
}

// v3Deployment rebuilds a deployment of the engine in the old format
func (this *Handler) v3Deployment(r *http.Request, d model.Deployment, options bool) (models.Deployment, error) {
	version, err := this.c.GetDeploymentVersion(r.Context(), token(r), processes, d.Id, d.Version)
	if err != nil {
		return models.Deployment{}, err
	}
	process, _, err := bpmn.Parse(version.Xml)
	if err != nil {
		return models.Deployment{}, model.BadRequest("%v", err)
	}
	bpmn.ApplyParameters(process, version.Parameters)
	available, err := this.options(r, version.Xml, options)
	if err != nil {
		return models.Deployment{}, err
	}
	return models.Deployment{Version: 3, Id: d.Id, Name: d.Name, Description: d.Description, Diagram: models.Diagram{XmlRaw: version.Xml, XmlDeployed: version.Xml, Svg: version.Svg},
		Elements: elements(process, version.Selections, available), Executable: true,
		IncidentHandling: &models.IncidentHandling{Restart: version.IncidentHandling.Restart, Notify: version.IncidentHandling.Notify}, StartParameter: oldStartParameters(process)}, nil
}

// createDeployment godoc
//
//	@Summary		deploy in the format version 3
//	@Tags		deprecated-deployment
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		source	query	string	false	"source, e.g. sepl"
//	@Param		deployment	body	models.Deployment	true	"deployment"
//	@Success	200	{object}	models.Deployment
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/deployment/v3/deployments [post]
func (this *Handler) createDeployment(w http.ResponseWriter, r *http.Request) {
	old, err := decode[models.Deployment](r)
	if err != nil {
		writeError(w, err)
		return
	}
	input, err := this.deploymentInput(r, old)
	if err != nil {
		writeError(w, err)
		return
	}
	d, err := this.c.CreateDeployment(r.Context(), token(r), processes, input)
	if err != nil {
		writeError(w, err)
		return
	}
	result, err := this.v3Deployment(r, d, false)
	respond(w, result, err)
}

// updateDeployment replaces a deployment; process-deployment ended its running instances, so
// the update strategy is stop
//
//	@Summary		replace a deployment; running instances are stopped
//	@Tags		deprecated-deployment
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"deployment id"
//	@Param		deployment	body	models.Deployment	true	"deployment"
//	@Success	200	{object}	models.Deployment
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/deployment/v3/deployments/{id} [put]
func (this *Handler) updateDeployment(w http.ResponseWriter, r *http.Request) {
	old, err := decode[models.Deployment](r)
	if err != nil {
		writeError(w, err)
		return
	}
	if old.Id != r.PathValue("id") {
		writeError(w, model.BadRequest("the id of the body differs from the path"))
		return
	}
	input, err := this.deploymentInput(r, old)
	if err != nil {
		writeError(w, err)
		return
	}
	input.UpdateStrategy = model.UpdateStop
	d, err := this.c.PutDeployment(r.Context(), token(r), processes, old.Id, input)
	if err != nil {
		writeError(w, err)
		return
	}
	result, err := this.v3Deployment(r, d, false)
	respond(w, result, err)
}

// getV3Deployment godoc
//
//	@Summary		deployment in the format version 3
//	@Tags		deprecated-deployment
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"deployment id"
//	@Param		with_options	query	bool	false	"device options, default true"
//	@Success	200	{object}	models.Deployment
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/deployment/v3/deployments/{id} [get]
func (this *Handler) getV3Deployment(w http.ResponseWriter, r *http.Request) {
	d, err := this.c.GetDeployment(r.Context(), token(r), processes, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	result, err := this.v3Deployment(r, d, withOptions(r))
	respond(w, result, err)
}

// listV3Deployments godoc
//
//	@Summary		deployments in the format version 3
//	@Tags		deprecated-deployment
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		limit	query	int	false	"page size, 0 is all"
//	@Param		offset	query	int	false	"offset"
//	@Param		sort	query	string	false	"name with .asc or .desc"
//	@Success	200	{array}		models.Deployment
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/deployment/v3/deployments [get]
func (this *Handler) listV3Deployments(w http.ResponseWriter, r *http.Request) {
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
	options := model.DeploymentListOptions{NetworkId: "cloud", Sort: sort(r.URL.Query().Get("sort"), map[string]string{"name": "name"})}
	deployments, _, err := paged(limit, offset, func(page model.PageOptions) (model.Page[model.Deployment], error) {
		options.PageOptions = page
		return this.c.ListDeployments(r.Context(), token(r), processes, options)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	result := make([]models.Deployment, 0, len(deployments))
	for _, d := range deployments {
		old, err := this.v3Deployment(r, d, false)
		if err != nil {
			writeError(w, err)
			return
		}
		result = append(result, old)
	}
	writeJson(w, http.StatusOK, result)
}

// deleteV3Deployment godoc
//
//	@Summary		delete a deployment
//	@Tags		deprecated-deployment
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"deployment id"
//	@Success	200	{object}	bool
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/deployment/v3/deployments/{id} [delete]
func (this *Handler) deleteV3Deployment(w http.ResponseWriter, r *http.Request) {
	respond(w, true, this.c.DeleteDeployment(r.Context(), token(r), processes, r.PathValue("id")))
}

// dependencies lists the devices and events a deployment uses; device names are not kept
func (this *Handler) dependencies(r *http.Request, d model.Deployment) (Dependencies, error) {
	result := Dependencies{DeploymentId: d.Id, Owner: d.Owner, Devices: []DeviceDependency{}, Events: []EventDependency{}}
	version, err := this.c.GetDeploymentVersion(r.Context(), token(r), processes, d.Id, d.Version)
	if err != nil {
		return result, err
	}
	index := map[string]int{}
	for _, elementId := range sortedKeys(version.Selections) {
		deviceId := version.Selections[elementId].DeviceId
		if deviceId == "" {
			continue
		}
		i, ok := index[deviceId]
		if !ok {
			i = len(result.Devices)
			index[deviceId] = i
			result.Devices = append(result.Devices, DeviceDependency{DeviceId: deviceId})
		}
		result.Devices[i].BpmnResources = append(result.Devices[i].BpmnResources, BpmnResource{Id: elementId})
	}
	descriptions, err := this.c.ListEventDescriptions(r.Context(), token(r), processes, d.Id)
	if err != nil {
		return result, err
	}
	for _, e := range descriptions {
		result.Events = append(result.Events, EventDependency{EventId: e.Id, BpmnResources: []BpmnResource{{Id: e.ElementId}}})
	}
	return result, nil
}

// getDependencies godoc
//
//	@Summary		devices and events a deployment uses
//	@Tags		deprecated-deployment
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"deployment id"
//	@Success	200	{object}	Dependencies
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/deployment/dependencies/{id} [get]
func (this *Handler) getDependencies(w http.ResponseWriter, r *http.Request) {
	d, err := this.c.GetDeployment(r.Context(), token(r), processes, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	result, err := this.dependencies(r, d)
	respond(w, result, err)
}

// listDependencies godoc
//
//	@Summary		devices and events deployments use
//	@Tags		deprecated-deployment
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		ids	query	string	false	"comma separated deployment ids"
//	@Param		limit	query	int	false	"page size, default 100"
//	@Param		offset	query	int	false	"offset"
//	@Success	200	{array}		Dependencies
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/deployment/dependencies [get]
func (this *Handler) listDependencies(w http.ResponseWriter, r *http.Request) {
	var deployments []model.Deployment
	if r.URL.Query().Has("ids") {
		for _, id := range splitList(r.URL.Query().Get("ids")) {
			d, err := this.c.GetDeployment(r.Context(), token(r), processes, id)
			if errors.Is(err, model.ErrNotFound) || errors.Is(err, model.ErrForbidden) {
				continue
			}
			if err != nil {
				writeError(w, err)
				return
			}
			deployments = append(deployments, d)
		}
	} else {
		limit, err := intParam(r, "limit", 100)
		if err != nil {
			writeError(w, err)
			return
		}
		offset, err := intParam(r, "offset", 0)
		if err != nil {
			writeError(w, err)
			return
		}
		options := model.DeploymentListOptions{NetworkId: "cloud"}
		deployments, _, err = paged(limit, offset, func(page model.PageOptions) (model.Page[model.Deployment], error) {
			options.PageOptions = page
			return this.c.ListDeployments(r.Context(), token(r), processes, options)
		})
		if err != nil {
			writeError(w, err)
			return
		}
	}
	result := make([]Dependencies, 0, len(deployments))
	for _, d := range deployments {
		dependencies, err := this.dependencies(r, d)
		if err != nil {
			writeError(w, err)
			return
		}
		result = append(result, dependencies)
	}
	writeJson(w, http.StatusOK, result)
}
