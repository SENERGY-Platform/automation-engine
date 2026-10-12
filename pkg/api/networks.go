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

import "net/http"

// listNetworks godoc
//
//	@Summary		gateways the cloud synchronises with
//	@Description	Gateways the caller may read (permissions-v2 topic hubs), with last contact, versions, backlog of the synchronisation in both directions and storage (SYNC-12).
//	@Tags			networks
//	@Security		Bearer
//	@Produce		json
//	@Success		200	{array}	model.Network
//	@Router			/networks [get]
func (h *handlers) listNetworks(w http.ResponseWriter, r *http.Request) {
	result, err := h.c.ListNetworks(r.Context(), token(r))
	respond(w, http.StatusOK, result, err)
}

// getNetwork godoc
//
//	@Summary		a gateway with the desired and applied state of its resources
//	@Description	Per namespace, deployment and instance: the version the cloud desires, the version the gateway applied and its error (SYNC-13).
//	@Tags			networks
//	@Security		Bearer
//	@Produce		json
//	@Param			id	path		string	true	"network id, the hub id"
//	@Success		200	{object}	model.Network
//	@Failure		404	{object}	model.Error
//	@Router			/networks/{id} [get]
func (h *handlers) getNetwork(w http.ResponseWriter, r *http.Request) {
	result, err := h.c.GetNetwork(r.Context(), token(r), r.PathValue("id"))
	respond(w, http.StatusOK, result, err)
}
