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
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/devices"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/prepare"
	"github.com/SENERGY-Platform/models/go/models"
	"github.com/google/uuid"
)

// Imports reads import instances and types with the token of the deployment owner.
type Imports interface {
	Instance(token devices.Token, id string) (models.Import, error)
	Type(token devices.Token, id string) (models.ImportType, error)
}

type httpImports struct {
	deployUrl     string
	repositoryUrl string
}

// NewImports reads from import-deploy and import-repository.
func NewImports(importDeployUrl string, importRepositoryUrl string) Imports {
	return &httpImports{deployUrl: importDeployUrl, repositoryUrl: importRepositoryUrl}
}

func (this *httpImports) Instance(token devices.Token, id string) (result models.Import, err error) {
	err = token.GetJSON(this.deployUrl+"/instances/"+url.PathEscape(id), &result)
	return result, err
}

func (this *httpImports) Type(token devices.Token, id string) (result models.ImportType, err error) {
	err = token.GetJSON(this.repositoryUrl+"/import-types/"+url.PathEscape(id), &result)
	return result, err
}

// Describer derives the event descriptions of a deployment version (DEP-07), following
// event-deployment v0.2.0: a device group becomes device × matching service, a device without
// service all its matching services, an import one description with an artificial service.
type Describer struct {
	repo    devices.Repository
	aspects *devices.AspectNodes
	imports Imports
}

func NewDescriber(repo devices.Repository, aspects *devices.AspectNodes, imports Imports) *Describer {
	return &Describer{repo: repo, aspects: aspects, imports: imports}
}

// Describe returns the descriptions and the device groups they depend on.
func (this *Describer) Describe(deployment model.Deployment, version int, process *bpmn.Process, selections map[string]model.Selection) (result []model.EventDescription, groups []string, err error) {
	var token devices.Token
	for _, element := range process.Ordered() {
		selection, ok := selections[element.Id]
		if !element.IsDeviceEvent() || !ok {
			continue
		}
		if token == "" {
			if token, err = this.repo.GetToken(deployment.Owner); err != nil {
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
			group, err := this.repo.GetDeviceGroup(token, selection.DeviceGroupId)
			if err != nil {
				return nil, nil, fmt.Errorf("device group %v: %w", selection.DeviceGroupId, err)
			}
			for _, deviceId := range group.DeviceIds {
				d, err := this.deviceDescriptions(token, base, deviceId, "")
				if err != nil {
					return nil, nil, err
				}
				descriptions = append(descriptions, d...)
			}
		case selection.DeviceId != "":
			descriptions, err = this.deviceDescriptions(token, base, selection.DeviceId, selection.ServiceId)
		case selection.ImportId != "":
			descriptions, err = this.importDescription(token, base, selection)
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
		if selection.Path.CharacteristicId != "" {
			d.CharacteristicId = selection.Path.CharacteristicId
		}
	}
	return d, nil
}

func (this *Describer) deviceDescriptions(token devices.Token, base model.EventDescription, deviceId string, serviceId string) ([]model.EventDescription, error) {
	device, err := this.repo.GetDevice(token, deviceId)
	if err != nil {
		return nil, fmt.Errorf("device %v: %w", deviceId, err)
	}
	deviceType, err := this.repo.GetDeviceType(token, device.DeviceTypeId)
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
		aspects, err := this.aspects.ResolveIds(token, base.AspectIds)
		if err != nil {
			return nil, err
		}
		for _, s := range deviceType.Services {
			isEvent := s.Interaction == models.EVENT || s.Interaction == models.EVENT_AND_REQUEST
			if isEvent && devices.ContentsMatch(s.Outputs, base.FunctionId, aspects) {
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
		d.Topic = devices.ServiceIdToTopic(s.Id)
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

func (this *Describer) importDescription(token devices.Token, base model.EventDescription, selection model.Selection) ([]model.EventDescription, error) {
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
	result := models.ContentVariable{Name: v.Name, Type: models.Type(v.Type), CharacteristicId: v.CharacteristicId, FunctionId: v.FunctionId, AspectId: v.AspectId}
	for _, sub := range v.SubContentVariables {
		result.SubContentVariables = append(result.SubContentVariables, importVariable(sub))
	}
	return result
}
