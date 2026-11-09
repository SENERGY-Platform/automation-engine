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
	"encoding/json"
	"time"
)

// EventDescription tells event workers which device or import data concerns which device
// event and how to evaluate it (DEP-07, EVT-03, EVT-04). It is derived at deploy time from the
// device event and its selection; a device group is expanded to device × matching service.
type EventDescription struct {
	Id           string `json:"id"`
	DeploymentId string `json:"deployment_id"`
	Version      int    `json:"version"`
	ElementId    string `json:"element_id"`
	Namespace    string `json:"namespace"`
	Owner        string `json:"owner"`
	// the message name the correlation uses, stable across updates
	MessageName   string `json:"message_name"`
	DeviceId      string `json:"device_id,omitempty"`
	ServiceId     string `json:"service_id,omitempty"`
	DeviceGroupId string `json:"device_group_id,omitempty"`
	ImportId      string `json:"import_id,omitempty"`
	// the operator of an analytics pipeline whose output the description evaluates; its value
	// is the object "analytics" of the operator message, without unmarshalling (SNRGY-5113)
	PipelineId string `json:"pipeline_id,omitempty"`
	OperatorId string `json:"operator_id,omitempty"`
	// the Kafka topic with the data
	Topic string `json:"topic"`
	// condition: true, "true" or a number >= 1 triggers the event
	Script        string            `json:"script"`
	ValueVariable string            `json:"value_variable"`
	Variables     map[string]string `json:"variables,omitempty"`
	// 1: the message is acknowledged only after the correlation; 0: at most once (EVT-05)
	Qos              int      `json:"qos"`
	CharacteristicId string   `json:"characteristic_id,omitempty"`
	FunctionId       string   `json:"function_id,omitempty"`
	AspectIds        []string `json:"aspect_ids,omitempty"`
	// empty: the path follows from function and aspects
	Path string `json:"path,omitempty"`
	// the service to unmarshal with; artificial for imports
	Service  json.RawMessage `json:"service,omitempty"`
	Revision int64           `json:"revision"`
	// why the condition is no longer evaluated: it ended the process with a fatal error (EXPR-03);
	// empty while it is evaluated
	Suspended string `json:"suspended,omitempty"`
	// when the deployment version of the description was created; the event worker of the cloud
	// ignores messages from before, which Kafka can deliver again (SNRGY-5037). Only filled for
	// the worker, not part of the API.
	ActiveSince time.Time `json:"-"`
}

// BlockedScript is a script that ended the engine with a fatal error; it is refused on save and
// deploy and not run, also in copies that differ only in comments and whitespace (EXPR-03).
type BlockedScript struct {
	// SHA-256 of the token sequence of the script
	Fingerprint string `json:"fingerprint"`
	Source      string `json:"source"`
	// the error the script ended the engine with
	Reason string `json:"reason"`
	// where it ran, e.g. the element of a deployment
	Origin    string    `json:"origin"`
	CreatedAt time.Time `json:"created_at"`
	// when the stored deployments were searched for it; empty until then
	ScannedAt *time.Time `json:"scanned_at,omitempty"`
}
