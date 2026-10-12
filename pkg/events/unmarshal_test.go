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

package events_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/models/go/models"
)

// The tests of this file are those of the marshaller and the device-repository access of
// event-worker v1.2.0.

// repoFixture answers the marshaller from maps, so that a test describes the aspect tree once
type repoFixture struct {
	characteristics map[string]models.Characteristic
	concepts        map[string]models.Concept
	functions       map[string]string
	aspects         map[string]models.AspectNode
}

func (this repoFixture) GetCharacteristic(id string) (models.Characteristic, error) {
	if c, ok := this.characteristics[id]; ok {
		return c, nil
	}
	return models.Characteristic{}, errors.New("unknown characteristic: " + id)
}

func (this repoFixture) GetConcept(id string) (models.Concept, error) {
	if c, ok := this.concepts[id]; ok {
		return c, nil
	}
	return models.Concept{}, errors.New("unknown concept: " + id)
}

func (this repoFixture) GetConceptIdOfFunction(id string) string {
	return this.functions[id]
}

func (this repoFixture) GetAspectNode(id string) (models.AspectNode, error) {
	if node, ok := this.aspects[id]; ok {
		return node, nil
	}
	return models.AspectNode{}, errors.New("unknown aspect node: " + id)
}

func (this repoFixture) GetAspectNodes(ids []string) (result []models.AspectNode, err error) {
	for _, id := range ids {
		node, err := this.GetAspectNode(id)
		if err != nil {
			return nil, err
		}
		result = append(result, node)
	}
	return result, nil
}

// the device sends its value in devicevalue, the event asks for requestvalue = devicevalue + 10
func conceptFixture(aspects map[string]models.AspectNode) repoFixture {
	return repoFixture{
		characteristics: map[string]models.Characteristic{
			"devicevalue":  {Id: "devicevalue", Name: "devicevalue", Type: models.Integer},
			"requestvalue": {Id: "requestvalue", Name: "requestvalue", Type: models.Integer},
		},
		concepts: map[string]models.Concept{"testconcept": {
			Id: "testconcept", Name: "testconcept", CharacteristicIds: []string{"devicevalue", "requestvalue"}, BaseCharacteristicId: "requestvalue",
			Conversions: []models.ConverterExtension{
				{From: "requestvalue", To: "devicevalue", Formula: "x - 10", PlaceholderName: "x"},
				{From: "devicevalue", To: "requestvalue", Formula: "x + 10", PlaceholderName: "x"},
			},
		}},
		functions: map[string]string{"fid": "testconcept"},
		aspects:   aspects,
	}
}

func node(id string, parent string, root string, children []string, ancestors []string, descendents []string) models.AspectNode {
	return models.AspectNode{Id: id, Name: id, ParentId: parent, RootId: root, ChildIds: children, AncestorIds: ancestors, DescendentIds: descendents}
}

func outputService(withIds bool, outputs ...models.ContentVariable) models.Service {
	service := models.Service{Interaction: models.EVENT, Outputs: []models.Content{{
		ContentVariable:   models.ContentVariable{Name: "outputcontent", Type: models.Structure, SubContentVariables: outputs},
		Serialization:     models.JSON,
		ProtocolSegmentId: "output",
	}}}
	if withIds {
		service.Id, service.LocalId, service.ProtocolId = "sid", "lsid", "pid"
		service.Outputs[0].Id, service.Outputs[0].ContentVariable.Id = "output", "outputcontentid"
	}
	return service
}

func output(name string, aspectId string, aspectIds []string) models.ContentVariable {
	return models.ContentVariable{Id: name, Name: name, Type: models.Integer, CharacteristicId: "devicevalue", FunctionId: "fid", AspectId: aspectId, AspectIds: aspectIds}
}

func TestMarshallerConvertsTheValueOfTheEvent(t *testing.T) {
	m, err := events.NewMarshaller(conceptFixture(map[string]models.AspectNode{
		"aid_parent": node("aid_parent", "", "aid_parent", []string{"aid"}, []string{}, []string{"aid", "aid_child"}),
		"aid":        node("aid", "aid_parent", "aid_parent", []string{"aid_child"}, []string{"aid_parent"}, []string{"aid_child"}),
		"aid_child":  node("aid_child", "aid", "aid_parent", []string{}, []string{"aid_parent", "aid"}, []string{}),
	}))
	if err != nil {
		t.Fatal(err)
	}
	message := map[string]any{"outputcontent": map[string]any{"time": 3333, "value": 42}}
	timeVariable := models.ContentVariable{Id: "time", Name: "time", Type: models.Integer}
	cases := []struct {
		name    string
		service models.Service
		path    string
	}{
		{"without path", outputService(true, output("value", "aid", nil), timeVariable), ""},
		{"with path", outputService(true, output("value", "aid", nil), timeVariable), "outputcontent.value"},
		{"device uses child aspect", outputService(true, output("value", "aid_child", nil), timeVariable), ""},
		{"without ids (import like service)", outputService(false, output("value", "aid", nil), timeVariable), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			value, err := m.Unmarshal(events.MessageDesc{Service: c.service, CharacteristicId: "requestvalue", FunctionId: "fid", AspectIds: []string{"aid"}, Path: c.path, Message: message})
			if err != nil {
				t.Fatal(err)
			}
			if value != float64(52) {
				t.Errorf("%#v", value)
			}
		})
	}
}

// Every requested aspect has to be carried by the same content variable, each covering its own
// subtree downwards. The message carries a different number per path and the conversion adds
// ten, so the value names the chosen path.
func TestMarshallerSelectsThePathByAllAspects(t *testing.T) {
	m, err := events.NewMarshaller(conceptFixture(map[string]models.AspectNode{
		"air":         node("air", "", "air", []string{"inside_air", "outside_air"}, []string{}, []string{"inside_air", "outside_air", "kitchen_air"}),
		"inside_air":  node("inside_air", "air", "air", []string{"kitchen_air"}, []string{"air"}, []string{"kitchen_air"}),
		"outside_air": node("outside_air", "air", "air", []string{}, []string{"air"}, []string{}),
		"kitchen_air": node("kitchen_air", "inside_air", "air", []string{}, []string{"air", "inside_air"}, []string{}),
		"electricity": node("electricity", "", "electricity", []string{}, []string{}, []string{}),
	}))
	if err != nil {
		t.Fatal(err)
	}
	message := map[string]any{"outputcontent": map[string]any{"inside": 1, "outside": 2, "kitchen": 3, "power": 4, "far": 3, "near": 5}}
	// 'inside' is listed first, it is the answer of a selection by one aspect instead of all
	service := outputService(true, output("inside", "", []string{"inside_air"}), output("outside", "", []string{"outside_air"}),
		output("kitchen", "", []string{"kitchen_air", "electricity"}), output("power", "", []string{"electricity"}))
	// both answer air and electricity, the farther one first
	distance := outputService(true, output("far", "", []string{"kitchen_air", "electricity"}), output("near", "", []string{"inside_air", "electricity"}))
	unmarshal := func(service models.Service, aspectIds ...string) (any, error) {
		return m.Unmarshal(events.MessageDesc{Service: service, CharacteristicId: "requestvalue", FunctionId: "fid", AspectIds: aspectIds, Message: message})
	}
	cases := []struct {
		name      string
		service   models.Service
		aspectIds []string
		value     any
		err       string
	}{
		{"selects the path that carries every requested aspect", service, []string{"inside_air", "electricity"}, float64(13), ""},
		{"reads a single aspect", service, []string{"outside_air"}, float64(12), ""},
		{"does not accept a path that carries only an ancestor of a requested aspect", service, []string{"kitchen_air"}, float64(13), ""},
		{"prefers the path whose aspects sit closest to the requested ones", distance, []string{"air", "electricity"}, float64(15), ""},
		{"finds no path for two sibling aspects", service, []string{"inside_air", "outside_air"}, nil, "no output path found for criteria"},
		{"rejects a description without any aspect", service, nil, nil, "missing aspect id"},
		{"surfaces the error of an aspect that cannot be resolved", service, []string{"inside_air", "unknown"}, nil, "unknown aspect node"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			value, err := unmarshal(c.service, c.aspectIds...)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("expected %q, got %v", c.err, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if value != c.value {
				t.Errorf("%#v", value)
			}
		})
	}
}

// repository runs handler as device-repository, so that the requests of the client are tested
func repository(t *testing.T, handler http.HandlerFunc) *events.CachedDeviceRepo {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	repo, err := events.NewCachedDeviceRepo(server.URL, auth.Static("Bearer client"), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestDeviceRepoAsksTheEndpointOfEachEntityWithTheClientToken(t *testing.T) {
	var mux sync.Mutex
	requests := []string{}
	repo := repository(t, func(w http.ResponseWriter, r *http.Request) {
		mux.Lock()
		requests = append(requests, r.URL.RequestURI()+" "+r.Header.Get("Authorization"))
		mux.Unlock()
		responses := map[string]any{
			"/aspect-nodes/urn:aspect:1":            models.AspectNode{Id: "urn:aspect:1", Name: "aspect", ChildIds: []string{"urn:aspect:2"}},
			"/characteristics/urn:characteristic:1": models.Characteristic{Id: "urn:characteristic:1", Name: "characteristic", Type: models.Integer},
			"/functions/urn:function:1":             models.Function{Id: "urn:function:1", Name: "function", ConceptId: "urn:concept:1"},
			"/concepts/urn:concept:1":               models.Concept{Id: "urn:concept:1", Name: "concept", CharacteristicIds: []string{"urn:characteristic:1"}},
		}
		response, ok := responses[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	})
	if node, err := repo.GetAspectNode("urn:aspect:1"); err != nil || !reflect.DeepEqual(node.ChildIds, []string{"urn:aspect:2"}) {
		t.Fatalf("aspect node: %+v %v", node, err)
	}
	if c, err := repo.GetCharacteristic("urn:characteristic:1"); err != nil || c.Type != models.Integer {
		t.Fatalf("characteristic: %+v %v", c, err)
	}
	// the marshaller needs the ids and conversions of a concept, not its characteristics
	if c, err := repo.GetConcept("urn:concept:1"); err != nil || !reflect.DeepEqual(c.CharacteristicIds, []string{"urn:characteristic:1"}) {
		t.Fatalf("concept: %+v %v", c, err)
	}
	if id := repo.GetConceptIdOfFunction("urn:function:1"); id != "urn:concept:1" {
		t.Fatalf("concept of function: %v", id)
	}
	expected := []string{
		"/aspect-nodes/urn:aspect:1 Bearer client",
		"/characteristics/urn:characteristic:1 Bearer client",
		"/concepts/urn:concept:1?sub-class=false Bearer client",
		"/functions/urn:function:1 Bearer client",
	}
	if !reflect.DeepEqual(requests, expected) {
		t.Errorf("%v", requests)
	}
}

func TestDeviceRepoCachesAnswers(t *testing.T) {
	var mux sync.Mutex
	count := 0
	repo := repository(t, func(w http.ResponseWriter, r *http.Request) {
		mux.Lock()
		count++
		mux.Unlock()
		_ = json.NewEncoder(w).Encode(models.AspectNode{Id: "urn:aspect:1", Name: "aspect"})
	})
	for i := 0; i < 3; i++ {
		if node, err := repo.GetAspectNode("urn:aspect:1"); err != nil || node.Id != "urn:aspect:1" {
			t.Fatalf("%+v %v", node, err)
		}
	}
	if count != 1 {
		t.Errorf("expected one request, got %v", count)
	}
}

// An unavailable device-repository makes the message to be read again; a rejected request is
// final and reaches the owner as notification.
func TestDeviceRepoMarksOnlyServerErrorsAsUnavailable(t *testing.T) {
	cases := []struct {
		status      int
		unavailable bool
	}{{http.StatusNotFound, false}, {http.StatusBadRequest, false}, {http.StatusInternalServerError, true}, {http.StatusServiceUnavailable, true}}
	for _, c := range cases {
		t.Run(strconv.Itoa(c.status), func(t *testing.T) {
			repo := repository(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte("no"))
			})
			_, err := repo.GetAspectNode("urn:aspect:" + strconv.Itoa(c.status))
			if err == nil || errors.Is(err, events.ErrUnavailable) != c.unavailable {
				t.Errorf("%v", err)
			}
		})
	}
	t.Run("unreachable", func(t *testing.T) {
		server := httptest.NewServer(http.NotFoundHandler())
		server.Close()
		repo, err := events.NewCachedDeviceRepo(server.URL, auth.Static("Bearer client"), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = repo.GetAspectNode("urn:aspect:1"); !errors.Is(err, events.ErrUnavailable) {
			t.Errorf("%v", err)
		}
	})
}

// The aspects of one event description are read in one request; an id device-repository does
// not know is reported rather than dropped.
func TestDeviceRepoReadsAspectNodeLists(t *testing.T) {
	known := map[string]models.AspectNode{
		"urn:aspect:1": {Id: "urn:aspect:1", Name: "one"},
		"urn:aspect:2": {Id: "urn:aspect:2", Name: "two"},
		"urn:aspect:3": {Id: "urn:aspect:3", Name: "three"},
	}
	var mux sync.Mutex
	requests := []string{}
	repo := repository(t, func(w http.ResponseWriter, r *http.Request) {
		mux.Lock()
		defer mux.Unlock()
		if id, single := strings.CutPrefix(r.URL.Path, "/aspect-nodes/"); single {
			requests = append(requests, r.URL.Path)
			n, ok := known[id]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(n)
			return
		}
		requests = append(requests, r.URL.Path+"?ids="+r.URL.Query().Get("ids"))
		nodes := []models.AspectNode{}
		for _, id := range strings.Split(r.URL.Query().Get("ids"), ",") {
			if n, ok := known[id]; ok {
				nodes = append(nodes, n)
			}
		}
		// the client reads the total of a list from this header
		w.Header().Set("X-Total-Count", strconv.Itoa(len(nodes)))
		_ = json.NewEncoder(w).Encode(nodes)
	})
	since := func(before int) []string {
		mux.Lock()
		defer mux.Unlock()
		return append([]string{}, requests[before:]...)
	}

	if nodes, err := repo.GetAspectNodes([]string{"urn:aspect:1", "urn:aspect:2"}); err != nil || len(nodes) != 2 {
		t.Fatalf("%+v %v", nodes, err)
	}
	if !reflect.DeepEqual(since(0), []string{"/v2/aspect-nodes?ids=urn:aspect:1,urn:aspect:2"}) {
		t.Fatalf("one request for several nodes: %v", since(0))
	}
	before := len(since(0))
	if _, err := repo.GetAspectNodes([]string{"urn:aspect:1", "urn:aspect:2"}); err != nil || len(since(before)) != 0 {
		t.Fatalf("a repeated read is cached: %v %v", since(before), err)
	}
	if nodes, err := repo.GetAspectNodes([]string{"urn:aspect:2", "urn:aspect:2"}); err != nil || len(nodes) != 1 ||
		!reflect.DeepEqual(since(before), []string{"/v2/aspect-nodes?ids=urn:aspect:2"}) {
		t.Fatalf("the same id twice is one aspect: %+v %v %v", nodes, since(before), err)
	}
	// a one element list and a single read of the same node are cached side by side
	_, _ = repo.GetAspectNodes([]string{"urn:aspect:3"})
	_, _ = repo.GetAspectNode("urn:aspect:3")
	before = len(since(0))
	_, _ = repo.GetAspectNodes([]string{"urn:aspect:3"})
	_, _ = repo.GetAspectNode("urn:aspect:3")
	if len(since(before)) != 0 {
		t.Fatalf("both reads are cached: %v", since(before))
	}
	if _, err := repo.GetAspectNodes([]string{"urn:aspect:1", "urn:aspect:unknown"}); err == nil || !strings.Contains(err.Error(), "urn:aspect:unknown") {
		t.Fatalf("unknown ids are reported: %v", err)
	}
	before = len(since(0))
	if nodes, err := repo.GetAspectNodes(nil); err != nil || len(nodes) != 0 || len(since(before)) != 0 {
		t.Fatalf("nothing is read without an aspect: %+v %v %v", nodes, since(before), err)
	}
}
