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

// Package prepare tells what a user chooses before a deploy or a start: the devices, device
// groups and imports a deployment can select for its device tasks and device events (DEP-01),
// through device-selection, and the start parameters with their choices (Describer, SMS-08).
package prepare

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/devices"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	dsclient "github.com/SENERGY-Platform/device-selection/v2/pkg/client"
	dsmodel "github.com/SENERGY-Platform/device-selection/v2/pkg/model"
	"github.com/SENERGY-Platform/device-selection/v2/pkg/model/devicemodel"
	"github.com/SENERGY-Platform/models/go/models"
)

// Config mirrors the options of process-deployment; groups and imports are off there by default.
type Config struct {
	GroupsForTasks   bool
	GroupsForEvents  bool
	ImportsForEvents bool
	ModifiedDevices  bool
	// device-repository, for the devices of a hub when preparing for a gateway
	DeviceRepositoryUrl string
}

type Preparer struct {
	client dsclient.Client
	config Config
}

func New(client dsclient.Client, config Config) *Preparer {
	return &Preparer{client: client, config: config}
}

// CriteriaOf reads the criteria of a device task from its payload input and of a device event
// from its senergy attributes; ok is false for other elements.
func CriteriaOf(e *bpmn.Element) (result model.Criteria, ok bool) {
	switch {
	case e.IsDeviceCommand():
		for _, input := range e.Inputs {
			if input.Name != "payload" || input.Value.Kind != bpmn.ValueText {
				continue
			}
			var payload struct {
				Function         struct{ Id string }   `json:"function"`
				CharacteristicId string                `json:"characteristic_id"`
				DeviceClass      *struct{ Id string }  `json:"device_class"`
				Aspect           *struct{ Id string }  `json:"aspect"`
				Aspects          []struct{ Id string } `json:"aspects"`
			}
			if json.Unmarshal([]byte(input.Value.Text), &payload) != nil {
				return result, false
			}
			result.FunctionId = payload.Function.Id
			result.CharacteristicId = payload.CharacteristicId
			if payload.DeviceClass != nil {
				result.DeviceClassId = payload.DeviceClass.Id
			}
			for _, a := range payload.Aspects {
				result.AspectIds = append(result.AspectIds, a.Id)
			}
			if payload.Aspect != nil && payload.Aspect.Id != "" && !slices.Contains(result.AspectIds, payload.Aspect.Id) {
				result.AspectIds = append(result.AspectIds, payload.Aspect.Id)
			}
			return result, result.FunctionId != ""
		}
		return result, false
	case e.IsDeviceEvent():
		result.FunctionId = e.Attributes["function"]
		result.CharacteristicId = e.Attributes["characteristic"]
		result.DeviceClassId = e.Attributes["device_class"]
		for _, a := range strings.Split(e.Attributes["aspects"], ",") {
			if a = strings.TrimSpace(a); a != "" {
				result.AspectIds = append(result.AspectIds, a)
			}
		}
		if a := e.Attributes["aspect"]; a != "" && !slices.Contains(result.AspectIds, a) {
			result.AspectIds = append(result.AspectIds, a)
		}
		return result, result.FunctionId != ""
	}
	return result, false
}

// payloadNamesDevice reports a device task whose payload names its device, as generated
// processes do; a payload computed by a script is not inspected
func payloadNamesDevice(e *bpmn.Element) bool {
	for _, input := range e.Inputs {
		if input.Name != "payload" {
			continue
		}
		if input.Value.Kind != bpmn.ValueText {
			return true
		}
		var payload struct {
			DeviceId      string `json:"device_id"`
			DeviceGroupId string `json:"device_group_id"`
		}
		return json.Unmarshal([]byte(input.Value.Text), &payload) == nil && (payload.DeviceId != "" || payload.DeviceGroupId != "")
	}
	return false
}

func filter(c model.Criteria, interaction string) devicemodel.FilterCriteria {
	f := devicemodel.FilterCriteria{Interaction: interaction, FunctionId: c.FunctionId, DeviceClassId: c.DeviceClassId, AspectIds: c.AspectIds}
	if len(c.AspectIds) == 1 {
		f.AspectId = c.AspectIds[0]
	}
	return f
}

// Prepare lists the elements that need a device selection with their options. Device tasks
// of one lane share the criteria of all of them, so that one device serves the whole lane.
func (this *Preparer) Prepare(token string, process *bpmn.Process) ([]model.PreparedElement, error) {
	return this.prepare(token, process, nil)
}

// PrepareForHub offers only the devices of the hub, for a deployment to its gateway; the local
// ids of the hub go to device-selection as process-fog-deployment sent them.
func (this *Preparer) PrepareForHub(token string, process *bpmn.Process, hubId string) ([]model.PreparedElement, error) {
	if this.client == nil {
		return this.prepare(token, process, nil)
	}
	if this.config.DeviceRepositoryUrl == "" {
		return nil, model.Unavailable("device-repository is needed to offer the devices of a gateway")
	}
	var hub models.Hub
	if err := devices.Token(token).GetJSON(this.config.DeviceRepositoryUrl+"/hubs/"+url.PathEscape(hubId), &hub); err != nil {
		return nil, model.Unavailable("device-repository: hub %v: %v", hubId, err)
	}
	if len(hub.DeviceLocalIds) == 0 {
		// device-selection would read an empty list as no restriction; a hub without devices
		// offers none
		return (&Preparer{config: this.config}).prepare(token, process, nil)
	}
	return this.prepare(token, process, hub.DeviceLocalIds)
}

// HubNames reads the names of the hubs with one request to device-repository; hubs the user
// cannot read are missing.
func (this *Preparer) HubNames(token string, ids []string) (map[string]string, error) {
	result := map[string]string{}
	if len(ids) == 0 || this.config.DeviceRepositoryUrl == "" {
		return result, nil
	}
	var hubs []models.Hub
	if err := devices.Token(token).GetJSON(this.config.DeviceRepositoryUrl+"/hubs?ids="+url.QueryEscape(strings.Join(ids, ",")), &hubs); err != nil {
		return nil, err
	}
	for _, hub := range hubs {
		result[hub.Id] = hub.Name
	}
	return result, nil
}

func (this *Preparer) prepare(token string, process *bpmn.Process, localDevices []string) ([]model.PreparedElement, error) {
	result := []model.PreparedElement{}
	laneCriteria := map[string][]devicemodel.FilterCriteria{}
	for _, e := range process.Ordered() {
		criteria, ok := CriteriaOf(e)
		if !ok {
			continue
		}
		kind := model.PreparedTask
		if e.IsDeviceEvent() {
			kind = model.PreparedEvent
		}
		result = append(result, model.PreparedElement{ElementId: e.Id, Name: e.Name, Kind: kind, Lane: e.Lane, Criteria: criteria})
		if kind == model.PreparedTask && e.Lane != "" {
			laneCriteria[e.Lane] = append(laneCriteria[e.Lane], filter(criteria, ""))
		}
	}
	if len(result) == 0 || this.client == nil {
		return result, nil
	}
	bulk := dsmodel.BulkRequestV2{}
	for _, element := range result {
		request := dsmodel.BulkRequestElementV2{Id: element.ElementId, IncludeDevices: true, IncludeIdModifiedDevices: this.config.ModifiedDevices, LocalDevices: localDevices}
		if element.Kind == model.PreparedTask {
			request.IncludeGroups = this.config.GroupsForTasks
			request.Criteria = dsmodel.FilterCriteriaAndSet{filter(element.Criteria, "")}
			if element.Lane != "" {
				request.Criteria = laneCriteria[element.Lane]
			}
		} else {
			request.IncludeGroups = this.config.GroupsForEvents
			request.IncludeImports = this.config.ImportsForEvents
			request.Criteria = dsmodel.FilterCriteriaAndSet{filter(element.Criteria, string(devicemodel.EVENT))}
		}
		bulk = append(bulk, request)
	}
	answer, _, err := this.client.GetBulkSelectablesV2(token, bulk, &dsclient.GetBulkSelectablesOptions{CompleteServices: true})
	if err != nil {
		return nil, model.Unavailable("device-selection: %v", err)
	}
	byId := map[string][]dsmodel.Selectable{}
	for _, a := range answer {
		byId[a.Id] = a.Selectables
	}
	for i, element := range result {
		options := []model.SelectionOption{}
		for _, s := range byId[element.ElementId] {
			option, err := toOption(s)
			if err != nil {
				return nil, err
			}
			if element.Kind == model.PreparedTask && element.Lane != "" {
				option.Services = ownServices(option.Services, element.Criteria, s.ServicePathOptions)
			}
			options = append(options, option)
		}
		sort.SliceStable(options, func(a, b int) bool { return optionName(options[a]) < optionName(options[b]) })
		result[i].Options = options
	}
	return result, nil
}

// ownServices keeps the services that serve the criteria of the element itself. device-selection
// answers a lane task with the services of all criteria of the lane, so a task that switches a
// lamp off would also be offered the service that switches it on. An element outside a lane is
// asked with its own criteria alone and keeps the answer as it is.
func ownServices(services []model.NamedRef, criteria model.Criteria, paths map[string][]dsmodel.PathOption) []model.NamedRef {
	var result []model.NamedRef
	for _, service := range services {
		if slices.ContainsFunc(paths[service.Id], func(p dsmodel.PathOption) bool { return pathMatches(p, criteria) }) {
			result = append(result, service)
		}
	}
	return result
}

// pathMatches follows serviceMatchesCriteria of process-deployment: the path option carries the
// function, and every asked aspect is one of its aspect nodes or an ancestor of one, because an
// aspect covers its subtree. Several aspects are an AND on one path option, as in the
// device-repository.
func pathMatches(p dsmodel.PathOption, criteria model.Criteria) bool {
	if criteria.FunctionId != "" && p.FunctionId != criteria.FunctionId {
		return false
	}
	nodes := p.AspectNodes
	if len(nodes) == 0 && p.AspectNode.Id != "" {
		// AspectNode is the deprecated single element alias of AspectNodes
		nodes = []models.AspectNode{p.AspectNode}
	}
	for _, aspectId := range criteria.AspectIds {
		if !slices.ContainsFunc(nodes, func(n models.AspectNode) bool { return n.Id == aspectId || slices.Contains(n.AncestorIds, aspectId) }) {
			return false
		}
	}
	return true
}

func toOption(s dsmodel.Selectable) (result model.SelectionOption, err error) {
	if s.Device != nil {
		name := s.Device.DisplayName
		if name == "" {
			name = s.Device.Name
		}
		result.Device = &model.NamedRef{Id: s.Device.Id, Name: name}
	}
	for _, service := range s.Services {
		result.Services = append(result.Services, model.NamedRef{Id: service.Id, Name: service.Name})
	}
	if s.DeviceGroup != nil {
		result.DeviceGroup = &model.NamedRef{Id: s.DeviceGroup.Id, Name: s.DeviceGroup.Name}
	}
	if s.Import != nil {
		result.Import = &model.NamedRef{Id: s.Import.Id, Name: s.Import.Name}
	}
	if s.ImportType != nil {
		result.ImportType = &model.NamedRef{Id: s.ImportType.Id, Name: s.ImportType.Name}
	}
	if len(s.ServicePathOptions) > 0 {
		result.PathOptions = map[string]json.RawMessage{}
		for serviceId, options := range s.ServicePathOptions {
			raw, err := json.Marshal(options)
			if err != nil {
				return result, fmt.Errorf("path options of %v: %w", serviceId, err)
			}
			result.PathOptions[serviceId] = raw
		}
	}
	return result, nil
}

func optionName(o model.SelectionOption) string {
	switch {
	case o.Device != nil:
		return o.Device.Name
	case o.DeviceGroup != nil:
		return o.DeviceGroup.Name
	case o.Import != nil:
		return o.Import.Name
	}
	return ""
}

// ValidateSelections checks that selections refer to device tasks and device events and select
// exactly one source, and that device events and device tasks without a device in their
// payload have a selection (DEP-05).
func ValidateSelections(process *bpmn.Process, selections map[string]model.Selection) []model.Issue {
	var issues []model.Issue
	for _, e := range process.Ordered() {
		if _, ok := selections[e.Id]; ok {
			continue
		}
		if e.IsDeviceEvent() || (e.IsDeviceCommand() && !payloadNamesDevice(e)) {
			issues = append(issues, model.Issue{ElementId: e.Id, Message: "needs a device selection"})
		}
	}
	ids := make([]string, 0, len(selections))
	for id := range selections {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s := selections[id]
		e := process.Element(id)
		if e == nil || (!e.IsDeviceCommand() && !e.IsDeviceEvent()) {
			issues = append(issues, model.Issue{ElementId: id, Message: "a selection needs a device task or device event"})
			continue
		}
		sources := 0
		for _, v := range []string{s.DeviceId, s.DeviceGroupId, s.ImportId} {
			if v != "" {
				sources++
			}
		}
		if sources != 1 {
			issues = append(issues, model.Issue{ElementId: id, Message: "select exactly one of device, device group or import"})
		}
		if s.ImportId != "" && e.IsDeviceCommand() {
			issues = append(issues, model.Issue{ElementId: id, Message: "an import cannot receive commands"})
		}
		if s.ServiceId != "" && s.DeviceId == "" {
			issues = append(issues, model.Issue{ElementId: id, Message: "a service needs a device"})
		}
	}
	return issues
}
