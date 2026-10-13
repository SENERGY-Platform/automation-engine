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

// The routes of process-io-api (Kong /process-io) for users: variables, values and bulk. The
// routes the worker used are gone with the worker. A process definition id is the deployment.

type IoVariable struct {
	Key                 string `json:"key"`
	Value               any    `json:"value"`
	ProcessDefinitionId string `json:"process_definition_id,omitempty"`
	ProcessInstanceId   string `json:"process_instance_id,omitempty"`
}

type IoVariableWithUnixTimestamp struct {
	IoVariable
	UnixTimestampInS int64 `json:"unix_timestamp_in_s"`
}

type IoBulkRequest struct {
	Get []string     `json:"get"`
	Set []IoVariable `json:"set"`
}

func (this *Handler) io() {
	this.handle("GET /io/variables", this.listIo)
	this.handle("GET /io/count/variables", this.countIo)
	this.handle("GET /io/variables/{key...}", this.getIoVariable)
	this.handle("PUT /io/variables/{key}", this.putIoVariable)
	this.handle("DELETE /io/variables/{key}", this.deleteIo)
	this.handle("GET /io/values/{key...}", this.getIoValue)
	this.handle("PUT /io/values/{key}", this.putIoValue)
	this.handle("DELETE /io/values/{key}", this.deleteIo)
	this.handle("POST /io/bulk", this.bulkIo)
}

func ioVariable(v model.IoValue) IoVariableWithUnixTimestamp {
	result := IoVariableWithUnixTimestamp{IoVariable: IoVariable{Key: v.Key, Value: v.Value, ProcessInstanceId: v.InstanceId}}
	if v.DeploymentId != "" {
		result.ProcessDefinitionId = v.DeploymentId
	}
	if !v.UpdatedAt.IsZero() {
		result.UnixTimestampInS = v.UpdatedAt.Unix()
	}
	return result
}

func ioOptions(r *http.Request) model.IoListOptions {
	q := r.URL.Query()
	options := model.IoListOptions{KeyRegex: q.Get("key_regex"), InstanceId: q.Get("process_instance_id"),
		Sort: sort(q.Get("sort"), map[string]string{"key": "key", "unix_timestamp_in_s": "updated_at"})}
	if definition := q.Get("process_definition_id"); definition != "" {
		options.DeploymentId = deploymentOf(definition)
	}
	return options
}

// listIo godoc
//
//	@Summary		process-io values
//	@Tags		deprecated-io
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		limit	query	int	false	"page size, 0 is all"
//	@Param		offset	query	int	false	"offset"
//	@Param		sort	query	string	false	"key or unix_timestamp_in_s with .asc or .desc"
//	@Param		key_regex	query	string	false	"regular expression on the key"
//	@Param		process_instance_id	query	string	false	"instance"
//	@Param		process_definition_id	query	string	false	"process definition or deployment"
//	@Success	200	{array}		IoVariableWithUnixTimestamp
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/io/variables [get]
func (this *Handler) listIo(w http.ResponseWriter, r *http.Request) {
	limit, err := intParam(r, "limit", 0)
	if err != nil {
		writeError(w, err)
		return
	}
	offset, err := intParam(r, "offset", 0)
	if err != nil {
		writeError(w, err)
		return
	}
	options := ioOptions(r)
	values, _, err := paged(limit, offset, func(page model.PageOptions) (model.Page[model.IoValue], error) {
		options.PageOptions = page
		return this.c.ListIoValues(r.Context(), token(r), options)
	})
	if err != nil {
		writeError(w, err)
		return
	}
	result := make([]IoVariableWithUnixTimestamp, 0, len(values))
	for _, v := range values {
		result = append(result, ioVariable(v))
	}
	writeJson(w, http.StatusOK, result)
}

// countIo godoc
//
//	@Summary		number of process-io values
//	@Tags		deprecated-io
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		key_regex	query	string	false	"regular expression on the key"
//	@Success	200	{object}	Count
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/io/count/variables [get]
func (this *Handler) countIo(w http.ResponseWriter, r *http.Request) {
	options := ioOptions(r)
	options.PageOptions = model.PageOptions{Limit: 1}
	page, err := this.c.ListIoValues(r.Context(), token(r), options)
	respond(w, Count{Count: page.Total}, err)
}

// ioValue reads a value; a missing key is a null value, as before
func (this *Handler) ioValue(r *http.Request) (IoVariableWithUnixTimestamp, error) {
	key := r.PathValue("key")
	v, err := this.c.GetIoValue(r.Context(), token(r), key)
	if errors.Is(err, model.ErrNotFound) {
		return IoVariableWithUnixTimestamp{IoVariable: IoVariable{Key: key}}, nil
	}
	return ioVariable(v), err
}

// getIoVariable godoc
//
//	@Summary		process-io value; a missing key is null
//	@Tags		deprecated-io
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		key	path	string	true	"key"
//	@Success	200	{object}	IoVariableWithUnixTimestamp
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/io/variables/{key} [get]
func (this *Handler) getIoVariable(w http.ResponseWriter, r *http.Request) {
	v, err := this.ioValue(r)
	respond(w, v, err)
}

// getIoValue godoc
//
//	@Summary		raw process-io value
//	@Tags		deprecated-io
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		key	path	string	true	"key"
//	@Success	200	{object}	object
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/io/values/{key} [get]
func (this *Handler) getIoValue(w http.ResponseWriter, r *http.Request) {
	v, err := this.ioValue(r)
	respond(w, v.Value, err)
}

// putIoVariable godoc
//
//	@Summary		set a process-io value
//	@Tags		deprecated-io
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		key	path	string	true	"key"
//	@Param		variable	body	IoVariable	true	"value"
//	@Success	200
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/io/variables/{key} [put]
func (this *Handler) putIoVariable(w http.ResponseWriter, r *http.Request) {
	v, err := decode[IoVariable](r)
	if err != nil {
		writeError(w, err)
		return
	}
	if v.Key != r.PathValue("key") {
		writeError(w, model.BadRequest("the key of the body differs from the path"))
		return
	}
	input := model.IoValueInput{Value: v.Value, InstanceId: v.ProcessInstanceId}
	if v.ProcessDefinitionId != "" {
		input.DeploymentId = deploymentOf(v.ProcessDefinitionId)
	}
	this.noContent(w, this.c.SetIoValue(r.Context(), token(r), v.Key, input))
}

// putIoValue godoc
//
//	@Summary		set a raw process-io value
//	@Tags		deprecated-io
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		key	path	string	true	"key"
//	@Param		value	body	object	true	"JSON value"
//	@Success	200
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/io/values/{key} [put]
func (this *Handler) putIoValue(w http.ResponseWriter, r *http.Request) {
	value, err := decode[any](r)
	if err == nil {
		err = this.c.SetIoValue(r.Context(), token(r), r.PathValue("key"), model.IoValueInput{Value: value})
	}
	this.noContent(w, err)
}

// deleteIo godoc
//
//	@Summary		delete a process-io value
//	@Tags		deprecated-io
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		key	path	string	true	"key"
//	@Success	200
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/io/variables/{key} [delete]
//	@Router		/deprecated/io/values/{key} [delete]
func (this *Handler) deleteIo(w http.ResponseWriter, r *http.Request) {
	err := this.c.DeleteIoValue(r.Context(), token(r), r.PathValue("key"))
	if errors.Is(err, model.ErrNotFound) {
		err = nil
	}
	this.noContent(w, err)
}

// bulkIo godoc
//
//	@Summary		set, then read process-io values
//	@Tags		deprecated-io
//	@Deprecated
//	@Security	Bearer
//	@Produce	json
//	@Param		bulk	body	IoBulkRequest	true	"requests"
//	@Success	200	{array}		IoVariableWithUnixTimestamp
//	@Failure	400,403,404	{string}	string
//	@Router		/deprecated/io/bulk [post]
func (this *Handler) bulkIo(w http.ResponseWriter, r *http.Request) {
	request, err := decode[IoBulkRequest](r)
	if err != nil {
		writeError(w, err)
		return
	}
	bulk := model.IoBulk{Get: request.Get, Set: []model.IoValue{}}
	for _, v := range request.Set {
		value := model.IoValue{Key: v.Key, Value: v.Value, InstanceId: v.ProcessInstanceId}
		if v.ProcessDefinitionId != "" {
			value.DeploymentId = deploymentOf(v.ProcessDefinitionId)
		}
		bulk.Set = append(bulk.Set, value)
	}
	values, err := this.c.IoBulk(r.Context(), token(r), bulk)
	if err != nil {
		writeError(w, err)
		return
	}
	result := make([]IoVariableWithUnixTimestamp, 0, len(values))
	for _, v := range values {
		result = append(result, ioVariable(v))
	}
	writeJson(w, http.StatusOK, result)
}

func (this *Handler) noContent(w http.ResponseWriter, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
