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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/bpmn"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/tracing"
	"github.com/SENERGY-Platform/models/go/models"
)

// Characteristics reads a characteristic for the description of a parameter.
type Characteristics interface {
	GetCharacteristic(id string) (result models.Characteristic, err error, errCode int)
}

// Describer describes the start parameters of a start event with their choices, as
// smart-service-repository did for releases (SMS-08): form field properties, static options,
// characteristics and IoT options from device-selection.
type Describer struct {
	deviceSelectionUrl string
	characteristics    Characteristics
	http               *http.Client
}

// NewDescriber creates a describer; without deviceSelectionUrl, IoT parameters have no options.
func NewDescriber(deviceSelectionUrl string, characteristics Characteristics) *Describer {
	return &Describer{deviceSelectionUrl: deviceSelectionUrl, characteristics: characteristics, http: &http.Client{Timeout: 30 * time.Second}}
}

// field is a form field with its properties read
type field struct {
	bpmn.FormField
	description   string
	optional      bool
	order         int
	options       map[string]any
	multiple      bool
	autoSelectAll bool
	iot           bool
	typeFilter    []string
	// passed to device-selection unchanged, so that aspect_id and aspect_ids both arrive
	criteria   []json.RawMessage
	entityOnly bool
	sameEntity string
}

func read(f bpmn.FormField) (result field, err error) {
	result.FormField = f
	p := f.Properties
	result.description = p["description"]
	result.optional = strings.ToLower(strings.TrimSpace(p["optional"])) == "true"
	if order, ok := p["order"]; ok {
		if result.order, err = strconv.Atoi(order); err != nil {
			return result, fmt.Errorf("invalid order property for form field %v: %w", f.Id, err)
		}
	}
	if options, ok := p["options"]; ok {
		if p["characteristic_id"] != "" {
			return result, fmt.Errorf("form field %v: options and characteristic_id are mutually exclusive", f.Id)
		}
		if err = json.Unmarshal([]byte(options), &result.options); err != nil {
			return result, fmt.Errorf("invalid options property for form field %v: %w", f.Id, err)
		}
	}
	if multiple, ok := p["multiple"]; ok {
		if result.multiple, err = strconv.ParseBool(multiple); err != nil {
			return result, fmt.Errorf("invalid multiple property for form field %v: %w", f.Id, err)
		}
	}
	if autoSelectAll, ok := p["auto_select_all"]; ok {
		if result.autoSelectAll, err = strconv.ParseBool(autoSelectAll); err != nil {
			return result, fmt.Errorf("invalid auto_select_all property for form field %v: %w", f.Id, err)
		}
		if result.autoSelectAll && !result.multiple {
			return result, fmt.Errorf("form field %v: auto_select_all needs multiple", f.Id)
		}
	}
	if iot, ok := p["iot"]; ok {
		if result.options != nil || p["characteristic_id"] != "" {
			return result, fmt.Errorf("form field %v: iot excludes options and characteristic_id", f.Id)
		}
		result.iot = true
		for _, t := range strings.Split(strings.ReplaceAll(iot, " ", ""), ",") {
			if t != "" {
				result.typeFilter = append(result.typeFilter, t)
			}
		}
		if raw, ok := p["criteria"]; ok {
			result.criteria = []json.RawMessage{json.RawMessage(raw)}
		}
		if raw, ok := p["criteria_list"]; ok {
			if err = json.Unmarshal([]byte(raw), &result.criteria); err != nil {
				return result, fmt.Errorf("invalid criteria_list property for form field %v: %w", f.Id, err)
			}
		}
		for _, c := range result.criteria {
			if !json.Valid(c) {
				return result, fmt.Errorf("invalid criteria property for form field %v", f.Id)
			}
		}
		result.entityOnly, _ = strconv.ParseBool(p["entity_only"])
		result.sameEntity = p["same_entity"]
	}
	return result, nil
}

func schemaType(t string) string {
	switch t {
	case "boolean":
		return "https://schema.org/Boolean"
	case "string":
		return "https://schema.org/Text"
	case "long":
		return "https://schema.org/Integer"
	case "number":
		return "https://schema.org/Float"
	}
	return t
}

func defaultValue(f bpmn.FormField) any {
	switch f.Type {
	case "string":
		// defaultValue="" is a default, as in smart-service-repository
		if f.HasDefault || f.DefaultValue != "" {
			return f.DefaultValue
		}
	case "long":
		if v, err := strconv.ParseFloat(f.DefaultValue, 64); err == nil {
			return v
		}
	case "boolean":
		if v, err := strconv.ParseBool(f.DefaultValue); err == nil {
			return v
		}
	}
	return nil
}

// Describe describes the form fields of a start event; auto_select_all fields are filled at
// the start and not shown. token is the caller's, IoT options are what the caller may select.
func (this *Describer) Describe(ctx context.Context, token string, start *bpmn.Element) ([]model.ParameterDescription, error) {
	result := []model.ParameterDescription{}
	if start == nil {
		return result, nil
	}
	for _, f := range start.FormFields {
		desc, err := read(f)
		if err != nil {
			return nil, model.ValidationFailed([]model.Issue{{ElementId: start.Id, Message: err.Error()}})
		}
		if desc.autoSelectAll {
			continue
		}
		param := model.ParameterDescription{Id: f.Id, Label: f.Label, Description: desc.description, Type: schemaType(f.Type), DefaultValue: defaultValue(f),
			Multiple: desc.multiple, Order: desc.order, Optional: desc.optional, CharacteristicId: f.Properties["characteristic_id"]}
		if param.Label == "" {
			param.Label = f.Id
		}
		if param.CharacteristicId != "" && this.characteristics != nil {
			characteristic, err, _ := this.characteristics.GetCharacteristic(param.CharacteristicId)
			if err != nil {
				return nil, model.Unavailable("characteristic %v: %v", param.CharacteristicId, err)
			}
			param.Characteristic = characteristic
		}
		if param.Options, err = this.options(ctx, token, desc); err != nil {
			return nil, err
		}
		param.HasNoValidOption = !(param.Optional || !desc.iot || len(param.Options) > 0)
		if len(param.Options) > 0 && param.DefaultValue != nil {
			found := false
			for _, option := range param.Options {
				if option.Value == param.DefaultValue {
					found = true
				}
			}
			if !found {
				param.DefaultValue = nil
			}
		}
		sort.SliceStable(param.Options, func(i, j int) bool {
			return strings.ToLower(param.Options[i].Label) < strings.ToLower(param.Options[j].Label)
		})
		result = append(result, param)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Order < result[j].Order })
	return result, nil
}

// AutoSelected returns the values of auto_select_all fields: every option the owner may select.
func (this *Describer) AutoSelected(ctx context.Context, token string, start *bpmn.Element) (map[string]any, error) {
	result := map[string]any{}
	if start == nil {
		return result, nil
	}
	for _, f := range start.FormFields {
		desc, err := read(f)
		if err != nil {
			return nil, model.ValidationFailed([]model.Issue{{ElementId: start.Id, Message: err.Error()}})
		}
		if !desc.autoSelectAll {
			continue
		}
		options, err := this.options(ctx, token, desc)
		if err != nil {
			return nil, err
		}
		values := []any{}
		for _, option := range options {
			values = append(values, option.Value)
		}
		result[f.Id] = values
	}
	return result, nil
}

func (this *Describer) options(ctx context.Context, token string, desc field) ([]model.ParameterOption, error) {
	if desc.options != nil {
		result := []model.ParameterOption{}
		for label, value := range desc.options {
			result = append(result, model.ParameterOption{Value: value, Label: label})
		}
		return result, nil
	}
	if !desc.iot {
		return nil, nil
	}
	result := []model.ParameterOption{}
	if this.deviceSelectionUrl == "" {
		return result, nil
	}
	selectables, err := this.selectables(ctx, token, desc.typeFilter, desc.criteria)
	if err != nil {
		return nil, err
	}
	for _, s := range selectables {
		if s.Device != nil {
			result = append(result, deviceOptions(s, desc)...)
		}
		if s.DeviceGroup != nil {
			label := s.DeviceGroup.Name
			result = append(result, model.ParameterOption{Kind: "Device-Groups", EntityId: s.DeviceGroup.Id, NeedsSameEntityIdInParameter: desc.sameEntity, Label: label,
				Value: encode(iotOption{DeviceGroupSelection: &groupSelection{Id: s.DeviceGroup.Id}, Label: label})})
		}
		if s.Import != nil {
			result = append(result, importOptions(s, desc)...)
		}
	}
	return result, nil
}

func (this *Describer) selectables(ctx context.Context, token string, typeFilter []string, criteria []json.RawMessage) (result []models.Selectable, err error) {
	query := url.Values{}
	if len(typeFilter) == 0 {
		typeFilter = []string{"device", "group", "import"}
	}
	for _, t := range typeFilter {
		switch t {
		case "device":
			query.Set("include_devices", "true")
		case "group":
			query.Set("include_groups", "true")
		case "import":
			query.Set("include_imports", "true")
			query.Set("import_path_trim_first_element", "true")
		case "device_service_group":
			query.Set("include_devices", "true")
			query.Set("include_id_modified", "true")
		}
	}
	if criteria == nil {
		criteria = []json.RawMessage{}
	}
	body, err := json.Marshal(criteria)
	if err != nil {
		return nil, err
	}
	cache, _ := ctx.Value(selectablesCacheKey{}).(*selectablesCache)
	if cache == nil {
		return this.querySelectables(ctx, token, query, body)
	}
	entry := cache.entry(query.Encode() + "\n" + string(body))
	entry.once.Do(func() {
		entry.result, entry.err = this.querySelectables(ctx, token, query, body)
	})
	return entry.result, entry.err
}

type selectablesCacheKey struct{}

type selectablesCache struct {
	mux     sync.Mutex
	entries map[string]*selectablesEntry
}

type selectablesEntry struct {
	once   sync.Once
	result []models.Selectable
	err    error
}

func (this *selectablesCache) entry(key string) *selectablesEntry {
	this.mux.Lock()
	defer this.mux.Unlock()
	e, ok := this.entries[key]
	if !ok {
		e = &selectablesEntry{}
		this.entries[key] = e
	}
	return e
}

// WithSelectablesCache lets the parameter descriptions under ctx share their answers from
// device-selection: a query asked again, also while the first one runs, is sent once. Only for
// the descriptions of one request, since the answers depend on its token; the release list of
// the mobile app asks the same criteria for many releases.
func WithSelectablesCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, selectablesCacheKey{}, &selectablesCache{entries: map[string]*selectablesEntry{}})
}

func (this *Describer) querySelectables(ctx context.Context, token string, query url.Values, body []byte) (result []models.Selectable, err error) {
	// built by hand: the client of device-selection takes no context and does not know
	// import_path_trim_first_element (vault: The device-selection client takes no context)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, this.deviceSelectionUrl+"/v2/query/selectables?"+query.Encode(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	if err = tracing.Inject(ctx, req); err != nil {
		return nil, err
	}
	resp, err := this.http.Do(req)
	if err != nil {
		return nil, model.Unavailable("device-selection: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, model.Unavailable("device-selection answered %v: %s", resp.Status, bytes.TrimSpace(raw))
	}
	return result, json.NewDecoder(resp.Body).Decode(&result)
}

// the JSON of the selections modules read; field order and names as in smart-service-repository
type iotOption struct {
	DeviceSelection      *deviceSelection `json:"device_selection,omitempty"`
	DeviceGroupSelection *groupSelection  `json:"device_group_selection,omitempty"`
	ImportSelection      *importSelection `json:"import_selection,omitempty"`
	Label                string           `json:"label"`
}

type deviceSelection struct {
	DeviceId         string  `json:"device_id"`
	ServiceId        *string `json:"service_id"`
	Path             *string `json:"path"`
	CharacteristicId *string `json:"characteristic_id,omitempty"`
}

type groupSelection struct {
	Id string `json:"id"`
}

type importSelection struct {
	Id               string  `json:"id"`
	Path             *string `json:"path"`
	CharacteristicId *string `json:"characteristic_id,omitempty"`
}

func encode(option iotOption) string {
	raw, _ := json.Marshal(option)
	return string(raw)
}

func deviceOptions(s models.Selectable, desc field) (result []model.ParameterOption) {
	device := *s.Device
	option := func(service models.Service, services int, path string, characteristicId string, paths int) model.ParameterOption {
		label := device.DisplayName
		if label == "" {
			label = device.Name
		}
		if label == "" {
			label = device.Id
		}
		selection := deviceSelection{DeviceId: device.Id}
		if service.Id != "" {
			selection.ServiceId = &service.Id
			if services > 1 {
				label += " / " + service.Name
			}
			if characteristicId != "" {
				selection.CharacteristicId = &characteristicId
			}
			if path != "" {
				selection.Path = &path
				if paths > 1 {
					label += " / " + path
				}
			}
		}
		return model.ParameterOption{Kind: "Devices", EntityId: device.Id, NeedsSameEntityIdInParameter: desc.sameEntity, Label: label,
			Value: encode(iotOption{DeviceSelection: &selection, Label: label})}
	}
	if desc.entityOnly || len(s.Services) == 0 {
		return []model.ParameterOption{option(models.Service{}, 0, "", "", 0)}
	}
	for _, service := range s.Services {
		paths := s.ServicePathOptions[service.Id]
		if len(paths) == 0 {
			result = append(result, option(service, len(s.Services), "", "", 0))
		}
		for _, p := range paths {
			result = append(result, option(service, len(s.Services), p.Path, p.CharacteristicId, len(paths)))
		}
	}
	return result
}

func importOptions(s models.Selectable, desc field) (result []model.ParameterOption) {
	imp := *s.Import
	option := func(path string, characteristicId string, paths int) model.ParameterOption {
		label := imp.Name
		selection := importSelection{Id: imp.Id}
		if characteristicId != "" {
			selection.CharacteristicId = &characteristicId
		}
		if path != "" {
			selection.Path = &path
			if paths > 1 {
				label += " / " + path
			}
		}
		return model.ParameterOption{Kind: "Imports", EntityId: imp.Id, NeedsSameEntityIdInParameter: desc.sameEntity, Label: label,
			Value: encode(iotOption{ImportSelection: &selection, Label: label})}
	}
	paths := s.ServicePathOptions[imp.ImportTypeId]
	if desc.entityOnly || len(paths) == 0 {
		return []model.ParameterOption{option("", "", 0)}
	}
	for _, p := range paths {
		result = append(result, option(p.Path, p.CharacteristicId, len(paths)))
	}
	return result
}
