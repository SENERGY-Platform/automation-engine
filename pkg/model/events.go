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
	// when the deployment version of the description was created; the event worker of the cloud
	// ignores messages from before, which Kafka can deliver again (SNRGY-5037). Only filled for
	// the worker, not part of the API.
	ActiveSince time.Time `json:"-"`
}
