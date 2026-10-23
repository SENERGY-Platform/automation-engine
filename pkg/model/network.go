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

// Network is a gateway as the cloud synchronises with it (SYNC-12, SYNC-13).
type Network struct {
	Id string `json:"id"`
	// the name of the hub in device-repository; empty if it cannot be read
	Name          string `json:"name,omitempty"`
	EngineVersion string `json:"engine_version,omitempty"`
	Protocol      int    `json:"protocol,omitempty"`
	// false: the gateway speaks a protocol the cloud does not serve; it needs an update (SYNC-11)
	Compatible  bool       `json:"compatible"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	LastContact *time.Time `json:"last_contact,omitempty"`
	Online      bool       `json:"online"`
	// positions of the change stream of the gateway the cloud has not taken yet
	UpBacklog int64 `json:"up_backlog"`
	// desired resources the gateway has not applied yet, and those it could not apply
	DownBacklog int `json:"down_backlog"`
	Failed      int `json:"failed"`
	// storage of the gateway as it reported it (LONG-14)
	Storage json.RawMessage `json:"storage,omitempty"`
	// the desired state per resource; only when reading one network
	Resources []NetworkResource `json:"resources,omitempty"`
}

// NetworkResource is the desired and the applied state of a resource of a gateway (SYNC-13).
type NetworkResource struct {
	Kind           string `json:"kind"`
	Id             string `json:"id"`
	Deleted        bool   `json:"deleted,omitempty"`
	Version        int64  `json:"version"`
	AppliedVersion int64  `json:"applied_version"`
	// pending, applied or failed
	State     string     `json:"state"`
	Error     string     `json:"error,omitempty"`
	AppliedAt *time.Time `json:"applied_at,omitempty"`
	SentAt    *time.Time `json:"sent_at,omitempty"`
}

const (
	ResourcePending = "pending"
	ResourceApplied = "applied"
	ResourceFailed  = "failed"
)
