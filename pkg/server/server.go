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

// Package server wires store, engine, controller and HTTP API.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	runtimedebug "runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/api"
	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/controller"
	"github.com/SENERGY-Platform/automation-engine/pkg/debug"
	"github.com/SENERGY-Platform/automation-engine/pkg/deprecated"
	"github.com/SENERGY-Platform/automation-engine/pkg/devices"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/fogsync"
	"github.com/SENERGY-Platform/automation-engine/pkg/migrate/legacyio"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/modules"
	"github.com/SENERGY-Platform/automation-engine/pkg/mqtt"
	"github.com/SENERGY-Platform/automation-engine/pkg/namespaces"
	"github.com/SENERGY-Platform/automation-engine/pkg/notification"
	"github.com/SENERGY-Platform/automation-engine/pkg/permissions"
	"github.com/SENERGY-Platform/automation-engine/pkg/prepare"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/tasks/devicecommand"
	"github.com/SENERGY-Platform/automation-engine/pkg/tasks/httpcall"
	"github.com/SENERGY-Platform/automation-engine/pkg/tasks/processio"
	"github.com/SENERGY-Platform/automation-engine/pkg/tracing"
	devicerepositoryclient "github.com/SENERGY-Platform/device-repository/v3/lib/client"
	dsclient "github.com/SENERGY-Platform/device-selection/v2/pkg/client"
	permclient "github.com/SENERGY-Platform/permissions-v2/pkg/client"
	"github.com/SENERGY-Platform/service-commons/pkg/cache/invalidator"
	"github.com/SENERGY-Platform/service-commons/pkg/jwt"
	"github.com/SENERGY-Platform/service-commons/pkg/kafka"
)

type Server struct {
	Store       *store.Store
	Engine      *engine.Engine
	Controller  *controller.Controller
	Handler     http.Handler
	Namespaces  *namespaces.Registry
	Permissions *permissions.Service
	Metrics     *Metrics
	Live        *debug.Hub
	// removes the resources of deleted modules (SMS-06)
	ModuleRemover *modules.Remover
	// module task types and health checks (TASK-20 to TASK-29)
	Modules *modules.Runtime
	// nil if device commands are not configured
	DeviceCommands *devicecommand.Type
	// fog: the broker of the gateway and the event worker in the process (FOG-01, FOG-06)
	Broker mqtt.Broker
	Events *events.Worker
	// fog: the synchronisation with the cloud (SYNC-09)
	Sync *fogsync.Gateway
	// cloud: the synchronisation with gateways (SYNC-09)
	Networks *fogsync.Cloud
	config   configuration.Config
	logger   *slog.Logger
}

// Dependencies replace connections to other services, e.g. in tests.
type Dependencies struct {
	// a permissions-v2 client, e.g. permissions-v2's in-process test client
	Permissions permclient.Client
	AdminToken  auth.TokenSource
	// tokens of other users; the Keycloak token exchange if not given
	UserToken auth.UserTokenSource
	// device command dependencies; set together, they enable device commands without URLs
	DeviceRepository devices.Repository
	AspectNodes      *devices.AspectNodes
	Marshaller       devicecommand.Marshaller
	Timescale        devicecommand.Timescale
	Producer         devicecommand.Producer
	// event debugging: stored device messages and their unmarshalling (EVT-10, DBG-18)
	EventSeries       events.Series
	EventUnmarshaller events.Unmarshaller
	// fog: the broker of the gateway, e.g. mqtt.NewMemory() in tests
	Broker mqtt.Broker
	// cloud: the broker the gateways reach (SYNC-04), e.g. mqtt.NewMemory() in tests
	SyncBroker mqtt.Broker
}

func openStore(ctx context.Context, config configuration.Config) (*store.Store, error) {
	if config.Database == "postgres" {
		s, err := store.Open(ctx, store.Postgres, config.PostgresDsn.Value())
		if err == nil && config.PostgresMaxConns > 0 {
			s.SetMaxOpenConns(config.PostgresMaxConns)
		}
		return s, err
	}
	return store.Open(ctx, store.SQLite, config.SqlitePath)
}

func engineConfig(config configuration.Config) (engine.Config, error) {
	location, err := config.Location()
	if err != nil {
		return engine.Config{}, err
	}
	result := engine.DefaultConfig()
	result.Location = location
	result.MaxTokens = config.MaxTokens
	result.MaxVariableBytes = config.MaxVariableBytes
	result.MaxInstanceVariablesBytes = config.MaxInstanceVariablesBytes
	result.MaxActivities = config.MaxActivities
	result.ScriptTimeout = configuration.Duration(config.ScriptTimeout)
	result.MinTimerDuration = configuration.Duration(config.MinTimerDuration)
	result.TaskTimeout = configuration.Duration(config.TaskTimeout)
	result.TaskRetries = config.TaskRetries
	result.PollInterval = configuration.Duration(config.PollInterval)
	result.RetentionDefaultDays = config.RetentionDefaultDays
	result.RetentionMaxDays = config.RetentionMaxDays
	result.ModelVersionsKept = config.ModelVersionsKept
	result.StorageBudget = int64(config.StorageBudget)
	return result, nil
}

// deviceRepository builds the device-repository access with the user tokens and its aspect node
// resolver; nil if neither configured nor given
func deviceRepository(config configuration.Config, deps Dependencies, userToken auth.UserTokenSource) (devices.Repository, *devices.AspectNodes, error) {
	repo, aspects := deps.DeviceRepository, deps.AspectNodes
	if repo == nil {
		if config.DeviceRepositoryUrl == "" {
			return nil, nil, nil
		}
		timeout := time.Minute
		if config.DeviceCacheTimeout != "" && config.DeviceCacheTimeout != "-" {
			var err error
			if timeout, err = time.ParseDuration(config.DeviceCacheTimeout); err != nil {
				return nil, nil, fmt.Errorf("invalid device_cache_timeout: %w", err)
			}
		}
		iot, err := devices.NewRepository(config.DeviceRepositoryUrl, userToken, timeout)
		if err != nil {
			return nil, nil, err
		}
		repo = iot
	}
	if aspects == nil {
		aspects = devices.NewAspectNodes(config.DeviceRepositoryUrl)
	}
	return repo, aspects, nil
}

// New opens the store, applies migrations, loads the namespaces and builds engine, controller
// and handler of the engine role. Nothing runs yet; see Run.
func New(ctx context.Context, config configuration.Config, logger *slog.Logger, deps Dependencies) (*Server, error) {
	s, err := openStore(ctx, config)
	if err != nil {
		return nil, err
	}
	engineSettings, err := engineConfig(config)
	if err != nil {
		s.Close()
		return nil, err
	}

	permissionsClient, adminToken, userToken := deps.Permissions, deps.AdminToken, deps.UserToken
	if permissionsClient == nil && config.PermissionsV2Url != "" {
		permissionsClient = permclient.New(config.PermissionsV2Url)
	}
	if adminToken == nil && config.AuthEndpoint != "" {
		adminToken = auth.ClientCredentials(config.AuthEndpoint, config.AuthClientId, config.AuthClientSecret.Value())
	}
	if userToken == nil && config.AuthEndpoint != "" {
		userToken = auth.NewTokenExchange(config.AuthEndpoint, config.AuthClientId, config.AuthClientSecret.Value()).Token
	}
	fog := config.Mode == "fog"
	// the services of a gateway check no tokens; the mirror sets its own user (FOG-01)
	if userToken == nil && fog {
		userToken = auth.UnsignedTokens
	}
	broker := deps.Broker
	if broker == nil && fog {
		client, err := mqtt.Connect(ctx, mqtt.Config{Broker: config.MqttBroker, ClientId: config.MqttClientId, User: config.MqttUser, Password: config.MqttPassword.Value()}, logger)
		if err != nil {
			s.Close()
			return nil, err
		}
		broker = client
	}
	var authorizer permissions.Authorizer = permissions.OwnerOnly{}
	var service *permissions.Service
	var topics namespaces.Topics
	if permissionsClient != nil {
		if adminToken == nil || userToken == nil {
			s.Close()
			return nil, errors.New("permissions-v2 needs admin and user token sources, configure auth_endpoint")
		}
		service = permissions.New(permissionsClient, adminToken, userToken, nil, s, logger)
		authorizer, topics = service, service
	}
	registry, err := namespaces.New(ctx, s, config.Namespaces, topics, logger)
	if err != nil {
		s.Close()
		return nil, err
	}
	if service != nil {
		service.SetNamespaces(registry)
	} else {
		s.DisablePermissionRemovals()
	}

	e := engine.New(s, engineSettings, registry, logger)
	notifier := notification.New(config.NotifierUrl, config.DeveloperNotificationsUrl, logger)
	e.SetNotifier(notifier)
	m := NewMetrics(configuration.Duration(config.StalledAfter))
	e.SetMetrics(m)

	io := processio.New(s, processio.Limits{MaxValuesPerUser: config.MaxIoValuesPerUser, MaxValueBytes: config.MaxIoValueBytes})
	e.RegisterTaskType(processio.TaskType, processio.NewType(io))
	e.RegisterTaskType(notification.TaskType, notification.NewTask(notifier))
	httpCalls := httpcall.New(registry, config.Mode)
	if policy, local, _ := config.LocalHttpPolicy(); local {
		httpCalls.SetLocalPolicy(policy)
	}
	e.RegisterTaskType(httpcall.TaskType, httpCalls)
	moduleSettings := moduleSettings(config)
	runtime := modules.NewRuntime(s, moduleSettings, userToken, logger)

	repo, aspects, err := deviceRepository(config, deps, userToken)
	if err != nil {
		s.Close()
		return nil, err
	}
	commands, err := deviceCommands(ctx, config, deps, broker, repo, aspects, s, m, logger)
	if err != nil {
		s.Close()
		return nil, err
	}
	if commands != nil {
		commands.SetCompleter(e)
		e.RegisterTaskType(devicecommand.TaskType, commands)
	}

	var certs *jwt.KeycloakCertProvider
	if config.KeycloakCertUrl != "" {
		certs = &jwt.KeycloakCertProvider{CertUrl: config.KeycloakCertUrl}
	}
	// the controller verifies every token only with verify_tokens; the live stream verifies its own
	// regardless, see apiSettings
	var verifyAll *jwt.KeycloakCertProvider
	if config.VerifyTokens {
		verifyAll = certs
	}
	c := controller.New(s, e, registry, authorizer, verifyAll)
	var characteristics prepare.Characteristics
	if config.DeviceRepositoryUrl != "" {
		characteristics = devicerepositoryclient.NewClient(config.DeviceRepositoryUrl, nil)
	}
	c.SetParameters(prepare.NewDescriber(config.DeviceSelectionUrl, characteristics), userToken)
	runtime.AddProcessModules(c, registry)
	runtime.AddWatcher(c, httpCalls, modules.WatcherSettings{AllowGenericRequests: config.AllowGenericWatchRequests,
		MinInterval: configuration.Duration(config.WatcherMinInterval), DefaultInterval: configuration.Duration(config.WatcherDefaultInterval)})
	runtime.Register(e, moduleSettings)
	runtime.SetAdminToken(adminToken)
	c.SetIo(io)
	var selection dsclient.Client
	if config.DeviceSelectionUrl != "" {
		selection = dsclient.NewClient(config.DeviceSelectionUrl)
	}
	c.SetPreparation(
		prepare.New(selection, prepare.Config{GroupsForTasks: config.DeviceGroupsForTasks, GroupsForEvents: config.DeviceGroupsForEvents, ImportsForEvents: config.ImportsForEvents, ModifiedDevices: config.ModifiedDevices,
			DeviceRepositoryUrl: config.DeviceRepositoryUrl}),
		prepare.NewAccess(permissionsClient, config.ImportDeployUrl),
	)
	if repo != nil {
		var imports events.Imports
		if config.ImportDeployUrl != "" && config.ImportRepositoryUrl != "" {
			imports = events.NewImports(config.ImportDeployUrl, config.ImportRepositoryUrl)
		}
		describer := events.NewDescriber(repo, aspects, imports)
		if fog {
			describer = describer.Local()
		}
		c.SetDescriber(describer)
	}
	if service != nil && config.PermissionsKafkaTopic != "" && config.KafkaUrl != "" {
		service.PublishTo(config.PermissionsKafkaTopic)
	}
	dsn := ""
	if config.Database == "postgres" {
		dsn = config.PostgresDsn.Value()
	}
	series, unmarshaller := deps.EventSeries, deps.EventUnmarshaller
	if series == nil && config.TimescaleWrapperUrl != "" {
		series = events.NewTimescale(config.TimescaleWrapperUrl)
	}
	if series != nil && unmarshaller == nil && config.DeviceRepositoryUrl != "" {
		if unmarshaller, err = newUnmarshaller(config); err != nil {
			s.Close()
			return nil, err
		}
	}
	if series != nil && unmarshaller != nil {
		c.SetEventDebugging(series, events.NewEvaluator(unmarshaller), userToken)
	}
	var worker *events.Worker
	if fog && repo != nil {
		eventUnmarshaller := deps.EventUnmarshaller
		if eventUnmarshaller == nil {
			if eventUnmarshaller, err = newUnmarshaller(config); err != nil {
				s.Close()
				return nil, err
			}
		}
		workerConfig := events.DefaultWorkerConfig()
		workerConfig.Local = true
		worker = events.NewWorker(workerConfig, s, events.NewEvaluator(eventUnmarshaller), e, events.LocalConsumer(broker, 1, logger), logger)
	}
	var networks *fogsync.Cloud
	syncBroker := deps.SyncBroker
	if syncBroker == nil && config.SyncMqttBroker != "" {
		clientId := config.SyncMqttClientId
		if clientId == "" {
			// every replica has its own session; they share the messages of the gateways
			host, _ := os.Hostname()
			clientId = "automation-engine-" + host
		}
		client, err := mqtt.Connect(ctx, mqtt.Config{Broker: config.SyncMqttBroker, ClientId: clientId, User: config.SyncMqttUser, Password: config.SyncMqttPassword.Value(), CleanSession: true}, logger)
		if err != nil {
			s.Close()
			return nil, err
		}
		syncBroker = client
	}
	if syncBroker != nil && !fog {
		settings := fogsync.DefaultCloudSettings()
		settings.EngineVersion, settings.Group, settings.ProtocolWindow = Version(), config.SyncShareGroup, config.SyncProtocolWindow
		settings.ResendAfter, settings.OfflineAfter = configuration.Duration(config.SyncResendAfter), configuration.Duration(config.SyncOfflineAfter)
		settings.CleanupMaxAge = configuration.Duration(config.NetworkCleanupMaxAge)
		settings.CallTimeout = configuration.Duration(config.SyncCallTimeout)
		networks = fogsync.NewCloud(settings, s, syncBroker, logger)
		c.SetNetworks(networks)
	}
	var agent *fogsync.Gateway
	if fog {
		settings := fogsync.DefaultGatewaySettings()
		settings.EngineVersion, settings.StorageBudget = Version(), int64(config.StorageBudget)
		agent = fogsync.NewGateway(settings, s, e, registry, broker, logger)
		agent.SetLocal(c)
	}
	apiSettings := api.Settings{Gateway: fog, Cors: api.Cors{
		Origins: configuration.List(config.CorsAllowedOrigins),
		Headers: configuration.List(config.CorsAllowedHeaders),
	}}
	if certs != nil {
		apiSettings.VerifyToken = func(token string) error {
			_, err := jwt.ParseWithValidation(certs, token)
			return err
		}
	}
	if !fog {
		// the old APIs exist only in the cloud; Kong rewrites their paths (API-08)
		apiSettings.Deprecated = deprecated.New(c, m)
	}
	hub := debug.NewHub(s, dsn, logger)
	liveSettings := controller.DefaultLiveSettings()
	liveSettings.MaxPerUser = config.MaxLiveConnectionsPerUser
	c.SetLive(hub, liveSettings)
	// module removals of gateway resources reach the gateway through the controller (SMS-12)
	remover := modules.NewRemover(s, userToken, logger)
	remover.SetResources(c)
	remover.SetImportDeployUrl(config.ImportDeployUrl)
	return &Server{
		Store: s, Engine: e, Controller: c, Handler: api.New(c, s, logger, apiSettings), Namespaces: registry, Permissions: service, Metrics: m,
		Live: hub, ModuleRemover: remover, Modules: runtime, DeviceCommands: commands, Broker: broker, Events: worker, Sync: agent, Networks: networks, config: config, logger: logger,
	}, nil
}

// deviceCommands creates the task type device_command if its services are configured (TASK-01)
func deviceCommands(ctx context.Context, config configuration.Config, deps Dependencies, broker mqtt.Broker, repo devices.Repository, aspects *devices.AspectNodes, s *store.Store, m *Metrics, logger *slog.Logger) (*devicecommand.Type, error) {
	mar, ts, producer := deps.Marshaller, deps.Timescale, deps.Producer
	prefix := ""
	if config.Mode == "fog" && repo != nil {
		// marshaller in the process, mgw-last-value and the broker of the gateway (FOG-01)
		prefix = config.LocalCommandPrefix
		if mar == nil {
			var err error
			mar, err = devicecommand.NewLocalMarshaller(ctx, config.DeviceRepositoryUrl, configuration.Duration(config.DeviceCacheTimeout), configuration.Duration(config.ConceptRefreshInterval), logger)
			if err != nil {
				return nil, err
			}
		}
		if ts == nil && config.LastValueUrl != "" {
			ts = devicecommand.NewLastValues(config.LastValueUrl)
		}
		if producer == nil {
			producer = devicecommand.NewLocalTransport(broker, prefix)
		}
	}
	if repo == nil || (mar == nil && config.MarshallerUrl == "") {
		return nil, nil
	}
	if mar == nil {
		mar = devicecommand.NewMarshaller(config.MarshallerUrl)
	}
	if ts == nil && config.TimescaleWrapperUrl != "" {
		ts = devicecommand.NewTimescale(config.TimescaleWrapperUrl)
	}
	if producer == nil {
		producer = devicecommand.NewTransport(ctx, config.KafkaUrl)
	}
	return devicecommand.New(devicecommand.Config{
		GroupScheduler: config.GroupScheduler, ResponseTo: config.MetadataResponseTo, ErrorTo: config.MetadataErrorTo, LocalCommandPrefix: prefix,
	}, s, repo, aspects, mar, ts, producer, m, logger), nil
}

func (this *Server) kafkaConfig(group string, start int64) kafka.Config {
	return kafka.Config{
		KafkaUrl: this.config.KafkaUrl, ConsumerGroup: group, StartOffset: start, PartitionWatchInterval: time.Minute,
		OnError: func(err error) {
			this.logger.Error("kafka consumer failed", "error", err)
		},
	}
}

// startKafka consumes answers of protocol handlers and keeps the device cache fresh
func (this *Server) startKafka(ctx context.Context) error {
	if this.config.KafkaUrl == "" {
		return nil
	}
	if this.DeviceCommands != nil {
		group := this.kafkaConfig(this.config.KafkaConsumerGroup, kafka.LastOffset)
		err := kafka.NewConsumer(ctx, group, this.config.ResponseTopic, func(delivery []byte) error {
			return this.DeviceCommands.HandleResponse(ctx, delivery)
		})
		if err != nil {
			return err
		}
		err = kafka.NewConsumer(ctx, group, this.config.ErrorTopic, func(delivery []byte) error {
			return this.DeviceCommands.HandleError(ctx, delivery)
		})
		if err != nil {
			return err
		}
	}
	group := this.kafkaConfig(this.config.KafkaConsumerGroup, kafka.LastOffset)
	// a changed device group changes the event descriptions of deployments that select it (DEP-08)
	err := kafka.NewConsumer(ctx, group, this.config.DeviceGroupTopic, func(delivery []byte) error {
		var msg struct {
			Id string `json:"id"`
		}
		if json.Unmarshal(delivery, &msg) != nil || msg.Id == "" {
			return nil
		}
		return this.Controller.RecomputeDeviceGroup(ctx, msg.Id)
	})
	if err != nil {
		return err
	}
	if this.config.UserTopic != "" {
		// fallback for users deleted outside of user-management (SEC-14)
		err = kafka.NewConsumer(ctx, group, this.config.UserTopic, func(delivery []byte) error {
			var msg struct {
				Command string `json:"command"`
				Id      string `json:"id"`
			}
			if json.Unmarshal(delivery, &msg) != nil || msg.Command != "DELETE" || msg.Id == "" {
				return nil
			}
			if err := this.Controller.DeleteUserData(ctx, msg.Id); err != nil {
				if errors.Is(err, model.ErrConflict) {
					this.logger.Warn("user deletion from kafka rejected, resources of others depend on the user", "user_id", msg.Id, "error", err)
					return nil
				}
				return err
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if this.Permissions != nil && this.config.PermissionsKafkaTopic != "" {
		err = kafka.NewConsumer(ctx, group, this.config.PermissionsKafkaTopic, func(delivery []byte) error {
			return this.Permissions.HandleRightsMessage(ctx, delivery)
		})
		if err != nil {
			return err
		}
	}
	// every replica clears its own cache, so there is no consumer group
	all := this.kafkaConfig("", kafka.LastOffset)
	if len(this.config.CacheInvalidationAllTopics) > 0 {
		if err := invalidator.StartCacheInvalidatorAll(ctx, all, this.config.CacheInvalidationAllTopics, nil); err != nil {
			return err
		}
	}
	return invalidator.StartKnownCacheInvalidators(ctx, all, invalidator.KnownTopics{DeviceTopic: this.config.DeviceTopic, DeviceGroupTopic: this.config.DeviceGroupTopic}, nil)
}

// Version is the version of the engine from the build: the module version of a release, else
// the revision of the source.
func Version() string {
	info, ok := runtimedebug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			return setting.Value
		}
	}
	return "unknown"
}

// importLegacyIo takes the values of the old process module once; a failure is retried at the
// next start (MIG-06)
func (this *Server) importLegacyIo(ctx context.Context) {
	if this.config.Mode != "fog" || this.config.LegacyIoPostgres.Value() == "" {
		return
	}
	// the Postgres of the old module may still be starting next to the engine (MIG-06)
	retry(ctx, legacyIoRetry, func() error {
		result, done, err := legacyio.Import(ctx, this.Store, this.config.LegacyIoPostgres.Value(), this.config.LegacyIoOwner)
		switch {
		case err != nil:
			this.logger.Warn("unable to take over the values of process-io, trying again", "error", err)
		case done:
			this.logger.Info("values of process-io taken over", "imported", result.Imported, "existing", result.Existing, "instance_values_left_out", result.Skipped)
		}
		return err
	})
}

// retrySettings: the start waits up to Block for success, then the attempts go on in the
// background; the wait between them doubles from First up to Max
type retrySettings struct {
	First, Max, Block time.Duration
}

var legacyIoRetry = retrySettings{First: time.Second, Max: 30 * time.Second, Block: 2 * time.Minute}

// retry calls attempt until it succeeds or ctx is done
func retry(ctx context.Context, settings retrySettings, attempt func() error) {
	deadline := time.Now().Add(settings.Block)
	wait := settings.First
	next := func() bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(wait):
		}
		wait = min(wait*2, settings.Max)
		return true
	}
	for attempt() != nil {
		if time.Now().After(deadline) {
			go func() {
				for next() && attempt() != nil {
				}
			}()
			return
		}
		if !next() {
			return
		}
	}
}

// startLocal reads answers of device connectors and device events from the broker of a gateway
func (this *Server) startLocal(ctx context.Context) error {
	if this.Broker == nil {
		return nil
	}
	if this.Events != nil {
		go this.Events.Run(ctx)
	}
	if this.Sync != nil {
		if err := this.Sync.Start(ctx); err != nil {
			return err
		}
	}
	if this.DeviceCommands == nil {
		return nil
	}
	if err := this.Broker.Subscribe(devicecommand.LocalResponseTopic, 2, func(topic string, payload []byte) error {
		return this.DeviceCommands.HandleLocalResponse(ctx, topic, payload)
	}); err != nil {
		return err
	}
	return this.Broker.Subscribe(devicecommand.LocalErrorTopic, 2, func(topic string, payload []byte) error {
		return this.DeviceCommands.HandleLocalError(ctx, topic, payload)
	})
}

// internalHandler serves metrics and the HTTP answers of protocol handlers, which today post to
// the port of the external-task-worker (TASK-05)
func (this *Server) internalHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", this.Metrics.Handler())
	answer := func(handle func(context.Context, []byte) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if this.DeviceCommands == nil {
				http.Error(w, "device commands are not configured", http.StatusServiceUnavailable)
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err = handle(r.Context(), body); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
		}
	}
	mux.HandleFunc("POST /responses", answer(func(ctx context.Context, body []byte) error { return this.DeviceCommands.HandleResponse(ctx, body) }))
	mux.HandleFunc("POST /"+strings.TrimPrefix(this.config.ErrorTopic, "/"), answer(func(ctx context.Context, body []byte) error { return this.DeviceCommands.HandleError(ctx, body) }))
	return mux
}

// Start runs the background loops of engine, namespaces, permissions and metrics until ctx is done.
func (this *Server) Start(ctx context.Context) {
	go this.Engine.Run(ctx)
	go this.Namespaces.Run(ctx, 10*time.Second)
	if this.Permissions != nil {
		go this.Permissions.Run(ctx, time.Second)
	}
	go this.Metrics.Run(ctx, this.Store, time.Minute, this.logger)
	go this.Live.Run(ctx)
	go this.ModuleRemover.Run(ctx, 10*time.Second)
	go this.Modules.RunHealthChecks(ctx, configuration.Duration(this.config.ModuleHealthCheckInterval))
	go this.Modules.RunWatches(ctx, 5*time.Second)
	go this.Modules.RunResourceChecks(ctx, configuration.Duration(this.config.ResourceCheckCycle))
	if err := this.startKafka(ctx); err != nil {
		this.logger.Error("unable to start kafka consumers", "error", err)
	}
	if this.Networks != nil {
		if err := this.Networks.Start(ctx); err != nil {
			this.logger.Error("unable to subscribe to the gateways", "error", err)
		}
	}
	this.importLegacyIo(ctx)
	if err := this.startLocal(ctx); err != nil {
		this.logger.Error("unable to subscribe at the broker of the gateway", "error", err)
	}
}

// Run starts the background loops and serves the API and the metrics until ctx is done.
func (this *Server) Run(ctx context.Context) error {
	this.Start(ctx)
	go this.publishAsyncApi(ctx)
	if this.config.InternalPort > 0 {
		internal := &http.Server{Addr: net.JoinHostPort("", strconv.Itoa(this.config.InternalPort)), Handler: this.internalHandler(), ReadHeaderTimeout: 10 * time.Second}
		go func() {
			if err := internal.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				this.logger.Error("internal server stopped", "error", err)
			}
		}()
		go func() {
			<-ctx.Done()
			_ = internal.Close()
		}()
	}
	handler := tracing.Handler(this.config.OtelEndpoint, this.Handler)
	srv := &http.Server{Addr: net.JoinHostPort("", strconv.Itoa(this.config.ServerPort)), Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	this.logger.Info("listening", "port", this.config.ServerPort, "internal_port", this.config.InternalPort)
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("http server: %w", err)
}

func (this *Server) Close() error {
	return this.Store.Close()
}

// moduleSettings are the values of the former module workers (TASK-29)
func moduleSettings(config configuration.Config) modules.Settings {
	return modules.Settings{
		DeviceRepositoryUrl: config.DeviceRepositoryUrl, DeviceSelectionUrl: config.DeviceSelectionUrl, DeviceManagerUrl: config.DeviceManagerUrl,
		FlowEngineUrl: config.FlowEngineUrl, FlowParserUrl: config.FlowParserUrl, ImportDeployUrl: config.ImportDeployUrl, ServingServiceUrl: config.ServingServiceUrl,
		FlowRepoUrl: config.FlowRepoUrl, ImportRepositoryUrl: config.ImportRepositoryUrl,
		AnalyticsEnableMultiplePaths: config.AnalyticsEnableMultiplePaths, AnalyticsDevicePathPrefix: config.AnalyticsDevicePathPrefix,
		AnalyticsGroupPathPrefix: config.AnalyticsGroupPathPrefix, AnalyticsImportPathPrefix: config.AnalyticsImportPathPrefix,
		AnalyticsRemoveImportPathRoot: config.AnalyticsRemoveImportPathRoot, DeviceGroupNamePrefix: config.DeviceGroupNamePrefix,
		DeviceGroupAttributeOrigin: config.DeviceGroupAttributeOrigin, InfoAdditionalModuleDataFields: config.InfoAdditionalModuleDataFields,
	}
}
