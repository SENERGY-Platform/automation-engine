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

	"github.com/SENERGY-Platform/automation-engine/pkg/migrate"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/models/go/models"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
)

func logReport(t *testing.T, report *migrate.Report) {
	t.Helper()
	var out bytes.Buffer
	report.Write(&out)
	t.Log("\n" + out.String())
}

// itemsOf lists the items of a step whose message names the text
func itemsOf(report *migrate.Report, step string, text string) []migrate.Item {
	var result []migrate.Item
	for _, i := range report.Items {
		if i.Step == step && strings.Contains(i.Message, text) {
			result = append(result, i)
		}
	}
	return result
}

// An instance a run restarted under its business key keeps that id when a later run sees
// further running instances with the key, one of another deployment and one started after the
// first run; only these get ids of their own (MIG-02).
func TestInstanceIdsStayAcrossRuns(t *testing.T) {
	e := newEnv(t)
	s := e.sources()
	s.running["c1"][0].StartTime = time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)
	options := migrate.Options{Apply: true, CamundaOnly: map[string]bool{"c-old": true}}
	first := migrate.Run(ctx, options, s, e.target)
	if statusOf(first, "running-instances", "k1") != migrate.Migrated {
		logReport(t, first)
		t.Fatal("the first run restarts ci-1 as k1")
	}

	s.running["c-old"] = []migrate.CamundaInstance{{Id: "ci-3", BusinessKey: "k1", TenantId: "alice", Variables: map[string]any{"level": "3"}}}
	s.running["c1"] = append(s.running["c1"], migrate.CamundaInstance{Id: "ci-4", BusinessKey: "wardened:k1", TenantId: "alice",
		Variables: map[string]any{"level": "4"}, StartTime: time.Now().Add(time.Hour)})
	s.io = append(s.io, migrate.IoValue{UserId: "alice", Key: "ci-3_limit", Value: 30.0, InstanceId: "ci-3"})
	second := migrate.Run(ctx, options, s, e.target)
	logReport(t, second)
	if statusOf(second, "running-instances", "k1") != migrate.Existing || second.Count(migrate.Failed) != 0 {
		t.Errorf("ci-1 keeps k1 and is not restarted again")
	}
	if inst := e.instance("processes", "k1"); inst.Run != 1 || inst.StartVariables["level"] != "9" {
		t.Errorf("k1 is still the instance of ci-1: %+v", inst)
	}
	third, fourth := restartedAs(t, second, "ci-3"), restartedAs(t, second, "ci-4")
	for _, id := range []string{third, fourth} {
		if id == "k1" || id == third && id == fourth || statusOf(second, "running-instances", id) != migrate.Migrated {
			t.Errorf("the new instances get ids of their own: %v %v", third, fourth)
		}
	}
	if len(itemsOf(second, "running-instances", `business key "k1"`)) != 2 {
		t.Errorf("the report says why the new instances do not get the key")
	}
	if e.instance("processes", third).DeploymentId != "c-old" || e.instance("processes", fourth).StartVariables["level"] != "4" {
		t.Error("the new instances restart")
	}
	for id, value := range map[string]float64{"k1": 20.0, third: 30.0} {
		var v model.IoValue
		_ = e.srv.Store.Tx(ctx, func(tx *store.Tx) (err error) { v, _, err = tx.GetIoValue("alice", id+"_limit"); return err })
		if v.Value != value || v.InstanceId != id {
			t.Errorf("the IO value follows its own instance %v: %+v", id, v)
		}
	}

	again := migrate.Run(ctx, options, s, e.target)
	if again.Count(migrate.Migrated) != 0 || again.Count(migrate.Failed) != 0 || statusOf(again, "running-instances", third) != migrate.Existing {
		logReport(t, again)
		t.Error("a further run finds the same ids")
	}
}

// When a later run sees two running instances that both could be the one an earlier run
// restarted under the key, the start values tell them apart; with the same values the
// migration does not guess (MIG-02).
func TestAnInstanceOfAnEarlierRunIsToldApartByItsStartValues(t *testing.T) {
	for _, level := range []string{"7", "9"} {
		t.Run("level "+level, func(t *testing.T) {
			e := newEnv(t)
			s := e.sources()
			options := migrate.Options{Apply: true}
			if first := migrate.Run(ctx, options, s, e.target); statusOf(first, "running-instances", "k1") != migrate.Migrated {
				t.Fatal("the first run restarts ci-1 as k1")
			}
			s.running["c1"] = append(s.running["c1"], migrate.CamundaInstance{Id: "ci-5", BusinessKey: "k1", TenantId: "alice", Variables: map[string]any{"level": level}})
			second := migrate.Run(ctx, options, s, e.target)
			logReport(t, second)
			if level == "7" {
				if statusOf(second, "running-instances", "k1") != migrate.Existing || len(itemsOf(second, "running-instances", "Camunda instance ci-5")) != 1 {
					t.Error("ci-1 keeps k1, ci-5 restarts with an id of its own")
				}
				return
			}
			manual := itemsOf(second, "running-instances", "ci-1, ci-5")
			if len(manual) != 2 || manual[0].Status != migrate.Manual || manual[1].Status != migrate.Manual || second.ExitCode() != 1 {
				t.Errorf("neither is restarted, both need a decision: %+v", manual)
			}
			if inst := e.instance("processes", "k1"); inst.Run != 1 {
				t.Errorf("k1 stays as it is: %+v", inst)
			}
		})
	}
}

// Starts of process-sync get the business key as id only if no other instance of the migration
// has it; delete info of modules that names a business key points at the instance that had it,
// and where several had it, it is left for a decision (MIG-01, MIG-02, MIG-06).
func TestFogStartsAndDeleteInfoUseTheIdsOfTheMigration(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.rights.Token()
	for _, topic := range []string{"hubs", "smart_service_releases", "smart_service_instances"} {
		if _, err, _ := e.rights.Client.SetTopic(admin, permmodel.Topic{Id: topic}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err, _ := e.rights.Client.SetPermission(admin, "hubs", "hub-1", permmodel.ResourcePermissions{UserPermissions: map[string]permmodel.PermissionsMap{
		"alice": {Read: true, Write: true, Execute: true, Administrate: true},
	}}); err != nil {
		t.Fatal(err)
	}
	s := e.sources()
	s.running["c1"] = append(s.running["c1"],
		migrate.CamundaInstance{Id: "ci-7", BusinessKey: "fk1", TenantId: "alice", Variables: map[string]any{}},
		migrate.CamundaInstance{Id: "ci-8", BusinessKey: "dup", TenantId: "alice", Variables: map[string]any{}},
		migrate.CamundaInstance{Id: "ci-9", BusinessKey: "wardened:dup", TenantId: "alice", Variables: map[string]any{}})
	s.fog = []migrate.FogDeployment{
		{NetworkId: "hub-1", CamundaDeploymentId: "cam-1", Deployment: models.Deployment{Id: "fd1", Name: "Fog 1", Diagram: models.Diagram{XmlRaw: designerModel(), Svg: "<svg/>"}}},
		{NetworkId: "hub-1", CamundaDeploymentId: "cam-2", Deployment: models.Deployment{Id: "fd2", Name: "Fog 2", Diagram: models.Diagram{XmlRaw: designerModel(), Svg: "<svg/>"}}},
	}
	s.fogStarts = []migrate.FogStart{
		{NetworkId: "hub-1", BusinessKey: "wardened:fk1", DeploymentId: "cam-1"},
		{NetworkId: "hub-1", BusinessKey: "wardened:fk2", DeploymentId: "cam-1"},
		{NetworkId: "hub-1", BusinessKey: "fk2", DeploymentId: "cam-2"},
		{NetworkId: "hub-1", BusinessKey: "wardened:fk3", DeploymentId: "cam-1"},
	}
	s.releases = []migrate.Release{{Id: "r1", DesignId: "ds1", Name: "Room info", Creator: "alice", BpmnXml: smartServiceDesign("tile"), SvgXml: "<svg/>", CreatedAt: 100}}
	s.instances = []migrate.SmartServiceInstance{{Id: "si1", UserId: "alice", ReleaseId: "r1", Name: "Kitchen", Ready: true, CreatedAt: 1700000000}}
	sync, wrapper := "http://sync.fog-processes:8080/process-instances-by-business-key/hub-1/", "http://engine-wrapper:8080/process-instances-by-business-key/"
	s.modules = []migrate.Module{
		{Id: "fog-fk1", UserId: "alice", InstanceId: "si1", Type: "process_deployment_start", DeleteUrl: sync + "wardened:fk1", DeleteUser: "alice"},
		{Id: "fog-fk2", UserId: "alice", InstanceId: "si1", Type: "process_deployment_start", DeleteUrl: sync + "wardened:fk2", DeleteUser: "alice"},
		{Id: "fog-fk3", UserId: "alice", InstanceId: "si1", Type: "process_deployment_start", DeleteUrl: sync + "fk3", DeleteUser: "alice"},
		{Id: "cloud-fk1", UserId: "alice", InstanceId: "si1", Type: "process_deployment_start", DeleteUrl: wrapper + "fk1", DeleteUser: "alice"},
		{Id: "cloud-k1", UserId: "alice", InstanceId: "si1", Type: "process_deployment_start", DeleteUrl: wrapper + "wardened:k1", DeleteUser: "alice"},
		{Id: "cloud-dup", UserId: "alice", InstanceId: "si1", Type: "process_deployment_start", DeleteUrl: wrapper + "dup", DeleteUser: "alice"},
	}

	report := migrate.Run(ctx, migrate.Options{Apply: true}, s, e.target)
	logReport(t, report)
	if statusOf(report, "running-instances", "fk1") != migrate.Migrated || statusOf(report, "fog-instances", "fk3") != migrate.Migrated {
		t.Fatal("keys only one instance has stay ids")
	}
	fk1 := itemsOf(report, "fog-instances", `business key "fk1"`)
	if len(fk1) != 1 || fk1[0].Status != migrate.Migrated || fk1[0].Id == "fk1" {
		t.Fatalf("the start with the key of a cloud instance gets an id of its own: %+v", fk1)
	}
	fk2 := itemsOf(report, "fog-instances", `business key "fk2"`)
	if len(fk2) != 2 || fk2[0].Id == fk2[1].Id || fk2[0].Id == "fk2" || fk2[1].Id == "fk2" || fk2[0].Status != migrate.Migrated || fk2[1].Status != migrate.Migrated {
		t.Fatalf("two starts with one key get ids of their own: %+v", fk2)
	}
	for _, id := range []string{fk1[0].Id, fk2[0].Id, fk2[1].Id, "fk3"} {
		if inst := e.instance("processes", id); inst.NetworkId != "hub-1" {
			t.Errorf("%v is a start of the network: %+v", id, inst)
		}
	}
	if inst := e.instance("processes", "fk1"); inst.NetworkId != "" || inst.DeploymentId != d1 {
		t.Errorf("fk1 is the cloud instance: %+v", inst)
	}

	modules, err := e.srv.Controller.ListModules(ctx, alice, "smart-services", model.ModuleListOptions{InstanceId: "si1"})
	if err != nil || modules.Total != len(s.modules) {
		t.Fatalf("%+v %v", modules, err)
	}
	resolved := map[string]string{"fog-fk1": fk1[0].Id, "fog-fk3": "fk3", "cloud-fk1": "fk1", "cloud-k1": "k1"}
	for _, m := range modules.Items {
		if id, ok := resolved[m.Id]; ok {
			if m.DeleteInfo == nil || m.DeleteInfo.Resource == nil || m.DeleteInfo.Resource.Id != id || statusOf(report, "smart-service-modules", m.Id) != migrate.Migrated {
				t.Errorf("%v deletes instance %v: %+v", m.Id, id, m.DeleteInfo)
			}
			continue
		}
		if m.DeleteInfo == nil || m.DeleteInfo.Resource != nil || !strings.HasSuffix(m.DeleteInfo.Url, "fk2") && !strings.HasSuffix(m.DeleteInfo.Url, "dup") {
			t.Errorf("%v keeps its URL: %+v", m.Id, m.DeleteInfo)
		}
		if statusOf(report, "smart-service-modules", m.Id) != migrate.Manual {
			t.Errorf("%v needs a decision", m.Id)
		}
	}

	again := migrate.Run(ctx, migrate.Options{Apply: true}, s, e.target)
	if again.Count(migrate.Migrated) != 0 || again.Count(migrate.Failed) != 0 {
		logReport(t, again)
		t.Error("a second run finds the same ids")
	}
}
