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

// listModules godoc
//
//	@Summary		modules of instances
//	@Description	Resources instances created through task types, e.g. deployments, imports or pipelines (SMS-12); only of instances the caller may read.
//	@Tags			modules
//	@Security		Bearer
//	@Produce		json
//	@Param			ns			path		string	true	"namespace"
//	@Param			instance_id	query		string	false	"modules of this instance"
//	@Param			module_type	query		string	false	"module type"
//	@Param			key			query		string	false	"modules with this key"
//	@Param			limit		query		int		false	"page size"
//	@Param			offset		query		int		false	"page offset"
//	@Success		200			{object}	model.Page[model.Module]
//	@Router			/namespaces/{ns}/modules [get]
func (h *handlers) listModules(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	options := model.ModuleListOptions{PageOptions: q.page(), InstanceId: q.str("instance_id"), ModuleType: q.str("module_type"), Key: q.str("key")}
	if q.err != nil {
		writeError(w, q.err)
		return
	}
	result, err := h.c.ListModules(r.Context(), token(r), r.PathValue("ns"), options)
	respond(w, http.StatusOK, result, err)
}

// getModule godoc
//
//	@Summary	module
//	@Tags		modules
//	@Security	Bearer
//	@Produce	json
//	@Param		ns	path		string	true	"namespace"
//	@Param		id	path		string	true	"module id"
//	@Success	200	{object}	model.Module
//	@Failure	404	{object}	model.Error
//	@Router		/namespaces/{ns}/modules/{id} [get]
func (h *handlers) getModule(w http.ResponseWriter, r *http.Request) {
	result, err := h.c.GetModule(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"))
	respond(w, http.StatusOK, result, err)
}

// deleteModule godoc
//
//	@Summary		delete a module
//	@Description	Needs administrate on the instance. The resource is removed afterwards, with retries until it succeeds (SMS-06).
//	@Tags			modules
//	@Security		Bearer
//	@Param			ns	path	string	true	"namespace"
//	@Param			id	path	string	true	"module id"
//	@Success		204
//	@Failure		404	{object}	model.Error
//	@Router			/namespaces/{ns}/modules/{id} [delete]
func (h *handlers) deleteModule(w http.ResponseWriter, r *http.Request) {
	respondEmpty(w, h.c.DeleteModule(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id")))
}
