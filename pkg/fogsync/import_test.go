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

package fogsync_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/fogsync"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/store/storetest"
)

var operator = client.TestToken("ops", "admin")

// replaceCloud connects the gateway to a new cloud with an empty database, as after the move
// into the unicluster (SNRGY-5066); the first cloud is cut off. The returned flag switches the
// connection to the new cloud.
func (this *env) replaceCloud(connected bool) (*server.Server, *atomic.Bool) {
	this.t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	this.t.Cleanup(cancel)
	this.online.Store(false)
	online := &atomic.Bool{}
	online.Store(connected)
	broker := mqtt.NewMemory()
	// the same filter replaces the subscription of the bridge to the first cloud
	_ = broker.Subscribe("processes/"+network+"/cmd/#", 1, func(topic string, payload []byte) error {
		if online.Load() {
			_ = this.gatewayBroker.Publish(ctx, "processes/cmd/"+strings.TrimPrefix(topic, "processes/"+network+"/cmd/"), 1, payload)
		}
		return nil
	})
	_ = this.gatewayBroker.Subscribe("processes/state/#", 1, func(topic string, payload []byte) error {
		if online.Load() {
			_ = broker.Publish(ctx, "processes/"+network+"/state/"+strings.TrimPrefix(topic, "processes/state/"), 1, payload)
		}
		return nil
	})
	config := configuration.Default()
	config.SqlitePath = storetest.MemoryDsn()
	config.PollInterval = "20ms"
	config.SyncResendAfter = "300ms"
	config.SyncCallTimeout = "500ms"
	return this.startCloud(ctx, config, broker, slog.New(slog.NewTextHandler(io.Discard, nil))), online
}

// exportGateway reads the gateway as export-gateway does
func (this *env) exportGateway() model.GatewayExport {
	this.t.Helper()
	var result model.GatewayExport
	err := this.gateway.Store.Tx(ctx, func(tx *store.Tx) (err error) { result, err = tx.GatewayExport(); return err })
	if err != nil {
		this.t.Fatal(err)
	}
	result.Format, result.FormatVersion, result.ExportedAt, result.ExporterVersion = model.GatewayExportFormat, model.GatewayExportVersion, time.Now().UTC(), "test"
	return result
}

func variant(message string) string {
	return b.Process(b.Message("m", message), b.Start("start"), b.CatchMessage("wait", "m"), b.End("end"), b.Flow("start", "wait"), b.Flow("wait", "end"))
}

// runningInVersion3 deploys a model in three versions to the gateway, one deployment version
// per model version, and starts instances of the last one there
func (this *env) runningInVersion3(instanceIds ...string) model.Deployment {
	this.t.Helper()
	c := this.cloud.Controller
	m, err := c.CreateModel(ctx, alice, "processes", model.ModelInput{Name: "heating", Xml: variant("v1")})
	if err != nil {
		this.t.Fatal(err)
	}
	d, err := c.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{ModelId: m.Id, ModelVersion: 1, NetworkId: network})
	if err != nil {
		this.t.Fatal(err)
	}
	// the gateway keeps only the versions it applied; one that a later one overtakes is missing
	// there and in its export
	this.waitFor("version 1 on the gateway", func() bool { gd, err := deploymentOn(this.gateway, d.Id); return err == nil && gd.Version == 1 })
	for version := 2; version <= 3; version++ {
		xml := variant("v" + string(rune('0'+version)))
		if _, err = c.UpdateModel(ctx, alice, "processes", m.Id, model.ModelInput{Name: "heating", Xml: xml}); err != nil {
			this.t.Fatal(err)
		}
		if d, err = c.PutDeployment(ctx, alice, "processes", d.Id, model.DeploymentInput{ModelId: m.Id, ModelVersion: version, UpdateStrategy: model.UpdateDrain}); err != nil {
			this.t.Fatal(err)
		}
		this.waitFor("the version on the gateway", func() bool { gd, err := deploymentOn(this.gateway, d.Id); return err == nil && gd.Version == version })
	}
	if d.Version != 3 || d.ModelVersion != 3 {
		this.t.Fatalf("deployment in version 3 of model version 3: %+v", d)
	}
	for _, id := range instanceIds {
		if _, err = c.PutInstance(ctx, alice, "processes", id, model.StartInstanceInput{DeploymentId: d.Id}); err != nil {
			this.t.Fatal(err)
		}
		this.waitFor("the running instance on the gateway", func() bool {
			inst, err := instanceOn(this.gateway, id)
			return err == nil && inst.State == model.StateRunning
		})
	}
	return d
}

// contact makes the gateway report to the cloud before its next status: it completes an
// instance, and the cloud answers the batch with the position it wants to continue from
func (this *env) contact(instanceId string) {
	this.t.Helper()
	if _, err := this.gateway.Engine.Correlate(ctx, engine.CorrelationRequest{MessageInput: model.MessageInput{Name: "v3", InstanceId: instanceId}}); err != nil {
		this.t.Fatal(err)
	}
}

func desiredOn(srv *server.Server, kind string, id string) (result store.Desired, found bool) {
	_ = srv.Store.Tx(ctx, func(tx *store.Tx) (err error) { result, found, err = tx.GetDesired(network, kind, id); return err })
	return result, found
}

func networkOn(srv *server.Server) (result store.Network, found bool) {
	_ = srv.Store.Tx(ctx, func(tx *store.Tx) (err error) { result, found, err = tx.GetNetwork(network); return err })
	return result, found
}

// SNRGY-5066, acceptance: a deployment in version 3 moves with all versions and its model into
// an empty cloud, the gateway applies it without an update and its instance arrives
func TestImportTakesOverTheDeploymentsOfAGateway(t *testing.T) {
	e := newEnv(t)
	d := e.runningInVersion3("i-1", "i-2")
	doc := e.exportGateway()
	before, err := deploymentOn(e.gateway, d.Id)
	if err != nil {
		t.Fatal(err)
	}

	fresh, _ := e.replaceCloud(true)
	// the gateway reports to the new cloud, which drops the instance of the unknown deployment
	e.waitFor("the change stream taken by the new cloud", func() bool {
		n, found := networkOn(fresh)
		return found && n.Latest > 0 && n.Acked == n.Latest
	})
	if _, err = instanceOn(fresh, "i-1"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("the new cloud drops instances of unknown deployments: %v", err)
	}

	report, err := fresh.Controller.ImportNetwork(ctx, operator, network, doc, model.NetworkImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Owner != "alice" || !report.SnapshotRequested || len(report.Deployments) != 1 || report.Deployments[0].Status != model.ImportCreated || report.Deployments[0].Reason != "" {
		t.Fatalf("one deployment created for the owner of the hub: %+v", report)
	}
	if len(report.Models) != 1 || report.Models[0].Status != model.ImportCreated || len(report.Models[0].VersionsAdded) != 3 {
		t.Fatalf("the model with its three versions: %+v", report.Models)
	}
	imported, err := deploymentOn(fresh, d.Id)
	if err != nil || imported.Version != 3 || imported.NetworkId != network || imported.Owner != "alice" || imported.ModelId != d.ModelId || imported.ModelVersion != 3 {
		t.Fatalf("the deployment with id and version of the gateway: %+v %v", imported, err)
	}
	_ = fresh.Store.Tx(ctx, func(tx *store.Tx) error {
		versions, err := tx.ListDeploymentVersions(d.Id, model.PageOptions{})
		if err != nil || versions.Total != 3 {
			t.Errorf("all versions of the deployment: %+v %v", versions, err)
		}
		m, err := tx.GetModel(d.ModelId, false)
		if err != nil || m.Version != 3 || m.Owner != "alice" || m.Namespace != "processes" || m.Published {
			t.Errorf("the model of the deployment: %+v %v", m, err)
		}
		modelVersions, err := tx.ListModelVersions(d.ModelId, model.PageOptions{})
		if err != nil || modelVersions.Total != 3 {
			t.Errorf("a model version per deployment version: %+v %v", modelVersions, err)
		}
		v2, err := tx.GetModelVersion(d.ModelId, 2)
		if err != nil || !strings.Contains(v2.Xml, "v2") {
			t.Errorf("the model version carries the XML of its deployment version: %v", err)
		}
		return nil
	})
	if _, found := desiredOn(fresh, fogsync.ResourceDeployment, d.Id); !found {
		t.Fatal("the deployment is desired on the gateway")
	}

	e.waitFor("the deployment applied by the gateway", func() bool {
		desired, found := desiredOn(fresh, fogsync.ResourceDeployment, d.Id)
		return found && desired.AppliedVersion == desired.Version && desired.Error == ""
	})
	after, err := deploymentOn(e.gateway, d.Id)
	if err != nil || after.Version != 3 || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("the gateway keeps its deployment without an update: %+v %v", after, err)
	}
	// i-1 does not change; it only arrives because the gateway sends everything again
	e.contact("i-2")
	e.waitFor("the instances in the new cloud", func() bool {
		inst, err := instanceOn(fresh, "i-1")
		ended, endedErr := instanceOn(fresh, "i-2")
		return err == nil && inst.State == model.StateRunning && inst.DeploymentVersion == 3 && inst.Owner == "alice" &&
			endedErr == nil && ended.State == model.StateCompleted
	})

	again, err := fresh.Controller.ImportNetwork(ctx, operator, network, doc, model.NetworkImportOptions{})
	if err != nil || len(again.Deployments) != 1 || again.Deployments[0].Status != model.ImportExists || len(again.Models) != 0 || again.SnapshotRequested {
		t.Errorf("a second import creates nothing: %+v %v", again, err)
	}
}

func TestImportBeforeTheFirstContactCreatesTheNetworkOnlyOnRequest(t *testing.T) {
	e := newEnv(t)
	d := e.runningInVersion3("i-1", "i-2")
	doc := e.exportGateway()
	fresh, online := e.replaceCloud(false)

	dry, err := fresh.Controller.ImportNetwork(ctx, operator, network, doc, model.NetworkImportOptions{DryRun: true, CreateNetwork: true})
	if err != nil || !dry.DryRun || dry.Deployments[0].Status != model.ImportCreated {
		t.Fatalf("a dry run reports: %+v %v", dry, err)
	}
	if _, err = deploymentOn(fresh, d.Id); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("a dry run writes nothing: %v", err)
	}
	if _, found := networkOn(fresh); found {
		t.Errorf("a dry run creates no network")
	}

	if _, err = fresh.Controller.ImportNetwork(ctx, operator, network, doc, model.NetworkImportOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, found := networkOn(fresh); found {
		t.Errorf("without create_network the first contact creates the network")
	}
	report, err := fresh.Controller.ImportNetwork(ctx, operator, network, doc, model.NetworkImportOptions{CreateNetwork: true})
	if err != nil || report.Deployments[0].Status != model.ImportExists {
		t.Fatalf("%+v %v", report, err)
	}
	n, found := networkOn(fresh)
	if !found || n.Acked != 0 || time.Since(n.LastContact) > time.Minute {
		t.Errorf("create_network creates the network with the import as its last contact, so that the cleanup keeps it: %+v", n)
	}

	online.Store(true)
	e.contact("i-2")
	e.waitFor("the instance in the new cloud", func() bool {
		inst, err := instanceOn(fresh, "i-1")
		return err == nil && inst.State == model.StateRunning
	})
}

// exportOf is an export with one deployment in version 1
func exportOf(d model.Deployment) model.GatewayExport {
	d.Version = 1
	return model.GatewayExport{Format: model.GatewayExportFormat, FormatVersion: model.GatewayExportVersion, Deployments: []model.ExportedDeployment{{
		Deployment: d,
		Versions:   []model.DeploymentVersion{{DeploymentId: d.Id, Version: 1, ModelVersion: d.ModelVersion, Xml: waitProcess(), CreatedAt: time.Now().UTC()}},
	}}}
}

func TestImportChecksCallerHubAndFormat(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	doc := exportOf(model.Deployment{Id: "d-1", Namespace: "processes", Name: "x", Owner: "alice"})
	if _, err := c.ImportNetwork(ctx, alice, network, doc, model.NetworkImportOptions{}); !errors.Is(err, model.ErrForbidden) {
		t.Errorf("admins only: %v", err)
	}
	if _, err := c.ImportNetwork(ctx, operator, "hub-unknown", doc, model.NetworkImportOptions{}); !errors.Is(err, model.ErrUnprocessable) || model.AsError(err).Kind.HttpStatus() != 422 {
		t.Errorf("a hub device-repository does not know is 422: %v", err)
	}
	wrong := doc
	wrong.FormatVersion = 2
	if _, err := c.ImportNetwork(ctx, operator, network, wrong, model.NetworkImportOptions{}); !errors.Is(err, model.ErrBadRequest) {
		t.Errorf("an unknown format version is 400: %v", err)
	}
	if _, err := deploymentOn(e.cloud, "d-1"); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("nothing written: %v", err)
	}
}

func TestImportSkipsNamespacesTheCloudDoesNotRunOnGatewaysAndTakesTheOwnerOfTheHub(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	doc := exportOf(model.Deployment{Id: "d-1", Namespace: "processes", Name: "x", Owner: "bob"})
	doc.Deployments[0].EventDescriptions = []model.EventDescription{{Id: "e-1", DeploymentId: "d-1", Version: 1, ElementId: "wait", Namespace: "processes", Owner: "bob",
		MessageName: "d-1_wait", DeviceGroupId: "g-1", Topic: "t", Script: "x", ValueVariable: "v"}}
	doc.Deployments[0].DeviceGroups = []string{"g-1"}
	doc.Deployments = append(doc.Deployments, exportOf(model.Deployment{Id: "d-2", Namespace: "smart-services", Name: "y", Owner: "alice"}).Deployments[0],
		exportOf(model.Deployment{Id: "d-3", Namespace: "unknown", Name: "z", Owner: "alice"}).Deployments[0])
	report, err := c.ImportNetwork(ctx, operator, network, doc, model.NetworkImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]model.ImportedDeployment{}
	for _, d := range report.Deployments {
		got[d.Id] = d
	}
	if got["d-1"].Status != model.ImportCreated || !strings.Contains(got["d-1"].Reason, "bob") {
		t.Errorf("a deployment of another owner is created for the owner of the hub and named: %+v", got["d-1"])
	}
	if d, err := deploymentOn(e.cloud, "d-1"); err != nil || d.Owner != "alice" {
		t.Errorf("owner of the hub: %+v %v", d, err)
	}
	_ = e.cloud.Store.Tx(ctx, func(tx *store.Tx) error {
		descriptions, err := tx.ListEventDescriptions("d-1")
		if err != nil || len(descriptions) != 1 || descriptions[0].Owner != "alice" || descriptions[0].DeviceGroupId != "g-1" {
			t.Errorf("event descriptions with the owner of the hub: %+v %v", descriptions, err)
		}
		groups, err := tx.DeviceGroupsOfDeployment("d-1")
		if err != nil || len(groups) != 1 {
			t.Errorf("device groups: %v %v", groups, err)
		}
		return nil
	})
	if got["d-2"].Status != model.ImportSkipped || got["d-3"].Status != model.ImportSkipped || got["d-2"].Reason == "" || got["d-3"].Reason == "" {
		t.Errorf("cloud-only and unknown namespaces are skipped with a reason: %+v", report.Deployments)
	}
}

// a gateway never learns that a model was deleted; whatever an export says, the model is created
func TestImportCreatesTheModelOfADeploymentMarkedAsDeleted(t *testing.T) {
	e := newEnv(t)
	doc := exportOf(model.Deployment{Id: "d-1", Namespace: "processes", Name: "x", Owner: "alice", ModelId: "m-1", ModelVersion: 1, ModelDeleted: true})
	report, err := e.cloud.Controller.ImportNetwork(ctx, operator, network, doc, model.NetworkImportOptions{})
	if err != nil || len(report.Models) != 1 || report.Models[0].Status != model.ImportCreated {
		t.Fatalf("the model is created: %+v %v", report, err)
	}
	if d, err := deploymentOn(e.cloud, "d-1"); err != nil || !d.ModelDeleted {
		t.Errorf("the deployment keeps model_deleted as exported: %+v %v", d, err)
	}
}

func TestImportAddsMissingVersionsToAnExistingModel(t *testing.T) {
	e := newEnv(t)
	c := e.cloud.Controller
	m, err := c.CreateModel(ctx, alice, "processes", model.ModelInput{Name: "heating", Xml: variant("v1")})
	if err != nil {
		t.Fatal(err)
	}
	doc := exportOf(model.Deployment{Id: "d-1", Namespace: "processes", Name: "x", Owner: "alice", ModelId: m.Id, ModelVersion: 2})
	entry := &doc.Deployments[0]
	entry.Deployment.Version = 3
	entry.Versions = []model.DeploymentVersion{
		{Version: 3, ModelVersion: 2, Xml: variant("late"), CreatedAt: time.Now().UTC()},
		{Version: 1, ModelVersion: 1, Xml: variant("other v1"), CreatedAt: time.Now().UTC()},
		{Version: 2, ModelVersion: 2, Xml: variant("v2"), CreatedAt: time.Now().UTC()},
	}
	report, err := c.ImportNetwork(ctx, operator, network, doc, model.NetworkImportOptions{})
	if err != nil || len(report.Models) != 1 || report.Models[0].Status != model.ImportExtended || len(report.Models[0].VersionsAdded) != 1 || report.Models[0].VersionsAdded[0] != 2 {
		t.Fatalf("only the missing model version is added: %+v %v", report, err)
	}
	_ = e.cloud.Store.Tx(ctx, func(tx *store.Tx) error {
		got, err := tx.GetModel(m.Id, false)
		if err != nil || got.Version != 2 {
			t.Errorf("the model version is the highest: %+v %v", got, err)
		}
		v1, _ := tx.GetModelVersion(m.Id, 1)
		v2, _ := tx.GetModelVersion(m.Id, 2)
		if !strings.Contains(v1.Xml, `"v1"`) || !strings.Contains(v2.Xml, `"v2"`) {
			t.Errorf("an existing version stays, a new one comes from the first deployment version that names it: %q %q", v1.Xml, v2.Xml)
		}
		return nil
	})
}

func TestImportOverHttpRejectsUnreadableOptions(t *testing.T) {
	e := newEnv(t)
	body, err := json.Marshal(exportOf(model.Deployment{Id: "d-1", Namespace: "processes", Name: "x", Owner: "alice"}))
	if err != nil {
		t.Fatal(err)
	}
	put := func(query string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPut, "/networks/"+network+"/import"+query, bytes.NewReader(body))
		r.Header.Set("Authorization", operator)
		w := httptest.NewRecorder()
		e.cloud.Handler.ServeHTTP(w, r)
		return w
	}
	if w := put("?dry_run=1"); w.Code != http.StatusOK {
		t.Errorf("1 is true: %v %v", w.Code, w.Body.String())
	}
	if w := put("?dry_run=yes"); w.Code != http.StatusBadRequest {
		t.Errorf("an unreadable value is rejected rather than taken as false: %v %v", w.Code, w.Body.String())
	}
	if _, err = deploymentOn(e.cloud, "d-1"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("nothing written: %v", err)
	}
	w := put("?create_network=true")
	var report model.NetworkImport
	if err = json.Unmarshal(w.Body.Bytes(), &report); err != nil || w.Code != http.StatusOK || report.DryRun || report.Deployments[0].Status != model.ImportCreated {
		t.Errorf("the import: %v %v", w.Code, w.Body.String())
	}
}
