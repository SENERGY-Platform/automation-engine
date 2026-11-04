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
//	@Description	Allowed for admins and users who administrate the resource; the new owner must administrate it already. With 204 the owner holds all rights in permissions-v2. 503 means the owner is changed but its rights are not written yet; the engine tries again every second, and repeating the call writes them at once.
//	@Tags			owners
//	@Security		Bearer
//	@Accept			json
//	@Param			ns		path	string				true	"namespace"
//	@Param			kind	path	string				true	"models, deployments or instances"
//	@Param			id		path	string				true	"resource id"
//	@Param			body	body	model.OwnerInput	true	"new owner"
//	@Success		204
//	@Failure		400,403,404,503	{object}	model.Error
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
//	@Description	Clears the faulty mark (`faulty_since`) set after a restart loop (INC-04). For a deployment of a gateway, the gateway counts the restarts and reports the mark with its status, so the reset is done there first and needs the right x on the gateway; an unreachable gateway answers 503 and nothing changes.
//	@Tags			deployments
//	@Security		Bearer
//	@Produce		json
//	@Param			ns	path		string	true	"namespace"
//	@Param			id	path		string	true	"deployment id"
//	@Success		200	{object}	model.Deployment
//	@Failure		403,404,503	{object}	model.Error
//	@Router			/namespaces/{ns}/deployments/{id}/reset-restarts [post]
func (h *handlers) resetRestarts(w http.ResponseWriter, r *http.Request) {
	result, err := h.c.ResetRestarts(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"))
	respond(w, http.StatusOK, result, err)
}

// listBlockedScripts godoc
//
//	@Summary		scripts that ended the engine
//	@Description	Admins only. A script that ended the engine with a fatal error, e.g. a stack overflow in goja, is refused on save and deploy and not run, also in copies that differ only in comments and whitespace; the stored deployments containing it were quarantined and their running instances stopped (EXPR-03).
//	@Tags			blocklist
//	@Security		Bearer
//	@Produce		json
//	@Success		200	{array}		model.BlockedScript
//	@Failure		403	{object}	model.Error
//	@Router			/blocked-scripts [get]
func (h *handlers) listBlockedScripts(w http.ResponseWriter, r *http.Request) {
	result, err := h.c.ListBlockedScripts(r.Context(), token(r))
	respond(w, http.StatusOK, result, err)
}

// unblockScript godoc
//
//	@Summary		remove a script from the blocklist
//	@Description	Admins only. The script may be saved, deployed and run again; quarantined deployments and stopped instances stay as they are, their owners reset or restart them.
//	@Tags			blocklist
//	@Security		Bearer
//	@Param			fingerprint	path	string	true	"fingerprint of the script"
//	@Success		204
//	@Failure		403,404	{object}	model.Error
//	@Router			/blocked-scripts/{fingerprint} [delete]
func (h *handlers) unblockScript(w http.ResponseWriter, r *http.Request) {
	respondEmpty(w, h.c.UnblockScript(r.Context(), token(r), r.PathValue("fingerprint")))
}

// getUserDependents godoc
//
//	@Summary		dry run of a user deletion
//	@Description	Admins only. Lists own resources and resources of others that depend on them: instances of others on the user's deployments, deployments of others that an instance deleted with the user manages through a module (e.g. a process a smart service deployed to someone else's gateway), and instances of others on those. blocked is true if the namespace of a dependent rejects them (user_deletion).
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
//	@Description	Admins only. Removes models, deployments and instances of the user in all namespaces and the user's rights on resources of others. Answers 409 with the dependents if a namespace rejects them; with user_deletion cascade they are deleted too, on gateways as well, and their owners notified.
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
