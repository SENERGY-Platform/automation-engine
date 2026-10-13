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

type InstanceState string

const (
	StateRunning    InstanceState = "running"
	StateCompleted  InstanceState = "completed"
	StateTerminated InstanceState = "terminated"
	StateIncident   InstanceState = "incident"
	// started in the cloud for a gateway that has not reported the instance yet (SYNC-01)
	StatePending InstanceState = "pending"
)

// RunCause names why a run was started (RUN-11).
type RunCause string

const (
	CauseStart     RunCause = "start"
	CauseRestart   RunCause = "restart"
	CauseUpdate    RunCause = "update"
	CauseIncident  RunCause = "incident"
	CauseMigration RunCause = "migration"
	// the start parameters changed (SMS-09)
	CauseParameters RunCause = "parameters"
)

// Instance has a stable id, chosen by the caller or generated, and one or more runs (RUN-01, RUN-11).
// State, timestamps and deployment version describe the current run.
type Instance struct {
	Id string `json:"id"`
	// set by users, e.g. for smart-service instances
	Name              string         `json:"name,omitempty"`
	Description       string         `json:"description,omitempty"`
	Namespace         string         `json:"namespace"`
	Owner             string         `json:"owner"`
	DeploymentId      string         `json:"deployment_id"`
	DeploymentName    string         `json:"deployment_name"`
	DeploymentVersion int            `json:"deployment_version"`
	ModelId           string         `json:"model_id,omitempty"`
	ModelVersion      int            `json:"model_version,omitempty"`
	Run               int            `json:"run"`
	State             InstanceState  `json:"state"`
	EndReason         string         `json:"end_reason,omitempty"`
	StartVariables    map[string]any `json:"start_variables,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	StartedAt         time.Time      `json:"started_at"`
	EndedAt           *time.Time     `json:"ended_at,omitempty"`
	// last time an element was entered or left (LONG-13)
	LastActivityAt time.Time `json:"last_activity_at"`
	// an automatic restart after an incident is due at this time (INC-02)
	RestartAt *time.Time `json:"restart_at,omitempty"`
	// a maintenance procedure refers to the instance it belongs to (SMS-13)
	ParentId string `json:"parent_id,omitempty"`
	// the gateway that runs the instance; empty is the cloud (API-05)
	NetworkId string `json:"network_id,omitempty"`
	// summary for users, derived on read (SMS-11)
	Status        InstanceStatus `json:"status,omitempty"`
	StatusMessage string         `json:"status_message,omitempty"`
}

type Run struct {
	InstanceId        string        `json:"instance_id"`
	Run               int           `json:"run"`
	DeploymentVersion int           `json:"deployment_version"`
	Cause             RunCause      `json:"cause"`
	State             InstanceState `json:"state"`
	EndReason         string        `json:"end_reason,omitempty"`
	StartedAt         time.Time     `json:"started_at"`
	EndedAt           *time.Time    `json:"ended_at,omitempty"`
}

type StartInstanceInput struct {
	DeploymentId string `json:"deployment_id"`
	// 0 selects the current version
	DeploymentVersion int            `json:"deployment_version,omitempty"`
	Variables         map[string]any `json:"variables,omitempty"`
	Owner             string         `json:"owner,omitempty"`
	Name              string         `json:"name,omitempty"`
	Description       string         `json:"description,omitempty"`
}

// InstanceInfoInput changes name and description of an instance.
type InstanceInfoInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type RestartInput struct {
	// 0 selects the current version of the deployment
	DeploymentVersion int `json:"deployment_version,omitempty"`
}

type IdsInput struct {
	Ids []string `json:"ids"`
}

type InstanceListOptions struct {
	PageOptions
	// "running", "finished" or empty for all
	State          string   `json:"state,omitempty"`
	DeploymentId   string   `json:"deployment_id,omitempty"`
	DeploymentName string   `json:"deployment_name,omitempty"`
	ModelId        string   `json:"model_id,omitempty"`
	Ids            []string `json:"ids,omitempty"`
	Owner          string   `json:"owner,omitempty"`
	Sort           string   `json:"sort,omitempty"`
	// "cloud" lists instances of the cloud, a network id those of a gateway, empty all (API-05)
	NetworkId string `json:"network_id,omitempty"`
}

type Variable struct {
	Name      string    `json:"name"`
	Value     any       `json:"value"`
	Type      string    `json:"type"`
	UpdatedAt time.Time `json:"updated_at"`
	ElementId string    `json:"element_id,omitempty"`
}

type VariableInput struct {
	Value any `json:"value"`
}

// ValueType names the JSON type of a variable value.
func ValueType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64, float32, int, int64, int32, uint, uint64, uint32:
		return "number"
	case []any:
		return "array"
	default:
		return "object"
	}
}
