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
	"strings"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/migrate"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/models/go/models"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
)

// smartServiceDesign is a design of the old designer: a start form and an info module
func smartServiceDesign(moduleType string) string {
	return b.Process(
		b.StartWithForm("start", "room=kitchen"),
		`<bpmn:serviceTask id="info" camunda:type="external" camunda:topic="info"><bpmn:extensionElements><camunda:inputOutput>`+
			`<camunda:inputParameter name="info.module_type">`+moduleType+`</camunda:inputParameter>`+
			`<camunda:inputParameter name="info.module_data">{"room":"{{.room}}"}</camunda:inputParameter>`+
			`</camunda:inputOutput></bpmn:extensionElements></bpmn:serviceTask>`,
		b.End("end"),
		b.Flow("start", "info"), b.Flow("info", "end"),
	)
}

func (this *env) instance(namespace string, id string) model.Instance {
	this.t.Helper()
	inst, err := this.srv.Controller.GetInstance(ctx, alice, namespace, id)
	if err != nil {
		this.t.Fatalf("%v: %v", id, err)
	}
	return inst
}

// Designs become models, the releases of a design versions of one deployment; instances whose
// setup ended keep their modules without running again, one in setup starts anew; module
// delete info points at the engine where the old URL pointed at a replaced service
// (MIG-01, MIG-02, MIG-08, MIG-09).
func TestSmartServicesMigrate(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.rights.Token()
	for _, topic := range []string{"smart_service_releases", "smart_service_instances"} {
		if _, err, _ := e.rights.Client.SetTopic(admin, permmodel.Topic{Id: topic}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err, _ := e.rights.Client.SetPermission(admin, "smart_service_releases", "r1", permmodel.ResourcePermissions{UserPermissions: map[string]permmodel.PermissionsMap{
		"alice": {Read: true, Write: true, Execute: true, Administrate: true}, "bob": {Read: true, Execute: true},
	}}); err != nil {
		t.Fatal(err)
	}
	s := &sources{PermissionsV2: e.rights}
	s.designs = []migrate.Design{{Id: "ds1", UserId: "alice", Name: "Room info", BpmnXml: smartServiceDesign("tile"), SvgXml: "<svg/>", UpdatedAt: 300}}
	s.releases = []migrate.Release{
		{Id: "r2", DesignId: "ds1", Name: "Room info", Creator: "alice", BpmnXml: smartServiceDesign("tile"), SvgXml: "<svg/>", CreatedAt: 200},
		{Id: "r1", DesignId: "ds1", Name: "Room info", Creator: "alice", BpmnXml: smartServiceDesign("widget"), SvgXml: "<svg/>", CreatedAt: 100},
		{Id: "r-invalid", DesignId: "ds-other", Creator: "placeholder-for-invalid-data", BpmnXml: smartServiceDesign("x"), CreatedAt: 50},
	}
	s.instances = []migrate.SmartServiceInstance{
		{Id: "si1", UserId: "alice", ReleaseId: "r1", Name: "Kitchen", Parameters: []migrate.Parameter{{Id: "room", Value: "kitchen"}}, Ready: true, CreatedAt: 1700000000, UpdatedAt: 1700000100},
		{Id: "si2", UserId: "alice", ReleaseId: "r2", Name: "Broken", Parameters: []migrate.Parameter{{Id: "room", Value: "hall"}}, Error: "boom"},
		{Id: "si3", UserId: "alice", ReleaseId: "r2", Name: "In setup", Parameters: []migrate.Parameter{{Id: "room", Value: "attic"}}},
		{Id: "si4", UserId: "alice", ReleaseId: "r-invalid", Name: "Orphan"},
	}
	s.modules = []migrate.Module{
		{Id: "pid.task1", UserId: "alice", InstanceId: "si1", Type: "process_deployment", Data: map[string]any{"process_deployment_id": "d-x"}, Keys: []string{"k"},
			DeleteUrl: "http://deployment.process-deployment:8080/v3/deployments/d-x", DeleteUser: "alice", LastUpdate: 1700000100},
		{Id: "pid.task2", UserId: "alice", InstanceId: "si1", Type: "process_deployment_start", Data: map[string]any{"business_key": "wardened:bk1"},
			DeleteUrl: "http://sync.fog-processes:8080/process-instances-by-business-key/hub-1/wardened:bk1", DeleteUser: "alice"},
		{Id: "pid.task3", UserId: "alice", InstanceId: "si1", Type: "export", DeleteUrl: "http://serving:8080/instance/e1", DeleteUser: "alice", Error: "export missing"},
		{Id: "pid.task4", UserId: "alice", InstanceId: "si1", Type: "watcher", DeleteUrl: "http://watcher:8080/watcher/w1", DeleteUser: "alice"},
		{Id: "other", UserId: "alice", InstanceId: "unknown", Type: "info"},
	}
	s.variables = []migrate.InstanceVariable{{InstanceId: "si1", Name: "counter", Value: 3.0}}

	report := migrate.Run(ctx, migrate.Options{Apply: true}, s, e.target)
	var out bytes.Buffer
	report.Write(&out)
	t.Log("\n" + out.String())
	expected := map[string]migrate.Status{
		"smart-service-designs/ds1": migrate.Migrated, "smart-service-releases/r1": migrate.Migrated, "smart-service-releases/r2": migrate.Migrated,
		"smart-service-releases/r-invalid": migrate.Manual, "smart-service-instances/si1": migrate.Migrated, "smart-service-instances/si4": migrate.Manual,
		"smart-service-modules/pid.task1": migrate.Migrated, "smart-service-modules/pid.task4": migrate.Manual, "smart-service-modules/other": migrate.Skipped,
		"smart-service-variables/si1/counter": migrate.Migrated,
	}
	for path, status := range expected {
		step, id, _ := strings.Cut(path, "/")
		if got := statusOf(report, step, id); got != status {
			t.Errorf("%v: %v, expected %v", path, got, status)
		}
	}

	d, err := e.srv.Controller.GetDeployment(ctx, alice, "smart-services", "r2")
	if err != nil || d.Version != 2 || d.ModelId != "ds1" {
		t.Fatalf("the releases are the versions of the deployment of the newest one: %+v %v", d, err)
	}
	if rights, _, _ := e.rights.Rights(ctx, "smart-services_deployments", "r2"); !rights.UserPermissions["bob"].Execute {
		t.Errorf("the rights of all releases go to the deployment (MIG-08): %+v", rights.UserPermissions)
	}
	si1 := e.instance("smart-services", "si1")
	if si1.State != model.StateCompleted || si1.DeploymentVersion != 1 || si1.Name != "Kitchen" || si1.StartVariables["room"] != "kitchen" {
		t.Errorf("an instance whose setup ended keeps its release and does not run again: %+v", si1)
	}
	if si1.Status != model.StatusError || !strings.Contains(si1.StatusMessage, "export missing") {
		t.Errorf("the error of a module shows in the status, as before: %+v", si1)
	}
	if si2 := e.instance("smart-services", "si2"); si2.Status != model.StatusError || si2.StatusMessage != "boom" {
		t.Errorf("an instance with an error keeps it: %+v", si2)
	}
	modules, err := e.srv.Controller.ListModules(ctx, alice, "smart-services", model.ModuleListOptions{InstanceId: "si1"})
	if err != nil || modules.Total != 4 {
		t.Fatalf("the modules of the instance: %+v %v", modules, err)
	}
	for _, m := range modules.Items {
		switch m.Id {
		case "pid.task1":
			if m.DeleteInfo == nil || m.DeleteInfo.Resource == nil || m.DeleteInfo.Resource.Id != "d-x" || m.DeleteInfo.Resource.Kind != model.ResourceDeployment || len(m.Keys) != 1 {
				t.Errorf("a deployment of process-deployment is a deployment of the engine: %+v", m.DeleteInfo)
			}
		case "pid.task2":
			if m.DeleteInfo == nil || m.DeleteInfo.Resource == nil || m.DeleteInfo.Resource.Id != "bk1" || m.DeleteInfo.Resource.Kind != model.ResourceInstance {
				t.Errorf("a start through process-sync is an instance of the engine: %+v", m.DeleteInfo)
			}
		case "pid.task3":
			if m.DeleteInfo == nil || m.DeleteInfo.Url != "http://serving:8080/instance/e1" || m.Error != "export missing" {
				t.Errorf("other services keep their URL and the module its error: %+v", m)
			}
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for e.instance("smart-services", "si3").State != model.StateCompleted {
		if time.Now().After(deadline) {
			t.Fatal("the instance in setup starts anew and runs")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if si3 := e.instance("smart-services", "si3"); si3.DeploymentVersion != 2 || si3.Name != "In setup" {
		t.Errorf("%+v", si3)
	}

	again := migrate.Run(ctx, migrate.Options{Apply: true}, s, e.target)
	if again.Count(migrate.Migrated) != 0 || again.Count(migrate.Failed) != 0 {
		var out bytes.Buffer
		again.Write(&out)
		t.Errorf("a second run creates nothing:\n%v", out.String())
	}
}

// What process-sync keeps for a gateway becomes deployments and starts of its network; the
// owner is the one administrator of the hub (MIG-06, MIG-09).
func TestFogDeploymentsMigrate(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.rights.Token()
	if _, err, _ := e.rights.Client.SetTopic(admin, permmodel.Topic{Id: "hubs"}); err != nil {
		t.Fatal(err)
	}
	if _, err, _ := e.rights.Client.SetPermission(admin, "hubs", "hub-1", permmodel.ResourcePermissions{UserPermissions: map[string]permmodel.PermissionsMap{
		"alice": {Read: true, Write: true, Execute: true, Administrate: true}, "bob": {Read: true},
	}}); err != nil {
		t.Fatal(err)
	}
	s := &sources{PermissionsV2: e.rights}
	s.fog = []migrate.FogDeployment{{NetworkId: "hub-1", CamundaDeploymentId: "cam-1", Deployment: models.Deployment{Id: "fd1", Name: "Fog heating", Diagram: models.Diagram{XmlRaw: designerModel(), Svg: "<svg/>"}}}}
	s.fogStarts = []migrate.FogStart{
		{NetworkId: "hub-1", BusinessKey: "wardened:fk1", DeploymentId: "cam-1", Parameters: map[string]any{"level": "4"}},
		{NetworkId: "hub-1", BusinessKey: "wardened:fk2", DeploymentId: "unknown"},
	}
	report := migrate.Run(ctx, migrate.Options{Apply: true}, s, e.target)
	var out bytes.Buffer
	report.Write(&out)
	t.Log("\n" + out.String())
	if statusOf(report, "fog-deployments", "fd1") != migrate.Migrated || statusOf(report, "fog-instances", "fk1") != migrate.Migrated || statusOf(report, "fog-instances", "fk2") != migrate.Skipped {
		t.Fatal("fog deployments and starts")
	}
	d, err := e.srv.Controller.GetDeployment(ctx, alice, "processes", "fd1")
	if err != nil || d.NetworkId != "hub-1" || d.Owner != "alice" {
		t.Fatalf("%+v %v", d, err)
	}
	inst := e.instance("processes", "fk1")
	if inst.State != model.StatePending || inst.NetworkId != "hub-1" || inst.StartVariables["level"] != "4" {
		t.Errorf("the start waits for the gateway: %+v", inst)
	}
}
