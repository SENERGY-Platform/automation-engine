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

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// The containers variant of every scenario runs the engine against the real dependencies:
// Postgres, MongoDB, Kafka, permissions-v2, device-repository, import-repository and
// import-deploy, in the images the platform deploys. Two services stay stand-ins, as in the
// tests of other platform services: Keycloak, because the services do not verify token
// signatures behind Kong, and Rancher, where import-deploy would start the import containers.
// The containers start once per test binary and are shared; scenarios keep apart through a
// database of their own and resources with ids of their own. With -short, nothing starts.

const (
	mongoImage            = "mongo:7.0"
	kafkaImage            = "apache/kafka:3.9.1"
	permissionsV2Image    = "ghcr.io/senergy-platform/permissions-v2:dev"
	deviceRepositoryImage = "ghcr.io/senergy-platform/device-repository:dev"
	importRepositoryImage = "ghcr.io/senergy-platform/import-repository:dev"
	importDeployImage     = "ghcr.io/senergy-platform/import-deploy:dev"
)

// realPlatform holds the URLs of the running dependencies, as the engine reaches them
type realPlatform struct {
	kafka            string
	permissionsV2    string
	deviceRepository string
	importRepository string
	importDeploy     string
	keycloak         string
}

var platformContainers struct {
	once     sync.Once
	platform *realPlatform
	err      error
	stop     context.CancelFunc
	wg       sync.WaitGroup
}

func TestMain(m *testing.M) {
	code := m.Run()
	if platformContainers.stop != nil {
		platformContainers.stop()
		platformContainers.wg.Wait()
	}
	os.Exit(code)
}

// containers returns the running dependencies; with -short the test is skipped, and a
// dependency that does not start fails it, so that a missing Docker is not mistaken for a pass
func containers(t *testing.T) *realPlatform {
	t.Helper()
	if testing.Short() {
		t.Skip("the variant against the real dependencies in containers runs without -short")
	}
	platformContainers.once.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		platformContainers.stop = cancel
		start, startCancel := context.WithTimeout(ctx, 10*time.Minute)
		defer startCancel()
		platformContainers.platform, platformContainers.err = startPlatform(start, ctx, &platformContainers.wg)
	})
	if platformContainers.err != nil {
		t.Fatal("the dependencies did not start:", platformContainers.err)
	}
	return platformContainers.platform
}

// startPlatform starts the containers within start; they run until life is done
func startPlatform(start context.Context, life context.Context, wg *sync.WaitGroup) (*realPlatform, error) {
	provider, err := testcontainers.NewDockerProvider(testcontainers.DefaultNetwork("bridge"))
	if err != nil {
		return nil, err
	}
	// containers reach the host, and the host the advertised Kafka listener, through the
	// gateway of the bridge network
	gateway, err := provider.GetGatewayIP(start)
	if err != nil {
		return nil, err
	}
	result := &realPlatform{keycloak: serve(life, wg, "127.0.0.1:0", keycloakStandIn())}
	rancher := serve(life, wg, ":0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// import-deploy deploys and removes import containers here; nothing runs
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusCreated)
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	rancher = "http://" + gateway + ":" + rancher[strings.LastIndex(rancher, ":")+1:]

	var mongo string
	var group errorGroup
	group.run(func() (err error) {
		_, mongo, err = startContainer(start, life, wg, testcontainers.ContainerRequest{
			Image: mongoImage, ExposedPorts: []string{"27017/tcp"}, WaitingFor: wait.ForListeningPort("27017/tcp"),
		}, "27017/tcp")
		mongo = "mongodb://" + strings.TrimPrefix(mongo, "http://")
		return err
	})
	group.run(func() (err error) {
		result.kafka, err = startKafka(start, life, wg, gateway)
		return err
	})
	if err = group.wait(); err != nil {
		return nil, err
	}

	var permissionsV2 string
	result.permissionsV2, permissionsV2, err = startService(start, life, wg, permissionsV2Image, map[string]string{
		"MONGO_URL": mongo, "KAFKA_URL": result.kafka, "DEV_NOTIFIER_URL": "",
	})
	if err != nil {
		return nil, err
	}
	var deviceRepository string
	result.deviceRepository, deviceRepository, err = startService(start, life, wg, deviceRepositoryImage, map[string]string{
		"MONGO_URL": mongo, "KAFKA_URL": result.kafka, "PERMISSIONS_V2_URL": permissionsV2,
		"SKIP_DEVICE_GROUP_GENERATION_FROM_DEVICE": "true", "DISABLE_STRICT_VALIDATION_FOR_TESTING": "true",
	})
	if err != nil {
		return nil, err
	}
	var importRepository string
	result.importRepository, importRepository, err = startService(start, life, wg, importRepositoryImage, map[string]string{
		"MONGO_URL": mongo, "KAFKA_BOOTSTRAP": result.kafka, "PERMISSIONS_V2_URL": permissionsV2, "DEVICE_REPO_URL": deviceRepository,
	})
	if err != nil {
		return nil, err
	}
	result.importDeploy, _, err = startService(start, life, wg, importDeployImage, map[string]string{
		"MONGO_URL": mongo, "MONGO_REPL_SET": "false", "KAFKA_BOOTSTRAP": result.kafka, "IMPORT_REPO_URL": importRepository,
		"PERMISSION_V2_URL": permissionsV2, "DEPLOY_MODE": "rancher2", "RANCHER_URL": rancher + "/",
		"RANCHER_ACCESS_KEY": "access", "RANCHER_SECRET_KEY": "secret",
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// keycloakStandIn answers client credentials with a token of the engine with the admin role
// and token exchanges with a token of the requested user, both unsigned
func keycloakStandIn() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/token") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = r.ParseForm()
		token := auth.UnsignedToken(r.Form.Get("requested_subject"))
		if r.Form.Get("requested_subject") == "" {
			token = auth.UnsignedToken("automation-engine", "admin")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": strings.TrimPrefix(token, "Bearer "), "expires_in": 300, "token_type": "bearer"})
	})
}

// serve runs handler on address until life is done and returns its URL
func serve(life context.Context, wg *sync.WaitGroup, address string, handler http.Handler) string {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		panic(err)
	}
	srv := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
	srv.Start()
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-life.Done()
		srv.Close()
	}()
	return srv.URL
}

// startService starts a platform service that listens on 8080 and returns its URL for the
// host and for other containers
func startService(start context.Context, life context.Context, wg *sync.WaitGroup, image string, env map[string]string) (string, string, error) {
	return startContainer(start, life, wg, testcontainers.ContainerRequest{
		Image: image, Env: env, ExposedPorts: []string{"8080/tcp"}, AlwaysPullImage: true,
		WaitingFor: wait.ForListeningPort("8080/tcp").WithStartupTimeout(3 * time.Minute),
	}, "8080/tcp")
}

func startContainer(start context.Context, life context.Context, wg *sync.WaitGroup, request testcontainers.ContainerRequest, port string) (host string, internal string, err error) {
	c, err := testcontainers.GenericContainer(start, testcontainers.GenericContainerRequest{ContainerRequest: request, Started: true})
	if c != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-life.Done()
			_ = c.Terminate(context.Background())
		}()
	}
	if err != nil {
		return "", "", fmt.Errorf("%v: %w%v", request.Image, err, containerLog(c))
	}
	ip, err := c.ContainerIP(start)
	if err != nil {
		return "", "", err
	}
	mapped, err := c.MappedPort(start, port)
	if err != nil {
		return "", "", err
	}
	containerPort := strings.TrimSuffix(port, "/tcp")
	return "http://localhost:" + mapped.Port(), "http://" + ip + ":" + containerPort, nil
}

func containerLog(c testcontainers.Container) string {
	if c == nil {
		return ""
	}
	reader, err := c.Logs(context.Background())
	if err != nil {
		return ""
	}
	raw, _ := io.ReadAll(io.LimitReader(reader, 1<<14))
	return "\n" + string(raw)
}

// startKafka starts a broker that advertises the gateway address under a port the host
// chose, so that the host and the containers reach it under the same address
func startKafka(start context.Context, life context.Context, wg *sync.WaitGroup, gateway string) (string, error) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return "", err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	address := gateway + ":" + strconv.Itoa(port)
	_, _, err = startContainer(start, life, wg, testcontainers.ContainerRequest{
		Image:        kafkaImage,
		ExposedPorts: []string{"9092/tcp"},
		// the port the broker advertises has to be the one on the host
		HostConfigModifier: func(config *container.HostConfig) {
			config.PortBindings = network.PortMap{network.MustParsePort("9092/tcp"): {{HostPort: strconv.Itoa(port)}}}
		},
		WaitingFor: wait.ForLog("Kafka Server started").WithStartupTimeout(2 * time.Minute),
		Env: map[string]string{
			"KAFKA_NODE_ID":                                  "1",
			"CLUSTER_ID":                                     "IrzuggcFT-mWom7mj7PgtA",
			"KAFKA_PROCESS_ROLES":                            "controller,broker",
			"KAFKA_LISTENERS":                                "BROKER://:9092,CONTROLLER://:9093",
			"KAFKA_CONTROLLER_LISTENER_NAMES":                "CONTROLLER",
			"KAFKA_INTER_BROKER_LISTENER_NAME":               "BROKER",
			"KAFKA_LISTENER_SECURITY_PROTOCOL_MAP":           "CONTROLLER:PLAINTEXT,BROKER:PLAINTEXT",
			"KAFKA_ADVERTISED_LISTENERS":                     "BROKER://" + address,
			"KAFKA_CONTROLLER_QUORUM_VOTERS":                 "1@localhost:9093",
			"KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR":         "1",
			"KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR": "1",
			"KAFKA_TRANSACTION_STATE_LOG_MIN_ISR":            "1",
			"KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS":         "0",
			"KAFKA_NUM_PARTITIONS":                           "1",
		},
	}, "9092/tcp")
	if err != nil {
		return "", err
	}
	deadline := time.Now().Add(time.Minute)
	for {
		conn, err := kafka.DialContext(start, "tcp", address)
		if err == nil {
			_, err = conn.Brokers()
			_ = conn.Close()
		}
		if err == nil {
			return address, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("kafka at %v: %w", address, err)
		}
		time.Sleep(time.Second)
	}
}

// errorGroup runs functions concurrently and returns their errors together
type errorGroup struct {
	wg   sync.WaitGroup
	mux  sync.Mutex
	errs []error
}

func (this *errorGroup) run(fn func() error) {
	this.wg.Add(1)
	go func() {
		defer this.wg.Done()
		if err := fn(); err != nil {
			this.mux.Lock()
			this.errs = append(this.errs, err)
			this.mux.Unlock()
		}
	}()
}

func (this *errorGroup) wait() error {
	this.wg.Wait()
	return errors.Join(this.errs...)
}
