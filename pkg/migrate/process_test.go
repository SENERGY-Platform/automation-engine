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
	"slices"
	"strings"
	"testing"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/migrate"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/models/go/models"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
)

// restartedAs finds the instance id a running Camunda instance restarted with
func restartedAs(t *testing.T, report *migrate.Report, camundaId string) string {
	t.Helper()
	for _, i := range report.Items {
		mentioned := strings.Contains(i.Message, "Camunda instance "+camundaId+";") || strings.HasSuffix(i.Message, "Camunda instance "+camundaId)
		if i.Step == "running-instances" && i.Kind == "instance" && mentioned {
			return i.Id
		}
	}
	t.Fatalf("%v is not restarted", camundaId)
	return ""
}

// A business key becomes the instance id only if no other running instance has it, across the
// deployments and tenants, since instance ids are unique in the engine; `wardened:` does not
// make a key different. The colliding instances get ids of their own, the report says why, and
// the IO values follow each instance (MIG-02, RUN-01).
func TestRunningInstancesWithTheSameBusinessKeyInDifferentDeploymentsRestartEach(t *testing.T) {
	e := newEnv(t)
	s := e.sources()
	s.running["c-old"] = []migrate.CamundaInstance{
		{Id: "ci-3", BusinessKey: "k1", TenantId: "alice", Variables: map[string]any{"level": "3"}},
	}
	s.io = append(s.io, migrate.IoValue{UserId: "alice", Key: "ci-3_limit", Value: 30.0, InstanceId: "ci-3"})
	options := migrate.Options{Apply: true, CamundaOnly: map[string]bool{"c-old": true}}

	report := migrate.Run(ctx, options, s, e.target)
	var out bytes.Buffer
	report.Write(&out)
	t.Log("\n" + out.String())

	for _, i := range report.Items {
		if i.Step == "running-instances" && i.Status != migrate.Migrated {
			t.Errorf("every running instance restarts: %+v", i)
		}
	}
	first, second := restartedAs(t, report, "ci-1"), restartedAs(t, report, "ci-3")
	if first == second || first == "k1" || second == "k1" {
		t.Fatalf("a business key two running instances share is no instance id: %v %v", first, second)
	}
	for _, id := range []string{first, second} {
		i := slices.IndexFunc(report.Items, func(i migrate.Item) bool { return i.Step == "running-instances" && i.Id == id })
		if !strings.Contains(report.Items[i].Message, `business key "k1" is not unique`) {
			t.Errorf("the report names the business key: %+v", report.Items[i])
		}
	}
	if inst := e.instance("processes", first); inst.DeploymentId != d1 || inst.StartVariables["level"] != "9" {
		t.Errorf("%+v", inst)
	}
	if inst := e.instance("processes", second); inst.DeploymentId != "c-old" || inst.StartVariables["level"] != "3" {
		t.Errorf("%+v", inst)
	}
	for id, value := range map[string]float64{first: 20.0, second: 30.0} {
		var v model.IoValue
		_ = e.srv.Store.Tx(ctx, func(tx *store.Tx) (err error) { v, _, err = tx.GetIoValue("alice", id+"_limit"); return err })
		if v.Value != value || v.InstanceId != id {
			t.Errorf("the IO value follows its own instance %v: %+v", id, v)
		}
	}

	again := migrate.Run(ctx, options, s, e.target)
	if again.Count(migrate.Migrated) != 0 || again.Count(migrate.Failed) != 0 {
		var out bytes.Buffer
		again.Write(&out)
		t.Errorf("a second run finds the same ids:\n%v", out.String())
	}
}

// dailyModel waits for a JUEL duration and then a day, as R/P1D in timeDuration
func dailyModel() string {
	return b.Process(
		b.StartWithForm("start", "level=5"),
		`<bpmn:intermediateCatchEvent id="wait"><bpmn:timerEventDefinition><bpmn:timeDuration xsi:type="bpmn:tFormalExpression">PT${level}M</bpmn:timeDuration></bpmn:timerEventDefinition></bpmn:intermediateCatchEvent>`,
		b.CatchTimer("daily", "timeDuration", "R/P1D"),
		b.End("end"),
		b.Flow("start", "wait"), b.Flow("wait", "daily"), b.Flow("daily", "end"),
	)
}

// findings lists the findings of the model rewrite the report has for a deployment
func findings(report *migrate.Report, step string, id string) map[migrate.Status][]string {
	result := map[migrate.Status][]string{}
	for _, i := range report.Items {
		if i.Step == step && i.Id == id && i.Kind == "model" {
			result[i.Status] = append(result[i.Status], i.Message)
		}
	}
	return result
}

// The report names per deployment what the rewrite of its model changed, what is to be checked
// by hand and what is not supported, in every step that rewrites models, in a dry run as with
// apply (MIG-04).
func TestTheReportListsTheFindingsOfTheModelRewrite(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.rights.Token()
	for _, topic := range []string{"hubs", "smart_service_releases"} {
		if _, err, _ := e.rights.Client.SetTopic(admin, permmodel.Topic{Id: topic}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err, _ := e.rights.Client.SetPermission(admin, "hubs", "hub-1", permmodel.ResourcePermissions{UserPermissions: map[string]permmodel.PermissionsMap{
		"alice": {Read: true, Write: true, Execute: true, Administrate: true},
	}}); err != nil {
		t.Fatal(err)
	}
	s := &sources{PermissionsV2: e.rights}
	s.deployments = []migrate.ProcessDeployment{
		{Id: d1, Owner: "alice", Deployment: models.Deployment{Version: 3, Id: d1, Name: "Daily", Diagram: models.Diagram{XmlRaw: dailyModel(), Svg: "<svg/>"}}},
		{Id: d2, Owner: "alice", Deployment: models.Deployment{Version: 3, Id: d2, Name: "Mail", Diagram: models.Diagram{XmlRaw: mailModel(), Svg: "<svg/>"}}},
	}
	s.camunda = []migrate.CamundaDeployment{{Engine: "shard-2", Id: "c-old", Name: "Old", TenantId: "alice", Key: "old_process", Bpmn: dailyModel()}}
	s.fog = []migrate.FogDeployment{{NetworkId: "hub-1", Deployment: models.Deployment{Id: "fd1", Name: "Fog daily", Diagram: models.Diagram{XmlRaw: dailyModel(), Svg: "<svg/>"}}}}
	s.releases = []migrate.Release{{Id: "r1", DesignId: "ds1", Name: "Room info", Creator: "alice", BpmnXml: smartServiceDesign("tile"), SvgXml: "<svg/>", CreatedAt: 100}}
	options := migrate.Options{CamundaOnly: map[string]bool{"c-old": true}}

	rewritten := "wait: timer: JUEL became JavaScript"
	check := "daily: timer: the duration R/P1D became P1D; Camunda waited once as well"
	for _, apply := range []bool{false, true} {
		options.Apply = apply
		report := migrate.Run(ctx, options, s, e.target)
		var out bytes.Buffer
		report.Write(&out)
		t.Logf("apply %v\n%v", apply, out.String())

		for _, deployment := range []struct{ step, id string }{{"process-deployments", d1}, {"camunda-deployments", "c-old"}, {"fog-deployments", "fd1"}} {
			if status := statusOf(report, deployment.step, deployment.id); status != migrate.Migrated && status != migrate.Planned {
				t.Fatalf("%v %v: %v", deployment.step, deployment.id, status)
			}
			f := findings(report, deployment.step, deployment.id)
			if !slices.Contains(f[migrate.Rewritten], rewritten) || !slices.Equal(f[migrate.Check], []string{check}) || len(f[migrate.Unsupported]) != 0 {
				t.Errorf("%v %v: %+v", deployment.step, deployment.id, f)
			}
			if !strings.Contains(out.String(), check) || !strings.Contains(out.String(), rewritten) {
				t.Errorf("the printed report lists the findings")
			}
		}
		if f := findings(report, "smart-service-releases", "r1"); !slices.ContainsFunc(f[migrate.Rewritten], func(m string) bool { return strings.Contains(m, "external task topic info") }) {
			t.Errorf("a release lists its findings as well: %+v", f)
		}
		if statusOf(report, "process-deployments", d2) != migrate.Manual {
			t.Errorf("an unsupported part needs a decision")
		}
		if f := findings(report, "process-deployments", d2); len(f[migrate.Unsupported]) == 0 {
			t.Errorf("the unsupported parts are findings of the deployment: %+v", f)
		}
		if report.ExitCode() != 1 {
			t.Errorf("only the unsupported deployment keeps the run open")
		}
	}
}

// Findings do not count as open decisions: a run whose models only were rewritten or are to
// be checked is complete (MIG-04).
func TestFindingsToCheckDoNotMakeTheRunPartial(t *testing.T) {
	e := newEnv(t)
	s := &sources{PermissionsV2: e.rights}
	s.deployments = []migrate.ProcessDeployment{
		{Id: d1, Owner: "alice", Deployment: models.Deployment{Version: 3, Id: d1, Name: "Daily", Diagram: models.Diagram{XmlRaw: dailyModel(), Svg: "<svg/>"}}},
	}
	report := migrate.Run(ctx, migrate.Options{Apply: true}, s, e.target)
	if f := findings(report, "process-deployments", d1); len(f[migrate.Check]) != 1 || report.ExitCode() != 0 {
		var out bytes.Buffer
		report.Write(&out)
		t.Errorf("exit code %v, findings %+v\n%v", report.ExitCode(), f, out.String())
	}
}

// Models are taken over as they are; the report names per model what the conversion at deploy
// would do, in a dry run as with apply, and the stored issues are those of a save: a hint
// instead of the findings of the old format (MIG-04).
func TestTheReportListsWhatTheConversionWouldDoPerModel(t *testing.T) {
	e := newEnv(t)
	s := &sources{PermissionsV2: e.rights}
	engineModel := b.Process(b.Start("start"), b.End("end"), b.Flow("start", "end"))
	s.models = []migrate.ProcessModel{
		{Id: "m-daily", Name: "Daily", Owner: "alice", BpmnXml: dailyModel()},
		{Id: "m-engine", Name: "Engine", Owner: "alice", BpmnXml: engineModel},
		{Id: "m-mail", Name: "Mail", Owner: "alice", BpmnXml: mailModel()},
	}
	for _, apply := range []bool{false, true} {
		report := migrate.Run(ctx, migrate.Options{Apply: apply}, s, e.target)
		f := findings(report, "process-models", "m-daily")
		if !slices.Contains(f[migrate.Rewritten], "wait: timer: JUEL became JavaScript") ||
			!slices.Equal(f[migrate.Check], []string{"daily: timer: the duration R/P1D became P1D; Camunda waited once as well"}) || len(f[migrate.Unsupported]) != 0 {
			t.Errorf("apply %v: the model lists the conversion: %+v", apply, f)
		}
		if f := findings(report, "process-models", "m-engine"); len(f[migrate.Rewritten])+len(f[migrate.Check])+len(f[migrate.Unsupported]) != 0 {
			t.Errorf("apply %v: a model of the engine has nothing to convert: %+v", apply, f)
		}
		if f := findings(report, "process-models", "m-mail"); len(f[migrate.Unsupported]) != 1 || statusOf(report, "process-models", "m-mail") == migrate.Manual {
			t.Errorf("apply %v: an unsupported part is a finding, the model is taken over anyway: %+v", apply, f)
		}
		if report.ExitCode() != 0 {
			t.Errorf("apply %v: findings of models leave the run complete", apply)
		}
	}
	v, err := e.srv.Controller.GetModelVersion(ctx, alice, "processes", "m-daily", 1)
	if err != nil || v.Xml != dailyModel() {
		t.Fatalf("the model keeps its BPMN: %v", err)
	}
	if len(v.Issues) != 2 || !v.Issues[0].Hint || !strings.Contains(v.Issues[0].Message, "converted when deployed") ||
		!v.Issues[1].Hint || v.Issues[1].ElementId != "daily" {
		t.Errorf("the issues are hints of the conversion: %+v", v.Issues)
	}
}

// The base URL of a Camunda engine may carry its basic auth; the report names the engine
// without it, because the report ends up in logs.
func TestTheReportLeavesOutTheCredentialsOfCamunda(t *testing.T) {
	e := newEnv(t)
	s := &sources{PermissionsV2: e.rights}
	s.camunda = []migrate.CamundaDeployment{{Engine: "http://camunda:s3cret@shard-2:8080/engine-rest", Id: "c-old", Name: "Old", TenantId: "alice", Key: "old_process", Bpmn: dailyModel()}}
	report := migrate.Run(ctx, migrate.Options{CamundaOnly: map[string]bool{}}, s, e.target)
	var out bytes.Buffer
	report.Write(&out)
	if strings.Contains(out.String(), "s3cret") {
		t.Fatalf("the report contains the password:\n%v", out.String())
	}
	if !strings.Contains(out.String(), "http://shard-2:8080/engine-rest") {
		t.Fatalf("the report names the engine:\n%v", out.String())
	}
}
