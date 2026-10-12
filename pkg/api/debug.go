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
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

func historyOptions(q *query) model.HistoryOptions {
	options := model.HistoryOptions{Seq: q.int64("seq")}
	if raw := q.str("time"); raw != "" {
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil && q.err == nil {
			q.err = model.BadRequest("query parameter time must be an RFC 3339 time")
		}
		options.Time = &t
	}
	return options
}

// getDebugState godoc
//
//	@Summary		live state of an instance for the debugger
//	@Description	Active elements with their wait reasons, variables, open incidents and pass counters (DBG-02). Values above the transfer limit are cut and marked; GET .../variables/{name} loads them (DBG-04).
//	@Tags			debugging
//	@Security		Bearer
//	@Produce		json
//	@Param			ns				path		string	true	"namespace"
//	@Param			id				path		string	true	"instance id"
//	@Param			evaluate_events	query		bool	false	"evaluate the current value of waiting device events (DBG-18)"
//	@Success		200				{object}	model.DebugState
//	@Failure		404				{object}	model.Error
//	@Router			/namespaces/{ns}/instances/{id}/debug [get]
func (h *handlers) getDebugState(w http.ResponseWriter, r *http.Request) {
	options := model.DebugOptions{EvaluateEvents: newQuery(r).str("evaluate_events") == "true"}
	result, err := h.c.DebugState(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"), options)
	respond(w, http.StatusOK, result, err)
}

// getHistoryState godoc
//
//	@Summary		state of an instance after a step of its history
//	@Description	Reconstructed from the latest snapshot before the step and the activity entries after it (DBG-13, DBG-14). Without seq and time the latest step. first_seq is the earliest step that can still be reconstructed.
//	@Tags			debugging
//	@Security		Bearer
//	@Produce		json
//	@Param			ns		path		string	true	"namespace"
//	@Param			id		path		string	true	"instance id"
//	@Param			seq		query		int		false	"activity sequence number"
//	@Param			time	query		string	false	"last step at or before this RFC 3339 time"
//	@Success		200		{object}	model.DebugState
//	@Failure		404		{object}	model.Error
//	@Router			/namespaces/{ns}/instances/{id}/history [get]
func (h *handlers) getHistoryState(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	options := historyOptions(q)
	if q.err != nil {
		writeError(w, q.err)
		return
	}
	result, err := h.c.HistoryState(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"), options)
	respond(w, http.StatusOK, result, err)
}

// getHistoryVariable godoc
//
//	@Summary		complete value of a variable at a step of the history
//	@Description	Values the activity log holds cut are complete only at snapshots; available is false otherwise (DBG-04).
//	@Tags			debugging
//	@Security		Bearer
//	@Produce		json
//	@Param			ns			path		string	true	"namespace"
//	@Param			id			path		string	true	"instance id"
//	@Param			name		path		string	true	"variable name"
//	@Param			element_id	query		string	false	"local variable of this element"
//	@Param			seq			query		int		false	"activity sequence number"
//	@Param			time		query		string	false	"last step at or before this RFC 3339 time"
//	@Success		200			{object}	model.HistoryVariable
//	@Failure		404			{object}	model.Error
//	@Router			/namespaces/{ns}/instances/{id}/history/variables/{name} [get]
func (h *handlers) getHistoryVariable(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	options := historyOptions(q)
	if q.err != nil {
		writeError(w, q.err)
		return
	}
	result, err := h.c.HistoryVariable(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"), r.PathValue("name"), q.str("element_id"), options)
	respond(w, http.StatusOK, result, err)
}

// getVariable godoc
//
//	@Summary	complete current value of a variable
//	@Tags		debugging
//	@Security	Bearer
//	@Produce	json
//	@Param		ns			path		string	true	"namespace"
//	@Param		id			path		string	true	"instance id"
//	@Param		name		path		string	true	"variable name"
//	@Param		element_id	query		string	false	"local variable of a token at this element"
//	@Success	200			{object}	model.Variable
//	@Failure	404			{object}	model.Error
//	@Router		/namespaces/{ns}/instances/{id}/variables/{name} [get]
func (h *handlers) getVariable(w http.ResponseWriter, r *http.Request) {
	result, err := h.c.GetVariable(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"), r.PathValue("name"), newQuery(r).str("element_id"))
	respond(w, http.StatusOK, result, err)
}

// getEventEvaluations godoc
//
//	@Summary		evaluations of a device event reconstructed from the time series
//	@Description	For the times the element waited, the values of the event sources are read from timescale-wrapper with the token of the owner and evaluated like the event worker does. They are shown next to the actual correlations; deviations come with their possible causes (EVT-10, EVT-11). Paged per source; next continues.
//	@Tags			debugging
//	@Security		Bearer
//	@Produce		json
//	@Param			ns			path		string	true	"namespace"
//	@Param			id			path		string	true	"instance id"
//	@Param			element_id	query		string	true	"BPMN element id of the event"
//	@Param			run			query		int		false	"run, default the current one"
//	@Param			after		query		string	false	"evaluations after this RFC 3339 time, the next of the previous page"
//	@Param			limit		query		int		false	"evaluations per source, default 100, max 1000"
//	@Success		200			{object}	model.EventReconstruction
//	@Failure		404			{object}	model.Error
//	@Failure		503			{object}	model.Error
//	@Router			/namespaces/{ns}/instances/{id}/event-evaluations [get]
func (h *handlers) getEventEvaluations(w http.ResponseWriter, r *http.Request) {
	q := newQuery(r)
	options := model.EventReconstructionOptions{ElementId: q.str("element_id"), Run: q.int("run"), Limit: q.int("limit")}
	if raw := q.str("after"); raw != "" {
		after, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			writeError(w, model.BadRequest("query parameter after must be an RFC 3339 time"))
			return
		}
		options.After = &after
	}
	if q.err != nil {
		writeError(w, q.err)
		return
	}
	result, err := h.c.EventEvaluations(r.Context(), token(r), r.PathValue("ns"), r.PathValue("id"), options)
	respond(w, http.StatusOK, result, err)
}
