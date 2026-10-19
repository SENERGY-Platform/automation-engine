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

package devicecommand

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/models/go/models"
)

// The tests of this file are those of mgw-external-task-worker v1.2.2 pkg/marshaller
// (aspects_test.go): on a gateway the answer path is found by function and aspects.

const measureTemperature = models.MEASURING_FUNCTION_PREFIX + "getTemperature"

// aspectMirror serves aspect nodes like the device-repository mirror of a gateway
func aspectMirror(t *testing.T, nodes map[string]models.AspectNode) Marshaller {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		node, ok := nodes[strings.TrimPrefix(r.URL.Path, "/aspect-nodes/")]
		if !ok {
			http.Error(w, "unknown "+r.URL.Path, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(node)
	}))
	t.Cleanup(server.Close)
	m, err := NewLocalMarshaller(ctx, server.URL, time.Minute, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func flatAspects(t *testing.T) Marshaller {
	return aspectMirror(t, map[string]models.AspectNode{"air": {Id: "air"}, "inside": {Id: "inside"}, "outside": {Id: "outside"}})
}

func temperatureVariable(name string, aspectIds ...string) models.ContentVariable {
	return models.ContentVariable{Id: name, Name: name, Type: models.Integer, AspectIds: aspectIds, FunctionId: measureTemperature}
}

// both temperatures are air temperatures and differ in their second aspect; "inside" is the
// path found without any aspect
func temperatureRequest(variables ...models.ContentVariable) UnmarshallingV2Request {
	if len(variables) == 0 {
		variables = []models.ContentVariable{temperatureVariable("inside", "air", "inside"), temperatureVariable("outside", "air", "outside")}
	}
	return UnmarshallingV2Request{
		Service: models.Service{Id: "service_1", LocalId: "s1u", ProtocolId: "p1", Outputs: []models.Content{{
			Id: "metrics", Serialization: "json", ProtocolSegmentId: "ms1",
			ContentVariable: models.ContentVariable{Id: "metrics", Name: "metrics", Type: models.Structure, SubContentVariables: variables},
		}}},
		Protocol:   models.Protocol{Id: "p1", Handler: "protocol1", ProtocolSegments: []models.ProtocolSegment{{Id: "ms1", Name: "body"}}},
		Message:    map[string]string{"body": `{"inside":21,"outside":7}`},
		FunctionId: measureTemperature,
	}
}

func TestLocalMarshallerFindsTheAnswerByEveryNamedAspect(t *testing.T) {
	cases := []struct {
		name     string
		modify   func(request *UnmarshallingV2Request)
		expected any
	}{
		{"aspect nodes", func(r *UnmarshallingV2Request) { r.AspectNodes = []models.AspectNode{{Id: "air"}, {Id: "outside"}} }, 7.0},
		{"aspect node ids", func(r *UnmarshallingV2Request) { r.AspectNodeIds = []string{"air", "outside"} }, 7.0},
		{"deprecated aspect node is a list with one element", func(r *UnmarshallingV2Request) { r.AspectNode = models.AspectNode{Id: "outside"} }, 7.0},
		{"deprecated aspect node id is a list with one element", func(r *UnmarshallingV2Request) { r.AspectNodeId = "outside" }, 7.0},
		{"deprecated aspect node joins the aspect node ids", func(r *UnmarshallingV2Request) {
			r.AspectNode = models.AspectNode{Id: "air"}
			r.AspectNodeIds = []string{"outside"}
		}, 7.0},
		{"explicit path wins over aspects", func(r *UnmarshallingV2Request) {
			r.Path = "metrics.inside"
			r.AspectNodes = []models.AspectNode{{Id: "outside"}}
		}, 21.0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			request := temperatureRequest()
			c.modify(&request)
			result, err := flatAspects(t).UnmarshalV2(request)
			if err != nil {
				t.Fatal(err)
			}
			if result != c.expected {
				t.Errorf("expected %#v, got %#v", c.expected, result)
			}
		})
	}
}

func TestLocalMarshallerFindsNoAnswerForAspectsNoVariableCarriesTogether(t *testing.T) {
	request := temperatureRequest()
	request.AspectNodes = []models.AspectNode{{Id: "inside"}, {Id: "outside"}}
	if _, err := flatAspects(t).UnmarshalV2(request); err == nil {
		t.Fatal("no variable is inside and outside at once")
	}
}

func TestLocalMarshallerRejectsAnUnknownAspect(t *testing.T) {
	request := temperatureRequest()
	request.AspectNodeIds = []string{"air", "unknown"}
	if _, err := flatAspects(t).UnmarshalV2(request); err == nil {
		t.Fatal("an aspect the device-repository does not know is an error")
	}
}

func TestLocalMarshallerPrefersTheVariableClosestToTheAspect(t *testing.T) {
	// both variables match "air", "inside" only through its child "inside_air"
	request := temperatureRequest(temperatureVariable("inside", "inside_air"), temperatureVariable("outside", "air"))
	request.AspectNodeIds = []string{"air"}
	m := aspectMirror(t, map[string]models.AspectNode{
		"air":        {Id: "air", RootId: "air", ChildIds: []string{"inside_air"}, DescendentIds: []string{"inside_air"}},
		"inside_air": {Id: "inside_air", RootId: "air", ParentId: "air", AncestorIds: []string{"air"}},
	})
	result, err := m.UnmarshalV2(request)
	if err != nil {
		t.Fatal(err)
	}
	if result != 7.0 {
		t.Errorf("expected the value of the variable with the aspect itself, got %#v", result)
	}
}
