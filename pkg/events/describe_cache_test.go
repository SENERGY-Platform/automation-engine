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
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	devicerepository "github.com/SENERGY-Platform/device-repository/v3/lib/client"
	"github.com/SENERGY-Platform/models/go/models"
	"github.com/SENERGY-Platform/service-commons/pkg/signal"
)

// The describer reads device-repository through the cache of the client: a second deployment
// reads nothing again, and the cache invalidation signals clear what a change made stale.
func TestDescriberReadsThroughTheCacheOfTheClient(t *testing.T) {
	output := []models.Content{{ContentVariable: models.ContentVariable{Name: "value", FunctionId: measuring, AspectId: "air"}}}
	answers := map[string]any{
		"/devices/d1":      models.Device{Id: "d1", DeviceTypeId: "dt"},
		"/device-types/dt": models.DeviceType{Id: "dt", Services: []models.Service{{Id: "s1", Interaction: models.EVENT, Outputs: output}}},
	}
	var mux sync.Mutex
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.Lock()
		defer mux.Unlock()
		requests[r.URL.Path]++
		if r.URL.Path == "/v2/aspect-nodes" {
			nodes := []models.AspectNode{}
			for _, id := range strings.Split(r.URL.Query().Get("ids"), ",") {
				nodes = append(nodes, models.AspectNode{Id: id, RootId: id})
			}
			w.Header().Set("X-Total-Count", strconv.Itoa(len(nodes)))
			_ = json.NewEncoder(w).Encode(nodes)
			return
		}
		answer, ok := answers[r.URL.Path]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(answer)
	}))
	defer server.Close()
	client := devicerepository.NewClient(server.URL, devicerepository.Options{})
	defer devicerepository.InvalidateOnSignals(client, nil)()
	// the cache keys by the user of the token, so it has to name one
	describer := events.NewDescriber(client, time.Minute, auth.UnsignedTokens, nil)
	count := func() (devices, deviceTypes, aspectNodes int) {
		mux.Lock()
		defer mux.Unlock()
		return requests["/devices/d1"], requests["/device-types/dt"], requests["/v2/aspect-nodes"]
	}
	selection := model.Selection{DeviceId: "d1"}

	describe(t, describer, eventWith(`senergy:aspect="air"`), selection)
	describe(t, describer, eventWith(`senergy:aspect="air"`), selection)
	if devices, deviceTypes, aspectNodes := count(); devices != 1 || deviceTypes != 1 || aspectNodes != 1 {
		t.Errorf("a second description reads from the cache: %v %v %v requests", devices, deviceTypes, aspectNodes)
	}

	signal.Pub(signal.Known.DeviceCacheInvalidation, "d1")
	eventually(t, func() bool {
		describe(t, describer, eventWith(`senergy:aspect="air"`), selection)
		devices, _, _ := count()
		return devices == 2
	})
	// a list, like that of the aspect nodes, is cleared by every signal
	if _, deviceTypes, _ := count(); deviceTypes != 1 {
		t.Errorf("a changed device leaves its device type cached: %v requests", deviceTypes)
	}
}

// eventually retries until done holds, at most 5 s
func eventually(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal("not reached in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
