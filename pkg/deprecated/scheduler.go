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

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// The routes of process-scheduler (Kong /process/scheduler), used by the web-ui and by
// user-management. Schedules are those of the namespace processes.

type ScheduleEntry struct {
	Id                  string  `json:"id"`
	Cron                string  `json:"cron"`
	ProcessDeploymentId string  `json:"process_deployment_id"`
	ProcessAlias        *string `json:"process_alias,omitempty"`
	Disabled            *bool   `json:"disabled,omitempty"`
	CreatedBy           *string `json:"created_by,omitempty"`
}

func (this *Handler) scheduler() {
	this.handle("GET /scheduler/schedules", this.listSchedules)
	this.handle("POST /scheduler/schedules", this.createSchedule)
	this.handle("PUT /scheduler/schedules/{id}", this.updateSchedule)
	this.handle("DELETE /scheduler/schedules/{id}", this.deleteSchedule)
}

func optional[T comparable](value T) *T {
	var zero T
	if value == zero {
		return nil
	}
	return &value
}

func scheduleEntry(s model.Schedule) ScheduleEntry {
	disabled := s.Disabled
	return ScheduleEntry{Id: s.Id, Cron: s.Cron, ProcessDeploymentId: s.DeploymentId, ProcessAlias: optional(s.Alias), Disabled: &disabled, CreatedBy: optional(s.CreatedBy)}
}

func scheduleInput(e ScheduleEntry) model.ScheduleInput {
	input := model.ScheduleInput{DeploymentId: e.ProcessDeploymentId, Cron: e.Cron}
	if e.ProcessAlias != nil {
		input.Alias = *e.ProcessAlias
	}
	if e.Disabled != nil {
		input.Disabled = *e.Disabled
	}
	if e.CreatedBy != nil {
		input.CreatedBy = *e.CreatedBy
	}
	return input
}

// listSchedules godoc
//
//	@Summary		schedules of process-scheduler
//	@Tags		deprecated-scheduler
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		created_by	query	string	false	"client reference"
//	@Success	200	{array}		ScheduleEntry
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/scheduler/schedules [get]
func (this *Handler) listSchedules(w http.ResponseWriter, r *http.Request) {
	options := model.ScheduleListOptions{CreatedBy: r.URL.Query().Get("created_by")}
	schedules, err := all(func(page model.PageOptions) (model.Page[model.Schedule], error) {
		options.PageOptions = page
		return this.c.ListSchedules(r.Context(), token(r), processes, options)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	result := make([]ScheduleEntry, 0, len(schedules))
	for _, s := range schedules {
		result = append(result, scheduleEntry(s))
	}
	writeJson(w, http.StatusOK, result)
}

// createSchedule godoc
//
//	@Summary		create a schedule
//	@Tags		deprecated-scheduler
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		entry	body	ScheduleEntry	true	"schedule"
//	@Success	200	{object}	ScheduleEntry
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/scheduler/schedules [post]
func (this *Handler) createSchedule(w http.ResponseWriter, r *http.Request) {
	entry, err := decode[ScheduleEntry](r)
	if err != nil {
		writeError(w, err)
		return
	}
	s, err := this.c.CreateSchedule(r.Context(), token(r), processes, scheduleInput(entry))
	respond(w, scheduleEntry(s), err)
}

// updateSchedule godoc
//
//	@Summary		update a schedule
//	@Tags		deprecated-scheduler
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"schedule id"
//	@Param		entry	body	ScheduleEntry	true	"schedule"
//	@Success	200	{object}	ScheduleEntry
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/scheduler/schedules/{id} [put]
func (this *Handler) updateSchedule(w http.ResponseWriter, r *http.Request) {
	entry, err := decode[ScheduleEntry](r)
	if err != nil {
		writeError(w, err)
		return
	}
	if entry.Id != "" && entry.Id != r.PathValue("id") {
		writeError(w, model.BadRequest("the id of the body differs from the path"))
		return
	}
	s, err := this.c.UpdateSchedule(r.Context(), token(r), processes, r.PathValue("id"), scheduleInput(entry))
	respond(w, scheduleEntry(s), err)
}

// deleteSchedule answers an unknown id with success, as before
//
//	@Summary		delete a schedule; an unknown id is no error
//	@Tags		deprecated-scheduler
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"schedule id"
//	@Success	200
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/scheduler/schedules/{id} [delete]
func (this *Handler) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	err := this.c.DeleteSchedule(r.Context(), token(r), processes, r.PathValue("id"))
	if errors.Is(err, model.ErrNotFound) {
		err = nil
	}
	if err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
