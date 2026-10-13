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
	"errors"
	"net/http"
	"strings"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/models/go/models"
)

// The routes of process-sync (Kong /process-sync) and process-fog-deployment (Kong
// /process-fog-deployment) that the fog views of the web-ui use, for gateways that run the
// engine (decided for M7). Gateways with Camunda stay with process-sync until their process
// module is updated. A deployment of a gateway keeps one id; the Camunda deployment id of
// the old routes is that id.

type SyncInfo struct {
	NetworkId       string `json:"network_id"`
	IsPlaceholder   bool   `json:"is_placeholder"`
	MarkedForDelete bool   `json:"marked_for_delete"`
	MarkedAsMissing bool   `json:"marked_as_missing"`
	SyncDate        string `json:"sync_date"`
}

type SyncDeployment struct {
	CamundaDeployment
	SyncInfo
	Diagram      string `json:"diagram,omitempty"`
	DefinitionId string `json:"definition_id,omitempty"`
	Error        string `json:"error,omitempty"`
}

type SyncHistoricProcessInstance struct {
	HistoricProcessInstance
	SyncInfo
}

type SyncHistoricProcessInstancesWithTotal struct {
	Total int                           `json:"total"`
	Data  []SyncHistoricProcessInstance `json:"data"`
}

type SyncIncident struct {
	Incident
	SyncInfo
}

type DeploymentMetadata struct {
	CamundaDeploymentId string              `json:"camunda_deployment_id"`
	ProcessParameter    map[string]Variable `json:"process_parameter"`
	DeploymentModel     models.Deployment   `json:"deployment_model"`
	SyncInfo
}

type Hub struct {
	Id             string   `json:"id"`
	Name           string   `json:"name"`
	Hash           string   `json:"hash"`
	DeviceLocalIds []string `json:"device_local_ids"`
	DeviceIds      []string `json:"device_ids"`
	OwnerId        string   `json:"owner_id"`
}

func (this *Handler) processSync() {
	this.handle("GET /process-sync/networks", this.syncNetworks)
	this.handle("GET /process-sync/deployments", this.syncDeployments)
	this.handle("GET /process-sync/deployments/{networkId}/{id}", this.syncDeployment)
	this.handle("GET /process-sync/deployments/{networkId}/{id}/metadata", this.syncDeploymentMetadata)
	this.handle("GET /process-sync/metadata/{networkId}", this.syncMetadata)
	this.handle("GET /process-sync/deployments/{networkId}/{id}/start", this.syncStart)
	this.handle("DELETE /process-sync/deployments/{networkId}/{id}", this.syncDeleteDeployment)
	this.handle("POST /process-sync/sync/deployments/{networkId}", this.syncResync)
	this.handle("GET /process-sync/history/process-instances", this.syncHistory)
	this.handle("DELETE /process-sync/process-instances/{networkId}/{id}", this.syncStopInstance)
	this.handle("DELETE /process-sync/process-instances-by-business-key/{networkId}/{business_key}", this.syncStopByBusinessKey)
	this.handle("DELETE /process-sync/history/process-instances/{networkId}/{id}", this.syncDeleteInstance)
	this.handle("GET /process-sync/incidents", this.syncIncidents)

	this.handle("POST /fog-deployment/prepared-deployments/{networkId}", this.fogPrepareXml)
	this.handle("GET /fog-deployment/prepared-deployments/{networkId}/{modelId}", this.fogPrepareModel)
	this.handle("POST /fog-deployment/deployments/{networkId}", this.fogCreateDeployment)
	this.handle("DELETE /fog-deployment/deployments/{networkId}/{id}", this.syncDeleteDeployment)
	this.handle("GET /fog-deployment/deployments/{networkId}/{id}/start", this.syncStart)
}

// syncStates reads the state of the resources of a gateway and its last contact
func (this *Handler) syncStates(r *http.Request, networkId string) (map[string]model.NetworkResource, string, error) {
	n, err := this.c.GetNetwork(r.Context(), token(r), networkId)
	if err != nil {
		return nil, "", err
	}
	result := map[string]model.NetworkResource{}
	for _, resource := range n.Resources {
		result[resource.Kind+"|"+resource.Id] = resource
	}
	return result, formatTime(n.LastContact), nil
}

func syncInfo(networkId string, resource model.NetworkResource, known bool, contact string) SyncInfo {
	return SyncInfo{NetworkId: networkId, IsPlaceholder: known && resource.State != model.ResourceApplied, MarkedForDelete: known && resource.Deleted, SyncDate: contact}
}

// syncNetworks godoc
//
//	@Summary		gateways with the engine
//	@Tags		deprecated-process-sync
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Success	200	{array}		Hub
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/process-sync/networks [get]
func (this *Handler) syncNetworks(w http.ResponseWriter, r *http.Request) {
	networks, err := this.c.ListNetworks(r.Context(), token(r))
	if err != nil {
		writeError(w, err)
		return
	}
	result := make([]Hub, 0, len(networks))
	for _, n := range networks {
		result = append(result, Hub{Id: n.Id, Name: n.Id, DeviceLocalIds: []string{}, DeviceIds: []string{}})
	}
	writeJson(w, http.StatusOK, result)
}

// networkIds reads the required, comma separated network_id of a list
func networkIds(r *http.Request) ([]string, error) {
	ids := splitList(r.URL.Query().Get("network_id"))
	if len(ids) == 0 {
		return nil, model.BadRequest("network_id is required")
	}
	return ids, nil
}

// syncDeployments godoc
//
//	@Summary		deployments of gateways
//	@Tags		deprecated-process-sync
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		network_id	query	string	true	"comma separated network ids"
//	@Param		limit	query	int	false	"page size, default 100"
//	@Param		offset	query	int	false	"offset"
//	@Param		search	query	string	false	"part of the name"
//	@Param		sort	query	string	false	"name or deploymentTime with .asc or .desc"
//	@Param		extended	query	bool	false	"with diagram and definition"
//	@Success	200	{array}		SyncDeployment
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/process-sync/deployments [get]
func (this *Handler) syncDeployments(w http.ResponseWriter, r *http.Request) {
	ids, err := networkIds(r)
	if err != nil {
		writeError(w, err)
		return
	}
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
	result := []SyncDeployment{}
	for _, networkId := range ids {
		states, contact, err := this.syncStates(r, networkId)
		if err != nil {
			writeError(w, err)
			return
		}
		options := model.DeploymentListOptions{NetworkId: networkId, Search: r.URL.Query().Get("search"),
			Sort: sort(r.URL.Query().Get("sort"), map[string]string{"name": "name", "deploymentTime": "created_at"})}
		deployments, _, err := paged(limit, offset, func(page model.PageOptions) (model.Page[model.Deployment], error) {
			options.PageOptions = page
			return this.c.ListDeployments(r.Context(), token(r), processes, options)
		})
		if err != nil {
			writeError(w, err)
			return
		}
		for _, d := range deployments {
			resource, known := states["deployment|"+d.Id]
			item := SyncDeployment{CamundaDeployment: camundaDeployment(d), SyncInfo: syncInfo(networkId, resource, known, contact), Error: resource.Error}
			if boolParam(r, "extended") {
				item.DefinitionId = definitionId(d.Id, d.Version)
				if v, err := this.c.GetDeploymentVersion(r.Context(), token(r), processes, d.Id, d.Version); err == nil {
					item.Diagram = v.Svg
				}
			}
			result = append(result, item)
		}
	}
	writeJson(w, http.StatusOK, result)
}

// networkDeployment reads a deployment of a gateway; another gateway's is not found
func (this *Handler) networkDeployment(r *http.Request) (model.Deployment, error) {
	d, err := this.c.GetDeployment(r.Context(), token(r), processes, r.PathValue("id"))
	if err == nil && d.NetworkId != r.PathValue("networkId") {
		return d, model.NotFound("deployment %v not found on network %v", d.Id, r.PathValue("networkId"))
	}
	return d, err
}

// syncDeployment godoc
//
//	@Summary		deployment of a gateway
//	@Tags		deprecated-process-sync
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		networkId	path	string	true	"network id"
//	@Param		id	path	string	true	"deployment id"
//	@Success	200	{object}	SyncDeployment
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/process-sync/deployments/{networkId}/{id} [get]
func (this *Handler) syncDeployment(w http.ResponseWriter, r *http.Request) {
	d, err := this.networkDeployment(r)
	if err != nil {
		writeError(w, err)
		return
	}
	states, contact, err := this.syncStates(r, d.NetworkId)
	if err != nil {
		writeError(w, err)
		return
	}
	resource, known := states["deployment|"+d.Id]
	writeJson(w, http.StatusOK, SyncDeployment{CamundaDeployment: camundaDeployment(d), SyncInfo: syncInfo(d.NetworkId, resource, known, contact), Error: resource.Error})
}

func (this *Handler) metadata(r *http.Request, d model.Deployment) (DeploymentMetadata, error) {
	old, err := this.v3Deployment(r, d, false)
	if err != nil {
		return DeploymentMetadata{}, err
	}
	states, contact, err := this.syncStates(r, d.NetworkId)
	if err != nil {
		return DeploymentMetadata{}, err
	}
	resource, known := states["deployment|"+d.Id]
	parameters := map[string]Variable{}
	for _, p := range old.StartParameter {
		var value any = p.Default
		if p.Default == "" {
			value = nil
		}
		parameters[p.Id] = Variable{Value: value, Type: camundaType(p.Type), ValueInfo: map[string]any{}}
	}
	return DeploymentMetadata{CamundaDeploymentId: d.Id, ProcessParameter: parameters, DeploymentModel: old, SyncInfo: syncInfo(d.NetworkId, resource, known, contact)}, nil
}

// syncDeploymentMetadata godoc
//
//	@Summary		metadata of a deployment of a gateway
//	@Tags		deprecated-process-sync
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		networkId	path	string	true	"network id"
//	@Param		id	path	string	true	"deployment id"
//	@Success	200	{object}	DeploymentMetadata
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/process-sync/deployments/{networkId}/{id}/metadata [get]
func (this *Handler) syncDeploymentMetadata(w http.ResponseWriter, r *http.Request) {
	d, err := this.networkDeployment(r)
	if err != nil {
		writeError(w, err)
		return
	}
	result, err := this.metadata(r, d)
	respond(w, result, err)
}

// syncMetadata lists the metadata of a gateway's deployments, or of one by deployment_id or
// camunda_deployment_id, which are the same here
//
//	@Summary		metadata of the deployments of a gateway
//	@Tags		deprecated-process-sync
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		networkId	path	string	true	"network id"
//	@Param		deployment_id	query	string	false	"deployment id"
//	@Param		camunda_deployment_id	query	string	false	"the same id"
//	@Success	200	{array}		DeploymentMetadata
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/process-sync/metadata/{networkId} [get]
func (this *Handler) syncMetadata(w http.ResponseWriter, r *http.Request) {
	networkId := r.PathValue("networkId")
	var deployments []model.Deployment
	if id := firstNonEmpty(r.URL.Query().Get("deployment_id"), r.URL.Query().Get("camunda_deployment_id")); id != "" {
		d, err := this.c.GetDeployment(r.Context(), token(r), processes, id)
		if err != nil && !errors.Is(err, model.ErrNotFound) {
			writeError(w, err)
			return
		}
		if err == nil && d.NetworkId == networkId {
			deployments = append(deployments, d)
		}
	} else {
		var err error
		options := model.DeploymentListOptions{NetworkId: networkId}
		deployments, err = all(func(page model.PageOptions) (model.Page[model.Deployment], error) {
			options.PageOptions = page
			return this.c.ListDeployments(r.Context(), token(r), processes, options)
		})
		if err != nil {
			writeError(w, err)
			return
		}
	}
	result := make([]DeploymentMetadata, 0, len(deployments))
	for _, d := range deployments {
		m, err := this.metadata(r, d)
		if err != nil {
			writeError(w, err)
			return
		}
		result = append(result, m)
	}
	writeJson(w, http.StatusOK, result)
}

// syncStart godoc
//
//	@Summary		start a deployment of a gateway
//	@Tags		deprecated-process-sync
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		networkId	path	string	true	"network id"
//	@Param		id	path	string	true	"deployment id"
//	@Param		business_key	query	string	false	"instance id"
//	@Success	200	{object}	bool
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/process-sync/deployments/{networkId}/{id}/start [get]
//	@Router		/deprecated/fog-deployment/deployments/{networkId}/{id}/start [get]
func (this *Handler) syncStart(w http.ResponseWriter, r *http.Request) {
	d, err := this.networkDeployment(r)
	if err == nil {
		_, err = this.start(r.Context(), r, d.Id)
	}
	respond(w, true, err)
}

// syncDeleteDeployment godoc
//
//	@Summary		delete a deployment of a gateway
//	@Tags		deprecated-process-sync
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		networkId	path	string	true	"network id"
//	@Param		id	path	string	true	"deployment id"
//	@Success	200	{object}	bool
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/process-sync/deployments/{networkId}/{id} [delete]
//	@Router		/deprecated/fog-deployment/deployments/{networkId}/{id} [delete]
func (this *Handler) syncDeleteDeployment(w http.ResponseWriter, r *http.Request) {
	d, err := this.networkDeployment(r)
	if errors.Is(err, model.ErrNotFound) {
		// process-fog-deployment answered an unknown deployment with success
		writeJson(w, http.StatusOK, true)
		return
	}
	if err == nil {
		err = this.c.DeleteDeployment(r.Context(), token(r), processes, d.Id)
	}
	respond(w, true, err)
}

// syncResync asked process-sync to send everything again; the engine reconciles by itself
//
//	@Summary		no longer needed; the engine reconciles by itself
//	@Tags		deprecated-process-sync
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		networkId	path	string	true	"network id"
//	@Success	200	{object}	bool
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/process-sync/sync/deployments/{networkId} [post]
func (this *Handler) syncResync(w http.ResponseWriter, r *http.Request) {
	_, err := this.c.GetNetwork(r.Context(), token(r), r.PathValue("networkId"))
	respond(w, true, err)
}

// syncHistory godoc
//
//	@Summary		instances of gateways
//	@Tags		deprecated-process-sync
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		network_id	query	string	true	"comma separated network ids"
//	@Param		limit	query	int	false	"page size, default 100"
//	@Param		offset	query	int	false	"offset"
//	@Param		sort	query	string	false	"startTime, endTime or processDefinitionName with .asc or .desc"
//	@Param		search	query	string	false	"part of the deployment name"
//	@Param		business_key	query	string	false	"comma separated business keys"
//	@Param		processDefinitionId	query	string	false	"process definition id"
//	@Param		state	query	string	false	"finished or unfinished"
//	@Param		with_total	query	bool	false	"answer {total, data}"
//	@Success	200	{array}		SyncHistoricProcessInstance
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/process-sync/history/process-instances [get]
func (this *Handler) syncHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ids, err := networkIds(r)
	if err != nil {
		writeError(w, err)
		return
	}
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
	result := []SyncHistoricProcessInstance{}
	total := 0
	for _, networkId := range ids {
		_, contact, err := this.syncStates(r, networkId)
		if err != nil {
			writeError(w, err)
			return
		}
		options := model.InstanceListOptions{NetworkId: networkId, DeploymentName: q.Get("search"),
			Sort: sort(q.Get("sort"), map[string]string{"startTime": "started_at", "endTime": "ended_at", "processDefinitionName": "deployment_name"})}
		if keys := splitList(q.Get("business_key")); len(keys) > 0 {
			for i := range keys {
				keys[i] = strings.TrimPrefix(keys[i], "wardened:")
			}
			options.Ids = keys
		}
		if definition := q.Get("processDefinitionId"); definition != "" {
			options.DeploymentId = deploymentOf(definition)
		}
		switch q.Get("state") {
		case "finished":
			options.State = "finished"
		case "unfinished":
			options.State = "running"
		}
		instances, count, err := paged(limit, offset, func(page model.PageOptions) (model.Page[model.Instance], error) {
			options.PageOptions = page
			return this.c.ListInstances(r.Context(), token(r), processes, options)
		})
		if err != nil {
			writeError(w, err)
			return
		}
		total += count
		for _, i := range instances {
			result = append(result, SyncHistoricProcessInstance{HistoricProcessInstance: historic(i),
				SyncInfo: SyncInfo{NetworkId: networkId, IsPlaceholder: i.State == model.StatePending, SyncDate: contact}})
		}
	}
	if boolParam(r, "with_total") {
		writeJson(w, http.StatusOK, SyncHistoricProcessInstancesWithTotal{Total: total, Data: result})
		return
	}
	writeJson(w, http.StatusOK, result)
}

// networkInstance checks that an instance runs on the gateway of the path
func (this *Handler) networkInstance(r *http.Request, id string) (model.Instance, error) {
	i, err := this.c.GetInstance(r.Context(), token(r), processes, id)
	if err == nil && i.NetworkId != r.PathValue("networkId") {
		return i, model.NotFound("instance %v not found on network %v", id, r.PathValue("networkId"))
	}
	return i, err
}

// syncStopInstance godoc
//
//	@Summary		stop an instance of a gateway
//	@Tags		deprecated-process-sync
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		networkId	path	string	true	"network id"
//	@Param		id	path	string	true	"instance id"
//	@Success	200	{object}	bool
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/process-sync/process-instances/{networkId}/{id} [delete]
func (this *Handler) syncStopInstance(w http.ResponseWriter, r *http.Request) {
	i, err := this.networkInstance(r, r.PathValue("id"))
	if err == nil {
		err = this.c.StopInstances(r.Context(), token(r), processes, []string{i.Id})
	}
	respond(w, true, err)
}

// syncStopByBusinessKey godoc
//
//	@Summary		stop the instance of a business key on a gateway
//	@Tags		deprecated-process-sync
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		networkId	path	string	true	"network id"
//	@Param		business_key	path	string	true	"business key, with or without wardened:"
//	@Success	200	{object}	bool
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/process-sync/process-instances-by-business-key/{networkId}/{business_key} [delete]
func (this *Handler) syncStopByBusinessKey(w http.ResponseWriter, r *http.Request) {
	i, err := this.networkInstance(r, strings.TrimPrefix(r.PathValue("business_key"), "wardened:"))
	switch {
	case errors.Is(err, model.ErrNotFound):
		err = nil
	case err == nil && (i.State == model.StateRunning || i.State == model.StatePending):
		err = this.c.StopInstances(r.Context(), token(r), processes, []string{i.Id})
	}
	respond(w, true, err)
}

// syncDeleteInstance godoc
//
//	@Summary		delete an instance of a gateway
//	@Tags		deprecated-process-sync
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		networkId	path	string	true	"network id"
//	@Param		id	path	string	true	"instance id"
//	@Success	200	{object}	bool
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/process-sync/history/process-instances/{networkId}/{id} [delete]
func (this *Handler) syncDeleteInstance(w http.ResponseWriter, r *http.Request) {
	i, err := this.networkInstance(r, r.PathValue("id"))
	if err == nil {
		err = this.c.DeleteInstances(r.Context(), token(r), processes, []string{i.Id})
	}
	respond(w, true, err)
}

// syncIncidents godoc
//
//	@Summary		incidents of gateways
//	@Tags		deprecated-process-sync
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		network_id	query	string	true	"comma separated network ids"
//	@Param		process_instance_id	query	string	false	"instance id"
//	@Success	200	{array}		SyncIncident
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/process-sync/incidents [get]
func (this *Handler) syncIncidents(w http.ResponseWriter, r *http.Request) {
	ids, err := networkIds(r)
	if err != nil {
		writeError(w, err)
		return
	}
	instanceId := r.URL.Query().Get("process_instance_id")
	result := []SyncIncident{}
	for _, networkId := range ids {
		_, contact, err := this.syncStates(r, networkId)
		if err != nil {
			writeError(w, err)
			return
		}
		instances := []string{instanceId}
		if instanceId == "" {
			running, err := all(func(page model.PageOptions) (model.Page[model.Instance], error) {
				return this.c.ListInstances(r.Context(), token(r), processes, model.InstanceListOptions{NetworkId: networkId, State: string(model.StateIncident), PageOptions: page})
			})
			if err != nil {
				writeError(w, err)
				return
			}
			instances = nil
			for _, i := range running {
				instances = append(instances, i.Id)
			}
		}
		for _, id := range instances {
			incidents, err := this.c.ListIncidents(r.Context(), token(r), processes, model.IncidentListOptions{InstanceId: id, PageOptions: model.PageOptions{Limit: pageSize}})
			if err != nil {
				writeError(w, err)
				return
			}
			for _, i := range incidents.Items {
				result = append(result, SyncIncident{Incident: Incident{Id: i.Id, ExternalTaskId: i.ElementId, ProcessInstanceId: i.InstanceId,
					ProcessDefinitionId: definitionId(i.DeploymentId, i.DeploymentVersion), ErrorMessage: i.Message, Time: i.Time.UTC().Format(camundaTime),
					TenantId: i.Owner, BusinessKey: i.InstanceId}, SyncInfo: SyncInfo{NetworkId: networkId, SyncDate: contact}})
			}
		}
	}
	writeJson(w, http.StatusOK, result)
}

// fogPrepareXml prepares a deployment for a gateway; the options are those of the platform,
// not restricted to the devices of the gateway as process-fog-deployment did
//
//	@Summary		prepare a deployment for a gateway
//	@Tags		deprecated-fog-deployment
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		networkId	path	string	true	"network id"
//	@Param		request	body	PrepareRequest	true	"BPMN and SVG"
//	@Success	200	{object}	models.Deployment
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/fog-deployment/prepared-deployments/{networkId} [post]
func (this *Handler) fogPrepareXml(w http.ResponseWriter, r *http.Request) {
	if _, err := this.c.GetNetwork(r.Context(), token(r), r.PathValue("networkId")); err != nil {
		writeError(w, err)
		return
	}
	this.prepareXml(w, r)
}

// fogPrepareModel godoc
//
//	@Summary		prepare a deployment of a model for a gateway
//	@Tags		deprecated-fog-deployment
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		networkId	path	string	true	"network id"
//	@Param		modelId	path	string	true	"model id"
//	@Success	200	{object}	models.Deployment
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/fog-deployment/prepared-deployments/{networkId}/{modelId} [get]
func (this *Handler) fogPrepareModel(w http.ResponseWriter, r *http.Request) {
	if _, err := this.c.GetNetwork(r.Context(), token(r), r.PathValue("networkId")); err != nil {
		writeError(w, err)
		return
	}
	this.prepareModel(w, r)
}

// fogCreateDeployment godoc
//
//	@Summary		deploy to a gateway in the format version 3
//	@Tags		deprecated-fog-deployment
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		networkId	path	string	true	"network id"
//	@Param		source	query	string	false	"source"
//	@Param		deployment	body	models.Deployment	true	"deployment"
//	@Success	200	{object}	models.Deployment
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/fog-deployment/deployments/{networkId} [post]
func (this *Handler) fogCreateDeployment(w http.ResponseWriter, r *http.Request) {
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
	input.NetworkId = r.PathValue("networkId")
	d, err := this.c.CreateDeployment(r.Context(), token(r), processes, input)
	if err != nil {
		writeError(w, err)
		return
	}
	result, err := this.v3Deployment(r, d, false)
	respond(w, result, err)
}
