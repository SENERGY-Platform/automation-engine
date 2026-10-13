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

// UpdateStrategy decides what happens to running instances when a deployment is updated (DEP-04).
type UpdateStrategy string

const (
	// stop running instances
	UpdateStop UpdateStrategy = "stop"
	// let running instances finish on their version, new starts use the new version
	UpdateDrain UpdateStrategy = "drain"
	// stop running instances and start a new run on the new version with the stored start variables
	UpdateRestart UpdateStrategy = "restart"
)

func (this UpdateStrategy) Valid() bool {
	return this == UpdateStop || this == UpdateDrain || this == UpdateRestart
}

// Deployment holds exactly one executable process; its id is the engine id (DEP-02).
type Deployment struct {
	Id           string `json:"id"`
	Namespace    string `json:"namespace"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Owner        string `json:"owner"`
	ModelId      string `json:"model_id,omitempty"`
	ModelVersion int    `json:"model_version,omitempty"`
	// the referenced model has been deleted; the deployment keeps its copy of the BPMN (MOD-08)
	ModelDeleted bool   `json:"model_deleted,omitempty"`
	Version      int    `json:"version"`
	Source       string `json:"source,omitempty"`
	// retention of history in days, 0 uses the namespace default; capped by the limits (HIST-04)
	RetentionDays int `json:"retention_days,omitempty"`
	// set when automatic restarts exceeded the restart policy; no automatic restarts until reset (INC-04)
	FaultySince *time.Time `json:"faulty_since,omitempty"`
	// the instance that manages the deployment through a module (NS-08)
	ManagedBy string `json:"managed_by,omitempty"`
	// the gateway that runs the deployment; empty runs it in the cloud (DEP-09)
	NetworkId string    `json:"network_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type DeploymentVersion struct {
	DeploymentId     string           `json:"deployment_id"`
	Version          int              `json:"version"`
	ModelVersion     int              `json:"model_version,omitempty"`
	Xml              string           `json:"xml"`
	Svg              string           `json:"svg"`
	UpdateStrategy   UpdateStrategy   `json:"update_strategy,omitempty"`
	IncidentHandling IncidentHandling `json:"incident_handling"`
	// device selection per BPMN element id (DEP-05)
	Selections map[string]Selection `json:"selections,omitempty"`
	// overrides per BPMN element id
	Parameters map[string]ElementParameters `json:"parameters,omitempty"`
	CreatedAt  time.Time                    `json:"created_at"`
}

// DeploymentInput creates or updates a deployment, either from a model (version) or from own BPMN.
type DeploymentInput struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	ModelId      string `json:"model_id,omitempty"`
	ModelVersion int    `json:"model_version,omitempty"`
	Xml          string `json:"xml,omitempty"`
	Svg          string `json:"svg,omitempty"`
	Source       string `json:"source,omitempty"`
	Owner        string `json:"owner,omitempty"`
	// nil uses the default of the namespace
	IncidentHandling *IncidentHandling `json:"incident_handling,omitempty"`
	RetentionDays    int               `json:"retention_days,omitempty"`
	// device selection per BPMN element id (DEP-05)
	Selections map[string]Selection `json:"selections,omitempty"`
	// overrides of inputs, event variables and timers per BPMN element id
	Parameters map[string]ElementParameters `json:"parameters,omitempty"`
	// required on update
	UpdateStrategy UpdateStrategy `json:"update_strategy,omitempty"`
	// set by the engine when a module creates the deployment (NS-08), not through the API
	ManagedBy string `json:"-"`
	// the gateway that runs the deployment; empty runs it in the cloud; fixed after creation (DEP-09)
	NetworkId string `json:"network_id,omitempty"`
}

type DeploymentListOptions struct {
	PageOptions
	Search  string   `json:"search,omitempty"`
	Sort    string   `json:"sort,omitempty"`
	Ids     []string `json:"ids,omitempty"`
	ModelId string   `json:"model_id,omitempty"`
	Owner   string   `json:"owner,omitempty"`
	Source  string   `json:"source,omitempty"`
	// "cloud" lists deployments of the cloud, a network id those of a gateway, empty all (API-05)
	NetworkId string `json:"network_id,omitempty"`
}

// StartParameter is a form field of the start event (BPMN-02, RUN-02).
type StartParameter struct {
	Id         string            `json:"id"`
	Label      string            `json:"label"`
	Type       string            `json:"type"`
	Default    string            `json:"default,omitempty"`
	Properties map[string]string `json:"properties,omitempty"`
}

// Schedule starts instances of a deployment by cron (RUN-07). It belongs to the deployment, not
// to a version, and starts the current version; starts run as the deployment owner (SEC-12).
type Schedule struct {
	Id           string `json:"id"`
	Namespace    string `json:"namespace"`
	DeploymentId string `json:"deployment_id"`
	Owner        string `json:"owner"`
	// cron with 5 or 6 fields (optional seconds first) or a descriptor such as @daily
	Cron     string `json:"cron"`
	Alias    string `json:"alias,omitempty"`
	Disabled bool   `json:"disabled"`
	// free reference of the client that created the schedule, e.g. a dashboard widget
	CreatedBy string     `json:"created_by,omitempty"`
	NextDue   *time.Time `json:"next_due,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type ScheduleInput struct {
	DeploymentId string `json:"deployment_id"`
	Cron         string `json:"cron"`
	Alias        string `json:"alias,omitempty"`
	Disabled     bool   `json:"disabled"`
	CreatedBy    string `json:"created_by,omitempty"`
}

type ScheduleListOptions struct {
	PageOptions
	DeploymentId string `json:"deployment_id,omitempty"`
	Owner        string `json:"owner,omitempty"`
	CreatedBy    string `json:"created_by,omitempty"`
}
