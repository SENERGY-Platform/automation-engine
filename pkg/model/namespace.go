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
	"net"
	"regexp"
	"time"
)

// DeviceSelection names the ways a namespace offers to select devices (NS-11).
type DeviceSelection struct {
	AtDeploy bool `json:"at_deploy"`
	AtStart  bool `json:"at_start"`
}

// Kind is a level of resources; each namespace and kind is a permissions-v2 topic (NS-05).
type Kind string

const (
	KindModel      Kind = "models"
	KindDeployment Kind = "deployments"
	KindInstance   Kind = "instances"
)

var Kinds = []Kind{KindModel, KindDeployment, KindInstance}

// Rights uses the JSON form of permissions-v2.
type Rights struct {
	Read         bool `json:"read"`
	Write        bool `json:"write"`
	Execute      bool `json:"execute"`
	Administrate bool `json:"administrate"`
}

// Principals map group paths, role names or user ids to rights.
type Principals struct {
	Groups map[string]Rights `json:"groups,omitempty"`
	Roles  map[string]Rights `json:"roles,omitempty"`
	Users  map[string]Rights `json:"users,omitempty"`
}

// LevelSettings decide how resources of one kind are shared (NS-05).
type LevelSettings struct {
	// rights of others from permissions-v2 count; otherwise only the owner and admins have access
	Shareable bool `json:"shareable"`
	// register resources in permissions-v2 although the level is not shareable, so that it can
	// become shareable later without backfill; nil means true. Shareable levels are always registered.
	Register *bool `json:"register,omitempty"`
	// rights every new resource gets besides the owner's administrate
	InitialRights Principals `json:"initial_rights"`
	// DefaultPermissions of the permissions-v2 topic; they apply to every resource of the topic
	TopicDefaultPermissions Principals `json:"topic_default_permissions"`
}

func (this LevelSettings) Registered() bool {
	return this.Shareable || this.Register == nil || *this.Register
}

// Retention in days; 0 means not set (HIST-04).
type Retention struct {
	DefaultDays int `json:"default_days,omitempty"`
	MaxDays     int `json:"max_days,omitempty"`
}

// IncidentHandling decides what follows an incident (INC-02).
type IncidentHandling struct {
	Restart bool `json:"restart"`
	Notify  bool `json:"notify"`
}

// RestartPolicy protects against restart loops (INC-04); durations as Go durations, e.g. "1m".
type RestartPolicy struct {
	// automatic restarts per deployment within Window; above, the deployment is marked faulty
	MaxRestarts    int    `json:"max_restarts"`
	Window         string `json:"window"`
	InitialBackoff string `json:"initial_backoff"`
	MaxBackoff     string `json:"max_backoff"`
}

const (
	UserDeletionReject  = "reject"
	UserDeletionCascade = "cascade"
)

// Limits override the engine limits of section 12 for a namespace; 0 keeps the engine value.
type Limits struct {
	MaxTokens                 int `json:"max_tokens,omitempty"`
	MaxVariableBytes          int `json:"max_variable_bytes,omitempty"`
	MaxInstanceVariablesBytes int `json:"max_instance_variables_bytes,omitempty"`
	MaxActivities             int `json:"max_activities,omitempty"`
}

// HttpPolicy decides which targets the task type http may call (BPMN-10, NS-04). Deny lists
// win; with allow lists a target must match a host or network of them. Networks are checked on
// the resolved address, so that a host name cannot lead into a denied network.
type HttpPolicy struct {
	Enabled       bool     `json:"enabled"`
	AllowHosts    []string `json:"allow_hosts,omitempty"`
	DenyHosts     []string `json:"deny_hosts,omitempty"`
	AllowNetworks []string `json:"allow_networks,omitempty"`
	DenyNetworks  []string `json:"deny_networks,omitempty"`
	// empty allows every port
	AllowPorts []int `json:"allow_ports,omitempty"`
	// Go duration, default 10s
	Timeout          string `json:"timeout,omitempty"`
	MaxResponseBytes int    `json:"max_response_bytes,omitempty"`
}

type HttpSettings struct {
	Cloud HttpPolicy `json:"cloud"`
	Fog   HttpPolicy `json:"fog"`
}

// Namespace separates processes, smart services and further kinds of automations (NS-01 to NS-11).
type Namespace struct {
	Name            string          `json:"name"`
	Description     string          `json:"description,omitempty"`
	TaskTypes       ListRule        `json:"task_types"`
	Elements        ListRule        `json:"elements"`
	DeviceSelection DeviceSelection `json:"device_selection"`
	// namespaces whose resources this namespace may create through task types (NS-08)
	ReferencedNamespaces []string `json:"referenced_namespaces,omitempty"`

	Models      LevelSettings `json:"models"`
	Deployments LevelSettings `json:"deployments"`
	Instances   LevelSettings `json:"instances"`

	Retention Retention `json:"retention"`
	// default for deployments without own setting
	IncidentHandling IncidentHandling `json:"incident_handling"`
	RestartPolicy    RestartPolicy    `json:"restart_policy"`
	// what happens to resources of others that depend on a deleted user (SEC-13): reject or cascade
	UserDeletion string `json:"user_deletion"`
	Limits       Limits `json:"limits"`
	// targets of the task type http, separately for cloud and gateway (NS-04)
	Http HttpSettings `json:"http"`

	CreatedAt time.Time `json:"created_at,omitzero"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

func (this Namespace) Level(kind Kind) LevelSettings {
	switch kind {
	case KindModel:
		return this.Models
	case KindDeployment:
		return this.Deployments
	default:
		return this.Instances
	}
}

// Topic is the permissions-v2 topic of a kind in this namespace.
func (this Namespace) Topic(kind Kind) string {
	return Topic(this.Name, kind)
}

func Topic(namespace string, kind Kind) string {
	return namespace + "_" + string(kind)
}

var namespaceNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// WithDefaults fills settings that are not set.
func (this Namespace) WithDefaults() Namespace {
	if this.RestartPolicy.MaxRestarts == 0 {
		this.RestartPolicy.MaxRestarts = 10
	}
	if this.RestartPolicy.Window == "" {
		this.RestartPolicy.Window = "24h"
	}
	if this.RestartPolicy.InitialBackoff == "" {
		this.RestartPolicy.InitialBackoff = "1m"
	}
	if this.RestartPolicy.MaxBackoff == "" {
		this.RestartPolicy.MaxBackoff = "1h"
	}
	if this.UserDeletion == "" {
		this.UserDeletion = UserDeletionReject
	}
	for _, policy := range []*HttpPolicy{&this.Http.Cloud, &this.Http.Fog} {
		if policy.Timeout == "" {
			policy.Timeout = "10s"
		}
		if policy.MaxResponseBytes == 0 {
			policy.MaxResponseBytes = 1 << 20
		}
	}
	return this
}

func (this Namespace) Validate() error {
	if !namespaceNamePattern.MatchString(this.Name) {
		return BadRequest("namespace name %q must match %v", this.Name, namespaceNamePattern.String())
	}
	for _, d := range []string{this.RestartPolicy.Window, this.RestartPolicy.InitialBackoff, this.RestartPolicy.MaxBackoff} {
		if _, err := time.ParseDuration(d); err != nil {
			return BadRequest("restart_policy: invalid duration %q", d)
		}
	}
	if this.UserDeletion != UserDeletionReject && this.UserDeletion != UserDeletionCascade {
		return BadRequest("user_deletion must be %v or %v", UserDeletionReject, UserDeletionCascade)
	}
	if this.Retention.DefaultDays < 0 || this.Retention.MaxDays < 0 {
		return BadRequest("retention must not be negative")
	}
	if this.Retention.MaxDays > 0 && this.Retention.DefaultDays > this.Retention.MaxDays {
		return BadRequest("retention default %v exceeds the maximum %v", this.Retention.DefaultDays, this.Retention.MaxDays)
	}
	for _, policy := range []HttpPolicy{this.Http.Cloud, this.Http.Fog} {
		if _, err := time.ParseDuration(policy.Timeout); policy.Timeout != "" && err != nil {
			return BadRequest("http: invalid timeout %q", policy.Timeout)
		}
		for _, network := range append(append([]string{}, policy.AllowNetworks...), policy.DenyNetworks...) {
			if _, _, err := net.ParseCIDR(network); err != nil {
				return BadRequest("http: invalid network %q", network)
			}
		}
	}
	for _, kind := range Kinds {
		if len(this.Level(kind).InitialRights.Users) > 0 {
			return BadRequest("%v.initial_rights: users are not allowed, the owner is added per resource", kind)
		}
	}
	return nil
}

func (this RestartPolicy) Durations() (window, initial, maximum time.Duration) {
	window, _ = time.ParseDuration(this.Window)
	initial, _ = time.ParseDuration(this.InitialBackoff)
	maximum, _ = time.ParseDuration(this.MaxBackoff)
	return
}

// UserSettings are limits an admin sets per user (HIST-04).
type UserSettings struct {
	UserId           string `json:"user_id"`
	MaxRetentionDays int    `json:"max_retention_days,omitempty"`
}

// Dependent is a resource of another owner that depends on a user to be deleted (SEC-13).
type Dependent struct {
	Namespace string `json:"namespace"`
	Kind      Kind   `json:"kind"`
	Id        string `json:"id"`
	Owner     string `json:"owner"`
	Reason    string `json:"reason"`
}

// UserDependents is the dry run of a user deletion (SEC-14).
type UserDependents struct {
	UserId string `json:"user_id"`
	// counts of own resources per namespace and kind, e.g. "processes_models"
	Owned      map[string]int `json:"owned"`
	Dependents []Dependent    `json:"dependents"`
	// true if the deletion would be rejected because a namespace rejects foreign dependents
	Blocked bool `json:"blocked"`
}

// OwnerInput changes the owner of a resource (SEC-11).
type OwnerInput struct {
	Owner string `json:"owner"`
}

func (this Kind) Valid() error {
	switch this {
	case KindModel, KindDeployment, KindInstance:
		return nil
	}
	return BadRequest("unknown kind %q", string(this))
}
