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

// Package integration runs the usual scenarios of users end to end: a server with its
// background jobs, reached only over HTTP, as the web-ui and other services reach it. The
// tests of the other packages check single features; these check that the features work
// together in the order a user needs them.
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
)

var ctx = context.Background()
var alice = client.TestToken("alice")
var bob = client.TestToken("bob")

const (
	processes     = "processes"
	smartServices = "smart-services"
)

type env struct {
	t *testing.T
	// the HTTP client of the new API
	c   client.Client
	url string
	// only to run a background job at once instead of waiting for its next pass
	srv *server.Server
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	srv, err := client.NewTestServer(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(srv.Handler)
	t.Cleanup(httpServer.Close)
	return &env{t: t, c: client.New(httpServer.URL), url: httpServer.URL, srv: srv}
}

// call sends a request to a route of the deprecated layer and decodes the answer into result
func (this *env) call(token string, method string, path string, body any, result any) int {
	this.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, this.url+"/deprecated"+path, reader)
	req.Header.Set("Authorization", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		this.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if result != nil && resp.StatusCode < 300 {
		if err = json.Unmarshal(raw, result); err != nil {
			this.t.Fatalf("%v %v: %v: %s", method, path, err, raw)
		}
	}
	return resp.StatusCode
}

func (this *env) waitFor(what string, check func() bool) {
	this.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			this.t.Fatalf("timeout waiting for %v", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// instance waits until the instance is in the state and returns it
func (this *env) instance(namespace string, id string, state model.InstanceState) model.Instance {
	this.t.Helper()
	var inst model.Instance
	this.waitFor("instance "+id+" "+string(state), func() bool {
		var err error
		inst, err = this.c.GetInstance(ctx, alice, namespace, id)
		return err == nil && inst.State == state
	})
	return inst
}

// ready waits until a smart-service instance has set up its modules
func (this *env) ready(id string) model.Instance {
	this.t.Helper()
	var inst model.Instance
	this.waitFor("ready smart service "+id, func() bool {
		var err error
		inst, err = this.c.GetInstance(ctx, alice, smartServices, id)
		return err == nil && inst.Status == model.StatusReady
	})
	return inst
}

func must[T any](t *testing.T) func(T, error) T {
	return func(value T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
}

func variable(vars []model.Variable, name string) any {
	for _, v := range vars {
		if v.Name == name {
			return v.Value
		}
	}
	return nil
}
