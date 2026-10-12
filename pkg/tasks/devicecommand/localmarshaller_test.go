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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/models/go/models"
)

// mirror serves the concepts, functions and characteristics of the device-repository mirror
// of a gateway and counts the requests
type mirror struct {
	mux      sync.Mutex
	requests map[string]int
}

func (this *mirror) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	this.mux.Lock()
	this.requests[r.Method+" "+r.URL.Path]++
	this.mux.Unlock()
	celsius := models.Characteristic{Id: "celsius", Name: "celsius", Type: models.Float}
	list := func(items any, n int) {
		w.Header().Set("X-Total-Count", strconv.Itoa(n))
		_ = json.NewEncoder(w).Encode(items)
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v2/concepts":
		list([]models.Concept{{Id: "temperature", Name: "temperature", CharacteristicIds: []string{"celsius"}, BaseCharacteristicId: "celsius"}}, 1)
	case r.Method == http.MethodPost && r.URL.Path == "/query/functions":
		list([]models.Function{{Id: measuring, Name: "temperature", ConceptId: "temperature"}}, 1)
	case r.URL.Path == "/characteristics/celsius":
		_ = json.NewEncoder(w).Encode(celsius)
	case r.URL.Path == "/concepts/temperature":
		_ = json.NewEncoder(w).Encode(models.Concept{Id: "temperature", Name: "temperature", CharacteristicIds: []string{"celsius"}, BaseCharacteristicId: "celsius"})
	case r.URL.Path == "/functions/"+measuring:
		_ = json.NewEncoder(w).Encode(models.Function{Id: measuring, Name: "temperature", ConceptId: "temperature"})
	default:
		http.Error(w, "unknown "+r.URL.Path, http.StatusNotFound)
	}
}

func temperatureService(output bool) (models.Service, models.Protocol) {
	protocol := models.Protocol{Id: "p", Handler: "connector", ProtocolSegments: []models.ProtocolSegment{{Id: "seg", Name: "data"}}}
	content := models.Content{ProtocolSegmentId: "seg", Serialization: models.JSON, ContentVariable: models.ContentVariable{
		Name: "temperature", Type: models.Float, CharacteristicId: "celsius", FunctionId: measuring,
	}}
	service := models.Service{Id: "s", Interaction: models.REQUEST}
	if output {
		service.Outputs = []models.Content{content}
	} else {
		service.Inputs = []models.Content{content}
	}
	return service, protocol
}

// Commands before payload version 3 are marshalled with all concepts of the mirror, which are
// read once and then held; later commands use marshaller v2 on demand (FOG-01).
func TestLocalMarshallerReadsTheMirror(t *testing.T) {
	fake := &mirror{requests: map[string]int{}}
	server := httptest.NewServer(fake)
	defer server.Close()
	m, err := NewLocalMarshaller(ctx, server.URL, time.Minute, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	service, protocol := temperatureService(false)
	for range 2 {
		message, err := m.MarshalFromServiceAndProtocol("celsius", service, protocol, 21.5, nil)
		if err != nil || strings.TrimSpace(message["data"]) != "21.5" {
			t.Fatalf("v1 writes the value into its segment: %v %v", message, err)
		}
	}
	if fake.requests["GET /v2/concepts"] != 1 || fake.requests["POST /query/functions"] != 1 {
		t.Errorf("the concepts are read once and held: %v", fake.requests)
	}
	output, _ := temperatureService(true)
	value, err := m.UnmarshalFromServiceAndProtocol("celsius", output, protocol, map[string]string{"data": "21.5"}, nil)
	if err != nil || value != 21.5 {
		t.Errorf("v1 reads the value of its characteristic: %v %v", value, err)
	}
	value, err = m.UnmarshalV2(UnmarshallingV2Request{Service: output, Protocol: protocol, CharacteristicId: "celsius", FunctionId: measuring, Message: map[string]string{"data": "22.5"}})
	if err != nil || value != 22.5 {
		t.Errorf("v2 finds the path by the function: %v %v", value, err)
	}
	message, err := m.MarshalV2(service, protocol, []MarshallingV2RequestData{{Value: 23.5, CharacteristicId: "celsius", FunctionId: measuring}})
	if err != nil || strings.TrimSpace(message["data"]) != "23.5" {
		t.Errorf("v2 writes the value: %v %v", message, err)
	}
}
