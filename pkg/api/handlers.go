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

package api

import (
	"net/http"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// listNamespaces godoc
//
//	@Summary	list namespaces
//	@Tags		namespaces
//	@Security	Bearer
//	@Produce	json
//	@Success	200	{array}		model.Namespace
//	@Failure	403	{object}	model.Error
//	@Router		/namespaces [get]
func (h *handlers) listNamespaces(w http.ResponseWriter, r *http.Request) {
	result, err := h.c.ListNamespaces(r.Context(), token(r))
	respond(w, http.StatusOK, result, err)
}

// getNamespace godoc
//
//	@Summary	get a namespace with its rules
//	@Tags		namespaces
//	@Security	Bearer
//	@Produce	json
//	@Param		ns	path		string	true	"namespace"
//	@Success	200	{object}	model.Namespace
//	@Failure	404	{object}	model.Error
//	@Router		/namespaces/{ns} [get]
func (h *handlers) getNamespace(w http.ResponseWriter, r *http.Request) {
	result, err := h.c.GetNamespace(r.Context(), token(r), r.PathValue("ns"))
	respond(w, http.StatusOK, result, err)
}

// ValidateInput is the body of the validate endpoint.
type ValidateInput struct {
	Xml string `json:"xml"`
}

// validate godoc
//
//	@Summary	validate BPMN against the rules of a namespace
//	@Tags		namespaces
//	@Security	Bearer
//	@Accept		json
//	@Produce	json
//	@Param		ns		path		string			true	"namespace"
//	@Param		body	body		ValidateInput	true	"BPMN"
//	@Success	200		{array}		model.Issue
//	@Failure	400		{object}	model.Error
//	@Router		/namespaces/{ns}/validate [post]
func (h *handlers) validate(w http.ResponseWriter, r *http.Request) {
	input, ok := decode[ValidateInput](w, r)
	if !ok {
		return
	}
	result, err := h.c.Validate(r.Context(), token(r), r.PathValue("ns"), input.Xml)
	respond(w, http.StatusOK, result, err)
}

// convert godoc
//
//	@Summary		rewrite a model written for Camunda 7
//	@Description	JUEL becomes JavaScript, external task topics become task types, the http-connector becomes a notification or HTTP call; nothing is stored. The result lists what was rewritten, what to check by hand, what is unsupported and the issues under the rules of the namespace.
//	@Tags			namespaces
//	@Security		Bearer
//	@Accept			json
//	@Produce		json
//	@Param			ns		path		string			true	"namespace"
//	@Param			body	body		ValidateInput	true	"BPMN"
//	@Success		200		{object}	model.Conversion
//	@Failure		400		{object}	model.Error
//	@Router			/namespaces/{ns}/convert [post]
func (h *handlers) convert(w http.ResponseWriter, r *http.Request) {
	input, ok := decode[ValidateInput](w, r)
	if !ok {
		return
	}
	result, err := h.c.Convert(r.Context(), token(r), r.PathValue("ns"), input.Xml)
	respond(w, http.StatusOK, result, err)
}

// listModels godoc
//
//	@Summary	list models
//	@Tags		models
//	@Security	Bearer
//	@Produce	json
//	@Param		ns		path		string	true	"namespace"
//	@Param		limit	query		int		false	"page size, default 100, max 1000"
//	@Param		offset	query		int		false	"offset"
//	@Param		search	query		string	false	"substring of the name"
//	@Param		sort	query		string	false	"name, created_at or updated_at, with .asc or .desc"
//	@Param		ids		query		string	false	"comma separated ids"
//	@Param		owner	query		string	false	"owner"
//	@Param		not_owner	query		string	false	"models of other owners"
//	@Success	200		{object}	model.Page[model.Model]
//	@Failure	400		{object}	model.Error
//	@Router		/namespaces/{ns}/models [get]
func (h *handlers) listModels(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	options := model.ModelListOptions{PageOptions: q.page(), Search: q.str("search"), Sort: q.str("sort"), Ids: q.list("ids"), Owner: q.str("owner"), NotOwner: q.str("not_owner")}
	if q.err != nil {
		writeError(w, q.err)
		return
	}
	result, err := h.c.ListModels(r.Context(), token(r), r.PathValue("ns"), options)
	respond(w, http.StatusOK, result, err)
}

// createModel godoc
//
//	@Summary		create a model
//	@Description	Validation findings are returned as issues; a model with issues is stored but cannot be deployed.
//	@Tags			models
//	@Security		Bearer
//	@Accept			json
//	@Produce		json
//	@Param			ns		path		string				true	"namespace"
//	@Param			body	body		model.ModelInput	true	"model"
//	@Success		201		{object}	model.Model
//	@Failure		400		{object}	model.Error
//	@Router			/namespaces/{ns}/models [post]
func (h *handlers) createModel(w http.ResponseWriter, r *http.Request) {
	input, ok := decode[model.ModelInput](w, r)
	if !ok {
		return
	}
	result, err := h.c.CreateModel(r.Context(), token(r), r.PathValue("ns"), input)
	respond(w, http.StatusCreated, result, err)
}

// getModel godoc
//
//	@Summary	get a model
//	@Tags		models
//	@Security	Bearer
//	@Produce	json
//	@Param		ns	path		string	true	"namespace"
//	@Param		id	path		string	true	"model id"
//	@Success	200	{object}	model.Model
//	@Failure	404	{object}	model.Error
//	@Router		/namespaces/{ns}/models/{id} [get]
func (h *handlers) getModel(w http.ResponseWriter, r *http.Request) {
	result, err := h.c.GetModel(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"))
	respond(w, http.StatusOK, result, err)
}

// updateModel godoc
//
//	@Summary	save a new version of a model
//	@Tags		models
//	@Security	Bearer
//	@Accept		json
//	@Produce	json
//	@Param		ns		path		string				true	"namespace"
//	@Param		id		path		string				true	"model id"
//	@Param		body	body		model.ModelInput	true	"model"
//	@Success	200		{object}	model.Model
//	@Failure	400,403,404	{object}	model.Error
//	@Router		/namespaces/{ns}/models/{id} [put]
func (h *handlers) updateModel(w http.ResponseWriter, r *http.Request) {
	input, ok := decode[model.ModelInput](w, r)
	if !ok {
		return
	}
	result, err := h.c.UpdateModel(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"), input)
	respond(w, http.StatusOK, result, err)
}

// deleteModel godoc
//
//	@Summary		delete a model with all versions
//	@Description	Deployments keep their copy of the BPMN and mark the model as deleted.
//	@Tags			models
//	@Security		Bearer
//	@Param			ns	path	string	true	"namespace"
//	@Param			id	path	string	true	"model id"
//	@Success		204
//	@Failure		403,404	{object}	model.Error
//	@Router			/namespaces/{ns}/models/{id} [delete]
func (h *handlers) deleteModel(w http.ResponseWriter, r *http.Request) {
	respondEmpty(w, h.c.DeleteModel(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id")))
}

// publishModel godoc
//
//	@Summary	publish or unpublish a model
//	@Tags		models
//	@Security	Bearer
//	@Accept		json
//	@Produce	json
//	@Param		ns		path		string				true	"namespace"
//	@Param		id		path		string				true	"model id"
//	@Param		body	body		model.PublishInput	true	"publication"
//	@Success	200		{object}	model.Model
//	@Failure	403,404	{object}	model.Error
//	@Router		/namespaces/{ns}/models/{id}/publish [post]
func (h *handlers) publishModel(w http.ResponseWriter, r *http.Request) {
	input, ok := decode[model.PublishInput](w, r)
	if !ok {
		return
	}
	result, err := h.c.PublishModel(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"), input)
	respond(w, http.StatusOK, result, err)
}

// listModelVersions godoc
//
//	@Summary	list versions of a model, newest first, without XML and SVG
//	@Tags		models
//	@Security	Bearer
//	@Produce	json
//	@Param		ns		path		string	true	"namespace"
//	@Param		id		path		string	true	"model id"
//	@Param		limit	query		int		false	"page size"
//	@Param		offset	query		int		false	"offset"
//	@Success	200		{object}	model.Page[model.ModelVersion]
//	@Failure	404		{object}	model.Error
//	@Router		/namespaces/{ns}/models/{id}/versions [get]
func (h *handlers) listModelVersions(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	page := q.page()
	if q.err != nil {
		writeError(w, q.err)
		return
	}
	result, err := h.c.ListModelVersions(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"), page)
	respond(w, http.StatusOK, result, err)
}

// getModelVersion godoc
//
//	@Summary	get a version of a model
//	@Tags		models
//	@Security	Bearer
//	@Produce	json
//	@Param		ns		path		string	true	"namespace"
//	@Param		id		path		string	true	"model id"
//	@Param		version	path		int		true	"version"
//	@Success	200		{object}	model.ModelVersion
//	@Failure	404		{object}	model.Error
//	@Router		/namespaces/{ns}/models/{id}/versions/{version} [get]
func (h *handlers) getModelVersion(w http.ResponseWriter, r *http.Request) {
	version, err := pathInt(r, "version")
	if err != nil {
		writeError(w, err)
		return
	}
	result, err := h.c.GetModelVersion(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"), version)
	respond(w, http.StatusOK, result, err)
}

// prepareDeployment godoc
//
//	@Summary		prepare a deployment
//	@Description	Lists start parameters, validation findings and, per device task and device event, the devices, device groups and imports that can be selected.
//	@Tags			deployments
//	@Security		Bearer
//	@Accept			json
//	@Produce		json
//	@Param			ns		path		string				true	"namespace"
//	@Param			body	body		model.PrepareInput	true	"model version or BPMN"
//	@Success		200		{object}	model.PreparedDeployment
//	@Failure		400,403,404,503	{object}	model.Error
//	@Router			/namespaces/{ns}/prepared-deployments [post]
func (h *handlers) prepareDeployment(w http.ResponseWriter, r *http.Request) {
	input, ok := decode[model.PrepareInput](w, r)
	if !ok {
		return
	}
	result, err := h.c.PrepareDeployment(r.Context(), token(r), r.PathValue("ns"), input)
	respond(w, http.StatusOK, result, err)
}

// listDeployments godoc
//
//	@Summary	list deployments
//	@Tags		deployments
//	@Security	Bearer
//	@Produce	json
//	@Param		ns			path		string	true	"namespace"
//	@Param		limit		query		int		false	"page size"
//	@Param		offset		query		int		false	"offset"
//	@Param		search		query		string	false	"substring of the name"
//	@Param		sort		query		string	false	"name, created_at or updated_at, with .asc or .desc"
//	@Param		ids			query		string	false	"comma separated ids"
//	@Param		model_id	query		string	false	"model id"
//	@Param		owner		query		string	false	"owner"
//	@Param		source		query		string	false	"source, e.g. generated"
//	@Param		network_id	query		string	false	"cloud for deployments of the cloud, a network id for those of a gateway"
//	@Success	200			{object}	model.Page[model.Deployment]
//	@Failure	400			{object}	model.Error
//	@Router		/namespaces/{ns}/deployments [get]
func (h *handlers) listDeployments(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	options := model.DeploymentListOptions{PageOptions: q.page(), Search: q.str("search"), Sort: q.str("sort"), Ids: q.list("ids"), ModelId: q.str("model_id"), Owner: q.str("owner"), Source: q.str("source"), NetworkId: q.str("network_id")}
	if q.err != nil {
		writeError(w, q.err)
		return
	}
	result, err := h.c.ListDeployments(r.Context(), token(r), r.PathValue("ns"), options)
	respond(w, http.StatusOK, result, err)
}

// createDeployment godoc
//
//	@Summary		deploy a model version or own BPMN
//	@Description	Rejected with a validation error naming the elements if the BPMN is outside the supported subset or the rules of the namespace.
//	@Tags			deployments
//	@Security		Bearer
//	@Accept			json
//	@Produce		json
//	@Param			ns		path		string					true	"namespace"
//	@Param			body	body		model.DeploymentInput	true	"deployment"
//	@Success		201		{object}	model.Deployment
//	@Failure		400,403,404	{object}	model.Error
//	@Router			/namespaces/{ns}/deployments [post]
func (h *handlers) createDeployment(w http.ResponseWriter, r *http.Request) {
	input, ok := decode[model.DeploymentInput](w, r)
	if !ok {
		return
	}
	result, err := h.c.CreateDeployment(r.Context(), token(r), r.PathValue("ns"), input)
	respond(w, http.StatusCreated, result, err)
}

// getDeployment godoc
//
//	@Summary	get a deployment
//	@Tags		deployments
//	@Security	Bearer
//	@Produce	json
//	@Param		ns	path		string	true	"namespace"
//	@Param		id	path		string	true	"deployment id"
//	@Success	200	{object}	model.Deployment
//	@Failure	404	{object}	model.Error
//	@Router		/namespaces/{ns}/deployments/{id} [get]
func (h *handlers) getDeployment(w http.ResponseWriter, r *http.Request) {
	result, err := h.c.GetDeployment(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"))
	respond(w, http.StatusOK, result, err)
}

// putDeployment godoc
//
//	@Summary		create a deployment with this id or update it
//	@Description	An update creates a new version and needs update_strategy (stop, drain or restart) for the running instances.
//	@Tags			deployments
//	@Security		Bearer
//	@Accept			json
//	@Produce		json
//	@Param			ns		path		string					true	"namespace"
//	@Param			id		path		string					true	"deployment id"
//	@Param			body	body		model.DeploymentInput	true	"deployment"
//	@Success		200		{object}	model.Deployment
//	@Failure		400,403,404,409	{object}	model.Error
//	@Router			/namespaces/{ns}/deployments/{id} [put]
func (h *handlers) putDeployment(w http.ResponseWriter, r *http.Request) {
	input, ok := decode[model.DeploymentInput](w, r)
	if !ok {
		return
	}
	result, err := h.c.PutDeployment(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"), input)
	respond(w, http.StatusOK, result, err)
}

// deleteDeployment godoc
//
//	@Summary	delete a deployment with all versions, instances, history and incidents
//	@Tags		deployments
//	@Security	Bearer
//	@Param		ns	path	string	true	"namespace"
//	@Param		id	path	string	true	"deployment id"
//	@Success	204
//	@Failure	403,404	{object}	model.Error
//	@Router		/namespaces/{ns}/deployments/{id} [delete]
func (h *handlers) deleteDeployment(w http.ResponseWriter, r *http.Request) {
	respondEmpty(w, h.c.DeleteDeployment(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id")))
}

// listDeploymentVersions godoc
//
//	@Summary	list versions of a deployment, newest first, without XML and SVG
//	@Tags		deployments
//	@Security	Bearer
//	@Produce	json
//	@Param		ns		path		string	true	"namespace"
//	@Param		id		path		string	true	"deployment id"
//	@Param		limit	query		int		false	"page size"
//	@Param		offset	query		int		false	"offset"
//	@Success	200		{object}	model.Page[model.DeploymentVersion]
//	@Failure	404		{object}	model.Error
//	@Router		/namespaces/{ns}/deployments/{id}/versions [get]
func (h *handlers) listDeploymentVersions(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	page := q.page()
	if q.err != nil {
		writeError(w, q.err)
		return
	}
	result, err := h.c.ListDeploymentVersions(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"), page)
	respond(w, http.StatusOK, result, err)
}

// getDeploymentVersion godoc
//
//	@Summary	get a version of a deployment with BPMN and SVG
//	@Tags		deployments
//	@Security	Bearer
//	@Produce	json
//	@Param		ns		path		string	true	"namespace"
//	@Param		id		path		string	true	"deployment id"
//	@Param		version	path		int		true	"version"
//	@Success	200		{object}	model.DeploymentVersion
//	@Failure	404		{object}	model.Error
//	@Router		/namespaces/{ns}/deployments/{id}/versions/{version} [get]
func (h *handlers) getDeploymentVersion(w http.ResponseWriter, r *http.Request) {
	version, err := pathInt(r, "version")
	if err != nil {
		writeError(w, err)
		return
	}
	result, err := h.c.GetDeploymentVersion(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"), version)
	respond(w, http.StatusOK, result, err)
}

// getStartParameters godoc
//
//	@Summary	start parameters of the current version, without fields marked ignore_on_start
//	@Tags		deployments
//	@Security	Bearer
//	@Produce	json
//	@Param		ns	path		string	true	"namespace"
//	@Param		id	path		string	true	"deployment id"
//	@Success	200	{array}		model.StartParameter
//	@Failure	404	{object}	model.Error
//	@Router		/namespaces/{ns}/deployments/{id}/start-parameters [get]
func (h *handlers) getStartParameters(w http.ResponseWriter, r *http.Request) {
	result, err := h.c.GetStartParameters(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"))
	respond(w, http.StatusOK, result, err)
}

// listInstances godoc
//
//	@Summary	list running and finished instances
//	@Tags		instances
//	@Security	Bearer
//	@Produce	json
//	@Param		ns				path		string	true	"namespace"
//	@Param		limit			query		int		false	"page size"
//	@Param		offset			query		int		false	"offset"
//	@Param		state			query		string	false	"running, finished, completed, terminated or incident"
//	@Param		deployment_id	query		string	false	"deployment id"
//	@Param		deployment_name	query		string	false	"substring of the deployment name"
//	@Param		model_id		query		string	false	"model id"
//	@Param		ids				query		string	false	"comma separated instance ids"
//	@Param		owner			query		string	false	"owner"
//	@Param		sort			query		string	false	"started_at, ended_at, created_at, deployment_name or state, with .asc or .desc"
//	@Param		network_id		query		string	false	"cloud for instances of the cloud, a network id for those of a gateway"
//	@Success	200				{object}	model.Page[model.Instance]
//	@Failure	400				{object}	model.Error
//	@Router		/namespaces/{ns}/instances [get]
func (h *handlers) listInstances(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	options := model.InstanceListOptions{
		PageOptions: q.page(), State: q.str("state"), DeploymentId: q.str("deployment_id"), DeploymentName: q.str("deployment_name"),
		ModelId: q.str("model_id"), Ids: q.list("ids"), Owner: q.str("owner"), Sort: q.str("sort"), NetworkId: q.str("network_id"),
	}
	if q.err != nil {
		writeError(w, q.err)
		return
	}
	result, err := h.c.ListInstances(r.Context(), token(r), r.PathValue("ns"), options)
	respond(w, http.StatusOK, result, err)
}

// startInstance godoc
//
//	@Summary	start an instance with a generated id
//	@Tags		instances
//	@Security	Bearer
//	@Accept		json
//	@Produce	json
//	@Param		ns		path		string						true	"namespace"
//	@Param		body	body		model.StartInstanceInput	true	"start"
//	@Success	201		{object}	model.Instance
//	@Failure	400,403,404	{object}	model.Error
//	@Router		/namespaces/{ns}/instances [post]
func (h *handlers) startInstance(w http.ResponseWriter, r *http.Request) {
	input, ok := decode[model.StartInstanceInput](w, r)
	if !ok {
		return
	}
	result, err := h.c.StartInstance(r.Context(), token(r), r.PathValue("ns"), input)
	respond(w, http.StatusCreated, result, err)
}

// putInstance godoc
//
//	@Summary		start an instance with this id, idempotently
//	@Description	The same start again returns the existing instance; a start with different content is a conflict.
//	@Tags			instances
//	@Security		Bearer
//	@Accept			json
//	@Produce		json
//	@Param			ns		path		string						true	"namespace"
//	@Param			id		path		string						true	"instance id"
//	@Param			body	body		model.StartInstanceInput	true	"start"
//	@Success		200		{object}	model.Instance
//	@Failure		400,403,404,409	{object}	model.Error
//	@Router			/namespaces/{ns}/instances/{id} [put]
func (h *handlers) putInstance(w http.ResponseWriter, r *http.Request) {
	input, ok := decode[model.StartInstanceInput](w, r)
	if !ok {
		return
	}
	result, err := h.c.PutInstance(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"), input)
	respond(w, http.StatusOK, result, err)
}

// updateInstanceInfo godoc
//
//	@Summary	change name and description of an instance
//	@Tags		instances
//	@Security	Bearer
//	@Accept		json
//	@Produce	json
//	@Param		ns		path		string					true	"namespace"
//	@Param		id		path		string					true	"instance id"
//	@Param		body	body		model.InstanceInfoInput	true	"name and description"
//	@Success	200		{object}	model.Instance
//	@Failure	404		{object}	model.Error
//	@Router		/namespaces/{ns}/instances/{id}/info [put]
func (h *handlers) updateInstanceInfo(w http.ResponseWriter, r *http.Request) {
	input, ok := decode[model.InstanceInfoInput](w, r)
	if !ok {
		return
	}
	result, err := h.c.UpdateInstanceInfo(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"), input)
	respond(w, http.StatusOK, result, err)
}

// getInstance godoc
//
//	@Summary	get an instance
//	@Tags		instances
//	@Security	Bearer
//	@Produce	json
//	@Param		ns	path		string	true	"namespace"
//	@Param		id	path		string	true	"instance id"
//	@Success	200	{object}	model.Instance
//	@Failure	404	{object}	model.Error
//	@Router		/namespaces/{ns}/instances/{id} [get]
func (h *handlers) getInstance(w http.ResponseWriter, r *http.Request) {
	result, err := h.c.GetInstance(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"))
	respond(w, http.StatusOK, result, err)
}

// deleteInstance godoc
//
//	@Summary	delete an instance with its history
//	@Tags		instances
//	@Security	Bearer
//	@Param		ns	path	string	true	"namespace"
//	@Param		id	path	string	true	"instance id"
//	@Success	204
//	@Failure	403,404	{object}	model.Error
//	@Router		/namespaces/{ns}/instances/{id} [delete]
func (h *handlers) deleteInstance(w http.ResponseWriter, r *http.Request) {
	respondEmpty(w, h.c.DeleteInstances(r.Context(), token(r), r.PathValue("ns"), []string{r.PathValue("id")}))
}

// deleteInstances godoc
//
//	@Summary	delete several instances with their history
//	@Tags		instances
//	@Security	Bearer
//	@Param		ns	path	string	true	"namespace"
//	@Param		ids	query	string	true	"comma separated instance ids"
//	@Success	204
//	@Failure	400,403,404	{object}	model.Error
//	@Router		/namespaces/{ns}/instances [delete]
func (h *handlers) deleteInstances(w http.ResponseWriter, r *http.Request) {
	respondEmpty(w, h.c.DeleteInstances(r.Context(), token(r), r.PathValue("ns"), newQuery(r).list("ids")))
}

// stopInstances godoc
//
//	@Summary	stop several instances
//	@Tags		instances
//	@Security	Bearer
//	@Accept		json
//	@Param		ns		path	string			true	"namespace"
//	@Param		body	body	model.IdsInput	true	"instance ids"
//	@Success	204
//	@Failure	400,403,404	{object}	model.Error
//	@Router		/namespaces/{ns}/instances/stop [post]
func (h *handlers) stopInstances(w http.ResponseWriter, r *http.Request) {
	input, ok := decode[model.IdsInput](w, r)
	if !ok {
		return
	}
	respondEmpty(w, h.c.StopInstances(r.Context(), token(r), r.PathValue("ns"), input.Ids))
}

// stopInstance godoc
//
//	@Summary	stop an instance; its history stays
//	@Tags		instances
//	@Security	Bearer
//	@Param		ns	path	string	true	"namespace"
//	@Param		id	path	string	true	"instance id"
//	@Success	204
//	@Failure	403,404	{object}	model.Error
//	@Router		/namespaces/{ns}/instances/{id}/stop [post]
func (h *handlers) stopInstance(w http.ResponseWriter, r *http.Request) {
	respondEmpty(w, h.c.StopInstances(r.Context(), token(r), r.PathValue("ns"), []string{r.PathValue("id")}))
}

// restartInstance godoc
//
//	@Summary	start a new run of an instance with its start variables
//	@Tags		instances
//	@Security	Bearer
//	@Accept		json
//	@Produce	json
//	@Param		ns		path		string				true	"namespace"
//	@Param		id		path		string				true	"instance id"
//	@Param		body	body		model.RestartInput	false	"deployment version, 0 for the current"
//	@Success	200		{object}	model.Instance
//	@Failure	400,403,404	{object}	model.Error
//	@Router		/namespaces/{ns}/instances/{id}/restart [post]
func (h *handlers) restartInstance(w http.ResponseWriter, r *http.Request) {
	input, ok := decode[model.RestartInput](w, r)
	if !ok {
		return
	}
	result, err := h.c.RestartInstance(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"), input)
	respond(w, http.StatusOK, result, err)
}

// listRuns godoc
//
//	@Summary	list the runs of an instance
//	@Tags		instances
//	@Security	Bearer
//	@Produce	json
//	@Param		ns	path		string	true	"namespace"
//	@Param		id	path		string	true	"instance id"
//	@Success	200	{array}		model.Run
//	@Failure	404	{object}	model.Error
//	@Router		/namespaces/{ns}/instances/{id}/runs [get]
func (h *handlers) listRuns(w http.ResponseWriter, r *http.Request) {
	result, err := h.c.ListRuns(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"))
	respond(w, http.StatusOK, result, err)
}

// getVariables godoc
//
//	@Summary	variables of the current run
//	@Tags		instances
//	@Security	Bearer
//	@Produce	json
//	@Param		ns	path		string	true	"namespace"
//	@Param		id	path		string	true	"instance id"
//	@Success	200	{array}		model.Variable
//	@Failure	404	{object}	model.Error
//	@Router		/namespaces/{ns}/instances/{id}/variables [get]
func (h *handlers) getVariables(w http.ResponseWriter, r *http.Request) {
	result, err := h.c.GetVariables(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"))
	respond(w, http.StatusOK, result, err)
}

// setVariable godoc
//
//	@Summary	set a variable of a running instance
//	@Tags		instances
//	@Security	Bearer
//	@Accept		json
//	@Param		ns		path	string				true	"namespace"
//	@Param		id		path	string				true	"instance id"
//	@Param		name	path	string				true	"variable name"
//	@Param		body	body	model.VariableInput	true	"value"
//	@Success	204
//	@Failure	400,403,404,409	{object}	model.Error
//	@Router		/namespaces/{ns}/instances/{id}/variables/{name} [put]
func (h *handlers) setVariable(w http.ResponseWriter, r *http.Request) {
	input, ok := decode[model.VariableInput](w, r)
	if !ok {
		return
	}
	respondEmpty(w, h.c.SetVariable(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"), r.PathValue("name"), input.Value))
}

// listActivities godoc
//
//	@Summary	activity log of an instance in sequence order
//	@Tags		instances
//	@Security	Bearer
//	@Produce	json
//	@Param		ns			path		string	true	"namespace"
//	@Param		id			path		string	true	"instance id"
//	@Param		run			query		int		false	"run, 0 for all"
//	@Param		after_seq	query		int		false	"entries after this sequence number"
//	@Param		limit		query		int		false	"max entries, default and max 1000"
//	@Param		kind		query		string	false	"activity kind"
//	@Param		element_id	query		string	false	"BPMN element id"
//	@Success	200			{object}	model.ActivityPage
//	@Failure	404			{object}	model.Error
//	@Router		/namespaces/{ns}/instances/{id}/activities [get]
func (h *handlers) listActivities(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	options := model.ActivityListOptions{Run: q.int("run"), AfterSeq: q.int64("after_seq"), Limit: q.int("limit"), Kind: q.str("kind"), ElementId: q.str("element_id")}
	if q.err != nil {
		writeError(w, q.err)
		return
	}
	result, err := h.c.ListActivities(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"), options)
	respond(w, http.StatusOK, result, err)
}

// listElementStats godoc
//
//	@Summary	pass counters and last errors per element and taken sequence flow
//	@Tags		instances
//	@Security	Bearer
//	@Produce	json
//	@Param		ns	path		string	true	"namespace"
//	@Param		id	path		string	true	"instance id"
//	@Param		run	query		int		false	"run, 0 for all"
//	@Success	200	{array}		model.ElementStats
//	@Failure	404	{object}	model.Error
//	@Router		/namespaces/{ns}/instances/{id}/element-stats [get]
func (h *handlers) listElementStats(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	run := q.int("run")
	if q.err != nil {
		writeError(w, q.err)
		return
	}
	result, err := h.c.ListElementStats(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"), run)
	respond(w, http.StatusOK, result, err)
}

// listIncidents godoc
//
//	@Summary	list incidents
//	@Tags		incidents
//	@Security	Bearer
//	@Produce	json
//	@Param		ns				path		string	true	"namespace"
//	@Param		limit			query		int		false	"page size"
//	@Param		offset			query		int		false	"offset"
//	@Param		instance_id		query		string	false	"instance id"
//	@Param		run				query		int		false	"run"
//	@Param		deployment_id	query		string	false	"deployment id"
//	@Param		element_id		query		string	false	"BPMN element id"
//	@Param		sort			query		string	false	"time, deployment_id or element_id, with .asc or .desc"
//	@Success	200				{object}	model.Page[model.Incident]
//	@Failure	400				{object}	model.Error
//	@Router		/namespaces/{ns}/incidents [get]
func (h *handlers) listIncidents(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	options := model.IncidentListOptions{
		PageOptions: q.page(), InstanceId: q.str("instance_id"), Run: q.int("run"), DeploymentId: q.str("deployment_id"),
		ElementId: q.str("element_id"), Sort: q.str("sort"),
	}
	if q.err != nil {
		writeError(w, q.err)
		return
	}
	result, err := h.c.ListIncidents(r.Context(), token(r), r.PathValue("ns"), options)
	respond(w, http.StatusOK, result, err)
}

// correlate godoc
//
//	@Summary		correlate a message
//	@Description	Delivers a message to waiting catch events and message start events. A message without target is not an error.
//	@Tags			messages
//	@Security		Bearer
//	@Accept			json
//	@Produce		json
//	@Param			body	body		model.MessageInput	true	"message"
//	@Success		200		{object}	model.CorrelationResult
//	@Failure		400,403,409	{object}	model.Error
//	@Router			/messages [post]
func (h *handlers) correlate(w http.ResponseWriter, r *http.Request) {
	input, ok := decode[model.MessageInput](w, r)
	if !ok {
		return
	}
	result, err := h.c.CorrelateMessage(r.Context(), token(r), input)
	respond(w, http.StatusOK, result, err)
}
