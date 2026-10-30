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

// GatewayExportFormat names the document export-gateway writes and PUT /networks/{id}/import
// reads; it moves the deployments of a gateway into a new cloud with their ids and versions
// (SNRGY-5066). mgw-migrate builds on name, format and behaviour.
const (
	GatewayExportFormat  = "automation-engine-gateway-export"
	GatewayExportVersion = 1
)

// GatewayExport holds the deployments of a gateway as its database stores them. Instances,
// history, variables, incidents and IO values are not part of it; they come with the
// synchronisation once the cloud knows the deployments.
type GatewayExport struct {
	Format          string    `json:"format"`
	FormatVersion   int       `json:"format_version"`
	ExportedAt      time.Time `json:"exported_at"`
	ExporterVersion string    `json:"exporter_version"`
	// the highest migration applied to the database of the gateway
	SchemaMigration int                  `json:"schema_migration"`
	Namespaces      []Namespace          `json:"namespaces"`
	Deployments     []ExportedDeployment `json:"deployments"`
	// what the gateway applied of the desired state of its cloud, for diagnosis only
	Applied []ExportedApplied `json:"applied"`
}

type ExportedDeployment struct {
	Deployment Deployment `json:"deployment"`
	// all versions, ascending
	Versions          []DeploymentVersion `json:"versions"`
	EventDescriptions []EventDescription  `json:"event_descriptions"`
	DeviceGroups      []string            `json:"device_groups"`
}

type ExportedApplied struct {
	Kind      string    `json:"kind"`
	Id        string    `json:"id"`
	Version   int64     `json:"version"`
	Error     string    `json:"error"`
	AppliedAt time.Time `json:"applied_at"`
}

// NetworkImportOptions are the query parameters of PUT /networks/{id}/import.
type NetworkImportOptions struct {
	// report without writing
	DryRun bool
	// create the network if the cloud has never heard from the gateway, with the import as its
	// last contact; without it, the first contact creates it
	CreateNetwork bool
}

// NetworkImport reports what an import created or would create.
type NetworkImport struct {
	NetworkId   string               `json:"network_id"`
	Owner       string               `json:"owner"`
	DryRun      bool                 `json:"dry_run"`
	Deployments []ImportedDeployment `json:"deployments"`
	Models      []ImportedModel      `json:"models"`
	// the gateway sends all instances again on its next contact
	SnapshotRequested bool `json:"snapshot_requested"`
}

type ImportedDeployment struct {
	Id        string `json:"id"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	// created, exists or skipped
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type ImportedModel struct {
	Id string `json:"id"`
	// created, extended, exists or skipped
	Status        string `json:"status"`
	VersionsAdded []int  `json:"versions_added"`
	Reason        string `json:"reason,omitempty"`
}

const (
	ImportCreated  = "created"
	ImportExtended = "extended"
	ImportExists   = "exists"
	ImportSkipped  = "skipped"
)
