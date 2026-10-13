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
	"errors"
	"net/http"
	"strings"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// The routes of camunda-engine-wrapper (Kong /process/engine): v2, and of v1 the start and the
// list of deployments, which process-scheduler and api-aggregator use. Deployments and
// instances are those of the namespace processes in the cloud.

// ProcessInstance is a running instance in the form of Camunda.
type ProcessInstance struct {
	Id             string `json:"id,omitempty"`
	DefinitionId   string `json:"definitionId,omitempty"`
	BusinessKey    string `json:"businessKey,omitempty"`
	CaseInstanceId string `json:"caseInstanceId,omitempty"`
	Ended          bool   `json:"ended,omitempty"`
	Suspended      bool   `json:"suspended,omitempty"`
	TenantId       string `json:"tenantId,omitempty"`
}

// ProcessDefinition is a deployment version in the form of Camunda; Version is capitalised as
// in the wrapper.
type ProcessDefinition struct {
	Id                string `json:"id"`
	Key               string `json:"key"`
	Category          string `json:"category"`
	Description       string `json:"description"`
	Name              string `json:"name"`
	Version           int    `json:"Version"`
	Resource          string `json:"resource"`
	DeploymentId      string `json:"deploymentId"`
	Diagram           string `json:"diagram"`
	Suspended         bool   `json:"suspended"`
	TenantId          string `json:"tenantId"`
	VersionTag        string `json:"versionTag"`
	HistoryTimeToLive *int   `json:"historyTimeToLive"`
}

type CamundaDeployment struct {
	Id             string `json:"id"`
	Name           string `json:"name"`
	Source         string `json:"source"`
	DeploymentTime string `json:"deploymentTime"`
	TenantId       string `json:"tenantId"`
}

type ExtendedDeployment struct {
	CamundaDeployment
	Diagram      string `json:"diagram"`
	DefinitionId string `json:"definition_id"`
	Error        string `json:"error"`
}

type HistoricProcessInstance struct {
	Id                       string  `json:"id"`
	SuperProcessInstanceId   string  `json:"superProcessInstanceId"`
	SuperCaseInstanceId      string  `json:"superCaseInstanceId"`
	CaseInstanceId           string  `json:"caseInstanceId"`
	ProcessDefinitionName    string  `json:"processDefinitionName"`
	ProcessDefinitionKey     string  `json:"processDefinitionKey"`
	ProcessDefinitionVersion float64 `json:"processDefinitionVersion"`
	ProcessDefinitionId      string  `json:"processDefinitionId"`
	BusinessKey              string  `json:"businessKey"`
	StartTime                string  `json:"startTime"`
	EndTime                  string  `json:"endTime"`
	DurationInMillis         float64 `json:"durationInMillis"`
	StartUserId              string  `json:"startUserId"`
	StartActivityId          string  `json:"startActivityId"`
	DeleteReason             string  `json:"deleteReason"`
	TenantId                 string  `json:"tenantId"`
	State                    string  `json:"state"`
}

type HistoricProcessInstancesWithTotal struct {
	Total int                       `json:"total"`
	Data  []HistoricProcessInstance `json:"data"`
}

// Variable is a typed value in the form of Camunda.
type Variable struct {
	Value     any            `json:"value"`
	Type      string         `json:"type"`
	ValueInfo map[string]any `json:"valueInfo"`
}

type Count struct {
	Count int `json:"count"`
}

func (this *Handler) engine() {
	this.handle("GET /engine/v2/deployments", this.listDeployments)
	this.handle("GET /engine/deployment", this.listDeployments)
	this.handle("GET /engine/v2/deployments/{id}", this.getDeployment)
	this.handle("GET /engine/v2/deployments/{id}/exists", this.deploymentExists)
	this.handle("GET /engine/v2/deployments/{id}/start", this.startDeployment)
	this.handle("GET /engine/deployment/{id}/start", this.startDeployment)
	this.handle("GET /engine/v2/deployments/{id}/parameter", this.startParameters)
	this.handle("GET /engine/v2/deployments/{id}/definition", this.deploymentDefinitions)
	this.handle("GET /engine/v2/deployments/{id}/instances", this.deploymentInstances)
	this.handle("GET /engine/v2/process-definitions/{id}", this.getDefinition)
	this.handle("GET /engine/v2/process-definitions/{id}/diagram", this.definitionDiagram)
	this.handle("GET /engine/v2/process-definitions/{id}/start", this.startDefinition)
	this.handle("GET /engine/v2/process-definitions/{id}/start/id", this.startDefinitionWithId)
	this.handle("GET /engine/v2/process-instances", this.runningInstances)
	this.handle("GET /engine/v2/process-instances/count", this.countRunningInstances)
	this.handle("DELETE /engine/v2/process-instances/{id}", this.stopInstance)
	this.handle("DELETE /engine/v2/process-instances", this.stopInstances)
	this.handle("PUT /engine/v2/process-instances/{id}/variables/{variable_name}", this.setVariable)
	this.handle("DELETE /engine/v2/process-instances-by-business-key/{business_key}", this.stopByBusinessKey)
	this.handle("GET /engine/v2/history/process-instances", this.history)
	this.handle("DELETE /engine/v2/history/process-instances/{id}", this.deleteInstance)
	this.handle("DELETE /engine/v2/history/process-instances", this.deleteInstances)
}

func camundaDeployment(d model.Deployment) CamundaDeployment {
	source := d.Source
	if source == "" {
		source = "sepl"
	}
	return CamundaDeployment{Id: d.Id, Name: d.Name, Source: source, DeploymentTime: formatTime(&d.CreatedAt), TenantId: d.Owner}
}

func definition(d model.Deployment) ProcessDefinition {
	return ProcessDefinition{Id: definitionId(d.Id, d.Version), Key: definitionKey(d.Id), Description: d.Description, Name: d.Name, Version: d.Version,
		Resource: d.Name + ".bpmn", DeploymentId: d.Id, Diagram: d.Name + ".svg", TenantId: d.Owner}
}

func processInstance(i model.Instance) ProcessInstance {
	return ProcessInstance{Id: i.Id, DefinitionId: definitionId(i.DeploymentId, i.DeploymentVersion), BusinessKey: i.Id,
		Ended: i.State != model.StateRunning && i.State != model.StatePending, TenantId: i.Owner}
}

// historicState maps the state of an instance onto the states of the Camunda history
func historicState(state model.InstanceState) string {
	switch state {
	case model.StateCompleted:
		return "COMPLETED"
	case model.StateTerminated:
		return "EXTERNALLY_TERMINATED"
	case model.StateIncident:
		return "INTERNALLY_TERMINATED"
	}
	return "ACTIVE"
}

func historic(i model.Instance) HistoricProcessInstance {
	result := HistoricProcessInstance{
		Id: i.Id, ProcessDefinitionName: i.DeploymentName, ProcessDefinitionKey: definitionKey(i.DeploymentId), ProcessDefinitionVersion: float64(i.DeploymentVersion),
		ProcessDefinitionId: definitionId(i.DeploymentId, i.DeploymentVersion), BusinessKey: i.Id, StartTime: formatTime(&i.StartedAt), EndTime: formatTime(i.EndedAt),
		TenantId: i.Owner, State: historicState(i.State),
	}
	if i.EndedAt != nil {
		result.DurationInMillis = float64(i.EndedAt.Sub(i.StartedAt).Milliseconds())
		if i.State != model.StateCompleted {
			result.DeleteReason = i.EndReason
		}
	}
	return result
}

// listDeployments reads deployments with the parameters of Camunda: maxResults, firstResult,
// sortBy, sortOrder and nameLike
//
//	@Summary		deployments of the engine wrapper
//	@Tags		deprecated-engine
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		maxResults	query	int	false	"page size"
//	@Param		firstResult	query	int	false	"offset"
//	@Param		sortBy	query	string	false	"name or deploymentTime"
//	@Param		sortOrder	query	string	false	"asc or desc"
//	@Param		nameLike	query	string	false	"part of the name"
//	@Success	200	{array}		ExtendedDeployment
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/engine/v2/deployments [get]
//	@Router		/deprecated/engine/deployment [get]
func (this *Handler) listDeployments(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := intParam(r, "maxResults", 0)
	if err != nil {
		writeError(w, err)
		return
	}
	offset, err := intParam(r, "firstResult", 0)
	if err != nil {
		writeError(w, err)
		return
	}
	options := model.DeploymentListOptions{NetworkId: "cloud", Search: strings.Trim(q.Get("nameLike"), "%"),
		Sort: sort(q.Get("sortBy")+"."+q.Get("sortOrder"), map[string]string{"name": "name", "deploymentTime": "created_at"})}
	deployments, _, err := paged(limit, offset, func(page model.PageOptions) (model.Page[model.Deployment], error) {
		options.PageOptions = page
		return this.c.ListDeployments(r.Context(), token(r), processes, options)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	result := make([]ExtendedDeployment, 0, len(deployments))
	for _, d := range deployments {
		item := ExtendedDeployment{CamundaDeployment: camundaDeployment(d), DefinitionId: definitionId(d.Id, d.Version)}
		if version, err := this.c.GetDeploymentVersion(r.Context(), token(r), processes, d.Id, d.Version); err != nil {
			item.Error = err.Error()
		} else {
			item.Diagram = version.Svg
		}
		result = append(result, item)
	}
	writeJson(w, http.StatusOK, result)
}

// getDeployment godoc
//
//	@Summary		deployment of the engine wrapper
//	@Tags		deprecated-engine
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"deployment id"
//	@Success	200	{object}	CamundaDeployment
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/engine/v2/deployments/{id} [get]
func (this *Handler) getDeployment(w http.ResponseWriter, r *http.Request) {
	d, err := this.c.GetDeployment(r.Context(), token(r), processes, r.PathValue("id"))
	respond(w, camundaDeployment(d), err)
}

// deploymentExists godoc
//
//	@Summary		whether a deployment exists
//	@Tags		deprecated-engine
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"deployment id"
//	@Success	200	{object}	bool
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/engine/v2/deployments/{id}/exists [get]
func (this *Handler) deploymentExists(w http.ResponseWriter, r *http.Request) {
	_, err := this.c.GetDeployment(r.Context(), token(r), processes, r.PathValue("id"))
	if errors.Is(err, model.ErrNotFound) {
		writeJson(w, http.StatusOK, false)
		return
	}
	respond(w, err == nil, err)
}

// start starts an instance; a business key becomes the instance id, so that a start with the
// same key is answered with the existing instance
func (this *Handler) start(ctx context.Context, r *http.Request, deploymentId string) (model.Instance, error) {
	input := model.StartInstanceInput{DeploymentId: deploymentId, Variables: startVariables(r)}
	if key := r.URL.Query().Get("business_key"); key != "" {
		return this.c.PutInstance(ctx, token(r), processes, key, input)
	}
	return this.c.StartInstance(ctx, token(r), processes, input)
}

// startDeployment godoc
//
//	@Summary		start a deployment; business_key becomes the instance id, other query parameters variables
//	@Tags		deprecated-engine
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"deployment id"
//	@Param		business_key	query	string	false	"instance id"
//	@Success	200	{object}	ProcessInstance
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/engine/v2/deployments/{id}/start [get]
//	@Router		/deprecated/engine/deployment/{id}/start [get]
func (this *Handler) startDeployment(w http.ResponseWriter, r *http.Request) {
	inst, err := this.start(r.Context(), r, r.PathValue("id"))
	respond(w, processInstance(inst), err)
}

// startDefinition godoc
//
//	@Summary		start a process definition; answers []
//	@Tags		deprecated-engine
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"process definition id"
//	@Param		business_key	query	string	false	"instance id"
//	@Success	200	{array}		string
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/engine/v2/process-definitions/{id}/start [get]
func (this *Handler) startDefinition(w http.ResponseWriter, r *http.Request) {
	_, err := this.start(r.Context(), r, deploymentOf(r.PathValue("id")))
	respond(w, []any{}, err)
}

// startDefinitionWithId godoc
//
//	@Summary		start a process definition
//	@Tags		deprecated-engine
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"process definition id"
//	@Param		business_key	query	string	false	"instance id"
//	@Success	200	{object}	ProcessInstance
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/engine/v2/process-definitions/{id}/start/id [get]
func (this *Handler) startDefinitionWithId(w http.ResponseWriter, r *http.Request) {
	inst, err := this.start(r.Context(), r, deploymentOf(r.PathValue("id")))
	respond(w, processInstance(inst), err)
}

// camundaType names the type of a start parameter as a Camunda form field did
func camundaType(t string) string {
	switch t {
	case "long":
		return "Long"
	case "boolean":
		return "Boolean"
	case "date":
		return "Date"
	}
	return "String"
}

// startParameters godoc
//
//	@Summary		start parameters as Camunda form variables
//	@Tags		deprecated-engine
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"deployment id"
//	@Success	200	{object}	map[string]Variable
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/engine/v2/deployments/{id}/parameter [get]
func (this *Handler) startParameters(w http.ResponseWriter, r *http.Request) {
	parameters, err := this.c.GetStartParameters(r.Context(), token(r), processes, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	result := map[string]Variable{}
	for _, p := range parameters {
		if p.Properties["ignore_on_start"] == "true" {
			continue
		}
		var value any = p.Default
		if p.Default == "" {
			value = nil
		}
		result[p.Id] = Variable{Value: value, Type: camundaType(p.Type), ValueInfo: map[string]any{}}
	}
	writeJson(w, http.StatusOK, result)
}

// deploymentDefinitions godoc
//
//	@Summary		process definition of a deployment
//	@Tags		deprecated-engine
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"deployment id"
//	@Success	200	{array}		ProcessDefinition
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/engine/v2/deployments/{id}/definition [get]
func (this *Handler) deploymentDefinitions(w http.ResponseWriter, r *http.Request) {
	d, err := this.c.GetDeployment(r.Context(), token(r), processes, r.PathValue("id"))
	respond(w, []ProcessDefinition{definition(d)}, err)
}

// getDefinition godoc
//
//	@Summary		process definition
//	@Tags		deprecated-engine
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"process definition id"
//	@Success	200	{object}	ProcessDefinition
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/engine/v2/process-definitions/{id} [get]
func (this *Handler) getDefinition(w http.ResponseWriter, r *http.Request) {
	d, err := this.c.GetDeployment(r.Context(), token(r), processes, deploymentOf(r.PathValue("id")))
	respond(w, definition(d), err)
}

// definitionDiagram godoc
//
//	@Summary		SVG of a process definition
//	@Tags		deprecated-engine
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"process definition id"
//	@Success	200	{string}	string
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/engine/v2/process-definitions/{id}/diagram [get]
func (this *Handler) definitionDiagram(w http.ResponseWriter, r *http.Request) {
	d, err := this.c.GetDeployment(r.Context(), token(r), processes, deploymentOf(r.PathValue("id")))
	if err != nil {
		writeError(w, err)
		return
	}
	version, err := this.c.GetDeploymentVersion(r.Context(), token(r), processes, d.Id, d.Version)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	_, _ = w.Write([]byte(version.Svg))
}

func (this *Handler) instances(r *http.Request, options model.InstanceListOptions) ([]model.Instance, error) {
	options.NetworkId = "cloud"
	return all(func(page model.PageOptions) (model.Page[model.Instance], error) {
		options.PageOptions = page
		return this.c.ListInstances(r.Context(), token(r), processes, options)
	})
}

func processInstances(instances []model.Instance) []ProcessInstance {
	result := make([]ProcessInstance, 0, len(instances))
	for _, i := range instances {
		result = append(result, processInstance(i))
	}
	return result
}

// deploymentInstances godoc
//
//	@Summary		running instances of a deployment
//	@Tags		deprecated-engine
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"deployment id"
//	@Success	200	{array}		ProcessInstance
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/engine/v2/deployments/{id}/instances [get]
func (this *Handler) deploymentInstances(w http.ResponseWriter, r *http.Request) {
	instances, err := this.instances(r, model.InstanceListOptions{State: "running", DeploymentId: r.PathValue("id")})
	respond(w, processInstances(instances), err)
}

// runningInstances godoc
//
//	@Summary		running instances
//	@Tags		deprecated-engine
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Success	200	{array}		ProcessInstance
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/engine/v2/process-instances [get]
func (this *Handler) runningInstances(w http.ResponseWriter, r *http.Request) {
	instances, err := this.instances(r, model.InstanceListOptions{State: "running"})
	respond(w, processInstances(instances), err)
}

// countRunningInstances godoc
//
//	@Summary		number of running instances
//	@Tags		deprecated-engine
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Success	200	{object}	Count
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/engine/v2/process-instances/count [get]
func (this *Handler) countRunningInstances(w http.ResponseWriter, r *http.Request) {
	page, err := this.c.ListInstances(r.Context(), token(r), processes, model.InstanceListOptions{State: "running", NetworkId: "cloud", PageOptions: model.PageOptions{Limit: 1}})
	respond(w, Count{Count: page.Total}, err)
}

// Deleting a running instance in Camunda ended it and kept its history: here it is a stop.
//
//	@Summary		stop a running instance
//	@Tags		deprecated-engine
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"instance id"
//	@Success	200	{string}	string
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/engine/v2/process-instances/{id} [delete]
func (this *Handler) stopInstance(w http.ResponseWriter, r *http.Request) {
	respond(w, "ok", this.c.StopInstances(r.Context(), token(r), processes, []string{r.PathValue("id")}))
}

// stopInstances godoc
//
//	@Summary		stop running instances
//	@Tags		deprecated-engine
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		ids	body	[]string	true	"instance ids"
//	@Success	200	{string}	string
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/engine/v2/process-instances [delete]
func (this *Handler) stopInstances(w http.ResponseWriter, r *http.Request) {
	ids, err := decode[[]string](r)
	if err == nil {
		err = this.c.StopInstances(r.Context(), token(r), processes, ids)
	}
	respond(w, "ok", err)
}

// setVariable godoc
//
//	@Summary		set a variable of an instance
//	@Tags		deprecated-engine
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"instance id"
//	@Param		variable_name	path	string	true	"name"
//	@Param		value	body	object	true	"JSON value"
//	@Success	200	{string}	string
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/engine/v2/process-instances/{id}/variables/{variable_name} [put]
func (this *Handler) setVariable(w http.ResponseWriter, r *http.Request) {
	value, err := decode[any](r)
	if err == nil {
		err = this.c.SetVariable(r.Context(), token(r), processes, r.PathValue("id"), r.PathValue("variable_name"), value)
	}
	respond(w, "ok", err)
}

// stopByBusinessKey stops the instance of a business key if it runs; an unknown key is no error
//
//	@Summary		stop the instance of a business key
//	@Tags		deprecated-engine
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		business_key	path	string	true	"business key, with or without wardened:"
//	@Success	200	{string}	string
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/engine/v2/process-instances-by-business-key/{business_key} [delete]
func (this *Handler) stopByBusinessKey(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.PathValue("business_key"), "wardened:")
	inst, err := this.c.GetInstance(r.Context(), token(r), processes, key)
	switch {
	case errors.Is(err, model.ErrNotFound):
		err = nil
	case err == nil && (inst.State == model.StateRunning || inst.State == model.StatePending):
		err = this.c.StopInstances(r.Context(), token(r), processes, []string{key})
	}
	respond(w, "ok", err)
}

// history lists instances with the filters of the Camunda history the callers use
//
//	@Summary		instances with the filters of the Camunda history
//	@Tags		deprecated-engine
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		maxResults	query	int	false	"page size"
//	@Param		firstResult	query	int	false	"offset"
//	@Param		sortBy	query	string	false	"startTime, endTime or processDefinitionName"
//	@Param		sortOrder	query	string	false	"asc or desc"
//	@Param		finished	query	bool	false	"ended instances"
//	@Param		unfinished	query	bool	false	"running instances"
//	@Param		processInstanceBusinessKey	query	string	false	"business key"
//	@Param		processDefinitionId	query	string	false	"process definition id"
//	@Param		processDefinitionNameLike	query	string	false	"part of the deployment name"
//	@Param		with_total	query	bool	false	"answer {total, data}"
//	@Success	200	{array}		HistoricProcessInstance
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/engine/v2/history/process-instances [get]
func (this *Handler) history(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := intParam(r, "maxResults", 0)
	if err != nil {
		writeError(w, err)
		return
	}
	offset, err := intParam(r, "firstResult", 0)
	if err != nil {
		writeError(w, err)
		return
	}
	options := model.InstanceListOptions{NetworkId: "cloud", DeploymentName: strings.Trim(q.Get("processDefinitionNameLike"), "%"),
		Sort: sort(q.Get("sortBy")+"."+q.Get("sortOrder"), map[string]string{"startTime": "started_at", "endTime": "ended_at", "processDefinitionName": "deployment_name"})}
	if key := q.Get("processInstanceBusinessKey"); key != "" {
		options.Ids = []string{strings.TrimPrefix(key, "wardened:")}
	}
	if definition := q.Get("processDefinitionId"); definition != "" {
		options.DeploymentId = deploymentOf(definition)
	}
	switch {
	case boolParam(r, "finished"):
		options.State = "finished"
	case boolParam(r, "unfinished"):
		options.State = "running"
	}
	instances, total, err := paged(limit, offset, func(page model.PageOptions) (model.Page[model.Instance], error) {
		options.PageOptions = page
		return this.c.ListInstances(r.Context(), token(r), processes, options)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	result := make([]HistoricProcessInstance, 0, len(instances))
	for _, i := range instances {
		result = append(result, historic(i))
	}
	if boolParam(r, "with_total") {
		writeJson(w, http.StatusOK, HistoricProcessInstancesWithTotal{Total: total, Data: result})
		return
	}
	writeJson(w, http.StatusOK, result)
}

// deleteInstance godoc
//
//	@Summary		delete an instance with its history
//	@Tags		deprecated-engine
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"instance id"
//	@Success	200	{string}	string
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/engine/v2/history/process-instances/{id} [delete]
func (this *Handler) deleteInstance(w http.ResponseWriter, r *http.Request) {
	respond(w, "ok", this.c.DeleteInstances(r.Context(), token(r), processes, []string{r.PathValue("id")}))
}

// deleteInstances godoc
//
//	@Summary		delete instances with their history
//	@Tags		deprecated-engine
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		ids	body	[]string	true	"instance ids"
//	@Success	200	{string}	string
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/engine/v2/history/process-instances [delete]
func (this *Handler) deleteInstances(w http.ResponseWriter, r *http.Request) {
	ids, err := decode[[]string](r)
	if err == nil {
		err = this.c.DeleteInstances(r.Context(), token(r), processes, ids)
	}
	respond(w, "ok", err)
}
