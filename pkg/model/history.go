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

// ActivityKind classifies entries of the activity log (HIST-05).
type ActivityKind string

const (
	ActivityRunStarted       ActivityKind = "run_started"
	ActivityRunEnded         ActivityKind = "run_ended"
	ActivityElementEntered   ActivityKind = "element_entered"
	ActivityElementLeft      ActivityKind = "element_left"
	ActivityFlowTaken        ActivityKind = "flow_taken"
	ActivityCondition        ActivityKind = "condition_evaluated"
	ActivityVariableSet      ActivityKind = "variable_set"
	ActivityScriptLog        ActivityKind = "script_log"
	ActivityTimerScheduled   ActivityKind = "timer_scheduled"
	ActivityTimerFired       ActivityKind = "timer_fired"
	ActivityMessageWaiting   ActivityKind = "message_waiting"
	ActivityMessageReceived  ActivityKind = "message_correlated"
	ActivityWaitCancelled    ActivityKind = "wait_cancelled"
	ActivityTaskCreated      ActivityKind = "task_created"
	ActivityTaskFailed       ActivityKind = "task_failed"
	ActivityTaskCompleted    ActivityKind = "task_completed"
	ActivityIncident         ActivityKind = "incident"
	ActivityTokenJoinWaiting ActivityKind = "join_waiting"
	ActivityRestartScheduled ActivityKind = "restart_scheduled"
)

type Activity struct {
	InstanceId  string         `json:"instance_id"`
	Run         int            `json:"run"`
	Seq         int64          `json:"seq"`
	Time        time.Time      `json:"time"`
	Kind        ActivityKind   `json:"kind"`
	ElementId   string         `json:"element_id,omitempty"`
	ElementType string         `json:"element_type,omitempty"`
	Data        map[string]any `json:"data,omitempty"`
}

type ActivityListOptions struct {
	// 0 selects all runs
	Run       int    `json:"run,omitempty"`
	AfterSeq  int64  `json:"after_seq,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Kind      string `json:"kind,omitempty"`
	ElementId string `json:"element_id,omitempty"`
}

type ActivityPage struct {
	Items []Activity `json:"items"`
	// older entries have been evicted by the per-instance limit (LONG-03)
	Truncated bool `json:"truncated"`
}

// ElementStats counts passes instead of keeping one row per pass (LONG-04).
type ElementStats struct {
	ElementId string     `json:"element_id"`
	Run       int        `json:"run"`
	Count     int64      `json:"count"`
	LastAt    time.Time  `json:"last_at"`
	LastError string     `json:"last_error,omitempty"`
	ErrorAt   *time.Time `json:"error_at,omitempty"`
}

// Incident is created directly by the engine (INC-01) and stores the variables at failure time (INC-06).
type Incident struct {
	Id                string         `json:"id"`
	Namespace         string         `json:"namespace"`
	Owner             string         `json:"owner"`
	InstanceId        string         `json:"instance_id"`
	Run               int            `json:"run"`
	DeploymentId      string         `json:"deployment_id"`
	DeploymentVersion int            `json:"deployment_version"`
	ElementId         string         `json:"element_id,omitempty"`
	ElementName       string         `json:"element_name,omitempty"`
	Message           string         `json:"message"`
	Time              time.Time      `json:"time"`
	Variables         map[string]any `json:"variables,omitempty"`
}

type IncidentListOptions struct {
	PageOptions
	InstanceId   string `json:"instance_id,omitempty"`
	Run          int    `json:"run,omitempty"`
	DeploymentId string `json:"deployment_id,omitempty"`
	ElementId    string `json:"element_id,omitempty"`
	Sort         string `json:"sort,omitempty"`
}

// MessageInput correlates a message by name (EVT-01, EVT-02).
type MessageInput struct {
	Name string `json:"name"`
	// optional target instance; with a message start event the instance is started under this id
	InstanceId string `json:"instance_id,omitempty"`
	// correlate with all waiting instances and message start events instead of exactly one
	All bool `json:"all"`
	// restricts correlation to resources of this owner; required for admins acting for a tenant
	Owner          string         `json:"owner,omitempty"`
	Variables      map[string]any `json:"variables,omitempty"`
	LocalVariables map[string]any `json:"local_variables,omitempty"`
	// idempotency key; a message with a known id is not correlated twice with the same target (EVT-05)
	MessageId string `json:"message_id,omitempty"`
}

type Correlation struct {
	InstanceId string `json:"instance_id"`
	ElementId  string `json:"element_id"`
	Started    bool   `json:"started"`
	Duplicate  bool   `json:"duplicate,omitempty"`
}

type CorrelationResult struct {
	Correlations []Correlation `json:"correlations"`
}
