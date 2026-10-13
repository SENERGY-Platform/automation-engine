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

// Model is a process model; every save creates a new ModelVersion (MOD-01, MOD-05).
type Model struct {
	Id                 string     `json:"id"`
	Namespace          string     `json:"namespace"`
	Name               string     `json:"name"`
	Description        string     `json:"description"`
	Owner              string     `json:"owner"`
	Version            int        `json:"version"`
	Published          bool       `json:"published"`
	PublishDescription string     `json:"publish_description,omitempty"`
	PublishedAt        *time.Time `json:"published_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	// validation hints of the latest version; a model with issues can be saved but not deployed (MOD-04)
	Issues []Issue `json:"issues,omitempty"`
}

type ModelVersion struct {
	ModelId   string    `json:"model_id"`
	Version   int       `json:"version"`
	Xml       string    `json:"xml"`
	Svg       string    `json:"svg"`
	CreatedAt time.Time `json:"created_at"`
	Issues    []Issue   `json:"issues,omitempty"`
}

type ModelInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Xml         string `json:"xml"`
	Svg         string `json:"svg"`
	// only admins may create a resource for another owner (SEC-11)
	Owner string `json:"owner,omitempty"`
}

type PublishInput struct {
	Published   bool   `json:"published"`
	Description string `json:"description"`
}

type ModelListOptions struct {
	PageOptions
	Search string   `json:"search,omitempty"`
	Sort   string   `json:"sort,omitempty"`
	Ids    []string `json:"ids,omitempty"`
	Owner  string   `json:"owner,omitempty"`
	// models of other owners, e.g. those shared with the user
	NotOwner string `json:"not_owner,omitempty"`
}
