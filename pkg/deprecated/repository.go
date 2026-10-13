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
	"strconv"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// The routes of process-model-repository (Kong /process/repository) that the designer of the
// web-ui uses. Models are those of the namespace processes; a model keeps its id.

// Process is a model with its latest version in the form of process-model-repository.
type Process struct {
	Id              string `json:"_id"`
	Name            string `json:"name"`
	Date            int64  `json:"date"`
	Owner           string `json:"owner"`
	BpmnXml         string `json:"bpmn_xml"`
	SvgXml          string `json:"svgXML"`
	Publish         bool   `json:"publish"`
	PublishDate     string `json:"publish_date"`
	Description     string `json:"description"`
	LastUpdatedUnix int64  `json:"last_updated_unix"`
}

type PublicCommand struct {
	Publish     bool   `json:"publish"`
	Description string `json:"description"`
}

func (this *Handler) repository() {
	this.handle("GET /repository/v2/processes", this.listProcesses)
	this.handle("GET /repository/processes/{id}", this.getProcess)
	this.handle("POST /repository/processes", this.createProcess)
	this.handle("PUT /repository/processes/{id}", this.updateProcess)
	this.handle("POST /repository/processes/{id}/publish", this.publishProcess)
	this.handle("DELETE /repository/processes/{id}", this.deleteProcess)
}

// process reads the latest version of a model for its content
func (this *Handler) process(r *http.Request, m model.Model) (Process, error) {
	version, err := this.c.GetModelVersion(r.Context(), token(r), processes, m.Id, m.Version)
	if err != nil {
		return Process{}, err
	}
	result := Process{Id: m.Id, Name: m.Name, Date: m.UpdatedAt.UnixMilli(), Owner: m.Owner, BpmnXml: version.Xml, SvgXml: version.Svg,
		Publish: m.Published, Description: m.Description, LastUpdatedUnix: m.UpdatedAt.Unix()}
	if m.PublishedAt != nil {
		result.PublishDate = m.PublishedAt.String()
	}
	if m.Published {
		result.Description = m.PublishDescription
	}
	return result, nil
}

// listProcesses godoc
//
//	@Summary		models of process-model-repository
//	@Tags		deprecated-repository
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		limit	query	int	false	"page size, default 100"
//	@Param		offset	query	int	false	"offset"
//	@Param		search	query	string	false	"part of the name"
//	@Param		sort	query	string	false	"name, date or last_updated_unix with .asc or .desc"
//	@Param		ids	query	string	false	"comma separated ids"
//	@Param		owner	query	string	false	"owner"
//	@Param		not-owner	query	string	false	"other owners"
//	@Success	200	{array}		Process
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/repository/v2/processes [get]
func (this *Handler) listProcesses(w http.ResponseWriter, r *http.Request) {
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
	options := model.ModelListOptions{Search: q.Get("search"), Owner: q.Get("owner"), NotOwner: q.Get("not-owner"),
		Sort: sort(q.Get("sort"), map[string]string{"name": "name", "date": "updated_at", "last_updated_unix": "updated_at"})}
	if q.Has("ids") {
		options.Ids = splitList(q.Get("ids"))
		if len(options.Ids) == 0 {
			writeJson(w, http.StatusOK, []Process{})
			return
		}
		limit, offset = 0, 0
	}
	models, total, err := paged(limit, offset, func(page model.PageOptions) (model.Page[model.Model], error) {
		options.PageOptions = page
		return this.c.ListModels(r.Context(), token(r), processes, options)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	result := make([]Process, 0, len(models))
	for _, m := range models {
		p, err := this.process(r, m)
		if err != nil {
			writeError(w, err)
			return
		}
		result = append(result, p)
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	w.Header().Set("Access-Control-Expose-Headers", "X-Total-Count")
	writeJson(w, http.StatusOK, result)
}

// getProcess godoc
//
//	@Summary		model
//	@Tags		deprecated-repository
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"model id"
//	@Success	200	{object}	Process
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/repository/processes/{id} [get]
func (this *Handler) getProcess(w http.ResponseWriter, r *http.Request) {
	m, err := this.c.GetModel(r.Context(), token(r), processes, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	p, err := this.process(r, m)
	respond(w, p, err)
}

func (this *Handler) saveProcess(w http.ResponseWriter, r *http.Request, save func(input model.ModelInput) (model.Model, error)) {
	p, err := decode[Process](r)
	if err != nil {
		writeError(w, err)
		return
	}
	m, err := save(model.ModelInput{Name: p.Name, Description: p.Description, Xml: p.BpmnXml, Svg: p.SvgXml})
	if err != nil {
		writeError(w, err)
		return
	}
	result, err := this.process(r, m)
	respond(w, result, err)
}

// createProcess godoc
//
//	@Summary		create a model
//	@Tags		deprecated-repository
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		process	body	Process	true	"model"
//	@Success	200	{object}	Process
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/repository/processes [post]
func (this *Handler) createProcess(w http.ResponseWriter, r *http.Request) {
	this.saveProcess(w, r, func(input model.ModelInput) (model.Model, error) {
		return this.c.CreateModel(r.Context(), token(r), processes, input)
	})
}

// updateProcess godoc
//
//	@Summary		update a model
//	@Tags		deprecated-repository
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"model id"
//	@Param		process	body	Process	true	"model"
//	@Success	200	{object}	Process
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/repository/processes/{id} [put]
func (this *Handler) updateProcess(w http.ResponseWriter, r *http.Request) {
	this.saveProcess(w, r, func(input model.ModelInput) (model.Model, error) {
		return this.c.UpdateModel(r.Context(), token(r), processes, r.PathValue("id"), input)
	})
}

// publishProcess godoc
//
//	@Summary		publish a model
//	@Tags		deprecated-repository
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"model id"
//	@Param		command	body	PublicCommand	true	"publish"
//	@Success	200	{object}	Process
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/repository/processes/{id}/publish [post]
func (this *Handler) publishProcess(w http.ResponseWriter, r *http.Request) {
	command, err := decode[PublicCommand](r)
	if err != nil {
		writeError(w, err)
		return
	}
	m, err := this.c.PublishModel(r.Context(), token(r), processes, r.PathValue("id"), model.PublishInput{Published: command.Publish, Description: command.Description})
	if err != nil {
		writeError(w, err)
		return
	}
	p, err := this.process(r, m)
	respond(w, p, err)
}

// deleteProcess godoc
//
//	@Summary		delete a model
//	@Tags		deprecated-repository
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		id	path	string	true	"model id"
//	@Success	200
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/repository/processes/{id} [delete]
func (this *Handler) deleteProcess(w http.ResponseWriter, r *http.Request) {
	if err := this.c.DeleteModel(r.Context(), token(r), processes, r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
