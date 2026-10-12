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

package model

import "time"

// Module is a resource an instance created through a task type, e.g. a deployment, an import
// or an analytics pipeline (SMS-12). It belongs to the instance; modules of a maintenance
// procedure belong to the instance the procedure belongs to.
type Module struct {
	Id         string `json:"id"`
	Namespace  string `json:"namespace"`
	InstanceId string `json:"instance_id"`
	// the instance whose task created the module, a maintenance procedure or the instance itself
	CreatedBy  string            `json:"created_by,omitempty"`
	ElementId  string            `json:"element_id,omitempty"`
	Owner      string            `json:"owner"`
	ModuleType string            `json:"module_type"`
	Data       map[string]any    `json:"module_data"`
	Keys       []string          `json:"keys"`
	DeleteInfo *ModuleDeleteInfo `json:"delete_info,omitempty"`
	// finding of the last health check; shown in the status of the instance (SMS-11)
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ModuleDeleteInfo says how the resource of a module is removed: by a DELETE on Url with the
// token of UserId, where 2xx and 404 count as removed, or as a resource of the engine.
type ModuleDeleteInfo struct {
	Url    string `json:"url,omitempty"`
	UserId string `json:"user_id,omitempty"`
	// a deployment, instance or watch of the engine (NS-08)
	Resource *ResourceRef `json:"resource,omitempty"`
}

type ResourceKind string

const (
	ResourceDeployment ResourceKind = "deployment"
	ResourceInstance   ResourceKind = "instance"
	ResourceWatch      ResourceKind = "watch"
)

type ResourceRef struct {
	Kind      ResourceKind `json:"kind"`
	Namespace string       `json:"namespace,omitempty"`
	Id        string       `json:"id"`
}

type ModuleListOptions struct {
	PageOptions
	InstanceId string `json:"instance_id,omitempty"`
	ModuleType string `json:"module_type,omitempty"`
	Key        string `json:"key,omitempty"`
}

// InstanceStatus summarizes an instance for users (SMS-11): it derives from the run state,
// incidents and module errors.
type InstanceStatus string

const (
	StatusRunning InstanceStatus = "running"
	StatusReady   InstanceStatus = "ready"
	StatusError   InstanceStatus = "error"
)

// ElementParameters overrides values of single elements at deploy time: input mappings of tasks
// by name, senergy:variables of device events, the timer definition and defaults of start form
// fields (DEP-01).
type ElementParameters struct {
	Inputs    map[string]string `json:"inputs,omitempty"`
	Variables map[string]string `json:"variables,omitempty"`
	Timer     string            `json:"timer,omitempty"`
	Defaults  map[string]string `json:"defaults,omitempty"`
}

// MaintenanceProcedure is a further message start event of the model of an instance, with own
// start parameters (SMS-13). Its id is the message name.
type MaintenanceProcedure struct {
	Id         string           `json:"id"`
	ElementId  string           `json:"element_id"`
	Name       string           `json:"name,omitempty"`
	Parameters []StartParameter `json:"parameters"`
}

type MaintenanceStartInput struct {
	// values of the procedure's parameters; they extend the start variables of the instance
	Variables map[string]any `json:"variables,omitempty"`
}

// WatchRequest is what the watcher task type requests periodically (TASK-27).
type WatchRequest struct {
	Method   string            `json:"method"`
	Endpoint string            `json:"endpoint"`
	Body     []byte            `json:"body,omitempty"`
	Header   map[string]string `json:"header,omitempty"`
	// send the token of the owner
	AddAuthToken bool `json:"add_auth_token"`
	// a request from a model, sent under the HTTP policy of the namespace (NS-04)
	Generic bool `json:"generic,omitempty"`
}

// ParametersInput changes the start parameters of an instance; a new run starts with them and
// the modules of the previous run are removed (SMS-09).
type ParametersInput struct {
	Variables map[string]any `json:"variables"`
	// 0 selects the current version of the deployment
	DeploymentVersion int `json:"deployment_version,omitempty"`
}

// ParameterDescription describes a start parameter with its choices for a start dialog, as
// smart-service-repository did for releases (SMS-08).
type ParameterDescription struct {
	Id          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	// https://schema.org/Text, Integer, Float, Boolean or the form field type
	Type             string `json:"type"`
	DefaultValue     any    `json:"default_value"`
	Multiple         bool   `json:"multiple"`
	Order            int    `json:"order"`
	Optional         bool   `json:"optional"`
	CharacteristicId string `json:"characteristic_id,omitempty"`
	Characteristic   any    `json:"characteristic,omitempty"`
	// nil allows free input; IoT parameters always have a list
	Options []ParameterOption `json:"options"`
	// a required IoT parameter without any option the user may choose
	HasNoValidOption bool `json:"has_no_valid_option"`
}

type ParameterOption struct {
	// for IoT parameters the JSON of the selection, the value modules read
	Value    any    `json:"value"`
	Label    string `json:"label"`
	Kind     string `json:"kind"`
	EntityId string `json:"entity_id"`
	// the option fits only to the entity selected in that parameter
	NeedsSameEntityIdInParameter string `json:"needs_same_entity_id_in_parameter,omitempty"`
}
