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

package commons_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SENERGY-Platform/automation-engine/pkg/commons"
	devicerepository "github.com/SENERGY-Platform/device-repository/v3/lib/client"
	devicerepomodel "github.com/SENERGY-Platform/device-repository/v3/lib/model"
	"github.com/SENERGY-Platform/models/go/models"
)

// aspectNodes answers ListAspectNodes like device-repository: sorted by name, unknown ids left
// out; every other method of the interface is not expected
type aspectNodes struct {
	devicerepository.Interface
	nodes    map[string]models.AspectNode
	requests [][]string
	err      error
}

func (this *aspectNodes) ListAspectNodes(_ context.Context, options devicerepomodel.AspectListOptions) ([]models.AspectNode, int64, error, int) {
	this.requests = append(this.requests, options.Ids)
	if this.err != nil {
		return nil, 0, this.err, http.StatusBadGateway
	}
	result := []models.AspectNode{}
	for _, id := range options.Ids {
		if node, ok := this.nodes[id]; ok {
			result = append(result, node)
		}
	}
	slices.SortFunc(result, func(a, b models.AspectNode) int { return strings.Compare(a.Name, b.Name) })
	return result, int64(len(result)), nil, http.StatusOK
}

func newAspectNodes() *aspectNodes {
	return &aspectNodes{nodes: map[string]models.AspectNode{
		"a": {Id: "a", Name: "zeta", RootId: "a"},
		"b": {Id: "b", Name: "alpha", RootId: "a"},
	}}
}

func TestAspectNodesByIdsReadsOnceInTheOrderOfTheIds(t *testing.T) {
	client := newAspectNodes()
	nodes, err, _ := commons.AspectNodesByIds(context.Background(), client, []string{"a", "b", "a"})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, node := range nodes {
		ids = append(ids, node.Id)
	}
	if !reflect.DeepEqual(ids, []string{"a", "b", "a"}) {
		t.Errorf("nodes in the order of the ids: %v", ids)
	}
	if !reflect.DeepEqual(client.requests, [][]string{{"a", "b"}}) {
		t.Errorf("one request with each id once: %v", client.requests)
	}
}

func TestAspectNodesByIdsReportsUnknownIds(t *testing.T) {
	_, err, code := commons.AspectNodesByIds(context.Background(), newAspectNodes(), []string{"a", "x"})
	if err == nil || code != http.StatusNotFound {
		t.Errorf("an unknown id is not found: %v %v", err, code)
	}
	failing := newAspectNodes()
	failing.err = errors.New("unavailable")
	if _, err, code = commons.AspectNodesByIds(context.Background(), failing, []string{"a"}); !errors.Is(err, failing.err) || code != http.StatusBadGateway {
		t.Errorf("the error and status of the client: %v %v", err, code)
	}
	if nodes, err, _ := commons.AspectNodesByIds(context.Background(), failing, nil); err != nil || len(nodes) != 0 {
		t.Errorf("no ids, no request: %v %v", nodes, err)
	}
}

func TestCompleteAspectNodesReadsOnlyNodesWithoutTheirHierarchy(t *testing.T) {
	client := newAspectNodes()
	given := []models.AspectNode{{Id: "b"}, {Id: "c", Name: "complete", RootId: "c"}, {}, {Id: "a"}}
	nodes, err, _ := commons.CompleteAspectNodes(context.Background(), client, given)
	if err != nil {
		t.Fatal(err)
	}
	expected := []models.AspectNode{client.nodes["b"], given[1], {}, client.nodes["a"]}
	if !reflect.DeepEqual(nodes, expected) {
		t.Errorf("%+v", nodes)
	}
	if !reflect.DeepEqual(client.requests, [][]string{{"a", "b"}}) {
		t.Errorf("one request for the incomplete nodes: %v", client.requests)
	}
}
