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

package modules_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
)

// repository is a fake flow-repo or import-repository: it answers with the resource as the
// test sets it, and only to the users the test lets read it
type repository struct {
	mux       sync.Mutex
	resources map[string]map[string]any
	readers   []string
}

func (this *repository) set(id string, resource map[string]any) {
	this.mux.Lock()
	defer this.mux.Unlock()
	if resource == nil {
		delete(this.resources, id)
	} else {
		this.resources[id] = resource
	}
}

func (this *repository) allow(users ...string) {
	this.mux.Lock()
	defer this.mux.Unlock()
	this.readers = users
}

func (this *repository) server(t *testing.T, prefix string) string {
	this.resources = map[string]map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		this.mux.Lock()
		defer this.mux.Unlock()
		if !slices.Contains(this.readers, subject(r.Header.Get("Authorization"))) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		resource, ok := this.resources[strings.TrimPrefix(r.URL.Path, prefix)]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(resource)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// subject reads the user of an unsigned test token
func subject(token string) string {
	parts := strings.Split(strings.TrimPrefix(token, "Bearer "), ".")
	if len(parts) != 3 {
		return ""
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Sub string `json:"sub"`
	}
	_ = json.Unmarshal(raw, &claims)
	return claims.Sub
}

// resourceServer runs the engine with the module services a smart service with process
// deployment, analytics and import tasks needs, and with fake flow and import repositories
func resourceServer(t *testing.T) (*server.Server, *repository, *repository) {
	flows, imports := &repository{}, &repository{}
	flowRepo, importRepo := flows.server(t, "/flow/"), imports.server(t, "/import-types/")
	srv := newServer(t, func(c *configuration.Config) {
		c.FlowEngineUrl, c.FlowParserUrl, c.DeviceRepositoryUrl, c.ImportDeployUrl = flowRepo, flowRepo, flowRepo, importRepo
		c.FlowRepoUrl, c.ImportRepositoryUrl = flowRepo, importRepo
		c.ResourceCheckCycle = "off"
	})
	return srv, flows, imports
}

func smartServiceUsing(modelId string) string {
	return b.Process(
		b.Start("start"),
		b.Service("deploy", "process_deployment", b.InText("process_deployment.process_model_id", modelId)),
		b.Service("analyse", "analytics", b.InText("analytics.flow_id", "flow-1")),
		b.Service("load", "import", b.InText("import.request", `{"name":"{{.room}}","import_type_id":"urn:type"}`)),
		b.Service("dynamic", "analytics", b.InText("analytics.flow_id", "{{.flow}}")),
		b.End("end"),
		b.Flow("start", "deploy"), b.Flow("deploy", "analyse"), b.Flow("analyse", "load"), b.Flow("load", "dynamic"), b.Flow("dynamic", "end"),
	)
}

func processModel(t *testing.T, srv *server.Server) model.Model {
	t.Helper()
	m, err := srv.Controller.CreateModel(ctx, alice, "processes", model.ModelInput{Xml: b.Process(b.Start("start"), b.End("end"), b.Flow("start", "end"))})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// A version names the resources its module tasks use as literal text; an expression is not
// known before the task runs. Repositories ask how many versions and instances use one, as they
// asked smart-service-repository before a delete (SMS-15).
func TestResourceUsageCountsVersionsAndInstancesOfAllUsers(t *testing.T) {
	srv, _, _ := resourceServer(t)
	c := srv.Controller
	m := processModel(t, srv)
	d, err := c.CreateDeployment(ctx, alice, "smart-services", model.DeploymentInput{Name: "kitchen", Xml: smartServiceUsing(m.Id)})
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.GetDeploymentVersion(ctx, alice, "smart-services", d.Id, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := model.UsedResources{ProcessModels: []string{m.Id}, Flows: []string{"flow-1"}, ImportTypes: []string{"urn:type"}}
	if v.UsedResources == nil || !slices.Equal(v.UsedResources.ProcessModels, want.ProcessModels) ||
		!slices.Equal(v.UsedResources.Flows, want.Flows) || !slices.Equal(v.UsedResources.ImportTypes, want.ImportTypes) {
		t.Fatalf("the version names its literal resources: %+v", v.UsedResources)
	}

	usage, err := c.ResourceUsage(ctx, alice, "smart-services", model.ResourceKindFlows, "flow-1")
	if err != nil {
		t.Fatal(err)
	}
	if usage.Versions != 1 || usage.Instances != 0 || len(usage.Readable) != 1 || usage.Readable[0].Id != d.Id || !usage.Readable[0].Current {
		t.Fatalf("the current version uses the flow: %+v", usage)
	}
	bob := client.TestToken("bob")
	usage, err = c.ResourceUsage(ctx, bob, "smart-services", model.ResourceKindFlows, "flow-1")
	if err != nil {
		t.Fatal(err)
	}
	if usage.Versions != 1 || len(usage.Readable) != 0 {
		t.Fatalf("others learn the count, not the deployment: %+v", usage)
	}
	if _, err = c.ResourceUsage(ctx, alice, "smart-services", "pipelines", "p1"); !errors.Is(err, model.ErrBadRequest) {
		t.Fatalf("an unknown kind is a bad request: %v", err)
	}
	if usage, _ = c.ResourceUsage(ctx, alice, "smart-services", model.ResourceKindFlows, "{{.flow}}"); usage.Versions != 0 {
		t.Fatalf("an expression is no resource: %+v", usage)
	}
}

// The old version stays counted while an instance runs on it; once none does, only the
// current version counts.
func TestResourceUsageFollowsInstancesOnOldVersions(t *testing.T) {
	srv, _, _ := resourceServer(t)
	c := srv.Controller
	m := processModel(t, srv)
	d, err := c.CreateDeployment(ctx, alice, "smart-services", model.DeploymentInput{Name: "kitchen", Xml: b.Process(
		b.Start("start"),
		b.Service("deploy", "process_deployment", b.InText("process_deployment.process_model_id", m.Id)),
		b.Message("m", "never"), b.CatchMessage("wait", "m"), b.End("end"),
		b.Flow("start", "wait"), b.Flow("wait", "deploy"), b.Flow("deploy", "end"),
	)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.StartInstance(ctx, alice, "smart-services", model.StartInstanceInput{DeploymentId: d.Id}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.PutDeployment(ctx, alice, "smart-services", d.Id, model.DeploymentInput{Name: "kitchen", UpdateStrategy: model.UpdateDrain, Xml: b.Process(b.Start("start"), b.End("end"), b.Flow("start", "end"))}); err != nil {
		t.Fatal(err)
	}
	usage, err := c.ResourceUsage(ctx, alice, "smart-services", model.ResourceKindProcessModels, m.Id)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Versions != 1 || usage.Instances != 1 || len(usage.Readable) != 1 || usage.Readable[0].Version != 1 || usage.Readable[0].Current {
		t.Fatalf("the old version with an instance uses the model: %+v", usage)
	}
	count, err := c.DeploymentInstanceCount(ctx, alice, "smart-services", d.Id, 1)
	if err != nil || count.Count != 1 {
		t.Fatalf("one instance runs on version 1: %v %v", count, err)
	}
	if count, err = c.DeploymentInstanceCount(ctx, alice, "smart-services", d.Id, 0); err != nil || count.Count != 0 {
		t.Fatalf("none on the current version: %v %v", count, err)
	}
	if _, err = c.DeploymentInstanceCount(ctx, client.TestToken("bob"), "smart-services", d.Id, 0); err == nil {
		t.Fatal("only who may administrate the deployment counts its instances")
	}
}

// A model that deployments of smart services deploy is not deleted by accident: a delete
// names the users and fails; an admin may force it.
func TestModelInUseIsDeletedOnlyByForce(t *testing.T) {
	srv, _, _ := resourceServer(t)
	c := srv.Controller
	m := processModel(t, srv)
	if _, err := c.CreateDeployment(ctx, alice, "smart-services", model.DeploymentInput{Name: "kitchen", Xml: smartServiceUsing(m.Id)}); err != nil {
		t.Fatal(err)
	}
	err := c.DeleteModel(ctx, alice, "processes", m.Id, false)
	if !errors.Is(err, model.ErrConflict) || !strings.Contains(err.Error(), "kitchen") {
		t.Fatalf("a model in use is not deleted: %v", err)
	}
	if err = c.DeleteModel(ctx, alice, "processes", m.Id, true); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("only admins force: %v", err)
	}
	if err = c.DeleteModel(ctx, client.TestToken("ops", "admin"), "processes", m.Id, true); err != nil {
		t.Fatalf("an admin forces the delete: %v", err)
	}
	unused := processModel(t, srv)
	if err = c.DeleteModel(ctx, alice, "processes", unused.Id, false); err != nil {
		t.Fatalf("an unused model is deleted: %v", err)
	}
}

// changes runs the check and returns the marks of the first version; an unreachable or
// unreadable resource is an error of the check, which fails the test unless failing is expected
func changes(t *testing.T, srv *server.Server, deploymentId string, failing ...bool) map[string]model.ChangedResource {
	t.Helper()
	if err := srv.Modules.CheckResources(ctx); (err != nil) != (len(failing) > 0 && failing[0]) {
		t.Fatalf("check error: %v", err)
	}
	v, err := srv.Controller.GetDeploymentVersion(ctx, alice, "smart-services", deploymentId, 1)
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]model.ChangedResource{}
	for _, c := range v.ChangedResources {
		result[c.Kind+"|"+c.Id] = c
	}
	return result
}

// The check marks a version whose process model, flow or import type was edited or deleted
// after it was deployed, as smart-service-repository marks its releases (SMS-15). A resource
// the owner may no longer read is asked for with the token of the engine.
func TestResourceCheckMarksChangedAndDeletedResources(t *testing.T) {
	srv, flows, imports := resourceServer(t)
	c := srv.Controller
	m := processModel(t, srv)
	flows.allow("alice", "automation-engine")
	imports.allow("alice", "automation-engine")
	flows.set("flow-1", map[string]any{"_id": "flow-1", "dateUpdated": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)})
	importType := map[string]any{"id": "urn:type", "name": "weather", "owner": "alice", "output": map[string]any{"name": "value", "type": "string"}}
	imports.set("urn:type", importType)
	d, err := c.CreateDeployment(ctx, alice, "smart-services", model.DeploymentInput{Name: "kitchen", Xml: smartServiceUsing(m.Id)})
	if err != nil {
		t.Fatal(err)
	}
	if found := changes(t, srv, d.Id); len(found) != 0 {
		t.Fatalf("nothing changed since the deployment: %v", found)
	}

	time.Sleep(5 * time.Millisecond)
	if _, err = c.UpdateModel(ctx, alice, "processes", m.Id, model.ModelInput{Name: "renamed", Xml: b.Process(b.Start("start"), b.End("end"), b.Flow("start", "end"))}); err != nil {
		t.Fatal(err)
	}
	updated := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	flows.set("flow-1", map[string]any{"_id": "flow-1", "dateUpdated": updated.Format(time.RFC3339)})
	imports.set("urn:type", map[string]any{"id": "urn:type", "name": "weather", "owner": "bob", "unknown": 1, "output": map[string]any{"name": "value", "type": "string"}})
	found := changes(t, srv, d.Id)
	if found["process_model|"+m.Id].Reason != "changed" || found["flow|flow-1"].Reason != "changed" || found["flow|flow-1"].At != updated.Unix() {
		t.Fatalf("the edited model and flow are marked: %v", found)
	}
	if _, marked := found["import_type|urn:type"]; marked {
		t.Fatalf("a new owner or an unknown field changes nothing an import does: %v", found)
	}

	imports.set("urn:type", map[string]any{"id": "urn:type", "name": "weather", "output": map[string]any{"name": "temperature", "type": "float"}})
	found = changes(t, srv, d.Id)
	first := found["import_type|urn:type"]
	if first.Reason != "changed" {
		t.Fatalf("a changed output marks the import type: %v", found)
	}
	time.Sleep(1100 * time.Millisecond)
	if again := changes(t, srv, d.Id)["import_type|urn:type"]; again != first {
		t.Fatalf("a mark keeps the time it was first seen: %v %v", first, again)
	}

	// the owner lost the right with the delete; the engine learns that it is gone
	flows.set("flow-1", nil)
	flows.allow("automation-engine")
	imports.allow()
	found = changes(t, srv, d.Id, true)
	if found["flow|flow-1"].Reason != "gone" {
		t.Fatalf("a deleted flow is marked gone: %v", found)
	}
	if found["import_type|urn:type"] != first {
		t.Fatalf("an unreadable import type keeps its earlier mark: %v", found)
	}
	if err = c.DeleteModel(ctx, client.TestToken("ops", "admin"), "processes", m.Id, true); err != nil {
		t.Fatal(err)
	}
	if found = changes(t, srv, d.Id, true); found["process_model|"+m.Id].Reason != "gone" {
		t.Fatalf("a deleted model is marked gone: %v", found)
	}
}
