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
	"context"
	"time"

	"github.com/SENERGY-Platform/models/go/models"
)

// AspectNodes resolves aspect node ids to full nodes, which the marshaller needs to match
// paths; process-deployment did this at deploy time. Nodes change rarely and are cached by the
// device-repository client, cleared by the cache invalidation signals.
type AspectNodes struct {
	fetch func(ctx context.Context, token Token, id string) (models.AspectNode, error)
}

// NewAspectNodesWith uses fetch to load nodes, e.g. in tests.
func NewAspectNodesWith(fetch func(ctx context.Context, token Token, id string) (models.AspectNode, error)) *AspectNodes {
	return &AspectNodes{fetch: fetch}
}

// NewAspectNodes reads the nodes without a token, they are readable for everyone
func NewAspectNodes(repoUrl string) *AspectNodes {
	client := newCachedClient(repoUrl, 10*time.Minute)
	return NewAspectNodesWith(func(ctx context.Context, _ Token, id string) (models.AspectNode, error) {
		return read(ctx, "/aspect-nodes/"+id, func(n models.AspectNode) bool { return n.Id != "" }, func(ctx context.Context) (models.AspectNode, error, int) {
			return client.GetAspectNode(ctx, id)
		})
	})
}

func (this *AspectNodes) ResolveIds(ctx context.Context, token Token, ids []string) ([]models.AspectNode, error) {
	result := make([]models.AspectNode, 0, len(ids))
	for _, id := range ids {
		node, err := this.fetch(ctx, token, id)
		if err != nil {
			return nil, err
		}
		result = append(result, node)
	}
	return result, nil
}

// resolve completes nodes that carry only their id, e.g. the aspects of a modeled payload
func (this *AspectNodes) Resolve(ctx context.Context, token Token, nodes []models.AspectNode) ([]models.AspectNode, error) {
	result := make([]models.AspectNode, 0, len(nodes))
	for _, n := range nodes {
		if n.RootId != "" || n.Id == "" {
			result = append(result, n)
			continue
		}
		full, err := this.fetch(ctx, token, n.Id)
		if err != nil {
			return nil, err
		}
		result = append(result, full)
	}
	return result, nil
}
