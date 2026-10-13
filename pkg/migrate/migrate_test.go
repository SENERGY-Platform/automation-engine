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

package migrate_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/migrate"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/models/go/models"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
)

var ctx = context.Background()
var alice = client.TestToken("alice")

// sources are the old services as test data
type sources struct {
	migrate.PermissionsV2
	models      []migrate.ProcessModel
	deployments []migrate.ProcessDeployment
	camunda     []migrate.CamundaDeployment
	running     map[string][]migrate.CamundaInstance
	schedules   []migrate.Schedule
	io          []migrate.IoValue
	designs     []migrate.Design
	releases    []migrate.Release
	instances   []migrate.SmartServiceInstance
	modules     []migrate.Module
	variables   []migrate.InstanceVariable
	fog         []migrate.FogDeployment
	fogStarts   []migrate.FogStart
}

func (this *sources) Designs(context.Context) ([]migrate.Design, error)   { return this.designs, nil }
func (this *sources) Releases(context.Context) ([]migrate.Release, error) { return this.releases, nil }
func (this *sources) SmartServiceInstances(context.Context) ([]migrate.SmartServiceInstance, error) {
	return this.instances, nil
}
func (this *sources) Modules(context.Context) ([]migrate.Module, error) { return this.modules, nil }
func (this *sources) SmartServiceVariables(context.Context) ([]migrate.InstanceVariable, error) {
	return this.variables, nil
}
func (this *sources) FogDeployments(context.Context) ([]migrate.FogDeployment, error) {
	return this.fog, nil
}
func (this *sources) FogStarts(context.Context) ([]migrate.FogStart, error) {
	return this.fogStarts, nil
}

func (this *sources) ProcessModels(context.Context) ([]migrate.ProcessModel, error) {
	return this.models, nil
}
func (this *sources) ProcessDeployments(context.Context) ([]migrate.ProcessDeployment, error) {
	return this.deployments, nil
}
func (this *sources) CamundaDeployments(context.Context) ([]migrate.CamundaDeployment, error) {
	return this.camunda, nil
}
func (this *sources) RunningInstances(_ context.Context, d migrate.CamundaDeployment) ([]migrate.CamundaInstance, error) {
	return this.running[d.Id], nil
}
func (this *sources) Schedules(context.Context) ([]migrate.Schedule, error) {
	return this.schedules, nil
}
func (this *sources) IoValues(context.Context) ([]migrate.IoValue, error) { return this.io, nil }

type env struct {
	t      *testing.T
	srv    *server.Server
	target *migrate.Target
	rights migrate.PermissionsV2
}

func newEnv(t *testing.T) *env {
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	var deps server.Dependencies
	srv, err := client.NewTestServer(ctx, func(_ *configuration.Config, d *server.Dependencies) {
		// deployments to gateways need the synchronisation; no gateway answers here
		d.SyncBroker = mqtt.NewMemory()
		deps = *d
	})
	if err != nil {
		t.Fatal(err)
	}
	admin := auth.UnsignedToken("migration", "admin")
	rights := migrate.PermissionsV2{Client: deps.Permissions, Token: func() (string, error) { return admin, nil }}
	if _, err, _ = deps.Permissions.SetTopic(admin, permmodel.Topic{Id: "processmodel"}); err != nil {
		t.Fatal(err)
	}
	return &env{t: t, srv: srv, rights: rights,
		target: &migrate.Target{Store: srv.Store, Engine: srv.Engine, Controller: srv.Controller, Namespaces: srv.Namespaces, AdminToken: func() (string, error) { return admin, nil }, Rights: rights}}
}

// designerModel is BPMN of the old designer: a start form, a JUEL timer and a wait
func designerModel() string {
	return b.Process(
		b.Message("m", "go"),
		b.StartWithForm("start", "level=5"),
		`<bpmn:intermediateCatchEvent id="wait"><bpmn:timerEventDefinition><bpmn:timeDuration xsi:type="bpmn:tFormalExpression">PT${level}M</bpmn:timeDuration></bpmn:timerEventDefinition></bpmn:intermediateCatchEvent>`,
		b.CatchMessage("msg", "m"),
		b.End("end"),
		b.Flow("start", "wait"), b.Flow("wait", "msg"), b.Flow("msg", "end"),
	)
}

func mailModel() string {
	return b.Process(b.Start("start"), `<bpmn:serviceTask id="mail"><bpmn:extensionElements><camunda:connector><camunda:connectorId>mail-send</camunda:connectorId></camunda:connector></bpmn:extensionElements></bpmn:serviceTask>`,
		b.End("end"), b.Flow("start", "mail"), b.Flow("mail", "end"))
}

const d1 = "4a7e3b0c-1d2e-4f56-8a9b-0c1d2e3f4a5b"
const d2 = "5b8f4c1d-2e3f-4a67-9bac-1d2e3f4a5b6c"
const d3 = "6c9a5d2e-3f4a-4b78-8cbd-2e3f4a5b6c7d"

func key(vid string) string {
	return "deplid_" + strings.ReplaceAll(vid, "-", "_")
}

func (this *env) sources() *sources {
	s := &sources{PermissionsV2: this.rights}
	s.models = []migrate.ProcessModel{
		{Id: "m1", Name: "Heating", Owner: "alice", BpmnXml: designerModel(), SvgXml: "<svg/>", Date: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC).UnixMilli(), LastUpdatedUnix: time.Date(2025, 3, 4, 0, 0, 0, 0, time.UTC).Unix()},
		{Id: "m-invalid", Name: "Orphan", Owner: "", BpmnXml: designerModel()},
	}
	s.deployments = []migrate.ProcessDeployment{
		{Id: d1, Owner: "alice", Source: "sepl", Deployment: models.Deployment{Version: 3, Id: d1, Name: "Heating", Diagram: models.Diagram{XmlRaw: designerModel(), Svg: "<svg/>"}}},
		{Id: d2, Owner: "alice", Deployment: models.Deployment{Version: 3, Id: d2, Name: "Mail", Diagram: models.Diagram{XmlRaw: mailModel(), Svg: "<svg/>"}}},
		{Id: d3, Owner: "alice", Deployment: models.Deployment{Version: 3, Id: d3, Name: "Switched off", Diagram: models.Diagram{XmlRaw: designerModel(), Svg: "<svg/>"}}},
	}
	s.camunda = []migrate.CamundaDeployment{
		{Engine: "shard-1", Id: "c1", Name: "Heating", TenantId: "alice", Key: key(d1), DefinitionId: key(d1) + ":1:x", Bpmn: designerModel()},
		{Engine: "shard-1", Id: "c-canary", Name: "canary", TenantId: "canary", Key: "canary_process", Bpmn: designerModel()},
		{Engine: "shard-2", Id: "c-old", Name: "Old", TenantId: "alice", Key: "old_process", Bpmn: designerModel()},
		{Engine: "shard-1", Id: "c3", Name: "Switched off", TenantId: "alice", Key: key(d3), DefinitionId: key(d3) + ":1:y", Bpmn: designerModel(), Suspended: true},
	}
	s.running = map[string][]migrate.CamundaInstance{
		"c1": {
			{Id: "ci-1", BusinessKey: "wardened:k1", TenantId: "alice", Variables: map[string]any{"level": "9", "other": 1}},
			{Id: "ci-2", TenantId: "alice", Variables: map[string]any{}},
		},
	}
	s.schedules = []migrate.Schedule{
		{Id: "s1", User: "alice", Cron: "0 7 * * *", DeploymentId: d1, CreatedBy: "widget-1"},
		{Id: "s2", User: "alice", Cron: "0 7 * * *", DeploymentId: d2},
	}
	s.io = []migrate.IoValue{
		{UserId: "alice", Key: "ci-1_limit", Value: 20.0, InstanceId: "ci-1", DefinitionId: key(d1) + ":1:x", Timestamp: 1700000000},
		{UserId: "alice", Key: "ci-ended_limit", Value: 1.0, InstanceId: "ci-ended"},
		{UserId: "alice", Key: "mode", Value: "eco"},
	}
	return s
}

func statusOf(report *migrate.Report, step string, id string) migrate.Status {
	for _, i := range report.Items {
		if i.Step == step && i.Id == id {
			return i.Status
		}
	}
	return ""
}

// A dry run writes nothing and lists what it would do and what needs a decision; the run with
// apply migrates, and a further run finds everything in place (MIG-01 to MIG-03, MIG-08).
func TestProcessesMigrateOnceAndReportWhatNeedsADecision(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.rights.Token()
	if _, err, _ := e.rights.Client.SetPermission(admin, "processmodel", "m1", permmodel.ResourcePermissions{UserPermissions: map[string]permmodel.PermissionsMap{
		"alice": {Read: true, Write: true, Execute: true, Administrate: true}, "bob": {Read: true}, "": {Read: true},
	}}); err != nil {
		t.Fatal(err)
	}
	s := e.sources()
	options := migrate.Options{CamundaOnly: map[string]bool{"c-old": true}}

	dry := migrate.Run(ctx, options, s, e.target)
	for step, id := range map[string]string{"process-models": "m1", "process-deployments": d1, "camunda-deployments": "c-old", "running-instances": "k1", "schedules": "s1"} {
		if got := statusOf(dry, step, id); got != migrate.Planned {
			t.Errorf("dry run plans %v %v, got %v", step, id, got)
		}
	}
	if _, err := e.srv.Controller.GetModel(ctx, alice, "processes", "m1"); err == nil {
		t.Fatal("a dry run writes nothing")
	}

	options.Apply = true
	report := migrate.Run(ctx, options, s, e.target)
	var out bytes.Buffer
	report.Write(&out)
	t.Log("\n" + out.String())
	expected := map[string]migrate.Status{
		"process-models/m1": migrate.Migrated, "process-models/m-invalid": migrate.Manual,
		"process-deployments/" + d1: migrate.Migrated, "process-deployments/" + d2: migrate.Manual,
		"camunda-deployments/c-canary": migrate.Manual, "camunda-deployments/c-old": migrate.Migrated,
		"running-instances/k1": migrate.Migrated, "schedules/s1": migrate.Migrated, "schedules/s2": migrate.Skipped,
		"process-io/alice/ci-ended_limit": migrate.Skipped, "process-io/alice/mode": migrate.Migrated,
	}
	for path, status := range expected {
		step, id, _ := strings.Cut(path, "/")
		if got := statusOf(report, step, id); got != status {
			t.Errorf("%v: %v, expected %v", path, got, status)
		}
	}
	if switched, err := e.srv.Controller.GetDeployment(ctx, alice, "processes", d3); err != nil || !switched.Disabled || statusOf(report, "camunda-deployments", d3) != migrate.Migrated {
		t.Errorf("a deployment switched off in the engine wrapper stays off (DEP-12): %+v %v", switched, err)
	}
	if report.ExitCode() != 1 {
		t.Errorf("open decisions make the run partial")
	}

	m, err := e.srv.Controller.GetModel(ctx, alice, "processes", "m1")
	if err != nil || m.Owner != "alice" || m.Version != 1 || m.CreatedAt.Year() != 2025 {
		t.Fatalf("the model keeps id, owner and dates: %+v %v", m, err)
	}
	if v, _ := e.srv.Controller.GetModelVersion(ctx, alice, "processes", "m1", 1); !strings.Contains(v.Xml, "PT${level}M") {
		t.Error("the model keeps the BPMN of the designer")
	}
	if rights, _, _ := e.rights.Rights(ctx, "processes_models", "m1"); !rights.UserPermissions["bob"].Read || !rights.UserPermissions["alice"].Administrate || rights.UserPermissions["bob"].Write {
		t.Errorf("the rights of processmodel go to processes_models (MIG-08): %+v", rights.UserPermissions)
	} else if _, empty := rights.UserPermissions[""]; empty {
		t.Error("users with an empty id are dropped")
	}
	d, err := e.srv.Controller.GetDeployment(ctx, alice, "processes", d1)
	if err != nil || d.ModelId != "m1" || d.Source != "sepl" || d.Owner != "alice" {
		t.Fatalf("the deployment keeps its id and finds its model by owner and BPMN: %+v %v", d, err)
	}
	inst, err := e.srv.Controller.GetInstance(ctx, alice, "processes", "k1")
	if err != nil || inst.DeploymentId != d1 || inst.StartVariables["level"] != "9" || inst.StartVariables["other"] != nil {
		t.Fatalf("the instance restarts with the business key as id and the form values (MIG-02): %+v %v", inst, err)
	}
	page, _ := e.srv.Controller.ListInstances(ctx, alice, "processes", model.InstanceListOptions{DeploymentId: d1})
	if page.Total != 2 {
		t.Errorf("the instance without business key gets an id of its own: %+v", page.Items)
	}
	schedule, err := e.srv.Controller.GetSchedule(ctx, alice, "processes", "s1")
	if err != nil || schedule.CreatedBy != "widget-1" || schedule.NextDue == nil {
		t.Errorf("schedules keep id and widget: %+v %v", schedule, err)
	}
	var limit model.IoValue
	_ = e.srv.Store.Tx(ctx, func(tx *store.Tx) (err error) { limit, _, err = tx.GetIoValue("alice", "k1_limit"); return err })
	if limit.Value != 20.0 || limit.InstanceId != "k1" || limit.DeploymentId != d1 {
		t.Errorf("an IO value of an instance follows its new id in key and binding: %+v", limit)
	}

	again := migrate.Run(ctx, options, s, e.target)
	if again.Count(migrate.Migrated) != 0 || again.Count(migrate.Failed) != 0 {
		var out bytes.Buffer
		again.Write(&out)
		t.Errorf("a second run creates nothing:\n%v", out.String())
	}
}
