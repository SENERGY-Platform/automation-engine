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

import (
	"time"
)

// DebugState is the state of an instance as the debugger shows it, live (DBG-02) and at a step
// of the history (DBG-13). Both have the same form; the live stream is the end of the history.
type DebugState struct {
	Instance Instance `json:"instance"`
	// the gateway of the instance did not answer; the state is the copy the cloud has (DBG-15)
	Offline bool  `json:"offline,omitempty"`
	Runs    []Run `json:"runs,omitempty"`
	// last activity entry the state includes
	Seq  int64     `json:"seq"`
	Time time.Time `json:"time"`
	// elements with a token, several through parallel branches and event-based gateways
	Active []ActiveElement `json:"active"`
	Waits  []Wait          `json:"waits"`
	// instance variables; local variables are at their element in Active
	Variables map[string]DebugVariable `json:"variables"`
	Incidents []Incident               `json:"incidents"`
	// passes of elements and sequence flows in the run (DBG-06, DBG-07)
	Elements []ElementStats `json:"elements"`
	// set by the history: the state was reconstructed from a snapshot and activity entries;
	// FirstSeq is the earliest step that can still be reconstructed (DBG-14)
	Reconstructed bool  `json:"reconstructed,omitempty"`
	FirstSeq      int64 `json:"first_seq,omitempty"`
	// older activity entries were evicted (LONG-03)
	Truncated bool `json:"truncated,omitempty"`
}

type ActiveElement struct {
	ElementId   string                   `json:"element_id"`
	ElementType string                   `json:"element_type"`
	Name        string                   `json:"name,omitempty"`
	Since       time.Time                `json:"since"`
	Tokens      int                      `json:"tokens"`
	Locals      map[string]DebugVariable `json:"locals,omitempty"`
}

type WaitKind string

const (
	WaitMessage WaitKind = "message"
	WaitTimer   WaitKind = "timer"
	WaitTask    WaitKind = "task"
	WaitJoin    WaitKind = "join"
)

// Wait is the reason an element waits (DBG-02).
type Wait struct {
	ElementId string    `json:"element_id"`
	Kind      WaitKind  `json:"kind"`
	Since     time.Time `json:"since"`
	// the event-based gateway the wait belongs to; the first event cancels the others
	GatewayId string       `json:"gateway_id,omitempty"`
	Message   *MessageWait `json:"message,omitempty"`
	Timer     *TimerWait   `json:"timer,omitempty"`
	Task      *TaskWait    `json:"task,omitempty"`
	Join      *JoinWait    `json:"join,omitempty"`
}

type MessageWait struct {
	Name string `json:"name"`
	// sources of a device or import event; empty for messages sent through the API
	Events []EventSource `json:"events,omitempty"`
}

// EventSource describes where a waiting event comes from and how it is evaluated (DBG-02).
type EventSource struct {
	EventDescriptionId string            `json:"event_description_id"`
	DeviceId           string            `json:"device_id,omitempty"`
	ServiceId          string            `json:"service_id,omitempty"`
	DeviceGroupId      string            `json:"device_group_id,omitempty"`
	ImportId           string            `json:"import_id,omitempty"`
	Script             string            `json:"script"`
	ValueVariable      string            `json:"value_variable"`
	Variables          map[string]string `json:"variables,omitempty"`
	// evaluation of the current value of the source, on request (DBG-18)
	Current *EventEvaluation `json:"current,omitempty"`
}

// EventEvaluation is the evaluation of one value of an event source, current (DBG-18) or
// reconstructed from the time series (EVT-10).
type EventEvaluation struct {
	Time      *time.Time `json:"time,omitempty"`
	Raw       any        `json:"raw,omitempty"`
	Value     any        `json:"value,omitempty"`
	Triggered bool       `json:"triggered"`
	Error     string     `json:"error,omitempty"`
	Logs      []string   `json:"logs,omitempty"`
}

type TimerWait struct {
	Definition string     `json:"definition"`
	Kind       string     `json:"kind,omitempty"`
	DueAt      *time.Time `json:"due_at,omitempty"`
}

type TaskWait struct {
	JobId     string     `json:"job_id"`
	TaskType  string     `json:"task_type"`
	State     string     `json:"state,omitempty"`
	Attempt   int        `json:"attempt"`
	Retries   int        `json:"retries"`
	Timeout   string     `json:"timeout,omitempty"`
	DueAt     *time.Time `json:"due_at,omitempty"`
	LastError string     `json:"last_error,omitempty"`
	// device commands: one per device, with groups several (TASK-04)
	Subtasks []SubtaskState `json:"subtasks,omitempty"`
}

type SubtaskState struct {
	Id        string     `json:"id"`
	DeviceId  string     `json:"device_id,omitempty"`
	ServiceId string     `json:"service_id,omitempty"`
	Done      bool       `json:"done"`
	Tries     int        `json:"tries"`
	LastTry   *time.Time `json:"last_try,omitempty"`
}

type JoinWait struct {
	Arrived  int `json:"arrived"`
	Required int `json:"required"`
}

// DebugVariable is a variable with type, last change and changing element. Values above the
// transfer limit are cut; Truncated marks them and they can be loaded one by one (DBG-04).
type DebugVariable struct {
	Value     any       `json:"value"`
	Type      string    `json:"type"`
	UpdatedAt time.Time `json:"updated_at"`
	ElementId string    `json:"element_id,omitempty"`
	Truncated bool      `json:"truncated,omitempty"`
	Size      int       `json:"size,omitempty"`
}

type DebugMessageType string

const (
	DebugSnapshot DebugMessageType = "snapshot"
	DebugDelta    DebugMessageType = "delta"
	// the instance ended; State holds the end state and the stream closes afterwards
	DebugEnd   DebugMessageType = "end"
	DebugError DebugMessageType = "error"
)

// DebugMessage is a message of the live stream (DBG-01). After the snapshot, deltas carry the
// new activity entries and the parts of the state that changed; a client replaces Active,
// Waits and Incidents when present, upserts Variables and Elements and removes
// RemovedVariables (CLI-05).
type DebugMessage struct {
	Type             DebugMessageType         `json:"type"`
	Seq              int64                    `json:"seq"`
	State            *DebugState              `json:"state,omitempty"`
	Activities       []Activity               `json:"activities,omitempty"`
	Instance         *Instance                `json:"instance,omitempty"`
	Active           []ActiveElement          `json:"active,omitempty"`
	Waits            []Wait                   `json:"waits,omitempty"`
	Variables        map[string]DebugVariable `json:"variables,omitempty"`
	RemovedVariables []string                 `json:"removed_variables,omitempty"`
	Incidents        []Incident               `json:"incidents,omitempty"`
	Elements         []ElementStats           `json:"elements,omitempty"`
	// Active, Waits or Incidents changed; an empty list then means none
	ActiveChanged    bool      `json:"active_changed,omitempty"`
	WaitsChanged     bool      `json:"waits_changed,omitempty"`
	IncidentsChanged bool      `json:"incidents_changed,omitempty"`
	Error            string    `json:"error,omitempty"`
	ErrorKind        ErrorKind `json:"error_kind,omitempty"`
}

// DebugOptions select optional parts of a debug state.
type DebugOptions struct {
	// evaluate the current value of waiting device events (DBG-18)
	EvaluateEvents bool `json:"evaluate_events,omitempty"`
}

// WatchOptions select what the live stream sends (DBG-01).
type WatchOptions struct {
	// resume after this activity sequence instead of starting with a snapshot; a snapshot comes
	// anyway when the gap is too large or evicted
	After int64 `json:"after,omitempty"`
	// evaluate the current value of waiting device events, throttled while connected (DBG-18)
	EvaluateEvents bool `json:"evaluate_events,omitempty"`
}

// HistoryOptions select a step of the history (DBG-13): an activity sequence, or the last
// entry at or before a time.
type HistoryOptions struct {
	Seq  int64      `json:"seq,omitempty"`
	Time *time.Time `json:"time,omitempty"`
}

// HistoryVariable is the full value of a variable at a step of the history (DBG-04). Values
// the activity log holds cut are complete only at snapshots; Available is false otherwise.
type HistoryVariable struct {
	Seq       int64  `json:"seq"`
	Name      string `json:"name"`
	ElementId string `json:"element_id,omitempty"`
	Value     any    `json:"value,omitempty"`
	Available bool   `json:"available"`
}

// EventReconstructionOptions select the evaluations of a waiting event to reconstruct (EVT-10).
type EventReconstructionOptions struct {
	ElementId string `json:"element_id"`
	// 0 selects the current run
	Run int `json:"run,omitempty"`
	// evaluations after this time; a page ends at Next of the previous one
	After *time.Time `json:"after,omitempty"`
	// evaluations per source and page, default 100, at most 1000
	Limit int `json:"limit,omitempty"`
}

// EventReconstruction shows the evaluations of an event reconstructed from the time series next
// to its actual correlations (EVT-11). Deviations are diagnostic hints, not facts.
type EventReconstruction struct {
	ElementId         string             `json:"element_id"`
	MessageName       string             `json:"message_name,omitempty"`
	Run               int                `json:"run"`
	DeploymentVersion int                `json:"deployment_version"`
	Waiting           []WaitPeriod       `json:"waiting"`
	Sources           []EventSource      `json:"sources"`
	Evaluations       []SourceEvaluation `json:"evaluations"`
	Correlations      []Activity         `json:"correlations"`
	Hints             []string           `json:"hints,omitempty"`
	Limitations       []string           `json:"limitations,omitempty"`
	// more evaluations follow; pass it as After for the next page
	Next *time.Time `json:"next,omitempty"`
}

// WaitPeriod is a time the element waited for the event; To is nil while it still waits.
type WaitPeriod struct {
	From       time.Time  `json:"from"`
	To         *time.Time `json:"to,omitempty"`
	Correlated bool       `json:"correlated"`
}

// SourceEvaluation is a reconstructed evaluation of a value of one event source.
type SourceEvaluation struct {
	EventDescriptionId string `json:"event_description_id"`
	EventEvaluation
}
