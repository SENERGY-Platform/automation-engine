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
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/migrate"
	"github.com/SENERGY-Platform/models/go/models"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func container(t *testing.T, gate string, request testcontainers.ContainerRequest, port string) string {
	t.Helper()
	if os.Getenv(gate) == "off" {
		t.Skip(gate + "=off")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: request, Started: true})
	if err != nil {
		t.Skipf("no container runtime: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	endpoint, err := c.PortEndpoint(ctx, port, "")
	if err != nil {
		t.Fatal(err)
	}
	return endpoint
}

// the documents as the old services wrote them, with the default codec of the driver: Go
// field names lower-cased, embedded structs as sub-documents
type oldDeploymentCommand struct {
	Command    string
	Id         string
	Owner      string
	Deployment *models.Deployment
	Source     string
	Version    int64
	SyncTodo   bool `bson:"sync_todo"`
	SyncDelete bool `bson:"sync_delete"`
}

type Variable struct {
	Key                 string
	Value               any
	ProcessDefinitionId string
	ProcessInstanceId   string
}

type VariableWithUnixTimestamp struct {
	Variable
	UnixTimestampInS int64
}

type oldIoVariable struct {
	VariableWithUnixTimestamp
	UserId string
}

// The readers read the collections as the services wrote them (MIG-01).
func TestMongoReaderReadsTheDocumentsOfTheOldServices(t *testing.T) {
	endpoint := container(t, "AE_TEST_MONGO", testcontainers.ContainerRequest{Image: "mongo:7", ExposedPorts: []string{"27017/tcp"},
		WaitingFor: wait.ForLog("Waiting for connections").WithStartupTimeout(2 * time.Minute)}, "27017/tcp")
	url := "mongodb://" + endpoint
	client, err := mongo.Connect(options.Client().ApplyURI(url))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(ctx)
	insert := func(db string, collection string, docs ...any) {
		if _, err := client.Database(db).Collection(collection).InsertMany(ctx, docs); err != nil {
			t.Fatal(err)
		}
	}
	device := "d1"
	insert("process_repository", "process", map[string]any{"_id": "m1", "name": "Heating", "owner": "alice", "bpmn_xml": "<x/>", "svgXML": "<svg/>", "publish": true, "date": int64(1700000000000), "last_updated_unix": int64(1700000000)})
	insert("process_deployment", "deployments",
		oldDeploymentCommand{Command: "PUT", Id: "dep-1", Owner: "alice", Source: "sepl", Version: 3, Deployment: &models.Deployment{Version: 3, Id: "dep-1", Name: "Heating",
			Diagram: models.Diagram{XmlRaw: "<raw/>", Svg: "<svg/>"},
			Elements: []models.Element{{BpmnId: "task", Task: &models.Task{Selection: models.Selection{SelectedDeviceId: &device,
				SelectedPath: &models.PathOption{Path: "value", Value: map[string]any{"unit": "°C"}}}}}}}},
		oldDeploymentCommand{Command: "PUT", Id: "dep-deleted", Owner: "alice", Version: 3, SyncDelete: true, Deployment: &models.Deployment{Version: 3}})
	insert("process_scheduler", "process_schedule", map[string]any{"id": "s1", "user": "alice", "cron": "@daily", "process_deployment_id": "dep-1", "created_by": "w1"})
	insert("process_io", "variables", oldIoVariable{VariableWithUnixTimestamp: VariableWithUnixTimestamp{Variable: Variable{Key: "limit", Value: int32(20), ProcessInstanceId: "ci-1"}, UnixTimestampInS: 1700000000}, UserId: "alice"})

	reader, err := migrate.OpenMongo(ctx, url, migrate.DefaultMongoDatabases())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close(ctx)
	processModels, err := reader.ProcessModels(ctx)
	if err != nil || len(processModels) != 1 || processModels[0].SvgXml != "<svg/>" || !processModels[0].Publish {
		t.Errorf("models: %+v %v", processModels, err)
	}
	deployments, err := reader.ProcessDeployments(ctx)
	if err != nil || len(deployments) != 1 || deployments[0].Deployment.Diagram.XmlRaw != "<raw/>" || deployments[0].Source != "sepl" {
		t.Fatalf("deployments without the deleted ones: %+v %v", deployments, err)
	}
	path := deployments[0].Deployment.Elements[0].Task.Selection.SelectedPath
	if raw, _ := json.Marshal(path.Value); string(raw) != `{"unit":"°C"}` {
		t.Errorf("documents in interface fields read as maps: %s", raw)
	}
	schedules, err := reader.Schedules(ctx)
	if err != nil || len(schedules) != 1 || schedules[0].CreatedBy != "w1" {
		t.Errorf("schedules: %+v %v", schedules, err)
	}
	values, err := reader.IoValues(ctx)
	if err != nil || len(values) != 1 || values[0].Key != "limit" || values[0].Value != 20.0 || values[0].InstanceId != "ci-1" || values[0].UserId != "alice" || values[0].Timestamp != 1700000000 {
		t.Errorf("io values from the nested documents: %+v %v", values, err)
	}
}

// The Camunda reader lists deployments with key and BPMN and running instances with their
// variables, as the platform's engine has them (MIG-02, MIG-03).
func TestCamundaReaderReadsDeploymentsAndRunningInstances(t *testing.T) {
	endpoint := container(t, "AE_TEST_CAMUNDA", testcontainers.ContainerRequest{Image: "camunda/camunda-bpm-platform:tomcat-7.24.0", ExposedPorts: []string{"8080/tcp"},
		WaitingFor: wait.ForHTTP("/engine-rest/engine").WithPort("8080/tcp").WithStartupTimeout(3 * time.Minute)}, "8080/tcp")
	base := "http://" + endpoint + "/engine-rest"
	key := "deplid_4a7e3b0c_1d2e_4f56_8a9b_0c1d2e3f4a5b"
	xml := strings.Replace(b.Process(b.Message("m", "go"), b.StartWithForm("start", "level=5"), b.CatchMessage("wait", "m"), b.End("end"),
		b.Flow("start", "wait"), b.Flow("wait", "end")), `isExecutable="true"`, `isExecutable="true" camunda:historyTimeToLive="1"`, 1)
	xml = strings.NewReplacer(`id="test_process" name="test process"`, `id="`+key+`" name="Heating"`, `id="Definitions_1">`, `id="Definitions_1" targetNamespace="t">`).Replace(xml)
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	_ = form.WriteField("deployment-name", "Heating")
	_ = form.WriteField("tenant-id", "alice")
	_ = form.WriteField("deployment-source", "sepl")
	file, _ := form.CreateFormFile("data", "Heating.bpmn")
	_, _ = file.Write([]byte(xml))
	_ = form.Close()
	resp, err := http.Post(base+"/deployment/create", form.FormDataContentType(), &buf)
	if err != nil || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("deploy: %v %s", err, body)
	}
	start, _ := json.Marshal(map[string]any{"businessKey": "wardened:k1", "variables": map[string]any{
		"level": map[string]any{"value": "9", "type": "String"}, "count": map[string]any{"value": 3, "type": "Integer"},
		"data": map[string]any{"value": `{"a":1}`, "type": "Json"},
	}})
	resp, err = http.Post(base+"/process-definition/key/"+key+"/tenant-id/alice/start", "application/json", bytes.NewReader(start))
	if err != nil || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("start: %v %s", err, body)
	}

	req, _ := http.NewRequest(http.MethodPut, base+"/process-definition/key/"+key+"/tenant-id/alice/suspended", strings.NewReader(`{"suspended":true,"includeProcessInstances":false}`))
	req.Header.Set("Content-Type", "application/json")
	if resp, err = http.DefaultClient.Do(req); err != nil || resp.StatusCode >= 300 {
		t.Fatalf("suspend: %v %v", resp, err)
	}
	reader := &migrate.Camunda{Urls: []string{base}}
	deployments, err := reader.CamundaDeployments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// the image brings the invoice example of Camunda besides
	var d migrate.CamundaDeployment
	for _, candidate := range deployments {
		if candidate.Key == key {
			d = candidate
		}
	}
	if d.Key != key || !d.Suspended || d.TenantId != "alice" || d.Source != "sepl" || d.Name != "Heating" || !strings.Contains(d.Bpmn, "level") || d.Time.IsZero() {
		t.Errorf("deployment: %+v", d)
	}
	instances, err := reader.RunningInstances(ctx, d)
	if err != nil || len(instances) != 1 {
		t.Fatalf("%+v %v", instances, err)
	}
	i := instances[0]
	data, _ := json.Marshal(i.Variables["data"])
	if i.StartTime.IsZero() || time.Since(i.StartTime) > time.Hour {
		t.Errorf("the start time comes from the history: %v", i.StartTime)
	}
	if i.BusinessKey != "wardened:k1" || i.TenantId != "alice" || i.Variables["level"] != "9" || i.Variables["count"] != 3.0 || string(data) != `{"a":1}` {
		t.Errorf("instance: %+v %v", i, fmt.Sprintf("%s", data))
	}
}

// as process-sync (master) wrote its metadata: Metadata and SyncInfo embedded without inline
type DeploymentWithEventDesc struct {
	models.Deployment `bson:",inline"`
	DeviceIdToLocalId map[string]string
}

type Metadata struct {
	CamundaDeploymentId string
	DeploymentModel     DeploymentWithEventDesc
}

type SyncInfo struct {
	NetworkId       string
	MarkedForDelete bool
	SyncDate        time.Time
}

type oldDeploymentMetadata struct {
	Metadata
	SyncInfo
}

func TestMongoReaderReadsSmartServicesAndProcessSync(t *testing.T) {
	endpoint := container(t, "AE_TEST_MONGO", testcontainers.ContainerRequest{Image: "mongo:7", ExposedPorts: []string{"27017/tcp"},
		WaitingFor: wait.ForLog("Waiting for connections").WithStartupTimeout(2 * time.Minute)}, "27017/tcp")
	url := "mongodb://" + endpoint
	client, err := mongo.Connect(options.Client().ApplyURI(url))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(ctx)
	insert := func(db string, collection string, docs ...any) {
		if _, err := client.Database(db).Collection(collection).InsertMany(ctx, docs); err != nil {
			t.Fatal(err)
		}
	}
	insert("smart_service_repository", "designs", map[string]any{"id": "ds1", "user_id": "alice", "name": "Room", "description": "d", "bpmn_xml": "<x/>", "svg_xml": "<svg/>", "updated_at": int64(5)})
	insert("smart_service_repository", "releases",
		map[string]any{"id": "r1", "design_id": "ds1", "name": "Room", "created_at": int64(7), "creator": "alice", "newreleaseid": "", "bpmn_xml": "<x/>", "svg_xml": "<svg/>", "marked_as_deleted": false},
		map[string]any{"id": "r-deleted", "design_id": "ds1", "marked_as_deleted": true})
	insert("smart_service_repository", "instances", map[string]any{"id": "si1", "user_id": "alice", "release_id": "r1", "name": "Kitchen", "description": "",
		"parameters": []map[string]any{{"id": "room", "value": "kitchen", "label": "Room", "valuelabel": "Kitchen"}}, "ready": true, "created_at": int64(9)})
	insert("smart_service_repository", "modules", map[string]any{"id": "pid.task", "user_id": "alice", "instance_id": "si1", "module_type": "export",
		"module_data": map[string]any{"n": int32(1)}, "keys": []string{"k"}, "delete_info": map[string]any{"url": "http://serving/instance/1", "user_id": "alice"}})
	insert("smart_service_repository", "variables", map[string]any{"instance_id": "si1", "user_id": "alice", "name": "counter", "value": int64(3)})
	insert("process_sync", "deployment_metadata",
		oldDeploymentMetadata{Metadata: Metadata{CamundaDeploymentId: "cam-1", DeploymentModel: DeploymentWithEventDesc{Deployment: models.Deployment{Version: 3, Id: "fd1", Name: "Fog", Diagram: models.Diagram{XmlRaw: "<raw/>"}}}}, SyncInfo: SyncInfo{NetworkId: "hub-1"}},
		oldDeploymentMetadata{Metadata: Metadata{CamundaDeploymentId: "cam-2", DeploymentModel: DeploymentWithEventDesc{Deployment: models.Deployment{Id: "fd2"}}}, SyncInfo: SyncInfo{NetworkId: "hub-1", MarkedForDelete: true}})
	insert("process_sync", "warden", map[string]any{"network_id": "hub-1", "business_key": "wardened:fk1", "process_deployment_id": "cam-1", "start_parameters": map[string]any{"level": "4"}})

	reader, err := migrate.OpenMongo(ctx, url, migrate.DefaultMongoDatabases())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close(ctx)
	designs, err := reader.Designs(ctx)
	if err != nil || len(designs) != 1 || designs[0].UserId != "alice" || designs[0].UpdatedAt != 5 {
		t.Errorf("designs: %+v %v", designs, err)
	}
	releases, err := reader.Releases(ctx)
	if err != nil || len(releases) != 1 || releases[0].Creator != "alice" || releases[0].CreatedAt != 7 {
		t.Errorf("releases without deleted ones: %+v %v", releases, err)
	}
	instances, err := reader.SmartServiceInstances(ctx)
	if err != nil || len(instances) != 1 || !instances[0].Ready || len(instances[0].Parameters) != 1 || instances[0].Parameters[0].Value != "kitchen" {
		t.Errorf("instances: %+v %v", instances, err)
	}
	modules, err := reader.Modules(ctx)
	if err != nil || len(modules) != 1 || modules[0].DeleteUrl != "http://serving/instance/1" || modules[0].Data["n"] != 1.0 || modules[0].Keys[0] != "k" {
		t.Errorf("modules: %+v %v", modules, err)
	}
	variables, err := reader.SmartServiceVariables(ctx)
	if err != nil || len(variables) != 1 || variables[0].Value != 3.0 {
		t.Errorf("variables: %+v %v", variables, err)
	}
	fog, err := reader.FogDeployments(ctx)
	if err != nil || len(fog) != 1 || fog[0].NetworkId != "hub-1" || fog[0].CamundaDeploymentId != "cam-1" || fog[0].Deployment.Id != "fd1" || fog[0].Deployment.Diagram.XmlRaw != "<raw/>" {
		t.Errorf("fog deployments from the nested metadata, without those marked for delete: %+v %v", fog, err)
	}
	starts, err := reader.FogStarts(ctx)
	if err != nil || len(starts) != 1 || starts[0].BusinessKey != "wardened:fk1" || starts[0].Parameters["level"] != "4" {
		t.Errorf("starts: %+v %v", starts, err)
	}
}
