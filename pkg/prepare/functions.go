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

package prepare

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	devicerepository "github.com/SENERGY-Platform/device-repository/v3/lib/client"
	devicerepomodel "github.com/SENERGY-Platform/device-repository/v3/lib/model"
	"github.com/SENERGY-Platform/models/go/models"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/commons"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// additionalFunction is an entry of additional_functions of the payload of a device command: a
// further controlling function the command sets in the same service call (SNRGY-5157). Its
// input is set by the task input inputs:<name>.
type additionalFunction struct {
	Name             string                `json:"name"`
	Function         struct{ Id string }   `json:"function"`
	CharacteristicId string                `json:"characteristic_id"`
	Aspects          []struct{ Id string } `json:"aspects"`
}

// commandPayload is what the checks of this file read of the payload of a device command
type commandPayload struct {
	Function            struct{ Id string }   `json:"function"`
	Aspect              *struct{ Id string }  `json:"aspect"`
	Aspects             []struct{ Id string } `json:"aspects"`
	AdditionalFunctions []additionalFunction  `json:"additional_functions"`
	// the configurables before version 3, which go only through the marshalling of version 2
	Configurables []json.RawMessage `json:"configurables"`
}

// readPayload parses the text payload of a device command; ok is false without one, with a
// payload computed by a script and with one that is no JSON object.
func readPayload(e *bpmn.Element) (payload commandPayload, ok bool) {
	for _, input := range e.Inputs {
		if input.Name != "payload" {
			continue
		}
		if input.Value.Kind != bpmn.ValueText {
			return payload, false
		}
		return payload, json.Unmarshal([]byte(input.Value.Text), &payload) == nil
	}
	return payload, false
}

func (this additionalFunction) criteria() model.Criteria {
	result := model.Criteria{FunctionId: this.Function.Id, CharacteristicId: this.CharacteristicId}
	for _, a := range this.Aspects {
		result.AspectIds = append(result.AspectIds, a.Id)
	}
	return result
}

// additionalCriteria is the criteria of the additional functions of a device command, in the
// order of the payload; nil without any.
func additionalCriteria(e *bpmn.Element) (result []model.Criteria) {
	if !e.IsDeviceCommand() {
		return nil
	}
	payload, ok := readPayload(e)
	if !ok {
		return nil
	}
	for _, f := range payload.AdditionalFunctions {
		result = append(result, f.criteria())
	}
	return result
}

const inputPrefix = "inputs:"

// ValidatePayloads checks the additional functions of device commands without calls to other
// services (SNRGY-5157): every entry has a unique name without "." and ":", which the inputs
// inputs:<name> and inputs:<name>.<path> use to address it, and a function; with additional
// functions only controlling functions are allowed; every such input names an entry.
func ValidatePayloads(process *bpmn.Process) []model.Issue {
	var issues []model.Issue
	for _, e := range process.Ordered() {
		if !e.IsDeviceCommand() {
			continue
		}
		// a command without additional functions is checked as before, inputs:<name> included,
		// which the runtime ignores there (SNRGY-5157)
		payload, ok := readPayload(e)
		if !ok || len(payload.AdditionalFunctions) == 0 {
			continue
		}
		issue := func(format string, args ...any) {
			issues = append(issues, model.Issue{ElementId: e.Id, Message: fmt.Sprintf(format, args...)})
		}
		names := map[string]bool{}
		reported := map[string]bool{}
		for i, f := range payload.AdditionalFunctions {
			switch {
			case f.Name == "":
				issue("additional function %d needs a name", i+1)
			case strings.ContainsAny(f.Name, ".:"):
				issue("additional function %q: a name may not contain \".\" or \":\"", f.Name)
			case names[f.Name]:
				if !reported[f.Name] {
					issue("additional function %q is named twice", f.Name)
					reported[f.Name] = true
				}
			}
			names[f.Name] = true
			if f.Function.Id == "" {
				issue("additional function %q needs a function", f.Name)
			}
		}
		if len(payload.AdditionalFunctions) > 0 {
			ids := []string{payload.Function.Id}
			for _, f := range payload.AdditionalFunctions {
				ids = append(ids, f.Function.Id)
			}
			for _, id := range ids {
				if id != "" && !strings.HasPrefix(id, models.CONTROLLING_FUNCTION_PREFIX) {
					issue("a device command with additional functions takes only controlling functions, %v is not one", id)
				}
			}
		}
		if len(payload.Configurables) > 0 {
			issue("a device command with additional functions takes no configurables before version 3")
		}
		for _, input := range e.Inputs {
			rest, ok := strings.CutPrefix(input.Name, inputPrefix)
			if !ok {
				continue
			}
			name, _, _ := strings.Cut(rest, ".")
			if !slices.ContainsFunc(payload.AdditionalFunctions, func(f additionalFunction) bool { return f.Name == name }) {
				issue("input %q names no additional function", input.Name)
			}
		}
	}
	return issues
}

// commandCriteria is a function of a device command with the label that names it in messages
type commandCriteria struct {
	label    string
	criteria commons.FunctionCriteria
}

// CheckFunctionFit checks for the device tasks with additional functions that have a selection
// that the selected device, or the selected group, has a service that serves all functions of the
// command, with the token of the user (SNRGY-5157). The issues name the element; an error means
// device-repository could not be asked. Elements without additional functions cost no request,
// and without a device-repository nothing is checked.
func (this *Preparer) CheckFunctionFit(ctx context.Context, token string, process *bpmn.Process, selections map[string]model.Selection) (issues []model.Issue, err error) {
	repo := this.config.DeviceRepository
	if repo == nil {
		return nil, nil
	}
	c := &fitCache{repo: repo, token: token, devices: map[string]*models.Device{}, types: map[string]models.DeviceType{}}
	for _, e := range process.Ordered() {
		selection, ok := selections[e.Id]
		if !ok || !e.IsDeviceCommand() || (selection.DeviceId == "" && selection.DeviceGroupId == "") {
			continue
		}
		functions, unknown, err := this.commandFunctions(ctx, e)
		if err != nil {
			return nil, err
		}
		if unknown != "" {
			issues = append(issues, model.Issue{ElementId: e.Id, Message: unknown})
			continue
		}
		if len(functions) == 0 {
			continue
		}
		labels := make([]string, len(functions))
		criteria := make([]commons.FunctionCriteria, len(functions))
		for i, f := range functions {
			labels[i], criteria[i] = f.label, f.criteria
		}
		message, err := c.fits(ctx, selection, criteria, labels)
		if err != nil {
			return nil, err
		}
		if message != "" {
			issues = append(issues, model.Issue{ElementId: e.Id, Message: message})
		}
	}
	return issues, nil
}

// commandFunctions is the primary function of a device command followed by its additional
// functions, with the aspect nodes read from device-repository, because the match compares
// against the descendants of the nodes; empty for a command without additional functions. An
// aspect device-repository does not know is a fault of the model, reported as unknown.
func (this *Preparer) commandFunctions(ctx context.Context, e *bpmn.Element) (result []commandCriteria, unknown string, err error) {
	payload, ok := readPayload(e)
	if !ok || len(payload.AdditionalFunctions) == 0 {
		return nil, "", nil
	}
	primary, ok := CriteriaOf(e)
	if !ok {
		return nil, "", nil
	}
	type entry struct {
		label    string
		function string
		aspects  []string
	}
	entries := []entry{{label: primary.FunctionId, function: primary.FunctionId, aspects: primary.AspectIds}}
	for _, f := range payload.AdditionalFunctions {
		c := f.criteria()
		entries = append(entries, entry{label: fmt.Sprintf("%v (%v)", f.Name, c.FunctionId), function: c.FunctionId, aspects: c.AspectIds})
	}
	for _, en := range entries {
		nodes, err, code := commons.AspectNodesByIds(ctx, this.config.DeviceRepository, en.aspects)
		if err != nil && code == http.StatusNotFound {
			return nil, err.Error(), nil
		}
		if err != nil {
			return nil, "", model.Unavailable("device-repository: %v (%v)", err, code)
		}
		result = append(result, commandCriteria{label: en.label, criteria: commons.FunctionCriteria{FunctionId: en.function, Aspects: nodes}})
	}
	return result, "", nil
}

// fitCache reads devices and device types of device-repository once per check
type fitCache struct {
	repo    devicerepository.Interface
	token   string
	devices map[string]*models.Device
	types   map[string]models.DeviceType
}

// device reads a device; nil if the user has none with this id
func (this *fitCache) device(ctx context.Context, id string) (*models.Device, error) {
	if d, ok := this.devices[id]; ok {
		return d, nil
	}
	d, err, code := this.repo.ReadDevice(ctx, id, this.token, devicerepomodel.READ)
	if err != nil {
		if code == http.StatusNotFound || code == http.StatusForbidden {
			this.devices[id] = nil
			return nil, nil
		}
		return nil, model.Unavailable("device-repository: device %v: %v", id, err)
	}
	this.devices[id] = &d
	return &d, nil
}

func (this *fitCache) deviceType(ctx context.Context, id string) (models.DeviceType, error) {
	if dt, ok := this.types[id]; ok {
		return dt, nil
	}
	dt, err, _ := this.repo.ReadDeviceType(ctx, id, this.token)
	if err != nil {
		return dt, model.Unavailable("device-repository: device type %v: %v", id, err)
	}
	this.types[id] = dt
	return dt, nil
}

// fits answers the issue for a selection, empty if the command fits
func (this *fitCache) fits(ctx context.Context, selection model.Selection, criteria []commons.FunctionCriteria, labels []string) (string, error) {
	all := strings.Join(labels, ", ")
	if selection.DeviceGroupId != "" {
		group, err, code := this.repo.ReadDeviceGroup(ctx, selection.DeviceGroupId, this.token, false)
		if err != nil {
			if code == http.StatusNotFound {
				return fmt.Sprintf("device group %v not found", selection.DeviceGroupId), nil
			}
			return "", model.Unavailable("device-repository: device group %v: %v", selection.DeviceGroupId, err)
		}
		for _, id := range group.DeviceIds {
			d, err := this.device(ctx, id)
			if err != nil {
				return "", err
			}
			if d == nil {
				// a member the user cannot read serves nothing
				continue
			}
			dt, err := this.deviceType(ctx, d.DeviceTypeId)
			if err != nil {
				return "", err
			}
			if anyServiceFits(dt, criteria) {
				return "", nil
			}
		}
		return fmt.Sprintf("no device of group %v has a service that serves all of %v", selection.DeviceGroupId, all), nil
	}
	d, err := this.device(ctx, selection.DeviceId)
	if err != nil {
		return "", err
	}
	if d == nil {
		return fmt.Sprintf("device %v not found", selection.DeviceId), nil
	}
	dt, err := this.deviceType(ctx, d.DeviceTypeId)
	if err != nil {
		return "", err
	}
	if selection.ServiceId == "" {
		if anyServiceFits(dt, criteria) {
			return "", nil
		}
		return fmt.Sprintf("no service of device %v serves all of %v", selection.DeviceId, all), nil
	}
	missing := labels
	if i := slices.IndexFunc(dt.Services, func(s models.Service) bool { return s.Id == selection.ServiceId }); i >= 0 {
		missing = missingLabels(dt.Services[i], criteria, labels)
	}
	if len(missing) == 0 {
		return "", nil
	}
	return fmt.Sprintf("service %v of device %v does not serve %v", selection.ServiceId, selection.DeviceId, strings.Join(missing, ", ")), nil
}

func missingLabels(service models.Service, criteria []commons.FunctionCriteria, labels []string) (result []string) {
	for _, i := range commons.MissingFunctions(service.Inputs, criteria) {
		result = append(result, labels[i])
	}
	return result
}

func anyServiceFits(dt models.DeviceType, criteria []commons.FunctionCriteria) bool {
	return slices.ContainsFunc(dt.Services, func(s models.Service) bool {
		return len(commons.MissingFunctions(s.Inputs, criteria)) == 0
	})
}
