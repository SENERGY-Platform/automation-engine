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

package modules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/SENERGY-Platform/automation-engine/pkg/devices"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/modules/compat"
	devicerepository "github.com/SENERGY-Platform/device-repository/v2/lib/client"
	"github.com/google/uuid"
)

const analyticsPrefix = TypeAnalytics + "."

// analyticsHandler creates or, by key, updates an analytics pipeline at flow-engine from a flow
// of flow-parser, with inputs from devices, device groups or imports (TASK-22); behavior of
// smart-service-module-worker-analytics.
type analyticsHandler struct {
	runtime *Runtime
}

type PipelineRequest struct {
	Id                 string         `json:"id,omitempty"`
	FlowId             string         `json:"flowId,omitempty"`
	Name               string         `json:"name,omitempty"`
	Description        string         `json:"description,omitempty"`
	WindowTime         int            `json:"windowTime,omitempty"`
	ConsumeAllMessages bool           `json:"consumeAllMessages,omitempty"`
	MergeStrategy      string         `json:"mergeStrategy,omitempty"`
	Nodes              []PipelineNode `json:"nodes,omitempty"`
}

type PipelineNode struct {
	NodeId      string       `json:"nodeId,omitempty"`
	Inputs      []NodeInput  `json:"inputs,omitempty"`
	Config      []NodeConfig `json:"config,omitempty"`
	PersistData bool         `json:"persistData,omitempty"`
}

type NodeConfig struct {
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

const (
	deviceFilterType = "deviceId"
	importFilterType = "ImportId"
)

type NodeInput struct {
	FilterIds  string      `json:"filterIds,omitempty"`
	FilterType string      `json:"filterType"`
	TopicName  string      `json:"topicName,omitempty"`
	Values     []NodeValue `json:"values,omitempty"`
}

type NodeValue struct {
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
}

// Pipeline is the answer of flow-engine; the module keeps it under "pipeline".
type Pipeline struct {
	Id          uuid.UUID  `json:"id,omitempty"`
	Name        string     `json:"name,omitempty"`
	Description string     `json:"description,omitempty"`
	Operators   []Operator `json:"operators,omitempty"`
}

type Operator struct {
	Id             string            `json:"id,omitempty"`
	Name           string            `json:"name,omitempty"`
	ImageId        string            `json:"imageId,omitempty"`
	DeploymentType string            `json:"deploymentType,omitempty"`
	OperatorId     string            `json:"operatorId,omitempty"`
	Config         map[string]string `json:"config,omitempty"`
	InputTopics    []InputTopic
}

type InputTopic struct {
	Name        string    `json:"name,omitempty"`
	FilterType  string    `json:"filterType,omitempty"`
	FilterValue string    `json:"filterValue,omitempty"`
	Mappings    []Mapping `json:"mappings,omitempty"`
}

type Mapping struct {
	Dest   string `json:"dest,omitempty"`
	Source string `json:"source,omitempty"`
}

// FlowCell is an operator of a flow with its input ports and configuration
type FlowCell struct {
	Id      string   `json:"id"`
	InPorts []string `json:"inPorts,omitempty"`
	Config  []struct {
		Name string `json:"name,omitempty"`
		Type string `json:"type,omitempty"`
	} `json:"config,omitempty"`
}

// FilterCriteria selects services and paths of devices for an input port.
type FilterCriteria struct {
	Interaction   string `json:"interaction"`
	FunctionId    string `json:"function_id"`
	DeviceClassId string `json:"device_class_id"`
	// Deprecated: use AspectIds; AspectId is an alias for an AspectIds list with a single element.
	AspectId  string   `json:"aspect_id"`
	AspectIds []string `json:"aspect_ids,omitempty"`
}

func (this *analyticsHandler) Do(ctx context.Context, task Task) ([]model.Module, map[string]any, error) {
	var module model.Module
	var outputs map[string]any
	var err error
	if key, ok := task.String(analyticsPrefix + "key"); ok {
		module, outputs, err = this.withKey(ctx, task, key)
	} else {
		module, outputs, err = this.create(ctx, task, []string{})
	}
	if err != nil {
		return nil, outputs, err
	}
	data := moduleData(task, analyticsPrefix)
	for key, value := range module.Data {
		data[key] = value
	}
	module.Data = data
	return []model.Module{module}, outputs, nil
}

// withKey updates the pipeline of the module with the key in place; the stored pipeline stays as
// created, like with the worker
func (this *analyticsHandler) withKey(ctx context.Context, task Task, key string) (model.Module, map[string]any, error) {
	existing, err := this.runtime.existing(ctx, task, key, TypeAnalytics)
	if err != nil {
		// the worker went on and created a second pipeline
		return model.Module{}, nil, err
	}
	if len(existing) == 0 {
		return this.create(ctx, task, []string{key})
	}
	module := existing[0]
	pipelineId, exists := module.Data["pipeline_id"]
	if !exists {
		this.runtime.logger.WarnContext(ctx, "pipeline-id output not found in module", "module_id", module.Id)
		return this.create(ctx, task, []string{key})
	}
	id, ok := pipelineId.(string)
	if !ok {
		return module, nil, fmt.Errorf("module pipeline-id output is not string: \n %#v", module)
	}
	setModuleUpdateVersion(&module)
	outputs := map[string]any{"pipeline_id": id}
	request, err := this.pipelineRequest(ctx, task)
	if err != nil {
		return module, outputs, err
	}
	request.Id = id
	if err = this.runtime.request(ctx, this.runtime.http, http.MethodPut, this.runtime.settings.FlowEngineUrl+"/pipeline", task.Owner, true, request, nil); err != nil {
		return module, outputs, err
	}
	return module, outputs, nil
}

func (this *analyticsHandler) create(ctx context.Context, task Task, keys []string) (model.Module, map[string]any, error) {
	request, err := this.pipelineRequest(ctx, task)
	if err != nil {
		return model.Module{}, nil, err
	}
	var pipeline Pipeline
	if err = this.runtime.request(ctx, this.runtime.http, http.MethodPost, this.runtime.settings.FlowEngineUrl+"/pipeline", task.Owner, true, request, &pipeline); err != nil {
		return model.Module{}, nil, err
	}
	id := pipeline.Id.String()
	return model.Module{Id: task.ModuleId(), ModuleType: TypeAnalytics, Keys: keys,
			Data:       map[string]any{"pipeline_id": id, "pipeline": pipeline},
			DeleteInfo: &model.ModuleDeleteInfo{Url: this.runtime.settings.FlowEngineUrl + "/pipeline/" + url.PathEscape(id), UserId: task.Owner}},
		map[string]any{"pipeline_id": id}, nil
}

func (this *analyticsHandler) pipelineRequest(ctx context.Context, task Task) (result PipelineRequest, err error) {
	flowId, _ := task.String(analyticsPrefix + "flow_id")
	if flowId == "" {
		return result, errors.New("missing flow id")
	}
	var cells []FlowCell
	if err = this.runtime.request(ctx, this.runtime.http, http.MethodGet, this.runtime.settings.FlowParserUrl+"/flow/getinputs/"+url.PathEscape(flowId), task.Owner, true, nil, &cells); err != nil {
		return result, err
	}
	result = PipelineRequest{FlowId: flowId}
	if result.Name, _ = task.String(analyticsPrefix + "name"); result.Name == "" {
		return result, errors.New("missing pipeline name")
	}
	if result.WindowTime, err = this.windowTime(task); err != nil {
		return result, err
	}
	result.ConsumeAllMessages = boolOrJson(task.Inputs[analyticsPrefix+"consume_all_messages"])
	result.MergeStrategy = "inner"
	if value, exists := task.Inputs[analyticsPrefix+"merge_strategy"]; exists {
		strategy, ok := value.(string)
		if !ok {
			return result, errors.New("expected string for merge_strategy")
		}
		if strategy != "" {
			result.MergeStrategy = strategy
		}
	}
	result.Description, _ = task.String(analyticsPrefix + "desc")
	result.Nodes, err = this.nodes(ctx, task, cells)
	return result, err
}

func (this *analyticsHandler) windowTime(task Task) (int, error) {
	value, exists := task.Inputs[analyticsPrefix+"window_time"]
	if !exists {
		return 0, nil
	}
	switch v := value.(type) {
	case string:
		return strconv.Atoi(v)
	case float64:
		return int(v), nil
	case int:
		return v, nil
	case int64:
		return int(v), nil
	default:
		return 0, errors.New("unknown type for window_time")
	}
}

// boolOrJson reads a boolean or its JSON text; anything else is false
func boolOrJson(value any) bool {
	if v, ok := value.(bool); ok {
		return v
	}
	return jsonBool(value)
}

// jsonBool reads the JSON text of a boolean; anything else is false, a boolean as well
func jsonBool(value any) bool {
	var result bool
	if s, ok := value.(string); ok && json.Unmarshal([]byte(s), &result) == nil {
		return result
	}
	return false
}

func (this *analyticsHandler) nodes(ctx context.Context, task Task, cells []FlowCell) (result []PipelineNode, err error) {
	for _, cell := range cells {
		node := PipelineNode{NodeId: cell.Id, PersistData: jsonBool(task.Inputs[analyticsPrefix+"persistData."+cell.Id])}
		for _, conf := range cell.Config {
			value, err := this.nodeConfig(task, cell.Id, conf.Name)
			if err != nil {
				return result, err
			}
			node.Config = append(node.Config, NodeConfig{Name: conf.Name, Value: value})
		}
		for _, port := range cell.InPorts {
			selection, err := this.selection(task, cell.Id, port)
			if err != nil {
				return result, err
			}
			if selection.DeviceSelection == nil && selection.ImportSelection == nil && selection.DeviceGroupSelection == nil {
				continue
			}
			inputs, err := this.selectionInputs(ctx, task, selection, cell.Id, port)
			if err != nil {
				return result, err
			}
			node.Inputs = append(node.Inputs, inputs...)
		}
		node.Inputs = groupInputs(node.Inputs)
		sort.Slice(node.Inputs, func(i, j int) bool { return node.Inputs[i].TopicName < node.Inputs[j].TopicName })
		result = append(result, node)
	}
	return result, nil
}

// nodeConfig reads analytics.conf.<node>.<name>: strings as they are, objects by their value,
// everything else as JSON
func (this *analyticsHandler) nodeConfig(task Task, nodeId string, name string) (string, error) {
	key := analyticsPrefix + "conf." + nodeId + "." + name
	value, exists := task.Inputs[key]
	if !exists || value == nil {
		return "", nil
	}
	if s, ok := value.(string); ok {
		return s, nil
	}
	if m, ok := value.(map[string]any); ok {
		value = m["value"]
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("unable to interpret pipeline input config %v (%v) \n%w", key, value, err)
	}
	return string(raw), nil
}

func (this *analyticsHandler) selection(task Task, nodeId string, port string) (result compat.IotOption, err error) {
	name := analyticsPrefix + "selection." + nodeId + "." + port
	value, exists := task.Inputs[name]
	if !exists {
		return result, errors.New("missing pipeline input selection (" + name + ")")
	}
	raw, ok := value.(string)
	if !ok {
		return result, errors.New("unable to interpret pipeline input selection (" + name + ")")
	}
	if err = json.Unmarshal([]byte(raw), &result); err != nil {
		return result, fmt.Errorf("unable to interpret pipeline input selection (%v): %w", name, err)
	}
	return result, nil
}

func (this *analyticsHandler) criteria(task Task, kind string, nodeId string, port string, required bool) (result []FilterCriteria, err error) {
	name := analyticsPrefix + kind + "." + nodeId + "." + port
	value, exists := task.Inputs[name]
	if !exists {
		if required {
			return nil, errors.New("missing pipeline input criteria (mandatory when selection is group or device without service) (" + name + ")")
		}
		return nil, nil
	}
	raw, ok := value.(string)
	if !ok {
		return nil, errors.New("unable to interpret pipeline input criteria (" + name + ")")
	}
	if raw == "" && !required {
		return nil, nil
	}
	if err = json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("unable to interpret pipeline input criteria (%v): %w", name, err)
	}
	return result, nil
}

func groupInputs(in []NodeInput) []NodeInput {
	groups := map[string][]NodeInput{}
	for _, element := range in {
		key := element.TopicName + "_" + element.FilterType + "_" + element.FilterIds
		groups[key] = append(groups[key], element)
	}
	out := []NodeInput{}
	for _, group := range groups {
		merged := NodeInput{}
		for _, sub := range group {
			merged.Values = append(merged.Values, sub.Values...)
			merged.FilterType, merged.FilterIds, merged.TopicName = sub.FilterType, sub.FilterIds, sub.TopicName
		}
		out = append(out, merged)
	}
	return out
}

func (this *analyticsHandler) selectionInputs(ctx context.Context, task Task, selection compat.IotOption, nodeId string, port string) ([]NodeInput, error) {
	settings := this.runtime.settings
	switch {
	case selection.DeviceSelection != nil && selection.DeviceSelection.ServiceId != nil:
		if selection.DeviceSelection.Path == nil {
			return nil, errors.New("expect device selection to contain path info")
		}
		return []NodeInput{{FilterIds: selection.DeviceSelection.DeviceId, FilterType: deviceFilterType, TopicName: devices.ServiceIdToTopic(*selection.DeviceSelection.ServiceId),
			Values: []NodeValue{{Name: port, Path: settings.AnalyticsDevicePathPrefix + *selection.DeviceSelection.Path}}}}, nil
	case selection.DeviceSelection != nil:
		return this.serviceInputs(ctx, task, []string{selection.DeviceSelection.DeviceId}, "", nodeId, port)
	case selection.ImportSelection != nil:
		if selection.ImportSelection.Id == "" {
			return nil, errors.New("expect import selection to contain id")
		}
		if selection.ImportSelection.Path == nil {
			return nil, errors.New("expect import selection to contain path")
		}
		var imported struct {
			KafkaTopic string `json:"kafka_topic"`
		}
		if err := this.runtime.request(ctx, this.runtime.http, http.MethodGet, settings.ImportDeployUrl+"/instances/"+url.PathEscape(selection.ImportSelection.Id), task.Owner, false, nil, &imported); err != nil {
			return nil, fmt.Errorf("unable to get topic for import (%v): %w", selection.ImportSelection.Id, err)
		}
		path := *selection.ImportSelection.Path
		if settings.AnalyticsRemoveImportPathRoot {
			if _, rest, found := strings.Cut(path, "."); found {
				path = rest
			}
		}
		return []NodeInput{{FilterIds: selection.ImportSelection.Id, FilterType: importFilterType, TopicName: imported.KafkaTopic,
			Values: []NodeValue{{Name: port, Path: settings.AnalyticsImportPathPrefix + path}}}}, nil
	default:
		return this.serviceInputs(ctx, task, nil, selection.DeviceGroupSelection.Id, nodeId, port)
	}
}

// serviceInputs finds the services and paths of devices, or of the devices of a group, that match
// the criteria of the port; service criteria narrow the services further
func (this *analyticsHandler) serviceInputs(ctx context.Context, task Task, deviceIds []string, groupId string, nodeId string, port string) ([]NodeInput, error) {
	criteria, err := this.criteria(task, "criteria", nodeId, port, true)
	if err != nil {
		return nil, err
	}
	token, err := this.runtime.token(task.Owner)
	if err != nil {
		return nil, err
	}
	if groupId != "" {
		group, err, _ := this.runtime.iot.ReadDeviceGroup(groupId, token, false)
		if err != nil {
			return nil, err
		}
		deviceIds = group.DeviceIds
	}
	list, err, _ := this.runtime.iot.ListDevices(token, devicerepository.DeviceListOptions{Ids: deviceIds, Limit: int64(len(deviceIds)), SortBy: "name.asc"})
	if err != nil {
		return nil, err
	}
	type device struct{ id, deviceTypeId string }
	found := []device{}
	deviceTypeIds := []string{}
	for _, d := range list {
		found = append(found, device{d.Id, d.DeviceTypeId})
		if !slices.Contains(deviceTypeIds, d.DeviceTypeId) {
			deviceTypeIds = append(deviceTypeIds, d.DeviceTypeId)
		}
	}
	servicesAndPaths := func(criteria []FilterCriteria) (serviceIds []string, toDevices map[string][]string, toPaths map[string][]string, err error) {
		options, err := this.pathOptions(ctx, task.Owner, criteria, deviceTypeIds)
		if err != nil {
			return nil, nil, nil, err
		}
		serviceIds, toDevices, toPaths = []string{}, map[string][]string{}, map[string][]string{}
		for _, d := range found {
			for _, option := range options[d.deviceTypeId] {
				if len(option.paths) == 0 {
					continue
				}
				toDevices[option.serviceId] = append(toDevices[option.serviceId], d.id)
				if _, ok := toPaths[option.serviceId]; !ok {
					serviceIds = append(serviceIds, option.serviceId)
				}
				toPaths[option.serviceId] = append(toPaths[option.serviceId], option.paths...)
			}
		}
		return serviceIds, toDevices, toPaths, nil
	}
	serviceIds, toDevices, toPaths, err := servicesAndPaths(criteria)
	if err != nil {
		return nil, err
	}
	serviceCriteria, err := this.criteria(task, "service_criteria", nodeId, port, false)
	if err != nil {
		return nil, err
	}
	if len(serviceCriteria) > 0 {
		allowed, _, _, err := servicesAndPaths(serviceCriteria)
		if err != nil {
			return nil, err
		}
		serviceIds = slices.DeleteFunc(serviceIds, func(id string) bool { return !slices.Contains(allowed, id) })
	}
	result := []NodeInput{}
	for _, serviceId := range serviceIds {
		ids := strings.Join(toDevices[serviceId], ",")
		paths := toPaths[serviceId]
		if ids == "" || len(paths) == 0 {
			this.runtime.logger.WarnContext(ctx, "missing devices or paths for service, skipped", "service_id", serviceId)
			continue
		}
		if !this.runtime.settings.AnalyticsEnableMultiplePaths {
			paths = paths[:1]
		}
		values := []NodeValue{}
		for _, path := range paths {
			values = append(values, NodeValue{Name: port, Path: this.runtime.settings.AnalyticsGroupPathPrefix + path})
		}
		result = append(result, NodeInput{FilterIds: ids, FilterType: deviceFilterType, TopicName: devices.ServiceIdToTopic(serviceId), Values: values})
	}
	return result, nil
}

type pathOption struct {
	serviceId string
	paths     []string
}

// pathOptions asks device-repository which services and paths of the device types fulfill the
// criteria; an empty interaction means event, and the deprecated aspect id joins the list
func (this *analyticsHandler) pathOptions(ctx context.Context, owner string, criteria []FilterCriteria, deviceTypeIds []string) (map[string][]pathOption, error) {
	for i, c := range criteria {
		if c.Interaction == "" {
			c.Interaction = "event"
		}
		if c.AspectId != "" && !slices.Contains(c.AspectIds, c.AspectId) {
			c.AspectIds = append(append([]string{}, c.AspectIds...), c.AspectId)
		}
		criteria[i] = c
	}
	var selectables []struct {
		DeviceTypeId       string `json:"device_type_id"`
		ServicePathOptions map[string][]struct {
			ServiceId string `json:"service_id"`
			Path      string `json:"path"`
		} `json:"service_path_options"`
	}
	target := this.runtime.settings.DeviceRepositoryUrl + "/v2/query/device-type-selectables?" + url.Values{"services_must_match_all_criteria": {"true"}, "include_id_modified": {"true"}}.Encode()
	if err := this.runtime.request(ctx, this.runtime.http, http.MethodPost, target, owner, false, criteria, &selectables); err != nil {
		return nil, err
	}
	result := map[string][]pathOption{}
	for _, deviceTypeId := range deviceTypeIds {
		for _, selectable := range selectables {
			if selectable.DeviceTypeId != deviceTypeId {
				continue
			}
			for serviceId, options := range selectable.ServicePathOptions {
				option := pathOption{serviceId: serviceId, paths: []string{}}
				for _, o := range options {
					if o.ServiceId == serviceId {
						option.paths = append(option.paths, o.Path)
					}
				}
				result[deviceTypeId] = append(result[deviceTypeId], option)
			}
		}
	}
	return result, nil
}
