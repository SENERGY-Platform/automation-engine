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
	"slices"
)

// PageOptions is the uniform paging of all list endpoints (API-06).
type PageOptions struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

const DefaultPageLimit = 100
const MaxPageLimit = 1000

func (this PageOptions) Normalized() PageOptions {
	if this.Limit <= 0 {
		this.Limit = DefaultPageLimit
	}
	if this.Limit > MaxPageLimit {
		this.Limit = MaxPageLimit
	}
	if this.Offset < 0 {
		this.Offset = 0
	}
	return this
}

type Page[T any] struct {
	Items  []T `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// ListRule is an allow or deny list; an empty allow list permits everything not denied.
type ListRule struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

func (this ListRule) Permits(value string) bool {
	if len(this.Allow) > 0 && !slices.Contains(this.Allow, value) {
		return false
	}
	return !slices.Contains(this.Deny, value)
}
