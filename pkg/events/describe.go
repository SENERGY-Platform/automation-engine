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

// Package events derives event descriptions at deploy time and runs the event worker role,
// which evaluates device and import data and correlates matching events (EVT-01 to EVT-08).
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/commons"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/prepare"
	devicerepository "github.com/SENERGY-Platform/device-repository/v3/lib/client"
	devicerepomodel "github.com/SENERGY-Platform/device-repository/v3/lib/model"
	"github.com/SENERGY-Platform/models/go/models"
	"github.com/google/uuid"
)

// Imports reads import instances and types with the token of the deployment owner.
type Imports interface {
	Instance(token string, id string) (models.Import, error)
	Type(token string, id string) (models.ImportType, error)
}

type httpImports struct {
	deployUrl     string
	repositoryUrl string
}

// NewImports reads from import-deploy and import-repository.
func NewImports(importDeployUrl string, importRepositoryUrl string) Imports {
	return &httpImports{deployUrl: importDeployUrl, repositoryUrl: importRepositoryUrl}
}

func (this *httpImports) Instance(token string, id string) (result models.Import, err error) {
	err = getJSON(token, this.deployUrl+"/instances/"+url.PathEscape(id), &result)
	return result, err
}

func (this *httpImports) Type(token string, id string) (result models.ImportType, err error) {
	err = getJSON(token, this.repositoryUrl+"/import-types/"+url.PathEscape(id), &result)
	return result, err
}

// statusError is an answer other than 2xx
type statusError struct {
	code int
	err  error
}

func (this *statusError) Error() string {
	return this.err.Error()
}

var importsClient = &http.Client{Timeout: 10 * time.Second}

// getJSON reads a resource with the token; an answer other than 2xx is an error with status and
// body
func getJSON(token string, resourceUrl string, result any) error {
	req, err := http.NewRequest(http.MethodGet, resourceUrl, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", token)
	resp, err := importsClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return &statusError{code: resp.StatusCode, err: fmt.Errorf("GET %v: %v %v", req.URL.Path, resp.StatusCode, strings.TrimSpace(string(body)))}
	}
	return json.NewDecoder(resp.Body).Decode(result)
}

// Pipelines reads analytics pipelines with the token of the deployment owner (SNRGY-5113).
type Pipelines interface {
	Pipeline(token string, id string) (Pipeline, error)
}

// Pipeline is the part of an analytics-pipeline pipeline the describer needs.
type Pipeline struct {
	Id        string             `json:"id"`
	UserId    string             `json:"userId"`
	Operators []PipelineOperator `json:"operators"`
}

type PipelineOperator struct {
	// the id the operator writes as operator_id into its messages
	Id   string `json:"id"`
	Name string `json:"name"`
	// cloud or local; the output of a local operator reaches Kafka only if it is forwarded
	DeploymentType string `json:"deploymentType"`
	// analytics-<operator name> in the cloud, shared by every pipeline with this operator
	OutputTopic string `json:"outputTopic"`
}

type httpPipelines struct {
	url string
}

// NewPipelines reads from analytics-pipeline, e.g. http://pipeline.analytics:8000.
func NewPipelines(analyticsPipelineUrl string) Pipelines {
	return &httpPipelines{url: analyticsPipelineUrl}
}

func (this *httpPipelines) Pipeline(token string, id string) (result Pipeline, err error) {
	err = getJSON(token, this.url+"/pipeline/"+url.PathEscape(id), &result)
	return result, err
}

// Describer derives the event descriptions of a deployment version (DEP-07), following
// event-deployment v0.2.0: a device group becomes device × matching service, a device without
// service all its matching services, an import one description with an artificial service.
type Describer struct {
	// read through devicerepository.WithCache with cacheTimeout
	iot          devicerepository.Interface
	cacheTimeout time.Duration
	// tokens of the owners, e.g. by token exchange
	tokens  auth.UserTokenSource
	imports Imports
	// nil: analytics events are rejected at deploy
	pipelines Pipelines
	// descriptions for a gateway read the topics of its broker (FOG-06)
	local bool
}

// Local makes the describer describe events for a gateway: the topic is
// event/{local device id}/{local service id}, and imports are not available.
func (this *Describer) Local() *Describer {
	result := *this
	result.local = true
	return &result
}

// WithPipelines lets deployments wait for the output of analytics operators (SNRGY-5113).
func (this *Describer) WithPipelines(pipelines Pipelines) *Describer {
	result := *this
	result.pipelines = pipelines
	return &result
}

// NewDescriber reads devices, device types, device groups and aspect nodes through iot, cached
// per user for cacheTimeout, with the tokens of the owners.
func NewDescriber(iot devicerepository.Interface, cacheTimeout time.Duration, tokens auth.UserTokenSource, imports Imports) *Describer {
	return &Describer{iot: iot, cacheTimeout: cacheTimeout, tokens: tokens, imports: imports}
}

// Describe returns the descriptions and the device groups they depend on.
func (this *Describer) Describe(ctx context.Context, deployment model.Deployment, version int, process *bpmn.Process, selections map[string]model.Selection) (result []model.EventDescription, groups []string, err error) {
	var token string
	for _, element := range process.Ordered() {
		selection, ok := selections[element.Id]
		if !element.IsDeviceEvent() || !ok {
			continue
		}
		if token == "" {
			if this.tokens == nil {
				return nil, nil, fmt.Errorf("no token source for the owner %v", deployment.Owner)
			}
			if token, err = this.tokens(deployment.Owner); err != nil {
				return nil, nil, fmt.Errorf("unable to get a token of the owner: %w", err)
			}
		}
		base, err := baseDescription(deployment, version, element, selection)
		if err != nil {
			return nil, nil, err
		}
		var descriptions []model.EventDescription
		switch {
		case selection.DeviceGroupId != "":
			groups = append(groups, selection.DeviceGroupId)
			group, err, _ := devicerepository.WithCache(this.iot, this.cacheTimeout).ReadDeviceGroup(ctx, selection.DeviceGroupId, token, false)
			if err != nil {
				return nil, nil, fmt.Errorf("device group %v: %w", selection.DeviceGroupId, err)
			}
			for _, deviceId := range group.DeviceIds {
				d, err := this.deviceDescriptions(ctx, token, base, deviceId, "")
				if err != nil {
					return nil, nil, err
				}
				descriptions = append(descriptions, d...)
			}
		case selection.DeviceId != "":
			descriptions, err = this.deviceDescriptions(ctx, token, base, selection.DeviceId, selection.ServiceId)
		case selection.ImportId != "":
			descriptions, err = this.importDescription(ctx, token, base, selection)
		case selection.PipelineId != "":
			descriptions, err = this.analyticsDescription(token, base, selection)
		}
		if err != nil {
			return nil, nil, err
		}
		result = append(result, descriptions...)
	}
	return result, groups, nil
}

func baseDescription(deployment model.Deployment, version int, element *bpmn.Element, selection model.Selection) (model.EventDescription, error) {
	criteria, _ := prepare.CriteriaOf(element)
	d := model.EventDescription{
		DeploymentId: deployment.Id, Version: version, ElementId: element.Id, Namespace: deployment.Namespace, Owner: deployment.Owner,
		MessageName: engine.EventMessageName(deployment.Id, element.Id), DeviceGroupId: selection.DeviceGroupId,
		Script: element.Attributes["script"], ValueVariable: element.Attributes["value_variable_name"],
		CharacteristicId: criteria.CharacteristicId, FunctionId: criteria.FunctionId, AspectIds: criteria.AspectIds,
	}
	if strings.TrimSpace(d.Script) == "" {
		// a device event without condition triggers on every value
		d.Script = "true"
	}
	if d.ValueVariable == "" {
		d.ValueVariable = "value"
	}
	if raw := element.Attributes["variables"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &d.Variables); err != nil {
			return d, model.ValidationFailed([]model.Issue{{ElementId: element.Id, Message: "senergy:variables must be a JSON object of strings"}})
		}
	}
	if raw := element.Attributes["qos"]; raw != "" {
		qos, err := strconv.Atoi(raw)
		if err != nil || (qos != 0 && qos != 1) {
			return d, model.ValidationFailed([]model.Issue{{ElementId: element.Id, Message: "senergy:qos must be 0 or 1"}})
		}
		d.Qos = qos
	}
	if selection.Path != nil {
		d.Path = selection.Path.Path
		// the characteristic of the path is the one the source sends; the condition reads the
		// value in the characteristic of the event (EVT-04), as event-deployment did
		if d.CharacteristicId == "" {
			d.CharacteristicId = selection.Path.CharacteristicId
		}
	}
	return d, nil
}

func (this *Describer) deviceDescriptions(ctx context.Context, token string, base model.EventDescription, deviceId string, serviceId string) ([]model.EventDescription, error) {
	cached := devicerepository.WithCache(this.iot, this.cacheTimeout)
	device, err, _ := cached.ReadDevice(ctx, deviceId, token, devicerepomodel.READ)
	if err != nil {
		return nil, fmt.Errorf("device %v: %w", deviceId, err)
	}
	deviceType, err, _ := cached.ReadDeviceType(ctx, device.DeviceTypeId, token)
	if err != nil {
		return nil, fmt.Errorf("device type %v: %w", device.DeviceTypeId, err)
	}
	var services []models.Service
	if serviceId != "" {
		for _, s := range deviceType.Services {
			if s.Id == serviceId {
				services = append(services, s)
			}
		}
		if len(services) == 0 {
			return nil, model.ValidationFailed([]model.Issue{{ElementId: base.ElementId, Message: fmt.Sprintf("device %v has no service %v", deviceId, serviceId)}})
		}
	} else {
		aspects, err, _ := commons.AspectNodesByIds(ctx, cached, base.AspectIds)
		if err != nil {
			return nil, err
		}
		for _, s := range deviceType.Services {
			isEvent := s.Interaction == models.EVENT || s.Interaction == models.EVENT_AND_REQUEST
			if isEvent && commons.ContentsMatch(s.Outputs, base.FunctionId, aspects) {
				services = append(services, s)
			}
		}
	}
	result := make([]model.EventDescription, 0, len(services))
	for _, s := range services {
		d := base
		d.Id = uuid.NewString()
		d.DeviceId = deviceId
		d.ServiceId = s.Id
		d.Topic = commons.ServiceIdToTopic(s.Id)
		if this.local {
			if device.LocalId == "" || s.LocalId == "" {
				return nil, model.ValidationFailed([]model.Issue{{ElementId: base.ElementId, Message: fmt.Sprintf("device %v or its service %v has no local id", deviceId, s.Id)}})
			}
			d.Topic = LocalEventTopic(device.LocalId, s.LocalId)
		}
		raw, err := json.Marshal(s)
		if err != nil {
			return nil, err
		}
		d.Service = raw
		d.AspectIds = slices.Clone(base.AspectIds)
		result = append(result, d)
	}
	return result, nil
}

func (this *Describer) importDescription(ctx context.Context, token string, base model.EventDescription, selection model.Selection) ([]model.EventDescription, error) {
	if this.local {
		return nil, model.ValidationFailed([]model.Issue{{ElementId: base.ElementId, Message: "import events are not available on gateways"}})
	}
	if this.imports == nil {
		return nil, model.ValidationFailed([]model.Issue{{ElementId: base.ElementId, Message: "import events are not configured"}})
	}
	instance, err := this.imports.Instance(token, selection.ImportId)
	if err != nil {
		return nil, fmt.Errorf("import %v: %w", selection.ImportId, err)
	}
	importType, err := this.imports.Type(token, instance.ImportTypeId)
	if err != nil {
		return nil, fmt.Errorf("import type %v: %w", instance.ImportTypeId, err)
	}
	service := models.Service{Id: importType.Id, Name: importType.Name, Interaction: models.EVENT}
	for _, v := range importType.Output.SubContentVariables {
		service.Outputs = append(service.Outputs, models.Content{ContentVariable: importVariable(v)})
	}
	raw, err := json.Marshal(service)
	if err != nil {
		return nil, err
	}
	d := base
	d.Id = uuid.NewString()
	d.ImportId = selection.ImportId
	d.Topic = instance.KafkaTopic
	d.Service = raw
	return []model.EventDescription{d}, nil
}

func importVariable(v models.ImportContentVariable) models.ContentVariable {
	// import-repository keeps the aspects in AspectIds and fills the deprecated AspectId with
	// the first of them; the unmarshalling needs all
	result := models.ContentVariable{Name: v.Name, Type: models.Type(v.Type), CharacteristicId: v.CharacteristicId, FunctionId: v.FunctionId,
		AspectId: v.AspectId, AspectIds: slices.Clone(v.AspectIds)}
	for _, sub := range v.SubContentVariables {
		result.SubContentVariables = append(result.SubContentVariables, importVariable(sub))
	}
	return result
}

// analyticsDescription describes the output of an operator of a pipeline of the owner: the topic
// of the operator, filtered by pipeline and operator id in the worker, since the topic is shared
// by every pipeline with an operator of the same name (SNRGY-5113)
func (this *Describer) analyticsDescription(token string, base model.EventDescription, selection model.Selection) ([]model.EventDescription, error) {
	issue := func(format string, args ...any) error {
		return model.ValidationFailed([]model.Issue{{ElementId: base.ElementId, Message: fmt.Sprintf(format, args...)}})
	}
	if this.local {
		return nil, issue("analytics events are not available on gateways")
	}
	if this.pipelines == nil {
		return nil, issue("analytics events are not configured")
	}
	pipeline, err := this.pipelines.Pipeline(token, selection.PipelineId)
	var status *statusError
	if errors.As(err, &status) && (status.code == http.StatusNotFound || status.code == http.StatusForbidden) {
		return nil, issue("pipeline %v does not exist or the owner of the deployment may not read it", selection.PipelineId)
	}
	if err != nil {
		return nil, fmt.Errorf("pipeline %v: %w", selection.PipelineId, err)
	}
	if pipeline.UserId != base.Owner {
		return nil, issue("pipeline %v does not belong to the owner of the deployment", selection.PipelineId)
	}
	index := slices.IndexFunc(pipeline.Operators, func(o PipelineOperator) bool { return o.Id == selection.OperatorId })
	if index < 0 {
		return nil, issue("pipeline %v has no operator %v", selection.PipelineId, selection.OperatorId)
	}
	operator := pipeline.Operators[index]
	if operator.DeploymentType == "local" {
		return nil, issue("operator %v of pipeline %v runs on a gateway; only the output of cloud operators is available", operator.Id, selection.PipelineId)
	}
	if operator.OutputTopic == "" {
		return nil, issue("operator %v of pipeline %v has no output topic", operator.Id, selection.PipelineId)
	}
	d := base
	d.Id = uuid.NewString()
	d.PipelineId = selection.PipelineId
	d.OperatorId = selection.OperatorId
	d.Topic = operator.OutputTopic
	// the value is the output of the operator as it is; device criteria of the element do not
	// apply to it
	d.CharacteristicId, d.FunctionId, d.AspectIds, d.Path = "", "", nil, ""
	return []model.EventDescription{d}, nil
}
