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

// IoValue is a value of the process-io store of a user (IO-02).
type IoValue struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
	// the value is deleted with this deployment or instance (IO-03)
	DeploymentId string    `json:"deployment_id,omitempty"`
	InstanceId   string    `json:"instance_id,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type IoValueInput struct {
	Value        any    `json:"value"`
	DeploymentId string `json:"deployment_id,omitempty"`
	InstanceId   string `json:"instance_id,omitempty"`
}

type IoListOptions struct {
	PageOptions
	// regular expression on the key
	KeyRegex     string `json:"key_regex,omitempty"`
	DeploymentId string `json:"deployment_id,omitempty"`
	InstanceId   string `json:"instance_id,omitempty"`
	Sort         string `json:"sort,omitempty"`
}

// IoBulk writes and then reads in one call, as process-io did.
type IoBulk struct {
	Get []string  `json:"get"`
	Set []IoValue `json:"set"`
}
