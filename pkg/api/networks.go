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

// an export holds XML and SVG of every version of every deployment of a gateway
const maxImportBytes = 256 << 20

// importNetwork godoc
//
//	@Summary		take over the deployments of a gateway into a new cloud
//	@Description	Admins only. The body is the output of "automation-engine export-gateway" on the gateway, unchanged. Deployments are created with their ids, versions, event descriptions and device groups, for the owner of the hub in device-repository, and desired on the gateway in the version it runs, so that it only replaces their event descriptions; deployments that exist stay as they are. Missing process models are created from the deployment versions, also those deleted in the former cloud, since a gateway never learns of the deletion. Namespaces come from the configuration of the cloud; deployments of unknown or cloud-only namespaces are skipped. The gateway then sends all its instances again. Everything happens in one transaction (SNRGY-5066).
//	@Tags			networks
//	@Security		Bearer
//	@Accept			json
//	@Produce		json
//	@Param			id						path		string				true	"network id, the hub id"
//	@Param			dry_run					query		bool				false	"report without writing"
//	@Param			create_network			query		bool				false	"create the network if the cloud has never heard from the gateway, with the import as its last contact; otherwise the first contact creates it"
//	@Param			body					body		model.GatewayExport	true	"export of the gateway"
//	@Success		200						{object}	model.NetworkImport
//	@Failure		400,403,422,503			{object}	model.Error
//	@Router			/networks/{id}/import [put]
func (h *handlers) importNetwork(w http.ResponseWriter, r *http.Request) {
	doc, ok := decodeUpTo[model.GatewayExport](w, r, maxImportBytes)
	if !ok {
		return
	}
	// a value that is not read as intended must not turn a dry run into an import
	q := newQuery(r)
	options := model.NetworkImportOptions{DryRun: q.boolean("dry_run"), CreateNetwork: q.boolean("create_network")}
	if q.err != nil {
		writeError(w, q.err)
		return
	}
	result, err := h.c.ImportNetwork(r.Context(), token(r), r.PathValue("id"), doc, options)
	respond(w, http.StatusOK, result, err)
}
