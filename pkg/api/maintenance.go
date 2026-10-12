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

// listMaintenanceProcedures godoc
//
//	@Summary		maintenance procedures of an instance
//	@Description	Further message start events of the model with own start parameters (SMS-13).
//	@Tags			instances
//	@Security		Bearer
//	@Produce		json
//	@Param			ns	path		string	true	"namespace"
//	@Param			id	path		string	true	"instance id"
//	@Success		200	{array}		model.MaintenanceProcedure
//	@Failure		404	{object}	model.Error
//	@Router			/namespaces/{ns}/instances/{id}/maintenance-procedures [get]
func (h *handlers) listMaintenanceProcedures(w http.ResponseWriter, r *http.Request) {
	result, err := h.c.ListMaintenanceProcedures(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"))
	respond(w, http.StatusOK, result, err)
}

// startMaintenance godoc
//
//	@Summary		start a maintenance procedure
//	@Description	Starts the procedure as an instance that refers to the given one; it shares its variables and modules. Needs administrate on the instance, which must be ready without errors (SMS-13).
//	@Tags			instances
//	@Security		Bearer
//	@Accept			json
//	@Produce		json
//	@Param			ns			path		string						true	"namespace"
//	@Param			id			path		string						true	"instance id"
//	@Param			procedure	path		string						true	"procedure id, the message name"
//	@Param			input		body		model.MaintenanceStartInput	true	"parameters"
//	@Success		201			{object}	model.Instance
//	@Failure		404			{object}	model.Error
//	@Failure		409			{object}	model.Error
//	@Router			/namespaces/{ns}/instances/{id}/maintenance-procedures/{procedure}/start [post]
func (h *handlers) startMaintenance(w http.ResponseWriter, r *http.Request) {
	input, ok := decode[model.MaintenanceStartInput](w, r)
	if !ok {
		return
	}
	result, err := h.c.StartMaintenance(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"), r.PathValue("procedure"), input)
	respond(w, http.StatusCreated, result, err)
}

// updateParameters godoc
//
//	@Summary		change the start parameters of an instance
//	@Description	Starts a new run with the new parameters; the modules of the previous run and its maintenance procedures are removed (SMS-03, SMS-09). Needs administrate on the instance and execute on the deployment.
//	@Tags			instances
//	@Security		Bearer
//	@Accept			json
//	@Produce		json
//	@Param			ns		path		string					true	"namespace"
//	@Param			id		path		string					true	"instance id"
//	@Param			input	body		model.ParametersInput	true	"parameters"
//	@Success		200		{object}	model.Instance
//	@Failure		404		{object}	model.Error
//	@Router			/namespaces/{ns}/instances/{id}/parameters [put]
func (h *handlers) updateParameters(w http.ResponseWriter, r *http.Request) {
	input, ok := decode[model.ParametersInput](w, r)
	if !ok {
		return
	}
	result, err := h.c.UpdateParameters(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"), input)
	respond(w, http.StatusOK, result, err)
}

// deleteVariable godoc
//
//	@Summary	delete a variable of an instance
//	@Tags		instances
//	@Security	Bearer
//	@Param		ns		path	string	true	"namespace"
//	@Param		id		path	string	true	"instance id"
//	@Param		name	path	string	true	"variable name"
//	@Success	204
//	@Failure	404	{object}	model.Error
//	@Router		/namespaces/{ns}/instances/{id}/variables/{name} [delete]
func (h *handlers) deleteVariable(w http.ResponseWriter, r *http.Request) {
	respondEmpty(w, h.c.DeleteVariable(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"), r.PathValue("name")))
}

// getDeploymentParameters godoc
//
//	@Summary		start parameters of a deployment with their options
//	@Description	Form fields of the start event with description, type, default and the options the caller may choose, IoT options through device-selection; fields with auto_select_all are filled at the start (SMS-08). Needs execute on the deployment.
//	@Tags			deployments
//	@Security		Bearer
//	@Produce		json
//	@Param			ns	path		string	true	"namespace"
//	@Param			id	path		string	true	"deployment id"
//	@Success		200	{array}		model.ParameterDescription
//	@Failure		404	{object}	model.Error
//	@Router			/namespaces/{ns}/deployments/{id}/parameters [get]
func (h *handlers) getDeploymentParameters(w http.ResponseWriter, r *http.Request) {
	result, err := h.c.DeploymentParameters(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"))
	respond(w, http.StatusOK, result, err)
}

// getMaintenanceParameters godoc
//
//	@Summary	parameters of a maintenance procedure with their options
//	@Tags		instances
//	@Security	Bearer
//	@Produce	json
//	@Param		ns			path		string	true	"namespace"
//	@Param		id			path		string	true	"instance id"
//	@Param		procedure	path		string	true	"procedure id"
//	@Success	200			{array}		model.ParameterDescription
//	@Failure	404			{object}	model.Error
//	@Router		/namespaces/{ns}/instances/{id}/maintenance-procedures/{procedure}/parameters [get]
func (h *handlers) getMaintenanceParameters(w http.ResponseWriter, r *http.Request) {
	result, err := h.c.MaintenanceParameters(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"), r.PathValue("procedure"))
	respond(w, http.StatusOK, result, err)
}
