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
	"net/http"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// The list of process-incident-api (Kong /process/incidents) that the monitor of the web-ui
// reads. Incidents are created by the engine itself; the writing routes of the old service
// were for the workers.

type Incident struct {
	Id                  string `json:"id"`
	MsgVersion          int64  `json:"msg_version,omitempty"`
	ExternalTaskId      string `json:"external_task_id"`
	ProcessInstanceId   string `json:"process_instance_id"`
	ProcessDefinitionId string `json:"process_definition_id"`
	WorkerId            string `json:"worker_id"`
	ErrorMessage        string `json:"error_message"`
	Time                string `json:"time"`
	TenantId            string `json:"tenant_id"`
	DeploymentName      string `json:"deployment_name"`
	BusinessKey         string `json:"business_key"`
}

func (this *Handler) incidents() {
	this.handle("GET /incidents/incidents", this.listIncidents)
}

// listIncidents godoc
//
//	@Summary		incidents of process-incident-api
//	@Tags		deprecated-incidents
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		limit	query	int	false	"page size, default 100"
//	@Param		offset	query	int	false	"offset"
//	@Param		sort	query	string	false	"time with .asc or .desc"
//	@Param		process_instance_id	query	string	false	"instance"
//	@Param		process_definition_id	query	string	false	"process definition or deployment"
//	@Param		external_task_id	query	string	false	"element"
//	@Success	200	{array}		Incident
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/incidents/incidents [get]
func (this *Handler) listIncidents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
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
	options := model.IncidentListOptions{InstanceId: q.Get("process_instance_id"), ElementId: q.Get("external_task_id"),
		Sort: sort(q.Get("sort"), map[string]string{"time": "time", "id": "time"})}
	if definition := q.Get("process_definition_id"); definition != "" {
		options.DeploymentId = deploymentOf(definition)
	}
	incidents, _, err := paged(limit, offset, func(page model.PageOptions) (model.Page[model.Incident], error) {
		options.PageOptions = page
		return this.c.ListIncidents(r.Context(), token(r), processes, options)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	names := map[string]string{}
	result := make([]Incident, 0, len(incidents))
	for _, i := range incidents {
		name, known := names[i.DeploymentId]
		if !known {
			if d, err := this.c.GetDeployment(r.Context(), token(r), processes, i.DeploymentId); err == nil {
				name = d.Name
			}
			names[i.DeploymentId] = name
		}
		result = append(result, Incident{Id: i.Id, ExternalTaskId: i.ElementId, ProcessInstanceId: i.InstanceId,
			ProcessDefinitionId: definitionId(i.DeploymentId, i.DeploymentVersion), ErrorMessage: i.Message, Time: i.Time.UTC().Format(time.RFC3339),
			TenantId: i.Owner, DeploymentName: name, BusinessKey: i.InstanceId})
	}
	writeJson(w, http.StatusOK, result)
}
