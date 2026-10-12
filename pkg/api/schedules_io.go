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

// listSchedules godoc
//
//	@Summary	list schedules of the caller
//	@Tags		schedules
//	@Security	Bearer
//	@Produce	json
//	@Param		ns				path		string	true	"namespace"
//	@Param		deployment_id	query		string	false	"deployment id"
//	@Param		limit			query		int		false	"page size"
//	@Param		offset			query		int		false	"offset"
//	@Success	200				{object}	model.Page[model.Schedule]
//	@Router		/namespaces/{ns}/schedules [get]
func (h *handlers) listSchedules(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	options := model.ScheduleListOptions{PageOptions: q.page(), DeploymentId: q.str("deployment_id"), Owner: q.str("owner")}
	if q.err != nil {
		writeError(w, q.err)
		return
	}
	result, err := h.c.ListSchedules(r.Context(), token(r), r.PathValue("ns"), options)
	respond(w, http.StatusOK, result, err)
}

// createSchedule godoc
//
//	@Summary		create a schedule
//	@Description	Cron with 5 fields, 6 with seconds first, or a descriptor like @daily. Starts run as the deployment owner.
//	@Tags			schedules
//	@Security		Bearer
//	@Accept			json
//	@Produce		json
//	@Param			ns		path		string				true	"namespace"
//	@Param			body	body		model.ScheduleInput	true	"schedule"
//	@Success		201		{object}	model.Schedule
//	@Failure		400,403,404	{object}	model.Error
//	@Router			/namespaces/{ns}/schedules [post]
func (h *handlers) createSchedule(w http.ResponseWriter, r *http.Request) {
	input, ok := decode[model.ScheduleInput](w, r)
	if !ok {
		return
	}
	result, err := h.c.CreateSchedule(r.Context(), token(r), r.PathValue("ns"), input)
	respond(w, http.StatusCreated, result, err)
}

// getSchedule godoc
//
//	@Summary	get a schedule
//	@Tags		schedules
//	@Security	Bearer
//	@Produce	json
//	@Param		ns	path		string	true	"namespace"
//	@Param		id	path		string	true	"schedule id"
//	@Success	200	{object}	model.Schedule
//	@Failure	404	{object}	model.Error
//	@Router		/namespaces/{ns}/schedules/{id} [get]
func (h *handlers) getSchedule(w http.ResponseWriter, r *http.Request) {
	result, err := h.c.GetSchedule(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"))
	respond(w, http.StatusOK, result, err)
}

// updateSchedule godoc
//
//	@Summary	change a schedule
//	@Tags		schedules
//	@Security	Bearer
//	@Accept		json
//	@Produce	json
//	@Param		ns		path		string				true	"namespace"
//	@Param		id		path		string				true	"schedule id"
//	@Param		body	body		model.ScheduleInput	true	"schedule"
//	@Success	200		{object}	model.Schedule
//	@Failure	400,403,404	{object}	model.Error
//	@Router		/namespaces/{ns}/schedules/{id} [put]
func (h *handlers) updateSchedule(w http.ResponseWriter, r *http.Request) {
	input, ok := decode[model.ScheduleInput](w, r)
	if !ok {
		return
	}
	result, err := h.c.UpdateSchedule(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"), input)
	respond(w, http.StatusOK, result, err)
}

// deleteSchedule godoc
//
//	@Summary	delete a schedule
//	@Tags		schedules
//	@Security	Bearer
//	@Param		ns	path	string	true	"namespace"
//	@Param		id	path	string	true	"schedule id"
//	@Success	204
//	@Failure	404	{object}	model.Error
//	@Router		/namespaces/{ns}/schedules/{id} [delete]
func (h *handlers) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	respondEmpty(w, h.c.DeleteSchedule(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id")))
}

// listIoValues godoc
//
//	@Summary	list process-io values of the caller
//	@Tags		io
//	@Security	Bearer
//	@Produce	json
//	@Param		key_regex		query		string	false	"regular expression on the key"
//	@Param		deployment_id	query		string	false	"bound to this deployment"
//	@Param		instance_id		query		string	false	"bound to this instance"
//	@Param		sort			query		string	false	"key or updated_at, with .asc or .desc"
//	@Param		limit			query		int		false	"page size"
//	@Param		offset			query		int		false	"offset"
//	@Success	200				{object}	model.Page[model.IoValue]
//	@Router		/io/values [get]
func (h *handlers) listIoValues(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	options := model.IoListOptions{PageOptions: q.page(), KeyRegex: q.str("key_regex"), DeploymentId: q.str("deployment_id"), InstanceId: q.str("instance_id"), Sort: q.str("sort")}
	if q.err != nil {
		writeError(w, q.err)
		return
	}
	result, err := h.c.ListIoValues(r.Context(), token(r), options)
	respond(w, http.StatusOK, result, err)
}

// getIoValue godoc
//
//	@Summary	get a process-io value; keys with prefix calculate_ are computed
//	@Tags		io
//	@Security	Bearer
//	@Produce	json
//	@Param		key	path		string	true	"key"
//	@Success	200	{object}	model.IoValue
//	@Failure	404	{object}	model.Error
//	@Router		/io/values/{key} [get]
func (h *handlers) getIoValue(w http.ResponseWriter, r *http.Request) {
	result, err := h.c.GetIoValue(r.Context(), token(r), r.PathValue("key"))
	respond(w, http.StatusOK, result, err)
}

// setIoValue godoc
//
//	@Summary	set a process-io value, optionally bound to a deployment or instance
//	@Tags		io
//	@Security	Bearer
//	@Accept		json
//	@Param		key		path	string				true	"key"
//	@Param		body	body	model.IoValueInput	true	"value"
//	@Success	204
//	@Failure	400,403,404,409	{object}	model.Error
//	@Router		/io/values/{key} [put]
func (h *handlers) setIoValue(w http.ResponseWriter, r *http.Request) {
	input, ok := decode[model.IoValueInput](w, r)
	if !ok {
		return
	}
	respondEmpty(w, h.c.SetIoValue(r.Context(), token(r), r.PathValue("key"), input))
}

// deleteIoValue godoc
//
//	@Summary	delete a process-io value
//	@Tags		io
//	@Security	Bearer
//	@Param		key	path	string	true	"key"
//	@Success	204
//	@Router		/io/values/{key} [delete]
func (h *handlers) deleteIoValue(w http.ResponseWriter, r *http.Request) {
	respondEmpty(w, h.c.DeleteIoValue(r.Context(), token(r), r.PathValue("key")))
}

// ioBulk godoc
//
//	@Summary	write, then read process-io values in one call
//	@Tags		io
//	@Security	Bearer
//	@Accept		json
//	@Produce	json
//	@Param		body	body		model.IoBulk	true	"values to set and keys to get"
//	@Success	200		{array}		model.IoValue
//	@Failure	400,403,409	{object}	model.Error
//	@Router		/io/bulk [post]
func (h *handlers) ioBulk(w http.ResponseWriter, r *http.Request) {
	input, ok := decode[model.IoBulk](w, r)
	if !ok {
		return
	}
	result, err := h.c.IoBulk(r.Context(), token(r), input)
	respond(w, http.StatusOK, result, err)
}
