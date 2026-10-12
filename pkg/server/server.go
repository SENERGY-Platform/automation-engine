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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/api"
	"github.com/SENERGY-Platform/automation-engine/pkg/auth"
	"github.com/SENERGY-Platform/automation-engine/pkg/configuration"
	"github.com/SENERGY-Platform/automation-engine/pkg/controller"
	"github.com/SENERGY-Platform/automation-engine/pkg/devices"
	"github.com/SENERGY-Platform/automation-engine/pkg/engine"
	"github.com/SENERGY-Platform/automation-engine/pkg/events"
	"github.com/SENERGY-Platform/automation-engine/pkg/live"
	"github.com/SENERGY-Platform/automation-engine/pkg/messaging"
	"github.com/SENERGY-Platform/automation-engine/pkg/metrics"
	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	"github.com/SENERGY-Platform/automation-engine/pkg/namespaces"
	"github.com/SENERGY-Platform/automation-engine/pkg/notification"
	"github.com/SENERGY-Platform/automation-engine/pkg/permissions"
	"github.com/SENERGY-Platform/automation-engine/pkg/prepare"
	"github.com/SENERGY-Platform/automation-engine/pkg/store"
	"github.com/SENERGY-Platform/automation-engine/pkg/tasks/devicecommand"
	"github.com/SENERGY-Platform/automation-engine/pkg/tasks/httpcall"
	"github.com/SENERGY-Platform/automation-engine/pkg/tasks/notify"
	"github.com/SENERGY-Platform/automation-engine/pkg/tasks/processio"
	"github.com/SENERGY-Platform/automation-engine/pkg/tracing"
	dsclient "github.com/SENERGY-Platform/device-selection/v2/pkg/client"
	"github.com/SENERGY-Platform/external-task-worker/lib/devicerepository"
	"github.com/SENERGY-Platform/external-task-worker/lib/marshaller"
	"github.com/SENERGY-Platform/external-task-worker/lib/timescale"
	"github.com/SENERGY-Platform/external-task-worker/util"
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
	Metrics     *metrics.Metrics
	Live        *live.Hub
	// nil if device commands are not configured
	DeviceCommands *devicecommand.Type
	config         configuration.Config
	logger         *slog.Logger
}

// Dependencies replace connections to other services, e.g. in tests.
type Dependencies struct {
	// a permissions-v2 client, e.g. permissions-v2's in-process test client
	Permissions permclient.Client
	AdminToken  auth.TokenSource
	// tokens of other users; the Keycloak token exchange if not given
	UserToken auth.UserTokenSource
	// device command dependencies; set together, they enable device commands without URLs
	DeviceRepository devicerepository.RepoInterface
	AspectNodes      *devices.AspectNodes
	Marshaller       marshaller.Interface
	Timescale        timescale.Interface
	Producer         messaging.Producer
	// event debugging: stored device messages and their unmarshalling (EVT-10, DBG-18)
	EventSeries       events.Series
	EventUnmarshaller events.Unmarshaller
}

func openStore(ctx context.Context, config configuration.Config) (*store.Store, error) {
	if config.Database == "postgres" {
		return store.Open(ctx, store.Postgres, config.PostgresDsn.Value())
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
	result.TaskTimeout = configuration.Duration(config.TaskTimeout)
	result.TaskRetries = config.TaskRetries
	result.PollInterval = configuration.Duration(config.PollInterval)
	result.RetentionDefaultDays = config.RetentionDefaultDays
	result.RetentionMaxDays = config.RetentionMaxDays
	result.ModelVersionsKept = config.ModelVersionsKept
	return result, nil
}

// deviceRepository builds the device-repository access with token exchange and its aspect node
// resolver; nil if neither configured nor given
func deviceRepository(config configuration.Config, deps Dependencies) (devicerepository.RepoInterface, *devices.AspectNodes, error) {
	repo, aspects := deps.DeviceRepository, deps.AspectNodes
	if repo == nil {
		if config.DeviceRepositoryUrl == "" {
			return nil, nil, nil
		}
		iot, err := devicerepository.NewIot(util.Config{
			DeviceRepoUrl: config.DeviceRepositoryUrl, AuthEndpoint: config.AuthEndpoint, AuthClientId: config.AuthClientId,
			AuthClientSecret: config.AuthClientSecret.Value(), CacheTimeout: config.DeviceCacheTimeout,
		})
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
	}

	e := engine.New(s, engineSettings, registry, logger)
	notifier := notification.New(config.NotifierUrl, config.DeveloperNotificationsUrl, logger)
	e.SetNotifier(notifier)
	m := metrics.New(configuration.Duration(config.StalledAfter))
	e.SetMetrics(m)

	io := processio.New(s, processio.Limits{MaxValuesPerUser: config.MaxIoValuesPerUser, MaxValueBytes: config.MaxIoValueBytes})
	e.RegisterTaskType(processio.TaskType, processio.NewType(io))
	e.RegisterTaskType(notify.TaskType, notify.New(notifier))
	e.RegisterTaskType(httpcall.TaskType, httpcall.New(registry, config.Mode))

	repo, aspects, err := deviceRepository(config, deps)
	if err != nil {
		s.Close()
		return nil, err
	}
	commands, err := deviceCommands(ctx, config, deps, repo, aspects, s, m, logger)
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
	c := controller.New(s, e, registry, authorizer, certs)
	c.SetIo(io)
	var selection dsclient.Client
	if config.DeviceSelectionUrl != "" {
		selection = dsclient.NewClient(config.DeviceSelectionUrl)
	}
	c.SetPreparation(
		prepare.New(selection, prepare.Config{GroupsForTasks: config.DeviceGroupsForTasks, GroupsForEvents: config.DeviceGroupsForEvents, ImportsForEvents: config.ImportsForEvents, ModifiedDevices: config.ModifiedDevices}),
		prepare.NewAccess(permissionsClient, config.ImportDeployUrl),
	)
	if repo != nil {
		var imports events.Imports
		if config.ImportDeployUrl != "" && config.ImportRepositoryUrl != "" {
			imports = events.NewImports(config.ImportDeployUrl, config.ImportRepositoryUrl)
		}
		c.SetDescriber(events.NewDescriber(repo, aspects, imports))
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
		if unmarshaller, err = newUnmarshaller(ctx, &sync.WaitGroup{}, config); err != nil {
			s.Close()
			return nil, err
		}
	}
	if series != nil && unmarshaller != nil {
		c.SetEventDebugging(series, events.NewEvaluator(unmarshaller), userToken)
	}
	hub := live.New(s, dsn, logger)
	liveSettings := controller.DefaultLiveSettings()
	liveSettings.MaxPerUser = config.MaxLiveConnectionsPerUser
	c.SetLive(hub, liveSettings)
	return &Server{
		Store: s, Engine: e, Controller: c, Handler: api.New(c, s, logger, api.Settings{VerifiesTokens: certs != nil}), Namespaces: registry, Permissions: service, Metrics: m,
		Live: hub, DeviceCommands: commands, config: config, logger: logger,
	}, nil
}

// deviceCommands creates the task type device_command if its services are configured (TASK-01)
func deviceCommands(ctx context.Context, config configuration.Config, deps Dependencies, repo devicerepository.RepoInterface, aspects *devices.AspectNodes, s *store.Store, m *metrics.Metrics, logger *slog.Logger) (*devicecommand.Type, error) {
	mar, ts, producer := deps.Marshaller, deps.Timescale, deps.Producer
	if repo == nil || (mar == nil && config.MarshallerUrl == "") {
		return nil, nil
	}
	if mar == nil {
		mar = marshaller.New(config.MarshallerUrl)
	}
	if ts == nil && config.TimescaleWrapperUrl != "" {
		ts = timescale.NewTimescale(config.TimescaleWrapperUrl)
	}
	if producer == nil {
		producer = messaging.New(ctx, config.KafkaUrl)
	}
	return devicecommand.New(devicecommand.Config{
		GroupScheduler: config.GroupScheduler, ResponseTo: config.MetadataResponseTo, ErrorTo: config.MetadataErrorTo,
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
	if err := this.startKafka(ctx); err != nil {
		this.logger.Error("unable to start kafka consumers", "error", err)
	}
}

// Run starts the background loops and serves the API and the metrics until ctx is done.
func (this *Server) Run(ctx context.Context) error {
	this.Start(ctx)
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
