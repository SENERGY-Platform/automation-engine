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

package devices

import (
	"net/url"
	"sync"
	"time"

	"github.com/SENERGY-Platform/models/go/models"
)

// AspectNodes resolves aspect node ids to full nodes, which the marshaller needs to match
// paths; process-deployment did this at deploy time. Nodes change rarely and are cached.
type AspectNodes struct {
	repoUrl string
	ttl     time.Duration
	mux     sync.Mutex
	cache   map[string]cachedNode
	fetch   func(token Token, id string) (models.AspectNode, error)
}

type cachedNode struct {
	node    models.AspectNode
	expires time.Time
}

// NewAspectNodesWith uses fetch to load nodes, e.g. in tests.
func NewAspectNodesWith(fetch func(token Token, id string) (models.AspectNode, error)) *AspectNodes {
	return &AspectNodes{ttl: 10 * time.Minute, cache: map[string]cachedNode{}, fetch: fetch}
}

func NewAspectNodes(repoUrl string) *AspectNodes {
	result := &AspectNodes{repoUrl: repoUrl, ttl: 10 * time.Minute, cache: map[string]cachedNode{}}
	result.fetch = func(token Token, id string) (node models.AspectNode, err error) {
		return node, getJson(defaultClient, token, result.repoUrl, "/aspect-nodes/"+url.PathEscape(id), &node)
	}
	return result
}

func (this *AspectNodes) ResolveIds(token Token, ids []string) ([]models.AspectNode, error) {
	result := make([]models.AspectNode, 0, len(ids))
	for _, id := range ids {
		node, err := this.get(token, id)
		if err != nil {
			return nil, err
		}
		result = append(result, node)
	}
	return result, nil
}

// resolve completes nodes that carry only their id, e.g. the aspects of a modeled payload
func (this *AspectNodes) Resolve(token Token, nodes []models.AspectNode) ([]models.AspectNode, error) {
	result := make([]models.AspectNode, 0, len(nodes))
	for _, n := range nodes {
		if n.RootId != "" || n.Id == "" {
			result = append(result, n)
			continue
		}
		full, err := this.get(token, n.Id)
		if err != nil {
			return nil, err
		}
		result = append(result, full)
	}
	return result, nil
}

func (this *AspectNodes) get(token Token, id string) (models.AspectNode, error) {
	this.mux.Lock()
	cached, ok := this.cache[id]
	this.mux.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cached.node, nil
	}
	node, err := this.fetch(token, id)
	if err != nil {
		return node, err
	}
	this.mux.Lock()
	this.cache[id] = cachedNode{node: node, expires: time.Now().Add(this.ttl)}
	this.mux.Unlock()
	return node, nil
}
