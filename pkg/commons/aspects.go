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

package commons

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	devicerepository "github.com/SENERGY-Platform/device-repository/v3/lib/client"
	devicerepomodel "github.com/SENERGY-Platform/device-repository/v3/lib/model"
	"github.com/SENERGY-Platform/models/go/models"
)

// AspectNodesByIds reads the nodes of ids with one request and returns them in the order of
// ids. Every id has to resolve: device-repository leaves an unknown id out of the list without
// an error, which is answered here with 404. Nodes are readable for everyone, so no token.
func AspectNodesByIds(ctx context.Context, client devicerepository.Interface, ids []string) ([]models.AspectNode, error, int) {
	unique := slices.Clone(ids)
	slices.Sort(unique)
	unique = slices.Compact(unique)
	if len(unique) == 0 {
		return []models.AspectNode{}, nil, http.StatusOK
	}
	list, _, err, code := client.ListAspectNodes(ctx, devicerepomodel.AspectListOptions{Ids: unique})
	if err != nil {
		return nil, fmt.Errorf("unable to read aspect nodes %v: %w", strings.Join(unique, ", "), err), code
	}
	byId := map[string]models.AspectNode{}
	for _, node := range list {
		byId[node.Id] = node
	}
	result := make([]models.AspectNode, 0, len(ids))
	missing := []string{}
	for _, id := range ids {
		node, ok := byId[id]
		if !ok {
			missing = append(missing, id)
			continue
		}
		result = append(result, node)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("unknown aspect nodes: %v", strings.Join(missing, ", ")), http.StatusNotFound
	}
	return result, nil, http.StatusOK
}

// CompleteAspectNodes reads the nodes that carry only their id, e.g. the aspects of a modeled
// payload, with one request; complete nodes stay as they are.
func CompleteAspectNodes(ctx context.Context, client devicerepository.Interface, nodes []models.AspectNode) ([]models.AspectNode, error, int) {
	ids := []string{}
	for _, node := range nodes {
		if incomplete(node) {
			ids = append(ids, node.Id)
		}
	}
	if len(ids) == 0 {
		return nodes, nil, http.StatusOK
	}
	read, err, code := AspectNodesByIds(ctx, client, ids)
	if err != nil {
		return nil, err, code
	}
	result := make([]models.AspectNode, 0, len(nodes))
	for _, node := range nodes {
		if incomplete(node) {
			node, read = read[0], read[1:]
		}
		result = append(result, node)
	}
	return result, nil, http.StatusOK
}

func incomplete(node models.AspectNode) bool {
	return node.RootId == "" && node.Id != ""
}
