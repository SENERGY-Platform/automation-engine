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

// putNamespace godoc
//
//	@Summary		create or change a namespace
//	@Description	Admins only. Writes the permissions-v2 topics of the namespace first.
//	@Tags			namespaces
//	@Security		Bearer
//	@Accept			json
//	@Produce		json
//	@Param			ns		path		string			true	"namespace"
//	@Param			body	body		model.Namespace	true	"namespace"
//	@Success		200		{object}	model.Namespace
//	@Failure		400,403,503	{object}	model.Error
//	@Router			/namespaces/{ns} [put]
func (h *handlers) putNamespace(w http.ResponseWriter, r *http.Request) {
	input, ok := decode[model.Namespace](w, r)
	if !ok {
		return
	}
	result, err := h.c.PutNamespace(r.Context(), token(r), r.PathValue("ns"), input)
	respond(w, http.StatusOK, result, err)
}

// deleteNamespace godoc
//
//	@Summary		delete a namespace without models, deployments and instances
//	@Description	Admins only.
//	@Tags			namespaces
//	@Security		Bearer
//	@Param			ns	path	string	true	"namespace"
//	@Success		204
//	@Failure		403,404,409	{object}	model.Error
//	@Router			/namespaces/{ns} [delete]
func (h *handlers) deleteNamespace(w http.ResponseWriter, r *http.Request) {
	respondEmpty(w, h.c.DeleteNamespace(r.Context(), token(r), r.PathValue("ns")))
}

// setOwner godoc
//
//	@Summary		change the owner of a model, deployment or instance
//	@Description	Allowed for admins and users who administrate the resource; the new owner must administrate it already.
//	@Tags			owners
//	@Security		Bearer
//	@Accept			json
//	@Param			ns		path	string				true	"namespace"
//	@Param			kind	path	string				true	"models, deployments or instances"
//	@Param			id		path	string				true	"resource id"
//	@Param			body	body	model.OwnerInput	true	"new owner"
//	@Success		204
//	@Failure		400,403,404	{object}	model.Error
//	@Router			/namespaces/{ns}/{kind}/{id}/owner [put]
func (h *handlers) setOwner(kind model.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		input, ok := decode[model.OwnerInput](w, r)
		if !ok {
			return
		}
		respondEmpty(w, h.c.SetOwner(r.Context(), token(r), r.PathValue("ns"), kind, r.PathValue("id"), input))
	}
}

// resetRestarts godoc
//
//	@Summary		allow automatic restarts again after a restart loop
//	@Tags			deployments
//	@Security		Bearer
//	@Produce		json
//	@Param			ns	path		string	true	"namespace"
//	@Param			id	path		string	true	"deployment id"
//	@Success		200	{object}	model.Deployment
//	@Failure		403,404	{object}	model.Error
//	@Router			/namespaces/{ns}/deployments/{id}/reset-restarts [post]
func (h *handlers) resetRestarts(w http.ResponseWriter, r *http.Request) {
	result, err := h.c.ResetRestarts(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"))
	respond(w, http.StatusOK, result, err)
}

// getUserDependents godoc
//
//	@Summary		dry run of a user deletion
//	@Description	Admins only. Lists own resources and resources of others that depend on them; blocked is true if the deletion would be rejected.
//	@Tags			users
//	@Security		Bearer
//	@Produce		json
//	@Param			id	path		string	true	"user id"
//	@Success		200	{object}	model.UserDependents
//	@Failure		403	{object}	model.Error
//	@Router			/users/{id}/dependents [get]
func (h *handlers) getUserDependents(w http.ResponseWriter, r *http.Request) {
	result, err := h.c.GetUserDependents(r.Context(), token(r), r.PathValue("id"))
	respond(w, http.StatusOK, result, err)
}

// deleteUser godoc
//
//	@Summary		delete everything a user owns
//	@Description	Admins only. Removes models, deployments and instances of the user in all namespaces and the user's rights on resources of others. Answers 409 with the dependents if a namespace rejects them.
//	@Tags			users
//	@Security		Bearer
//	@Param			id	path	string	true	"user id"
//	@Success		204
//	@Failure		403,409,503	{object}	model.Error
//	@Router			/users/{id} [delete]
func (h *handlers) deleteUser(w http.ResponseWriter, r *http.Request) {
	respondEmpty(w, h.c.DeleteUser(r.Context(), token(r), r.PathValue("id")))
}

// getUserSettings godoc
//
//	@Summary	limits of a user
//	@Tags		users
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path		string	true	"user id"
//	@Success	200	{object}	model.UserSettings
//	@Failure	403	{object}	model.Error
//	@Router		/users/{id}/settings [get]
func (h *handlers) getUserSettings(w http.ResponseWriter, r *http.Request) {
	result, err := h.c.GetUserSettings(r.Context(), token(r), r.PathValue("id"))
	respond(w, http.StatusOK, result, err)
}

// setUserSettings godoc
//
//	@Summary	set limits of a user, admins only
//	@Tags		users
//	@Security	Bearer
//	@Accept		json
//	@Produce	json
//	@Param		id		path		string				true	"user id"
//	@Param		body	body		model.UserSettings	true	"settings"
//	@Success	200		{object}	model.UserSettings
//	@Failure	400,403	{object}	model.Error
//	@Router		/users/{id}/settings [put]
func (h *handlers) setUserSettings(w http.ResponseWriter, r *http.Request) {
	input, ok := decode[model.UserSettings](w, r)
	if !ok {
		return
	}
	result, err := h.c.SetUserSettings(r.Context(), token(r), r.PathValue("id"), input)
	respond(w, http.StatusOK, result, err)
}

// listEventDescriptions godoc
//
//	@Summary	event descriptions of a deployment, as event workers evaluate them
//	@Tags		deployments
//	@Security	Bearer
//	@Produce	json
//	@Param		ns	path		string	true	"namespace"
//	@Param		id	path		string	true	"deployment id"
//	@Success	200	{array}		model.EventDescription
//	@Failure	404	{object}	model.Error
//	@Router		/namespaces/{ns}/deployments/{id}/event-descriptions [get]
func (h *handlers) listEventDescriptions(w http.ResponseWriter, r *http.Request) {
	result, err := h.c.ListEventDescriptions(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"))
	respond(w, http.StatusOK, result, err)
}
