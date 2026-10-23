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

// Package client is the Go client of the Automation-Engine API (CLI-01 to CLI-10). It uses
// the types of the server, so that fields cannot get lost between both sides.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/api"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// Client is the complete API; New returns the HTTP implementation, NewTestClient one that runs
// the engine in the process.
type Client interface {
	api.Controller
}

type ClientImpl struct {
	baseUrl string
	http    *http.Client
}

var _ Client = (*ClientImpl)(nil)

func New(baseUrl string) *ClientImpl {
	return &ClientImpl{baseUrl: strings.TrimSuffix(baseUrl, "/"), http: &http.Client{Timeout: time.Minute}}
}

// WithHttpClient replaces the HTTP client, e.g. for other timeouts or transports.
func (this *ClientImpl) WithHttpClient(c *http.Client) *ClientImpl {
	this.http = c
	return this
}

func path(parts ...string) string {
	escaped := make([]string, len(parts))
	for i, p := range parts {
		escaped[i] = url.PathEscape(p)
	}
	return "/" + strings.Join(escaped, "/")
}

// do sends a request and decodes the response into result (if not nil); errors become *model.Error
func (this *ClientImpl) do(ctx context.Context, token string, method string, p string, query url.Values, body any, result any) error {
	target := this.baseUrl + p
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return model.BadRequest("unable to encode request: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return model.BadRequest("%v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", bearer(token))
	}
	resp, err := this.http.Do(req)
	if err != nil {
		return model.Unavailable("%v %v: %v", method, p, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		e := &model.Error{}
		if json.Unmarshal(raw, e) != nil || e.Kind == "" {
			e = &model.Error{Kind: model.KindFromHttpStatus(resp.StatusCode), Message: fmt.Sprintf("%v %v: %v %v", method, p, resp.Status, strings.TrimSpace(string(raw)))}
		}
		return e
	}
	if result == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	decoder := json.NewDecoder(resp.Body)
	if err = decoder.Decode(result); err != nil {
		return model.Unavailable("unable to decode response of %v %v: %v", method, p, err)
	}
	return nil
}

func bearer(token string) string {
	if len(token) > 7 && strings.EqualFold(token[:7], "bearer ") {
		return token
	}
	return "Bearer " + token
}

func pageQuery(page model.PageOptions) url.Values {
	q := url.Values{}
	if page.Limit > 0 {
		q.Set("limit", strconv.Itoa(page.Limit))
	}
	if page.Offset > 0 {
		q.Set("offset", strconv.Itoa(page.Offset))
	}
	return q
}

func set(q url.Values, name string, value string) {
	if value != "" {
		q.Set(name, value)
	}
}

func setInt(q url.Values, name string, value int64) {
	if value != 0 {
		q.Set(name, strconv.FormatInt(value, 10))
	}
}

func setList(q url.Values, name string, values []string) {
	if len(values) > 0 {
		q.Set(name, strings.Join(values, ","))
	}
}

func (this *ClientImpl) ListNamespaces(ctx context.Context, token string) (result []model.Namespace, err error) {
	err = this.do(ctx, token, http.MethodGet, path("namespaces"), nil, nil, &result)
	return result, err
}

func (this *ClientImpl) GetNamespace(ctx context.Context, token string, namespace string) (result model.Namespace, err error) {
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace), nil, nil, &result)
	return result, err
}

func (this *ClientImpl) Validate(ctx context.Context, token string, namespace string, xml string) (result []model.Issue, err error) {
	err = this.do(ctx, token, http.MethodPost, path("namespaces", namespace, "validate"), nil, api.ValidateInput{Xml: xml}, &result)
	return result, err
}

func (this *ClientImpl) Convert(ctx context.Context, token string, namespace string, xml string) (result model.Conversion, err error) {
	err = this.do(ctx, token, http.MethodPost, path("namespaces", namespace, "convert"), nil, api.ValidateInput{Xml: xml}, &result)
	return result, err
}

func (this *ClientImpl) CreateModel(ctx context.Context, token string, namespace string, input model.ModelInput) (result model.Model, err error) {
	err = this.do(ctx, token, http.MethodPost, path("namespaces", namespace, "models"), nil, input, &result)
	return result, err
}

func (this *ClientImpl) UpdateModel(ctx context.Context, token string, namespace string, id string, input model.ModelInput) (result model.Model, err error) {
	err = this.do(ctx, token, http.MethodPut, path("namespaces", namespace, "models", id), nil, input, &result)
	return result, err
}

func (this *ClientImpl) GetModel(ctx context.Context, token string, namespace string, id string) (result model.Model, err error) {
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "models", id), nil, nil, &result)
	return result, err
}

func (this *ClientImpl) ListModels(ctx context.Context, token string, namespace string, options model.ModelListOptions) (result model.Page[model.Model], err error) {
	q := pageQuery(options.PageOptions)
	set(q, "search", options.Search)
	set(q, "sort", options.Sort)
	setList(q, "ids", options.Ids)
	set(q, "owner", options.Owner)
	set(q, "not_owner", options.NotOwner)
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "models"), q, nil, &result)
	return result, err
}

func (this *ClientImpl) DeleteModel(ctx context.Context, token string, namespace string, id string, force bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "true")
	}
	return this.do(ctx, token, http.MethodDelete, path("namespaces", namespace, "models", id), q, nil, nil)
}

func (this *ClientImpl) PublishModel(ctx context.Context, token string, namespace string, id string, input model.PublishInput) (result model.Model, err error) {
	err = this.do(ctx, token, http.MethodPost, path("namespaces", namespace, "models", id, "publish"), nil, input, &result)
	return result, err
}

func (this *ClientImpl) ListModelVersions(ctx context.Context, token string, namespace string, id string, options model.PageOptions) (result model.Page[model.ModelVersion], err error) {
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "models", id, "versions"), pageQuery(options), nil, &result)
	return result, err
}

func (this *ClientImpl) GetModelVersion(ctx context.Context, token string, namespace string, id string, version int) (result model.ModelVersion, err error) {
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "models", id, "versions", strconv.Itoa(version)), nil, nil, &result)
	return result, err
}

func (this *ClientImpl) CreateDeployment(ctx context.Context, token string, namespace string, input model.DeploymentInput) (result model.Deployment, err error) {
	err = this.do(ctx, token, http.MethodPost, path("namespaces", namespace, "deployments"), nil, input, &result)
	return result, err
}

func (this *ClientImpl) PutDeployment(ctx context.Context, token string, namespace string, id string, input model.DeploymentInput) (result model.Deployment, err error) {
	err = this.do(ctx, token, http.MethodPut, path("namespaces", namespace, "deployments", id), nil, input, &result)
	return result, err
}

func (this *ClientImpl) GetDeployment(ctx context.Context, token string, namespace string, id string) (result model.Deployment, err error) {
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "deployments", id), nil, nil, &result)
	return result, err
}

func (this *ClientImpl) ListDeployments(ctx context.Context, token string, namespace string, options model.DeploymentListOptions) (result model.Page[model.Deployment], err error) {
	q := pageQuery(options.PageOptions)
	set(q, "search", options.Search)
	set(q, "sort", options.Sort)
	setList(q, "ids", options.Ids)
	set(q, "model_id", options.ModelId)
	set(q, "owner", options.Owner)
	set(q, "source", options.Source)
	set(q, "network_id", options.NetworkId)
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "deployments"), q, nil, &result)
	return result, err
}

func (this *ClientImpl) DeploymentInstanceCount(ctx context.Context, token string, namespace string, id string, version int) (result model.Count, err error) {
	q := url.Values{}
	if version > 0 {
		q.Set("version", strconv.Itoa(version))
	}
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "deployments", id, "instance-count"), q, nil, &result)
	return result, err
}

func (this *ClientImpl) ResourceUsage(ctx context.Context, token string, namespace string, kind string, id string) (result model.ResourceUsage, err error) {
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "resource-usage", kind, id), nil, nil, &result)
	return result, err
}

func (this *ClientImpl) SetDeploymentDisabled(ctx context.Context, token string, namespace string, id string, input model.DisabledInput) (result model.Deployment, err error) {
	err = this.do(ctx, token, http.MethodPut, path("namespaces", namespace, "deployments", id, "disabled"), nil, input, &result)
	return result, err
}

func (this *ClientImpl) DeleteDeployment(ctx context.Context, token string, namespace string, id string) error {
	return this.do(ctx, token, http.MethodDelete, path("namespaces", namespace, "deployments", id), nil, nil, nil)
}

func (this *ClientImpl) ListDeploymentVersions(ctx context.Context, token string, namespace string, id string, options model.PageOptions) (result model.Page[model.DeploymentVersion], err error) {
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "deployments", id, "versions"), pageQuery(options), nil, &result)
	return result, err
}

func (this *ClientImpl) GetDeploymentVersion(ctx context.Context, token string, namespace string, id string, version int) (result model.DeploymentVersion, err error) {
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "deployments", id, "versions", strconv.Itoa(version)), nil, nil, &result)
	return result, err
}

func (this *ClientImpl) GetDeploymentSource(ctx context.Context, token string, namespace string, id string, version int) (result model.ModelVersion, err error) {
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "deployments", id, "versions", strconv.Itoa(version), "source"), nil, nil, &result)
	return result, err
}

func (this *ClientImpl) GetStartParameters(ctx context.Context, token string, namespace string, deploymentId string) (result []model.StartParameter, err error) {
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "deployments", deploymentId, "start-parameters"), nil, nil, &result)
	return result, err
}

func (this *ClientImpl) StartInstance(ctx context.Context, token string, namespace string, input model.StartInstanceInput) (result model.Instance, err error) {
	err = this.do(ctx, token, http.MethodPost, path("namespaces", namespace, "instances"), nil, input, &result)
	return result, err
}

func (this *ClientImpl) PutInstance(ctx context.Context, token string, namespace string, id string, input model.StartInstanceInput) (result model.Instance, err error) {
	err = this.do(ctx, token, http.MethodPut, path("namespaces", namespace, "instances", id), nil, input, &result)
	return result, err
}

func (this *ClientImpl) UpdateInstanceInfo(ctx context.Context, token string, namespace string, id string, input model.InstanceInfoInput) (result model.Instance, err error) {
	err = this.do(ctx, token, http.MethodPut, path("namespaces", namespace, "instances", id, "info"), nil, input, &result)
	return result, err
}

func (this *ClientImpl) GetInstance(ctx context.Context, token string, namespace string, id string) (result model.Instance, err error) {
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "instances", id), nil, nil, &result)
	return result, err
}

func (this *ClientImpl) ListInstances(ctx context.Context, token string, namespace string, options model.InstanceListOptions) (result model.Page[model.Instance], err error) {
	q := pageQuery(options.PageOptions)
	set(q, "state", options.State)
	set(q, "deployment_id", options.DeploymentId)
	set(q, "deployment_name", options.DeploymentName)
	set(q, "model_id", options.ModelId)
	setList(q, "ids", options.Ids)
	set(q, "owner", options.Owner)
	set(q, "sort", options.Sort)
	set(q, "network_id", options.NetworkId)
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "instances"), q, nil, &result)
	return result, err
}

func (this *ClientImpl) DeleteInstances(ctx context.Context, token string, namespace string, ids []string) error {
	if len(ids) == 0 {
		return model.BadRequest("ids are required")
	}
	q := url.Values{}
	setList(q, "ids", ids)
	return this.do(ctx, token, http.MethodDelete, path("namespaces", namespace, "instances"), q, nil, nil)
}

func (this *ClientImpl) StopInstances(ctx context.Context, token string, namespace string, ids []string) error {
	return this.do(ctx, token, http.MethodPost, path("namespaces", namespace, "instances", "stop"), nil, model.IdsInput{Ids: ids}, nil)
}

func (this *ClientImpl) RestartInstance(ctx context.Context, token string, namespace string, id string, input model.RestartInput) (result model.Instance, err error) {
	err = this.do(ctx, token, http.MethodPost, path("namespaces", namespace, "instances", id, "restart"), nil, input, &result)
	return result, err
}

func (this *ClientImpl) ListRuns(ctx context.Context, token string, namespace string, id string) (result []model.Run, err error) {
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "instances", id, "runs"), nil, nil, &result)
	return result, err
}

func (this *ClientImpl) GetVariables(ctx context.Context, token string, namespace string, id string) (result []model.Variable, err error) {
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "instances", id, "variables"), nil, nil, &result)
	return result, err
}

func (this *ClientImpl) SetVariable(ctx context.Context, token string, namespace string, id string, name string, value any) error {
	return this.do(ctx, token, http.MethodPut, path("namespaces", namespace, "instances", id, "variables", name), nil, model.VariableInput{Value: value}, nil)
}

func (this *ClientImpl) ListActivities(ctx context.Context, token string, namespace string, id string, options model.ActivityListOptions) (result model.ActivityPage, err error) {
	q := url.Values{}
	setInt(q, "run", int64(options.Run))
	setInt(q, "after_seq", options.AfterSeq)
	setInt(q, "limit", int64(options.Limit))
	set(q, "kind", options.Kind)
	set(q, "element_id", options.ElementId)
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "instances", id, "activities"), q, nil, &result)
	return result, err
}

func (this *ClientImpl) ListElementStats(ctx context.Context, token string, namespace string, id string, run int) (result []model.ElementStats, err error) {
	q := url.Values{}
	setInt(q, "run", int64(run))
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "instances", id, "element-stats"), q, nil, &result)
	return result, err
}

func (this *ClientImpl) GetVariable(ctx context.Context, token string, namespace string, id string, name string, elementId string) (result model.Variable, err error) {
	q := url.Values{}
	set(q, "element_id", elementId)
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "instances", id, "variables", name), q, nil, &result)
	return result, err
}

func (this *ClientImpl) DebugState(ctx context.Context, token string, namespace string, id string, options model.DebugOptions) (result model.DebugState, err error) {
	q := url.Values{}
	if options.EvaluateEvents {
		q.Set("evaluate_events", "true")
	}
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "instances", id, "debug"), q, nil, &result)
	return result, err
}

func historyQuery(options model.HistoryOptions) url.Values {
	q := url.Values{}
	setInt(q, "seq", options.Seq)
	if options.Time != nil {
		q.Set("time", options.Time.Format(time.RFC3339Nano))
	}
	return q
}

func (this *ClientImpl) HistoryState(ctx context.Context, token string, namespace string, id string, options model.HistoryOptions) (result model.DebugState, err error) {
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "instances", id, "history"), historyQuery(options), nil, &result)
	return result, err
}

func (this *ClientImpl) HistoryVariable(ctx context.Context, token string, namespace string, id string, name string, elementId string, options model.HistoryOptions) (result model.HistoryVariable, err error) {
	q := historyQuery(options)
	set(q, "element_id", elementId)
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "instances", id, "history", "variables", name), q, nil, &result)
	return result, err
}

func (this *ClientImpl) EventEvaluations(ctx context.Context, token string, namespace string, id string, options model.EventReconstructionOptions) (result model.EventReconstruction, err error) {
	q := url.Values{}
	set(q, "element_id", options.ElementId)
	setInt(q, "run", int64(options.Run))
	setInt(q, "limit", int64(options.Limit))
	if options.After != nil {
		q.Set("after", options.After.Format(time.RFC3339Nano))
	}
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "instances", id, "event-evaluations"), q, nil, &result)
	return result, err
}

func (this *ClientImpl) UpdateParameters(ctx context.Context, token string, namespace string, id string, input model.ParametersInput) (result model.Instance, err error) {
	err = this.do(ctx, token, http.MethodPut, path("namespaces", namespace, "instances", id, "parameters"), nil, input, &result)
	return result, err
}

func (this *ClientImpl) DeleteVariable(ctx context.Context, token string, namespace string, id string, name string) error {
	return this.do(ctx, token, http.MethodDelete, path("namespaces", namespace, "instances", id, "variables", name), nil, nil, nil)
}

func (this *ClientImpl) DeploymentParameters(ctx context.Context, token string, namespace string, id string) (result []model.ParameterDescription, err error) {
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "deployments", id, "parameters"), nil, nil, &result)
	return result, err
}

func (this *ClientImpl) MaintenanceParameters(ctx context.Context, token string, namespace string, id string, procedure string) (result []model.ParameterDescription, err error) {
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "instances", id, "maintenance-procedures", procedure, "parameters"), nil, nil, &result)
	return result, err
}

func (this *ClientImpl) ListMaintenanceProcedures(ctx context.Context, token string, namespace string, id string) (result []model.MaintenanceProcedure, err error) {
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "instances", id, "maintenance-procedures"), nil, nil, &result)
	return result, err
}

func (this *ClientImpl) StartMaintenance(ctx context.Context, token string, namespace string, id string, procedure string, input model.MaintenanceStartInput) (result model.Instance, err error) {
	err = this.do(ctx, token, http.MethodPost, path("namespaces", namespace, "instances", id, "maintenance-procedures", procedure, "start"), nil, input, &result)
	return result, err
}

func (this *ClientImpl) ListModules(ctx context.Context, token string, namespace string, options model.ModuleListOptions) (result model.Page[model.Module], err error) {
	q := pageQuery(options.PageOptions)
	set(q, "instance_id", options.InstanceId)
	set(q, "module_type", options.ModuleType)
	set(q, "key", options.Key)
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "modules"), q, nil, &result)
	return result, err
}

func (this *ClientImpl) GetModule(ctx context.Context, token string, namespace string, id string) (result model.Module, err error) {
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "modules", id), nil, nil, &result)
	return result, err
}

func (this *ClientImpl) DeleteModule(ctx context.Context, token string, namespace string, id string) error {
	return this.do(ctx, token, http.MethodDelete, path("namespaces", namespace, "modules", id), nil, nil, nil)
}

func (this *ClientImpl) ListIncidents(ctx context.Context, token string, namespace string, options model.IncidentListOptions) (result model.Page[model.Incident], err error) {
	q := pageQuery(options.PageOptions)
	set(q, "instance_id", options.InstanceId)
	setInt(q, "run", int64(options.Run))
	set(q, "deployment_id", options.DeploymentId)
	set(q, "element_id", options.ElementId)
	set(q, "sort", options.Sort)
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "incidents"), q, nil, &result)
	return result, err
}

func (this *ClientImpl) CorrelateMessage(ctx context.Context, token string, input model.MessageInput) (result model.CorrelationResult, err error) {
	err = this.do(ctx, token, http.MethodPost, path("messages"), nil, input, &result)
	return result, err
}

func (this *ClientImpl) PutNamespace(ctx context.Context, token string, name string, ns model.Namespace) (result model.Namespace, err error) {
	err = this.do(ctx, token, http.MethodPut, path("namespaces", name), nil, ns, &result)
	return result, err
}

func (this *ClientImpl) DeleteNamespace(ctx context.Context, token string, name string) error {
	return this.do(ctx, token, http.MethodDelete, path("namespaces", name), nil, nil, nil)
}

func (this *ClientImpl) SetOwner(ctx context.Context, token string, namespace string, kind model.Kind, id string, input model.OwnerInput) error {
	if err := kind.Valid(); err != nil {
		return err
	}
	return this.do(ctx, token, http.MethodPut, path("namespaces", namespace, string(kind), id, "owner"), nil, input, nil)
}

func (this *ClientImpl) ResetRestarts(ctx context.Context, token string, namespace string, id string) (result model.Deployment, err error) {
	err = this.do(ctx, token, http.MethodPost, path("namespaces", namespace, "deployments", id, "reset-restarts"), nil, nil, &result)
	return result, err
}

func (this *ClientImpl) ListNetworks(ctx context.Context, token string) (result []model.Network, err error) {
	err = this.do(ctx, token, http.MethodGet, path("networks"), nil, nil, &result)
	return result, err
}

func (this *ClientImpl) GetNetwork(ctx context.Context, token string, networkId string) (result model.Network, err error) {
	err = this.do(ctx, token, http.MethodGet, path("networks", networkId), nil, nil, &result)
	return result, err
}

func (this *ClientImpl) GetUserDependents(ctx context.Context, token string, userId string) (result model.UserDependents, err error) {
	err = this.do(ctx, token, http.MethodGet, path("users", userId, "dependents"), nil, nil, &result)
	return result, err
}

func (this *ClientImpl) DeleteUser(ctx context.Context, token string, userId string) error {
	return this.do(ctx, token, http.MethodDelete, path("users", userId), nil, nil, nil)
}

func (this *ClientImpl) GetUserSettings(ctx context.Context, token string, userId string) (result model.UserSettings, err error) {
	err = this.do(ctx, token, http.MethodGet, path("users", userId, "settings"), nil, nil, &result)
	return result, err
}

func (this *ClientImpl) SetUserSettings(ctx context.Context, token string, userId string, settings model.UserSettings) (result model.UserSettings, err error) {
	err = this.do(ctx, token, http.MethodPut, path("users", userId, "settings"), nil, settings, &result)
	return result, err
}

func (this *ClientImpl) PrepareDeployment(ctx context.Context, token string, namespace string, input model.PrepareInput) (result model.PreparedDeployment, err error) {
	err = this.do(ctx, token, http.MethodPost, path("namespaces", namespace, "prepared-deployments"), nil, input, &result)
	return result, err
}

func (this *ClientImpl) ListSchedules(ctx context.Context, token string, namespace string, options model.ScheduleListOptions) (result model.Page[model.Schedule], err error) {
	q := pageQuery(options.PageOptions)
	set(q, "deployment_id", options.DeploymentId)
	set(q, "owner", options.Owner)
	set(q, "created_by", options.CreatedBy)
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "schedules"), q, nil, &result)
	return result, err
}

func (this *ClientImpl) GetSchedule(ctx context.Context, token string, namespace string, id string) (result model.Schedule, err error) {
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "schedules", id), nil, nil, &result)
	return result, err
}

func (this *ClientImpl) CreateSchedule(ctx context.Context, token string, namespace string, input model.ScheduleInput) (result model.Schedule, err error) {
	err = this.do(ctx, token, http.MethodPost, path("namespaces", namespace, "schedules"), nil, input, &result)
	return result, err
}

func (this *ClientImpl) UpdateSchedule(ctx context.Context, token string, namespace string, id string, input model.ScheduleInput) (result model.Schedule, err error) {
	err = this.do(ctx, token, http.MethodPut, path("namespaces", namespace, "schedules", id), nil, input, &result)
	return result, err
}

func (this *ClientImpl) DeleteSchedule(ctx context.Context, token string, namespace string, id string) error {
	return this.do(ctx, token, http.MethodDelete, path("namespaces", namespace, "schedules", id), nil, nil, nil)
}

func (this *ClientImpl) ListIoValues(ctx context.Context, token string, options model.IoListOptions) (result model.Page[model.IoValue], err error) {
	q := pageQuery(options.PageOptions)
	set(q, "key_regex", options.KeyRegex)
	set(q, "deployment_id", options.DeploymentId)
	set(q, "instance_id", options.InstanceId)
	set(q, "sort", options.Sort)
	err = this.do(ctx, token, http.MethodGet, "/io/values", q, nil, &result)
	return result, err
}

// keys may contain slashes; each segment is escaped on its own
func ioPath(key string) string {
	return "/io/values" + path(strings.Split(key, "/")...)
}

func (this *ClientImpl) GetIoValue(ctx context.Context, token string, key string) (result model.IoValue, err error) {
	err = this.do(ctx, token, http.MethodGet, ioPath(key), nil, nil, &result)
	return result, err
}

func (this *ClientImpl) SetIoValue(ctx context.Context, token string, key string, input model.IoValueInput) error {
	return this.do(ctx, token, http.MethodPut, ioPath(key), nil, input, nil)
}

func (this *ClientImpl) DeleteIoValue(ctx context.Context, token string, key string) error {
	return this.do(ctx, token, http.MethodDelete, ioPath(key), nil, nil, nil)
}

func (this *ClientImpl) IoBulk(ctx context.Context, token string, bulk model.IoBulk) (result []model.IoValue, err error) {
	err = this.do(ctx, token, http.MethodPost, "/io/bulk", nil, bulk, &result)
	return result, err
}

func (this *ClientImpl) ListEventDescriptions(ctx context.Context, token string, namespace string, deploymentId string) (result []model.EventDescription, err error) {
	err = this.do(ctx, token, http.MethodGet, path("namespaces", namespace, "deployments", deploymentId, "event-descriptions"), nil, nil, &result)
	return result, err
}
