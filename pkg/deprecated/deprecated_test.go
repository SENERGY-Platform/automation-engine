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

package deprecated_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
)

var ctx = context.Background()
var alice = client.TestToken("alice")
var bob = client.TestToken("bob")

type env struct {
	t   *testing.T
	srv *server.Server
	url string
}

func newEnv(t *testing.T) *env {
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	srv, err := client.NewTestServer(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(srv.Handler)
	t.Cleanup(httpServer.Close)
	return &env{t: t, srv: srv, url: httpServer.URL}
}

// call sends a request to an old route and decodes the answer into result, if given
func (this *env) call(token string, method string, path string, body any, result any) (int, http.Header) {
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
	return resp.StatusCode, resp.Header
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

func waitingProcess() string {
	return b.Process(b.Message("m", "go"), b.StartWithForm("start", "level=5"), b.CatchMessage("wait", "m"), b.End("end"),
		b.Flow("start", "wait"), b.Flow("wait", "end"))
}

func (this *env) deploy(xml string) model.Deployment {
	this.t.Helper()
	d, err := this.srv.Controller.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Name: "Lights", Xml: xml, Svg: "<svg>lights</svg>"})
	if err != nil {
		this.t.Fatal(err)
	}
	return d
}

func (this *env) instance(id string) model.Instance {
	this.t.Helper()
	inst, err := this.srv.Controller.GetInstance(ctx, alice, "processes", id)
	if err != nil {
		this.t.Fatal(err)
	}
	return inst
}

// A copied start URL of the wrapper keeps working: the business key becomes the instance id,
// every other parameter a variable, read as JSON if it parses (API-08).
func TestCopiedStartUrlStartsWithBusinessKeyAndVariables(t *testing.T) {
	e := newEnv(t)
	d := e.deploy(waitingProcess())
	var started struct {
		Id, BusinessKey, DefinitionId string
	}
	status, _ := e.call(alice, http.MethodGet, "/engine/v2/deployments/"+d.Id+"/start?business_key=bk-1&level=7&name=abc", nil, &started)
	if status != http.StatusOK || started.Id != "bk-1" || started.BusinessKey != "bk-1" || !strings.HasPrefix(started.DefinitionId, "deplid_") {
		t.Fatalf("%v %+v", status, started)
	}
	inst := e.instance("bk-1")
	if inst.StartVariables["level"] != 7.0 || inst.StartVariables["name"] != "abc" || inst.StartVariables["business_key"] != nil {
		t.Errorf("query parameters are the variables, business_key is not one: %+v", inst.StartVariables)
	}
	if status, _ = e.call(alice, http.MethodGet, "/engine/v2/deployments/"+d.Id+"/start?business_key=bk-1&level=7&name=abc", nil, &started); status != http.StatusOK || started.Id != "bk-1" {
		t.Errorf("the same start answers with the instance: %v %+v", status, started)
	}
	if status, _ = e.call(alice, http.MethodGet, "/engine/v2/deployments/"+d.Id+"/start?business_key=bk-1&level=8", nil, nil); status != http.StatusConflict {
		t.Errorf("another start with the same business key is a conflict, not a second instance: %v", status)
	}
	if status, _ = e.call(alice, http.MethodGet, "/engine/deployment/"+d.Id+"/start", nil, nil); status != http.StatusOK {
		t.Errorf("process-scheduler's v1 start works: %v", status)
	}
	var starts []any
	if status, _ = e.call(alice, http.MethodGet, "/engine/v2/process-definitions/"+started.DefinitionId+"/start", nil, &starts); status != http.StatusOK || len(starts) != 0 {
		t.Errorf("the start by definition id answers []: %v %v", status, starts)
	}
	if status, _ = e.call(bob, http.MethodGet, "/engine/v2/deployments/"+d.Id+"/start", nil, nil); status != http.StatusNotFound {
		t.Errorf("another user cannot start: %v", status)
	}
}

func TestDeploymentsInTheFormOfTheWrapper(t *testing.T) {
	e := newEnv(t)
	d := e.deploy(waitingProcess())
	// a name sorts after Lights; two deployments of the same millisecond have no order by time
	if _, err := e.srv.Controller.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{Name: "Zebra", Xml: waitingProcess()}); err != nil {
		t.Fatal(err)
	}
	var list []struct {
		Id, Name, TenantId, Diagram string
		DefinitionId                string `json:"definition_id"`
	}
	if status, _ := e.call(alice, http.MethodGet, "/engine/v2/deployments?maxResults=1&firstResult=0&sortBy=name&sortOrder=asc", nil, &list); status != http.StatusOK || len(list) != 1 {
		t.Fatalf("paging with maxResults and firstResult: %v %+v", status, list)
	}
	if list[0].Id != d.Id || list[0].Diagram != "<svg>lights</svg>" || list[0].TenantId != "alice" || list[0].DefinitionId == "" {
		t.Errorf("the extended deployment carries svg and definition: %+v", list[0])
	}
	var exists bool
	if e.call(alice, http.MethodGet, "/engine/v2/deployments/"+d.Id+"/exists", nil, &exists); !exists {
		t.Error("exists")
	}
	if e.call(alice, http.MethodGet, "/engine/v2/deployments/unknown/exists", nil, &exists); exists {
		t.Error("an unknown deployment does not exist")
	}
	var parameters map[string]struct {
		Value any
		Type  string
	}
	if e.call(alice, http.MethodGet, "/engine/v2/deployments/"+d.Id+"/parameter", nil, &parameters); parameters["level"].Value != "5" || parameters["level"].Type != "String" {
		t.Errorf("start parameters as Camunda form variables: %+v", parameters)
	}
	var definitions []struct {
		Id           string
		DeploymentId string
		Version      int `json:"Version"`
	}
	e.call(alice, http.MethodGet, "/engine/v2/deployments/"+d.Id+"/definition", nil, &definitions)
	if len(definitions) != 1 || definitions[0].DeploymentId != d.Id || definitions[0].Version != 1 {
		t.Fatalf("%+v", definitions)
	}
	var definition struct{ DeploymentId string }
	if e.call(alice, http.MethodGet, "/engine/v2/process-definitions/"+definitions[0].Id, nil, &definition); definition.DeploymentId != d.Id {
		t.Errorf("a definition id maps back to its deployment: %+v", definition)
	}
	camundaId := "deplid_" + strings.ReplaceAll(d.Id, "-", "_") + ":3:0c4a7d4e-0000-11ee-be56-0242ac120002"
	if e.call(alice, http.MethodGet, "/engine/v2/process-definitions/"+camundaId, nil, &definition); definition.DeploymentId != d.Id {
		t.Errorf("a definition id of Camunda maps to the migrated deployment: %+v", definition)
	}
}

func TestMonitorOfTheWrapper(t *testing.T) {
	e := newEnv(t)
	d := e.deploy(waitingProcess())
	for _, key := range []string{"a", "b", "c"} {
		e.call(alice, http.MethodGet, "/engine/v2/deployments/"+d.Id+"/start?business_key="+key, nil, nil)
	}
	if _, err := e.srv.Engine.Correlate(ctx, engine.CorrelationRequest{MessageInput: model.MessageInput{Name: "go", InstanceId: "a"}}); err != nil {
		t.Fatal(err)
	}
	e.waitFor("the completed instance", func() bool { return e.instance("a").State == model.StateCompleted })
	var finished struct {
		Total int
		Data  []struct{ Id, State, EndTime string }
	}
	e.call(alice, http.MethodGet, "/engine/v2/history/process-instances?finished=true&with_total=true&maxResults=10&firstResult=0&sortBy=startTime&sortOrder=desc", nil, &finished)
	if finished.Total != 1 || finished.Data[0].Id != "a" || finished.Data[0].State != "COMPLETED" || finished.Data[0].EndTime == "" {
		t.Fatalf("finished instances with total: %+v", finished)
	}
	var running []struct{ Id string }
	if e.call(alice, http.MethodGet, "/engine/v2/process-instances", nil, &running); len(running) != 2 {
		t.Errorf("running instances: %+v", running)
	}
	var count struct{ Count int }
	if e.call(alice, http.MethodGet, "/engine/v2/process-instances/count", nil, &count); count.Count != 2 {
		t.Errorf("%+v", count)
	}
	if status, _ := e.call(alice, http.MethodDelete, "/engine/v2/process-instances/b", nil, nil); status != http.StatusOK {
		t.Fatalf("delete of a running instance: %v", status)
	}
	if state := e.instance("b").State; state != model.StateTerminated {
		t.Errorf("deleting a running instance stops it and keeps its history, as in Camunda: %v", state)
	}
	if status, _ := e.call(alice, http.MethodDelete, "/engine/v2/process-instances-by-business-key/wardened:c", nil, nil); status != http.StatusOK || e.instance("c").State != model.StateTerminated {
		t.Errorf("stop by business key, with the prefix of process-sync: %v", status)
	}
	if status, _ := e.call(alice, http.MethodDelete, "/engine/v2/process-instances-by-business-key/unknown", nil, nil); status != http.StatusOK {
		t.Errorf("an unknown business key is no error: %v", status)
	}
	var byKey []struct{ Id, State string }
	if e.call(alice, http.MethodGet, "/engine/v2/history/process-instances?processInstanceBusinessKey=b", nil, &byKey); len(byKey) != 1 || byKey[0].State != "EXTERNALLY_TERMINATED" {
		t.Errorf("history by business key: %+v", byKey)
	}
	if status, _ := e.call(alice, http.MethodDelete, "/engine/v2/history/process-instances", []string{"a", "b"}, nil); status != http.StatusOK {
		t.Fatal(status)
	}
	if _, err := e.srv.Controller.GetInstance(ctx, alice, "processes", "a"); err == nil {
		t.Error("deleting the history removes the instance")
	}
}

func TestProcessRepositoryOfTheDesigner(t *testing.T) {
	e := newEnv(t)
	type process struct {
		Id          string `json:"_id"`
		Name        string `json:"name"`
		Owner       string `json:"owner"`
		BpmnXml     string `json:"bpmn_xml"`
		SvgXml      string `json:"svgXML"`
		Publish     bool   `json:"publish"`
		Description string `json:"description"`
	}
	var created process
	status, _ := e.call(alice, http.MethodPost, "/repository/processes", map[string]any{"name": "Heating", "bpmn_xml": waitingProcess(), "svgXML": "<svg/>"}, &created)
	if status != http.StatusOK || created.Id == "" || created.Owner != "alice" || created.SvgXml != "<svg/>" {
		t.Fatalf("%v %+v", status, created)
	}
	if status, _ = e.call(alice, http.MethodPut, "/repository/processes/"+created.Id, map[string]any{"_id": created.Id, "name": "Heating 2", "bpmn_xml": waitingProcess(), "svgXML": "<svg>2</svg>"}, &created); status != http.StatusOK || created.Name != "Heating 2" || created.SvgXml != "<svg>2</svg>" {
		t.Fatalf("update: %v %+v", status, created)
	}
	e.call(bob, http.MethodPost, "/repository/processes", map[string]any{"name": "Other", "bpmn_xml": waitingProcess(), "svgXML": "<svg/>"}, nil)
	var list []process
	status, header := e.call(alice, http.MethodGet, "/repository/v2/processes?limit=10&offset=0&sort=name.asc&search=heat&owner=alice", nil, &list)
	if status != http.StatusOK || len(list) != 1 || header.Get("X-Total-Count") != "1" || list[0].BpmnXml == "" {
		t.Fatalf("list with total and content: %v %+v %v", status, list, header)
	}
	if e.call(alice, http.MethodGet, "/repository/v2/processes?not-owner=alice", nil, &list); len(list) != 0 {
		t.Errorf("models of others are only those shared with alice: %+v", list)
	}
	if e.call(alice, http.MethodPost, "/repository/processes/"+created.Id+"/publish", map[string]any{"publish": true, "description": "for all"}, &created); !created.Publish || created.Description != "for all" {
		t.Errorf("publish: %+v", created)
	}
	if status, _ = e.call(alice, http.MethodDelete, "/repository/processes/"+created.Id, nil, nil); status != http.StatusOK {
		t.Fatal(status)
	}
	if status, _ = e.call(alice, http.MethodGet, "/repository/processes/"+created.Id, nil, nil); status != http.StatusNotFound {
		t.Errorf("deleted: %v", status)
	}
}

func TestProcessIoOfUsers(t *testing.T) {
	e := newEnv(t)
	if status, _ := e.call(alice, http.MethodPut, "/io/variables/limit", map[string]any{"key": "limit", "value": 15}, nil); status != http.StatusNoContent {
		t.Fatal(status)
	}
	e.call(alice, http.MethodPut, "/io/values/mode", "eco", nil)
	var v struct {
		Key       string
		Value     any
		Timestamp int64 `json:"unix_timestamp_in_s"`
	}
	if e.call(alice, http.MethodGet, "/io/variables/limit", nil, &v); v.Value != 15.0 || v.Timestamp == 0 {
		t.Errorf("%+v", v)
	}
	if e.call(alice, http.MethodGet, "/io/variables/missing", nil, &v); v.Key != "missing" || v.Value != nil || v.Timestamp != 0 {
		t.Errorf("a missing key is a null value: %+v", v)
	}
	var raw any
	if e.call(alice, http.MethodGet, "/io/values/mode", nil, &raw); raw != "eco" {
		t.Errorf("%v", raw)
	}
	var list []struct{ Key string }
	if e.call(alice, http.MethodGet, "/io/variables?sort=key.desc&key_regex=^m", nil, &list); len(list) != 1 || list[0].Key != "mode" {
		t.Errorf("%+v", list)
	}
	var count struct{ Count int }
	if e.call(alice, http.MethodGet, "/io/count/variables", nil, &count); count.Count != 2 {
		t.Errorf("%+v", count)
	}
	var bulk []struct {
		Key   string
		Value any
	}
	e.call(alice, http.MethodPost, "/io/bulk", map[string]any{"get": []string{"limit", "x"}, "set": []map[string]any{{"key": "x", "value": true}}}, &bulk)
	if len(bulk) != 2 || bulk[0].Value != 15.0 || bulk[1].Value != true {
		t.Errorf("bulk sets, then gets: %+v", bulk)
	}
	if status, _ := e.call(alice, http.MethodDelete, "/io/variables/limit", nil, nil); status != http.StatusNoContent {
		t.Error(status)
	}
	if e.call(bob, http.MethodGet, "/io/variables", nil, &list); len(list) != 0 {
		t.Errorf("values belong to their user: %+v", list)
	}
}

func TestIncidentsOfTheMonitor(t *testing.T) {
	e := newEnv(t)
	d := e.deploy(b.Process(b.Start("start"), b.Script("fail", "x", "throw new Error('broken')"), b.End("end"), b.Flow("start", "fail"), b.Flow("fail", "end")))
	var started struct{ Id string }
	e.call(alice, http.MethodGet, "/engine/v2/deployments/"+d.Id+"/start", nil, &started)
	e.waitFor("the incident", func() bool { return e.instance(started.Id).State == model.StateIncident })
	var incidents []struct {
		ProcessInstanceId   string `json:"process_instance_id"`
		ProcessDefinitionId string `json:"process_definition_id"`
		DeploymentName      string `json:"deployment_name"`
		ErrorMessage        string `json:"error_message"`
		TenantId            string `json:"tenant_id"`
	}
	e.call(alice, http.MethodGet, "/incidents/incidents?process_instance_id="+started.Id+"&sort=time.desc&limit=5", nil, &incidents)
	if len(incidents) != 1 || incidents[0].DeploymentName != "Lights" || !strings.Contains(incidents[0].ErrorMessage, "broken") || incidents[0].TenantId != "alice" || !strings.HasPrefix(incidents[0].ProcessDefinitionId, "deplid_") {
		t.Errorf("%+v", incidents)
	}
}

func TestSchedulesOfWidgets(t *testing.T) {
	e := newEnv(t)
	d := e.deploy(waitingProcess())
	var entry struct {
		Id                  string
		Cron                string
		ProcessDeploymentId string  `json:"process_deployment_id"`
		CreatedBy           *string `json:"created_by"`
		Disabled            *bool
	}
	status, _ := e.call(alice, http.MethodPost, "/scheduler/schedules", map[string]any{"cron": "0 7 * * *", "process_deployment_id": d.Id, "created_by": "widget-1", "disabled": true}, &entry)
	if status != http.StatusOK || entry.Id == "" || entry.CreatedBy == nil || *entry.CreatedBy != "widget-1" || !*entry.Disabled {
		t.Fatalf("%v %+v", status, entry)
	}
	e.call(alice, http.MethodPost, "/scheduler/schedules", map[string]any{"cron": "0 8 * * *", "process_deployment_id": d.Id}, nil)
	var list []struct{ Id string }
	if e.call(alice, http.MethodGet, "/scheduler/schedules?created_by=widget-1", nil, &list); len(list) != 1 || list[0].Id != entry.Id {
		t.Errorf("the schedules of a widget: %+v", list)
	}
	if status, _ = e.call(alice, http.MethodPut, "/scheduler/schedules/"+entry.Id, map[string]any{"cron": "0 9 * * *", "process_deployment_id": d.Id, "created_by": "widget-1"}, &entry); status != http.StatusOK || entry.Cron != "0 9 * * *" || *entry.Disabled {
		t.Errorf("update: %v %+v", status, entry)
	}
	if status, _ = e.call(alice, http.MethodDelete, "/scheduler/schedules/unknown", nil, nil); status != http.StatusOK {
		t.Errorf("deleting an unknown schedule succeeds, as before: %v", status)
	}
}

func TestRequestsAreCountedPerRoute(t *testing.T) {
	e := newEnv(t)
	e.call(alice, http.MethodGet, "/engine/v2/process-instances/count", nil, nil)
	recorder := httptest.NewRecorder()
	e.srv.Metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(recorder.Body.String(), `automation_engine_deprecated_requests_total{route="GET /deprecated/engine/v2/process-instances/count"} 1`) {
		t.Errorf("the metric shows when the layer can go (API-03)")
	}
}

// The switch of the deployment list of the web-ui, as camunda-engine-wrapper v1.1.0 offered it.
func TestWrapperSwitchesDeploymentsOffAndOn(t *testing.T) {
	e := newEnv(t)
	d := e.deploy(waitingProcess())
	var answer struct{ Disabled bool }
	if status, _ := e.call(alice, http.MethodPut, "/engine/v2/deployments/"+d.Id+"/disabled", map[string]any{"disabled": true}, &answer); status != http.StatusOK || !answer.Disabled {
		t.Fatalf("%v %+v", status, answer)
	}
	var list []struct {
		Id       string
		Disabled bool
	}
	if e.call(alice, http.MethodGet, "/engine/v2/deployments", nil, &list); len(list) != 1 || !list[0].Disabled {
		t.Errorf("the list shows the state: %+v", list)
	}
	if status, _ := e.call(alice, http.MethodGet, "/engine/v2/deployments/"+d.Id+"/start", nil, nil); status != http.StatusConflict {
		t.Errorf("a disabled deployment answers a start with 409: %v", status)
	}
	if status, _ := e.call(alice, http.MethodPut, "/engine/v2/deployments/"+d.Id+"/disabled", map[string]any{}, nil); status != http.StatusBadRequest {
		t.Errorf("the field is required: %v", status)
	}
	e.call(alice, http.MethodPut, "/engine/v2/deployments/"+d.Id+"/disabled", map[string]any{"disabled": false}, nil)
	if status, _ := e.call(alice, http.MethodGet, "/engine/v2/deployments/"+d.Id+"/start", nil, nil); status != http.StatusOK {
		t.Errorf("switched on, it starts: %v", status)
	}
}
