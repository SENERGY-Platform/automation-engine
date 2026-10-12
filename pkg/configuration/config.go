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

package configuration

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
	confighdl "github.com/SENERGY-Platform/go-service-base/config-hdl"
	"github.com/SENERGY-Platform/go-service-base/config-hdl/types"
	structlogger "github.com/SENERGY-Platform/go-service-base/struct-logger"
	"github.com/SENERGY-Platform/go-service-base/struct-logger/handlers"
)

type Config struct {
	ServerPort int    `json:"server_port" env_var:"SERVER_PORT"`
	LogLevel   string `json:"log_level" env_var:"LOG_LEVEL"`

	// "postgres" or "sqlite"
	Database    string       `json:"database" env_var:"DATABASE"`
	PostgresDsn types.Secret `json:"postgres_dsn" env_var:"POSTGRES_DSN"`
	SqlitePath  string       `json:"sqlite_path" env_var:"SQLITE_PATH"`

	// with a certificate URL, token signatures are verified; otherwise Kong has verified them (SEC-04)
	// e.g. https://<keycloak>/auth/realms/<realm>/protocol/openid-connect/certs
	KeycloakCertUrl string `json:"keycloak_cert_url" env_var:"KEYCLOAK_CERT_URL"`

	// time zone for timers; empty uses the local zone of the process (TZ)
	Timezone string `json:"timezone" env_var:"TIMEZONE"`

	MaxTokens                 int    `json:"max_tokens" env_var:"MAX_TOKENS"`
	MaxVariableBytes          int    `json:"max_variable_bytes" env_var:"MAX_VARIABLE_BYTES"`
	MaxInstanceVariablesBytes int    `json:"max_instance_variables_bytes" env_var:"MAX_INSTANCE_VARIABLES_BYTES"`
	MaxActivities             int    `json:"max_activities" env_var:"MAX_ACTIVITIES"`
	ScriptTimeout             string `json:"script_timeout" env_var:"SCRIPT_TIMEOUT"`
	TaskTimeout               string `json:"task_timeout" env_var:"TASK_TIMEOUT"`
	TaskRetries               int    `json:"task_retries" env_var:"TASK_RETRIES"`
	PollInterval              string `json:"poll_interval" env_var:"POLL_INTERVAL"`

	RetentionDefaultDays int `json:"retention_default_days" env_var:"RETENTION_DEFAULT_DAYS"`
	// 0: no global maximum
	RetentionMaxDays  int `json:"retention_max_days" env_var:"RETENTION_MAX_DAYS"`
	ModelVersionsKept int `json:"model_versions_kept" env_var:"MODEL_VERSIONS_KEPT"`

	// empty: no permissions-v2, only owners and admins have access
	PermissionsV2Url string `json:"permissions_v2_url" env_var:"PERMISSIONS_V2_URL"`
	// Keycloak base URL and client for internal calls with client credentials (SEC-03)
	AuthEndpoint     string       `json:"auth_endpoint" env_var:"AUTH_ENDPOINT"`
	AuthClientId     string       `json:"auth_client_id" env_var:"AUTH_CLIENT_ID"`
	AuthClientSecret types.Secret `json:"auth_client_secret" env_var:"AUTH_CLIENT_SECRET"`

	// empty disables the target (INC-02)
	NotifierUrl               string `json:"notifier_url" env_var:"NOTIFIER_URL"`
	DeveloperNotificationsUrl string `json:"developer_notifications_url" env_var:"DEVELOPER_NOTIFICATIONS_URL"`

	// internal port, not routed through Kong: Prometheus metrics (OPS-05) and the HTTP answers
	// of protocol handlers (POST /responses, POST /errors, TASK-05); 0 disables
	InternalPort int `json:"internal_port" env_var:"INTERNAL_PORT"`
	// running instances without element change for this long count as stalled (LONG-13)
	StalledAfter string `json:"stalled_after" env_var:"STALLED_AFTER"`
	// OTLP collector over gRPC for traces; empty selects the platform default of
	// gin-middleware/otelx, "off" disables tracing
	OtelEndpoint string `json:"otel_endpoint" env_var:"OTEL_ENDPOINT"`
	// services and options of the module task types of smart services (TASK-29)
	DeviceManagerUrl               string `json:"device_manager_url" env_var:"DEVICE_MANAGER_URL"`
	FlowEngineUrl                  string `json:"flow_engine_url" env_var:"FLOW_ENGINE_URL"`
	FlowParserUrl                  string `json:"flow_parser_url" env_var:"FLOW_PARSER_URL"`
	ServingServiceUrl              string `json:"serving_service_url" env_var:"SERVING_SERVICE_URL"`
	AnalyticsEnableMultiplePaths   bool   `json:"analytics_enable_multiple_paths" env_var:"ANALYTICS_ENABLE_MULTIPLE_PATHS"`
	AnalyticsDevicePathPrefix      string `json:"analytics_device_path_prefix" env_var:"ANALYTICS_DEVICE_PATH_PREFIX"`
	AnalyticsGroupPathPrefix       string `json:"analytics_group_path_prefix" env_var:"ANALYTICS_GROUP_PATH_PREFIX"`
	AnalyticsImportPathPrefix      string `json:"analytics_import_path_prefix" env_var:"ANALYTICS_IMPORT_PATH_PREFIX"`
	AnalyticsRemoveImportPathRoot  bool   `json:"analytics_remove_import_path_root" env_var:"ANALYTICS_REMOVE_IMPORT_PATH_ROOT"`
	DeviceGroupNamePrefix          string `json:"device_group_name_prefix" env_var:"DEVICE_GROUP_NAME_PREFIX"`
	DeviceGroupAttributeOrigin     string `json:"device_group_attribute_origin" env_var:"DEVICE_GROUP_ATTRIBUTE_ORIGIN"`
	InfoAdditionalModuleDataFields bool   `json:"info_additional_module_data_fields" env_var:"INFO_ADDITIONAL_MODULE_DATA_FIELDS"`
	// watcher task type (TASK-27): requests from models besides device criteria, and intervals
	AllowGenericWatchRequests bool   `json:"allow_generic_watch_requests" env_var:"ALLOW_GENERIC_WATCH_REQUESTS"`
	WatcherMinInterval        string `json:"watcher_min_interval" env_var:"WATCHER_MIN_INTERVAL"`
	WatcherDefaultInterval    string `json:"watcher_default_interval" env_var:"WATCHER_DEFAULT_INTERVAL"`
	// interval of the health checks of modules (SMS-11)
	ModuleHealthCheckInterval string `json:"module_health_check_interval" env_var:"MODULE_HEALTH_CHECK_INTERVAL"`

	// live debug streams per user and replica (DBG-10)
	MaxLiveConnectionsPerUser int `json:"max_live_connections_per_user" env_var:"MAX_LIVE_CONNECTIONS_PER_USER"`

	// Kafka for device commands, their answers and cache invalidation; empty disables Kafka
	KafkaUrl           string `json:"kafka_url" env_var:"KAFKA_URL"`
	KafkaConsumerGroup string `json:"kafka_consumer_group" env_var:"KAFKA_CONSUMER_GROUP"`
	ResponseTopic      string `json:"response_topic" env_var:"RESPONSE_TOPIC"`
	ErrorTopic         string `json:"error_topic" env_var:"ERROR_TOPIC"`
	// written into each command; the handlers answer there
	MetadataResponseTo string `json:"metadata_response_to" env_var:"METADATA_RESPONSE_TO"`
	MetadataErrorTo    string `json:"metadata_error_to" env_var:"METADATA_ERROR_TO"`
	// any message on these topics clears the device cache; devices and device groups clear their entry
	CacheInvalidationAllTopics []string `json:"cache_invalidation_all_topics" env_var:"CACHE_INVALIDATION_ALL_TOPICS"`
	DeviceTopic                string   `json:"device_topic" env_var:"DEVICE_TOPIC"`
	DeviceGroupTopic           string   `json:"device_group_topic" env_var:"DEVICE_GROUP_TOPIC"`

	// device commands are available when device-repository and marshaller are configured (TASK-01)
	DeviceRepositoryUrl string `json:"device_repository_url" env_var:"DEVICE_REPOSITORY_URL"`
	MarshallerUrl       string `json:"marshaller_url" env_var:"MARSHALLER_URL"`
	// empty: services that only send events cannot be read (TASK-08)
	TimescaleWrapperUrl string `json:"timescale_wrapper_url" env_var:"TIMESCALE_WRAPPER_URL"`
	DeviceSelectionUrl  string `json:"device_selection_url" env_var:"DEVICE_SELECTION_URL"`
	ImportDeployUrl     string `json:"import_deploy_url" env_var:"IMPORT_DEPLOY_URL"`
	// what device-selection offers in deploy dialogs, as in process-deployment (DEP-01)
	DeviceGroupsForTasks  bool `json:"device_groups_for_tasks" env_var:"DEVICE_GROUPS_FOR_TASKS"`
	DeviceGroupsForEvents bool `json:"device_groups_for_events" env_var:"DEVICE_GROUPS_FOR_EVENTS"`
	ImportsForEvents      bool `json:"imports_for_events" env_var:"IMPORTS_FOR_EVENTS"`
	ModifiedDevices       bool `json:"modified_devices" env_var:"MODIFIED_DEVICES"`
	// parallel, sequential or round_robin for device groups (TASK-06)
	GroupScheduler     string `json:"group_scheduler" env_var:"GROUP_SCHEDULER"`
	DeviceCacheTimeout string `json:"device_cache_timeout" env_var:"DEVICE_CACHE_TIMEOUT"`

	// engine or event: the event role evaluates device data and correlates events (section 10)
	Role string `json:"role" env_var:"ROLE"`
	// consumer group of event workers, which share the device data topics
	EventConsumerGroup  string `json:"event_consumer_group" env_var:"EVENT_CONSUMER_GROUP"`
	ImportRepositoryUrl string `json:"import_repository_url" env_var:"IMPORT_REPOSITORY_URL"`
	// user deletions published by user-management, the fallback for users deleted outside of it (SEC-14)
	UserTopic string `json:"user_topic" env_var:"USER_TOPIC"`
	// permissions-v2 publishes rights changes of the engine topics here; the engine restores
	// the owner's administrate right (SEC-10); empty disables
	PermissionsKafkaTopic string `json:"permissions_kafka_topic" env_var:"PERMISSIONS_KAFKA_TOPIC"`

	// cloud or fog; selects the http policy of the namespaces (NS-04) and, in fog, the services
	// of the gateway (FOG-01)
	Mode string `json:"mode" env_var:"MODE"`
	// fog: the broker of the gateway for commands and device events, e.g. tcp://broker:1881
	MqttBroker   string       `json:"mqtt_broker" env_var:"MQTT_BROKER"`
	MqttClientId string       `json:"mqtt_client_id" env_var:"MQTT_CLIENT_ID"`
	MqttUser     string       `json:"mqtt_user" env_var:"MQTT_USER"`
	MqttPassword types.Secret `json:"mqtt_password" env_var:"MQTT_PASSWORD"`
	// fog: mgw-last-value, the last values of event services
	LastValueUrl string `json:"last_value_url" env_var:"LAST_VALUE_URL"`
	// fog: the prefix of the command ids of this engine on the broker of the gateway
	LocalCommandPrefix string `json:"local_command_prefix" env_var:"LOCAL_COMMAND_PREFIX"`
	// fog: concepts for commands before payload version 3 are refreshed at this interval
	ConceptRefreshInterval string `json:"concept_refresh_interval" env_var:"CONCEPT_REFRESH_INTERVAL"`
	// upper bound of the database in bytes; 0 is unlimited, in fog 1 GiB, negative unlimited (LONG-07)
	StorageBudget int64 `json:"storage_budget" env_var:"STORAGE_BUDGET"`
	// limits of the process-io store per user (LONG-11)
	MaxIoValuesPerUser int `json:"max_io_values_per_user" env_var:"MAX_IO_VALUES_PER_USER"`
	MaxIoValueBytes    int `json:"max_io_value_bytes" env_var:"MAX_IO_VALUE_BYTES"`

	// seeds namespaces that do not exist yet; afterwards the admin API manages them (NS-02)
	Namespaces []model.Namespace `json:"namespaces" env_var:"NAMESPACES"`
}

func Default() Config {
	return Config{
		ServerPort:                 8080,
		LogLevel:                   "info",
		Database:                   "sqlite",
		SqlitePath:                 "automation-engine.db",
		MaxTokens:                  100,
		MaxVariableBytes:           1 << 20,
		MaxInstanceVariablesBytes:  4 << 20,
		MaxActivities:              10000,
		ScriptTimeout:              "2s",
		TaskTimeout:                "2m",
		TaskRetries:                0,
		PollInterval:               "1s",
		RetentionDefaultDays:       7,
		ModelVersionsKept:          50,
		InternalPort:               8081,
		KafkaConsumerGroup:         "automation-engine",
		ResponseTopic:              "response",
		ErrorTopic:                 "errors",
		MetadataResponseTo:         "response",
		MetadataErrorTo:            "errors",
		CacheInvalidationAllTopics: []string{"device-types", "protocols"},
		DeviceTopic:                "devices",
		DeviceGroupTopic:           "device-groups",
		GroupScheduler:             "sequential",
		DeviceCacheTimeout:         "1m",
		StalledAfter:               "24h",
		ModifiedDevices:            true,
		Mode:                       "cloud",
		MqttClientId:               "automation-engine",
		LocalCommandPrefix:         "automation-engine-",
		ConceptRefreshInterval:     "1h",
		Role:                       "engine",
		EventConsumerGroup:         "automation-engine-events",
		UserTopic:                  "user",
		MaxIoValuesPerUser:         10000,
		MaxIoValueBytes:            256 << 10,
		MaxLiveConnectionsPerUser:  5,
		// the values of the former module workers
		AnalyticsDevicePathPrefix:      "value.",
		AnalyticsGroupPathPrefix:       "value.",
		DeviceGroupNamePrefix:          "smart-service generated: ",
		DeviceGroupAttributeOrigin:     "smart-service",
		InfoAdditionalModuleDataFields: true,
		ModuleHealthCheckInterval:      "1h",
		WatcherMinInterval:             "1m",
		WatcherDefaultInterval:         "1h",
		Namespaces:                     DefaultNamespaces(),
	}
}

// DefaultNamespaces reproduce today's behavior of processes and smart services (NS-02, NS-03,
// NS-05, NS-06): process models are shareable, deployments and instances of processes belong to
// their owner; smart service designs belong to their owner, releases and instances are shareable.
func DefaultNamespaces() []model.Namespace {
	return []model.Namespace{
		{
			Name:             "processes",
			Description:      "processes",
			TaskTypes:        model.ListRule{Allow: []string{"device_command", "process_io", "notification", "http"}},
			DeviceSelection:  model.DeviceSelection{AtDeploy: true},
			Models:           model.LevelSettings{Shareable: true},
			IncidentHandling: model.IncidentHandling{Restart: false, Notify: true},
			// in the cloud a user process must not reach services of the cluster; on a gateway it
			// may call devices in the local network (BPMN-10)
			Http: model.HttpSettings{
				Cloud: model.HttpPolicy{Enabled: false},
				Fog:   model.HttpPolicy{Enabled: true, AllowNetworks: []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7", "fe80::/10"}},
			},
		},
		{
			Name:                 "smart-services",
			Description:          "smart services",
			TaskTypes:            model.ListRule{Allow: []string{"info", "import", "export", "analytics", "device_repository", "watcher", "process_deployment", "process_deployment_start"}},
			Elements:             model.ListRule{Deny: []string{"startEvent:timer"}},
			DeviceSelection:      model.DeviceSelection{AtStart: true},
			ReferencedNamespaces: []string{"processes"},
			Deployments:          model.LevelSettings{Shareable: true},
			Instances:            model.LevelSettings{Shareable: true},
			IncidentHandling:     model.IncidentHandling{Restart: false, Notify: true},
			// processes that create modules run briefly; every new run removes the modules of the last one
			ForbidCycles:               true,
			MessageStartsAreProcedures: true,
			JsonStringParameters:       true,
		},
	}
}

// Load reads the JSON file at path (optional) and then environment variables.
func Load(path string) (Config, error) {
	config := Default()
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			path = ""
		}
	}
	err := confighdl.Load(&config, nil, []confighdl.EnvTypeParser{types.SecretEnvTypeParser}, nil, path)
	if err != nil {
		return config, err
	}
	if config.Mode == "fog" && config.StorageBudget == 0 {
		config.StorageBudget = DefaultGatewayStorageBudget
	}
	return config, config.validate()
}

// DefaultGatewayStorageBudget bounds the database on a gateway unless storage_budget is set;
// a negative budget is unlimited (LONG-07).
const DefaultGatewayStorageBudget = 1 << 30

func (this Config) validate() error {
	for _, d := range []string{this.ScriptTimeout, this.TaskTimeout, this.PollInterval, this.StalledAfter, this.ConceptRefreshInterval} {
		if _, err := time.ParseDuration(d); err != nil {
			return fmt.Errorf("invalid duration %q: %w", d, err)
		}
	}
	if this.Database != "postgres" && this.Database != "sqlite" {
		return fmt.Errorf("database must be postgres or sqlite, got %q", this.Database)
	}
	if this.Role != "engine" && this.Role != "event" {
		return fmt.Errorf("role must be engine or event")
	}
	if this.Mode != "cloud" && this.Mode != "fog" {
		return fmt.Errorf("mode must be cloud or fog")
	}
	switch this.GroupScheduler {
	case "parallel", "sequential", "round_robin":
	default:
		return fmt.Errorf("group_scheduler must be parallel, sequential or round_robin")
	}
	if this.Mode == "fog" && this.MqttBroker == "" {
		return fmt.Errorf("mode fog needs mqtt_broker, the broker of the gateway")
	}
	if this.Mode == "fog" && this.LocalCommandPrefix == "" {
		return fmt.Errorf("mode fog needs local_command_prefix")
	}
	// the device-repository mirror of a gateway needs no token
	if this.DeviceRepositoryUrl != "" && this.AuthEndpoint == "" && this.Mode != "fog" {
		return fmt.Errorf("device commands need auth_endpoint for token exchange")
	}
	if this.PermissionsV2Url != "" && (this.AuthEndpoint == "" || this.AuthClientId == "") {
		return fmt.Errorf("permissions_v2_url needs auth_endpoint and auth_client_id for internal calls")
	}
	seen := map[string]bool{}
	for _, ns := range this.Namespaces {
		if seen[ns.Name] {
			return fmt.Errorf("namespace %q is configured twice", ns.Name)
		}
		seen[ns.Name] = true
		if err := ns.WithDefaults().Validate(); err != nil {
			return fmt.Errorf("namespace %q: %w", ns.Name, err)
		}
	}
	return nil
}

func Duration(value string) time.Duration {
	d, _ := time.ParseDuration(value)
	return d
}

func (this Config) Location() (*time.Location, error) {
	if this.Timezone == "" {
		return time.Local, nil
	}
	return time.LoadLocation(this.Timezone)
}

// Logger writes structured logs. Records logged with a context (InfoContext, ...) carry the
// baggage of that context and become events of its span.
func (this Config) Logger() *slog.Logger {
	base := structlogger.New(structlogger.Config{
		Handler:    structlogger.JsonHandlerSelector,
		Level:      this.LogLevel,
		TimeFormat: time.RFC3339Nano,
		TimeUtc:    true,
		AddMeta:    true,
	}, os.Stdout, "github.com/SENERGY-Platform", "automation-engine")
	return slog.New(handlers.NewOpenTelemetryHandler(base.Handler()))
}
