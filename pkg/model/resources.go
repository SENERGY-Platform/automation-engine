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

// kinds of resources a process uses, named as by smart-service-repository
const (
	ResourceKindProcessModels = "process-models"
	ResourceKindFlows         = "flows"
	ResourceKindImportTypes   = "import-types"
)

// UsedResources are the process models, flows and import types the module tasks of a process
// name literally: process_deployment, analytics and import (SMS-15).
type UsedResources struct {
	ProcessModels []string `json:"process_models"`
	Flows         []string `json:"flows"`
	ImportTypes   []string `json:"import_types"`
}

// Of returns the ids of a kind; nil for an unknown kind.
func (this UsedResources) Of(kind string) []string {
	switch kind {
	case ResourceKindProcessModels:
		return this.ProcessModels
	case ResourceKindFlows:
		return this.Flows
	case ResourceKindImportTypes:
		return this.ImportTypes
	}
	return nil
}

// ResourceUsage counts, over all users, the deployment versions using a resource that are
// current or still have instances, and their instances; Readable names those the caller may
// read. Repositories ask before they delete a resource (SMS-15).
type ResourceUsage struct {
	Versions  int                       `json:"versions"`
	Instances int                       `json:"instances"`
	Readable  []ResourceUsageDeployment `json:"readable"`
}

type ResourceUsageDeployment struct {
	Id      string `json:"id"`
	Version int    `json:"version"`
	Current bool   `json:"current"`
	ModelId string `json:"model_id,omitempty"`
	Name    string `json:"name"`
}

// kinds of a ChangedResource, named as by smart-service-repository
const (
	ChangedProcessModel = "process_model"
	ChangedFlow         = "flow"
	ChangedImportType   = "import_type"
)

// ChangedResource names a resource a deployment version uses that changed or disappeared
// since the version was deployed; a hint for its owner (SMS-15).
type ChangedResource struct {
	Kind string `json:"kind"`
	Id   string `json:"id"`
	// changed or gone
	Reason string `json:"reason"`
	// unix seconds: the change time of the resource, or when the check first saw it gone or
	// (import types) changed
	At int64 `json:"at"`
}

type Count struct {
	Count int `json:"count"`
}
