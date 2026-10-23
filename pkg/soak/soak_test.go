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

// Package soak_test runs a gateway engine under steady load, connected to a cloud engine, and
// records how its resources develop over time (LONG-15). Without SOAK_DURATION it is a short
// smoke run in the normal suite; on a gateway it runs as a cross-compiled test binary, see the
// README.
package soak_test

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	b "github.com/SENERGY-Platform/automation-engine/pkg/bpmn/bpmntest"
	"github.com/SENERGY-Platform/automation-engine/pkg/client"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
	"github.com/SENERGY-Platform/automation-engine/pkg/permissions"
	"github.com/SENERGY-Platform/automation-engine/pkg/server"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/tasks/devicecommand"
	"github.com/SENERGY-Platform/go-service-base/config-hdl/types"
	"github.com/SENERGY-Platform/models/go/models"
	permmodel "github.com/SENERGY-Platform/permissions-v2/pkg/model"
)

const network = "soak-hub"
const measuring = "urn:infai:ses:measuring-function:temperature"

var alice = client.TestToken("alice")

// settings come from the environment, so that the cross-compiled binary needs no flags
type settings struct {
	duration time.Duration
	sample   time.Duration
	dir      string
	// empty uses a broker in the process; on a gateway its broker, e.g. tcp://localhost:1883
	broker, user, password string
	budget                 int64
	startsPerSecond        float64
	eventInterval          time.Duration
	// the connection to the cloud drops every offlineEvery for offlineFor; 0 never
	offlineEvery, offlineFor time.Duration
	report                   string
	// long runs check that resources settle; smoke runs only that the load completes
	long bool
}

func readSettings(t *testing.T) settings {
	duration, given := envDuration(t, "SOAK_DURATION", 5*time.Second)
	result := settings{
		duration:        duration,
		dir:             os.Getenv("SOAK_DIR"),
		broker:          os.Getenv("SOAK_MQTT_BROKER"),
		user:            os.Getenv("SOAK_MQTT_USER"),
		password:        os.Getenv("SOAK_MQTT_PASSWORD"),
		budget:          envInt(t, "SOAK_STORAGE_BUDGET", 32<<20),
		startsPerSecond: envFloat(t, "SOAK_STARTS_PER_SECOND", 2),
		long:            duration >= 30*time.Minute,
	}
	result.sample, _ = envDuration(t, "SOAK_SAMPLE_INTERVAL", min(max(duration/100, time.Second), time.Minute))
	result.eventInterval, _ = envDuration(t, "SOAK_EVENT_INTERVAL", 500*time.Millisecond)
	offlineEvery, offlineFor := duration/4, duration/40
	if !given {
		offlineEvery = 0
	}
	result.offlineEvery, _ = envDuration(t, "SOAK_OFFLINE_EVERY", offlineEvery)
	result.offlineFor, _ = envDuration(t, "SOAK_OFFLINE_FOR", offlineFor)
	if result.dir == "" {
		result.dir = t.TempDir()
	}
	result.report = os.Getenv("SOAK_REPORT")
	if result.report == "" {
		result.report = filepath.Join(result.dir, "soak.csv")
	}
	return result
}

func envDuration(t *testing.T, name string, fallback time.Duration) (time.Duration, bool) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, false
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		t.Fatalf("%v: %v", name, err)
	}
	return d, true
}

func envInt(t *testing.T, name string, fallback int64) int64 {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		t.Fatalf("%v: %v", name, err)
	}
	return n
}

func envFloat(t *testing.T, name string, fallback float64) float64 {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	n, err := strconv.ParseFloat(value, 64)
	if err != nil || n <= 0 {
		t.Fatalf("%v: a positive number is needed: %v", name, value)
	}
	return n
}

// repository answers like the device-repository mirror for one device with an event service and
// a request service
func repository() *httptest.Server {
	segment := events.LocalProtocol.ProtocolSegments[1].Id
	output := []models.Content{{ProtocolSegmentId: segment, Serialization: models.JSON, ContentVariable: models.ContentVariable{Name: "value", Type: models.Float, FunctionId: measuring, AspectId: "air"}}}
	answers := map[string]any{
		"/devices/d1": models.Device{Id: "d1", LocalId: "local-d1", DeviceTypeId: "dt"},
		"/device-types/dt": models.DeviceType{Id: "dt", Services: []models.Service{
			{Id: "s-event", LocalId: "temperature", ProtocolId: events.LocalProtocol.Id, Interaction: models.EVENT, Outputs: output},
			{Id: "s-get", LocalId: "get", ProtocolId: events.LocalProtocol.Id, Interaction: models.REQUEST, Outputs: output},
		}},
		"/protocols/" + events.LocalProtocol.Id: events.LocalProtocol,
		"/aspect-nodes/air":                     models.AspectNode{Id: "air", RootId: "air"},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if answer, ok := answers[r.URL.Path]; ok {
			_ = json.NewEncoder(w).Encode(answer)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
}

// marshaller passes the segment "data" through; the marshalling itself is not under test here
type marshaller struct{}

func (marshaller) MarshalV2(_ models.Service, _ models.Protocol, data []devicecommand.MarshallingV2RequestData) (map[string]string, error) {
	result := map[string]string{}
	for _, d := range data {
		raw, _ := json.Marshal(d.Value)
		result["data"] = string(raw)
	}
	return result, nil
}

func (marshaller) UnmarshalV2(request devicecommand.UnmarshallingV2Request) (any, error) {
	var v any
	err := json.Unmarshal([]byte(request.Message["data"]), &v)
	return v, err
}

func (this marshaller) MarshalFromServiceAndProtocol(_ string, service models.Service, protocol models.Protocol, data any, _ []devicecommand.Configurable) (map[string]string, error) {
	return this.MarshalV2(service, protocol, []devicecommand.MarshallingV2RequestData{{Value: data}})
}

func (this marshaller) UnmarshalFromServiceAndProtocol(_ string, _ models.Service, _ models.Protocol, message map[string]string, _ []string) (any, error) {
	return this.UnmarshalV2(devicecommand.UnmarshallingV2Request{Message: message})
}

type discard struct{}

func (discard) Produce(context.Context, string, string, []byte) error { return nil }

type unmarshaller struct{}

func (unmarshaller) Unmarshal(desc events.MessageDesc) (any, error) {
	return 25.0, nil
}

// workload waits for a device event, asks the device, waits a second and writes a variable of
// about 1 KiB, so that every instance touches events, commands, timers and history
func workload() string {
	payload := `{"version":2,"function":{"id":"` + measuring + `"},"characteristic_id":"c","device_id":"d1","service_id":"s-get","retries":1}`
	return b.Process(
		b.Start("start"),
		`<bpmn:intermediateCatchEvent id="reading" senergy:function="`+measuring+`" senergy:aspect="air" senergy:characteristic="c" senergy:script="x &gt; 20" senergy:value_variable_name="x"><bpmn:messageEventDefinition/></bpmn:intermediateCatchEvent>`,
		`<bpmn:serviceTask id="cmd" senergy:taskType="device_command" senergy:completionStrategy="pessimistic">`+b.IO(b.InText("payload", payload), b.OutScript("out", "result"))+`</bpmn:serviceTask>`,
		b.CatchTimer("pause", "timeDuration", "PT1S"),
		b.Script("work", "report", "JSON.stringify(out) + 'x'.repeat(1024)"),
		b.End("end"),
		b.Flow("start", "reading"), b.Flow("reading", "cmd"), b.Flow("cmd", "pause"), b.Flow("pause", "work"), b.Flow("work", "end"),
	)
}

type env struct {
	t        *testing.T
	settings settings
	cloud    *server.Server
	gateway  *server.Server
	// the broker of the gateway as the simulated devices and the bridge see it
	broker mqtt.Broker
	online atomic.Bool
	dbPath string
}

func newEnv(t *testing.T, ctx context.Context, s settings) *env {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	this := &env{t: t, settings: s, dbPath: filepath.Join(s.dir, "gateway.db")}
	this.online.Store(true)
	for _, name := range []string{"gateway.db", "gateway.db-wal", "gateway.db-shm", "cloud.db", "cloud.db-wal", "cloud.db-shm"} {
		_ = os.Remove(filepath.Join(s.dir, name))
	}
	repo := repository()
	t.Cleanup(repo.Close)

	cloudBroker := mqtt.NewMemory()
	cloudBroker.Discard = true
	var gatewayBroker mqtt.Broker
	if s.broker == "" {
		memory := mqtt.NewMemory()
		memory.Discard = true
		gatewayBroker = memory
	} else {
		connected, err := mqtt.Connect(ctx, mqtt.Config{Broker: s.broker, ClientId: "automation-engine-soak-devices", User: s.user, Password: s.password, CleanSession: true}, logger)
		if err != nil {
			t.Fatal(err)
		}
		gatewayBroker = connected
	}
	this.broker = gatewayBroker
	this.bridge(ctx, cloudBroker)

	config := configuration.Default()
	config.MinTimerDuration = "0s"
	config.SqlitePath = filepath.Join(s.dir, "cloud.db")
	config.DeviceRepositoryUrl = repo.URL
	deps, err := client.TestDependencies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	admin := auth.UnsignedToken("device-repository", "admin")
	if _, err, _ = deps.Permissions.SetTopic(admin, permmodel.Topic{Id: permissions.HubsTopic}); err != nil {
		t.Fatal(err)
	}
	full := map[string]permmodel.PermissionsMap{"alice": {Read: true, Write: true, Execute: true, Administrate: true}}
	for topic, id := range map[string]string{permissions.HubsTopic: network, "devices": "d1"} {
		if _, err, _ = deps.Permissions.SetPermission(admin, topic, id, permmodel.ResourcePermissions{UserPermissions: full}); err != nil {
			t.Fatal(err)
		}
	}
	// the cloud validates device commands of gateway deployments but sends none
	deps.SyncBroker, deps.Marshaller, deps.Producer = cloudBroker, marshaller{}, discard{}
	if this.cloud, err = server.New(ctx, config, logger, deps); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = this.cloud.Close() })
	this.cloud.Start(ctx)

	gatewayConfig := configuration.Default()
	gatewayConfig.MinTimerDuration = "0s"
	gatewayConfig.Mode = "fog"
	gatewayConfig.SqlitePath = this.dbPath
	gatewayConfig.StorageBudget = configuration.ByteSize(s.budget)
	gatewayConfig.DeviceRepositoryUrl = repo.URL
	gatewayConfig.MqttBroker, gatewayConfig.MqttClientId, gatewayConfig.MqttUser = "memory", "automation-engine-soak", s.user
	gatewayDeps := server.Dependencies{Marshaller: marshaller{}, EventUnmarshaller: unmarshaller{}}
	if s.broker == "" {
		gatewayDeps.Broker = gatewayBroker
	} else {
		gatewayConfig.MqttBroker = s.broker
		gatewayConfig.MqttPassword = types.Secret(s.password)
	}
	if this.gateway, err = server.New(ctx, gatewayConfig, logger, gatewayDeps); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = this.gateway.Close() })
	this.gateway.Start(ctx)
	this.devices(ctx)
	return this
}

// bridge forwards like mgw-device-cloud-connector, and drops everything while offline
func (this *env) bridge(ctx context.Context, cloudBroker *mqtt.Memory) {
	forward := func(to mqtt.Broker, topic string, payload []byte) {
		if this.online.Load() {
			_ = to.Publish(ctx, topic, 1, payload)
		}
	}
	_ = cloudBroker.Subscribe("processes/"+network+"/cmd/#", 1, func(topic string, payload []byte) error {
		go forward(this.broker, "processes/cmd/"+strings.TrimPrefix(topic, "processes/"+network+"/cmd/"), payload)
		return nil
	})
	_ = this.broker.Subscribe("processes/state/#", 1, func(topic string, payload []byte) error {
		go forward(cloudBroker, "processes/"+network+"/state/"+strings.TrimPrefix(topic, "processes/state/"), payload)
		return nil
	})
}

// devices answers commands like a device connector and sends events of d1
func (this *env) devices(ctx context.Context) {
	err := this.broker.Subscribe("command/local-d1/#", 2, func(topic string, payload []byte) error {
		var command struct {
			CommandId string `json:"command_id"`
		}
		if json.Unmarshal(payload, &command) != nil {
			return nil
		}
		answer, _ := json.Marshal(map[string]string{"command_id": command.CommandId, "data": "21.5"})
		go func() { _ = this.broker.Publish(ctx, "response/"+strings.TrimPrefix(topic, "command/"), 2, answer) }()
		return nil
	})
	if err != nil {
		this.t.Fatal(err)
	}
	go func() {
		ticker := time.NewTicker(this.settings.eventInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = this.broker.Publish(ctx, events.LocalEventTopic("local-d1", "temperature"), 0, []byte("25.5"))
			}
		}
	}()
}

type sample struct {
	elapsed                      time.Duration
	online                       bool
	dbBytes, walBytes, usedBytes int64
	heapBytes                    uint64
	goroutines, fds              int
	started, finished, running   int
	pending, incidents           int
	gatewayInstances, activities int
	upBacklog                    int64
	downBacklog                  int
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// openFiles counts the file descriptors of the process; -1 where /proc is missing
func openFiles() int {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return -1
	}
	return len(entries)
}

func (this *env) count(ctx context.Context, state string) int {
	page, err := this.cloud.Controller.ListInstances(ctx, alice, "processes", model.InstanceListOptions{State: state, NetworkId: network, PageOptions: model.PageOptions{Limit: 1}})
	if err != nil {
		this.t.Logf("unable to count %v instances: %v", state, err)
		return -1
	}
	return page.Total
}

func (this *env) sample(ctx context.Context, elapsed time.Duration, started int) sample {
	result := sample{elapsed: elapsed, online: this.online.Load(), started: started, dbBytes: fileSize(this.dbPath), walBytes: fileSize(this.dbPath + "-wal"), fds: openFiles(), goroutines: runtime.NumGoroutine()}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	result.heapBytes = memory.HeapAlloc
	err := this.gateway.Store.Tx(ctx, func(tx *store.Tx) (err error) {
		if result.usedBytes, err = tx.UsedBytes(); err != nil {
			return err
		}
		rows, err := tx.StorageRows()
		result.gatewayInstances, result.activities, result.incidents = rows["instances"], rows["activities"], rows["incidents"]
		return err
	})
	if err != nil {
		this.t.Logf("unable to read the gateway: %v", err)
	}
	result.finished, result.running, result.pending = this.count(ctx, "finished"), this.count(ctx, "running"), this.count(ctx, "pending")
	if networks, err := this.cloud.Controller.ListNetworks(ctx, alice); err == nil && len(networks) == 1 {
		result.upBacklog, result.downBacklog = networks[0].UpBacklog, networks[0].DownBacklog
	}
	return result
}

var header = []string{"elapsed_s", "online", "db_bytes", "wal_bytes", "used_bytes", "heap_bytes", "goroutines", "open_files", "started", "finished", "running", "pending", "gateway_incidents", "gateway_instances", "gateway_activities", "up_backlog", "down_backlog"}

func (this sample) record() []string {
	return []string{
		strconv.Itoa(int(this.elapsed.Seconds())), strconv.FormatBool(this.online), strconv.FormatInt(this.dbBytes, 10), strconv.FormatInt(this.walBytes, 10), strconv.FormatInt(this.usedBytes, 10),
		strconv.FormatUint(this.heapBytes, 10), strconv.Itoa(this.goroutines), strconv.Itoa(this.fds), strconv.Itoa(this.started), strconv.Itoa(this.finished), strconv.Itoa(this.running),
		strconv.Itoa(this.pending), strconv.Itoa(this.incidents), strconv.Itoa(this.gatewayInstances), strconv.Itoa(this.activities), strconv.FormatInt(this.upBacklog, 10), strconv.Itoa(this.downBacklog),
	}
}

func TestSoak(t *testing.T) {
	s := readSettings(t)
	if s.duration > 10*time.Second && testing.Short() {
		t.Skip("long soak run in short mode")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := newEnv(t, ctx, s)
	deployment, err := e.cloud.Controller.CreateDeployment(ctx, alice, "processes", model.DeploymentInput{
		Name: "soak", Xml: workload(), NetworkId: network, Selections: map[string]model.Selection{"reading": {DeviceId: "d1", ServiceId: "s-event"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := os.Create(s.report)
	if err != nil {
		t.Fatal(err)
	}
	defer report.Close()
	out := csv.NewWriter(report)
	_ = out.Write(header)
	t.Logf("soak run of %v, report in %v", s.duration, s.report)

	var started atomic.Int64
	var load sync.WaitGroup
	loadCtx, stopLoad := context.WithTimeout(ctx, s.duration)
	defer stopLoad()
	load.Add(1)
	go func() {
		defer load.Done()
		ticker := time.NewTicker(time.Duration(float64(time.Second) / s.startsPerSecond))
		defer ticker.Stop()
		for {
			select {
			case <-loadCtx.Done():
				return
			case <-ticker.C:
				if _, err := e.cloud.Controller.StartInstance(loadCtx, alice, "processes", model.StartInstanceInput{DeploymentId: deployment.Id}); err != nil {
					if loadCtx.Err() == nil {
						t.Errorf("unable to start an instance: %v", err)
					}
					continue
				}
				started.Add(1)
			}
		}
	}()
	if s.offlineEvery > 0 && s.offlineFor > 0 {
		go func() {
			for {
				select {
				case <-loadCtx.Done():
					e.online.Store(true)
					return
				case <-time.After(s.offlineEvery):
				}
				e.online.Store(false)
				select {
				case <-loadCtx.Done():
				case <-time.After(s.offlineFor):
				}
				e.online.Store(true)
			}
		}()
	}

	begin := time.Now()
	var samples []sample
	take := func() sample {
		current := e.sample(ctx, time.Since(begin), int(started.Load()))
		samples = append(samples, current)
		_ = out.Write(current.record())
		out.Flush()
		return current
	}
	ticker := time.NewTicker(s.sample)
	for loadCtx.Err() == nil {
		select {
		case <-loadCtx.Done():
		case <-ticker.C:
			take()
		}
	}
	ticker.Stop()
	load.Wait()

	// after the load, every started instance completes and reaches the cloud (LONG-10)
	drain := time.Now().Add(max(2*time.Minute, s.offlineFor+time.Minute))
	last := take()
	for (last.running != 0 || last.pending != 0) && time.Now().Before(drain) {
		time.Sleep(time.Second)
		last = take()
	}
	if last.running != 0 || last.pending != 0 {
		t.Errorf("instances left after the load: %v running, %v pending", last.running, last.pending)
	}
	if last.started == 0 || last.finished == 0 {
		t.Fatalf("the load did not run: %+v", last)
	}
	for _, current := range samples {
		if current.incidents > 0 {
			t.Errorf("incidents on the gateway at %v: %v", current.elapsed, current.incidents)
			break
		}
	}
	if incidents := e.count(ctx, string(model.StateIncident)); incidents > 0 {
		t.Errorf("instances in incident: %v", incidents)
	}
	t.Logf("%v instances started, %v finished, database %v bytes, heap %v bytes, %v goroutines", last.started, last.finished, last.dbBytes, last.heapBytes, last.goroutines)
	if s.long {
		checkSettled(t, s, samples)
	}
}

// checkSettled compares the second quarter of the run with the last: after the warmup the
// database file, heap, goroutines and file descriptors do not keep growing (LONG-06), and the
// used space stays within the budget (LONG-07). The used space may exceed the budget for a
// while: the budget is enforced once per housekeeping round, and instances whose end the cloud
// has not taken stay after an offline phase. Hence the median, not the peak.
func checkSettled(t *testing.T, s settings, samples []sample) {
	quarter := len(samples) / 4
	if quarter < 2 {
		t.Errorf("too few samples to judge the run: %v", len(samples))
		return
	}
	peak := func(part []sample, value func(sample) float64) float64 {
		result := 0.0
		for _, current := range part {
			result = max(result, value(current))
		}
		return result
	}
	settled, final := samples[quarter:2*quarter], samples[len(samples)-quarter:]
	checks := []struct {
		name   string
		value  func(sample) float64
		factor float64
		slack  float64
	}{
		{"database file", func(s sample) float64 { return float64(s.dbBytes) }, 1.25, 1 << 20},
		{"heap", func(s sample) float64 { return float64(s.heapBytes) }, 1.5, 8 << 20},
		{"goroutines", func(s sample) float64 { return float64(s.goroutines) }, 1.2, 20},
		{"open files", func(s sample) float64 { return float64(s.fds) }, 1.2, 10},
	}
	for _, check := range checks {
		before, after := peak(settled, check.value), peak(final, check.value)
		if after > before*check.factor+check.slack {
			t.Errorf("%v keeps growing: peak %v after the warmup, %v at the end", check.name, before, after)
		}
	}
	if s.budget > 0 {
		used := make([]int64, 0, len(final))
		for _, current := range final {
			used = append(used, current.usedBytes)
		}
		slices.Sort(used)
		if median := used[len(used)/2]; median > s.budget {
			t.Errorf("the database uses more than the budget of %v bytes: median %v", s.budget, median)
		}
	}
}
