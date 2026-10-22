# Automation-Engine

Replacement for Camunda 7 on the SENERGY platform: models, deployments and instances of processes and smart services in one engine. Requirements in `docs/requirements.md`, implementation status and decisions in `PLAN.md`.

## Running

```sh
go run . -config config.json
```

The configuration comes from `config.json` and from environment variables (names in `pkg/configuration/config.go`, e.g. `DATABASE=postgres`, `POSTGRES_DSN`, `SERVER_PORT`, `NAMESPACES` as JSON). The default is SQLite in `automation-engine.db`. The engine migrates the schema itself on start.

For platform operation, add:
- `POSTGRES_MAX_CONNS`: open connections per process (default 0, no limit). Behind PgBouncer in session mode, every open connection holds a server connection of the pool, so the limit times the number of pods (both roles and a running migration) has to fit the pool size.
- `PERMISSIONS_V2_URL` with `AUTH_ENDPOINT`, `AUTH_CLIENT_ID`, `AUTH_CLIENT_SECRET`: sharing through permissions-v2; the Keycloak client needs the realm role `admin`. Without the URL, only owners and admins have access.
- `NOTIFIER_URL`, `DEVELOPER_NOTIFICATIONS_URL`: notifications on incidents.
- `INTERNAL_PORT` (default 8081, replaces `METRICS_PORT`): Prometheus metrics under `/metrics` and answers of devices over HTTP under `/responses` and `/errors`. Do not route it through Kong.
- `RETENTION_DEFAULT_DAYS`, `RETENTION_MAX_DAYS`: retention of the history.
- `KAFKA_URL`: device commands and their answers (`RESPONSE_TOPIC`, `ERROR_TOPIC`), changes to devices and device groups (`DEVICE_TOPIC`, `DEVICE_GROUP_TOPIC`), user deletions (`USER_TOPIC`) and optionally the rights signal of permissions-v2 (`PERMISSIONS_KAFKA_TOPIC`).
- `DEVICE_REPOSITORY_URL`, `MARSHALLER_URL`, `TIMESCALE_WRAPPER_URL`, `DEVICE_SELECTION_URL`, `IMPORT_DEPLOY_URL`, `IMPORT_REPOSITORY_URL`: services for device commands, device selection and device events. Device commands additionally need `AUTH_ENDPOINT` for the token exchange.
- `MODE` (`cloud` or `fog`, default `cloud`): selects the policy for HTTP calls from processes.
- `MAX_IO_VALUES_PER_USER`, `MAX_IO_VALUE_BYTES`: limits for process-io.
- `MAX_LIVE_CONNECTIONS_PER_USER` (default 5): live debug connections per user and replica.
- `GROUP_SCHEDULER` (`parallel`, `sequential` or `round_robin`, default `sequential`): order of device commands to several devices; for the two orders one at a time, the task timeout is the answer window of each try and the retries count per device, see `docs/device-command-groups.md`.
- `MIN_TIMER_DURATION` (default `5s`): shortest timer interval. A literal `timeDuration` below it is rejected at deploy, in the model or as deploy parameter, as process-deployment had it. Everything else is limited at runtime instead, because expressions have a value only then and schedules are no BPMN: cycles (`R/<duration>` and cron) of timer start and intermediate events, schedules (also cron with seconds and `@every`) and durations or cycles computed by a JavaScript expression are never due earlier than the minimum after their previous due time; without one (first start after deploy or creation of the schedule, a token arriving at an intermediate timer) after that moment. An `R/` interval or `@every` thus runs at the longer of its interval and the minimum, a cron at its first time at or after that point: with `5s`, `* * * * * ?` starts every 5 seconds, `0 * * * * ?` deployed at 12:00:57 first at 12:02:00. Dates (`timeDate`) are not limited. An intermediate timer that was limited names the minimum in its `timer_scheduled` activity (`minimum`). `0s` switches both off.
- `CORS_ALLOWED_ORIGINS`, `CORS_ALLOWED_HEADERS` (default `Authorization, Content-Type`): CORS headers for browser applications that call the engine directly, without Kong in between. Origins are comma separated, without path or trailing slash, e.g. `https://ui.example.org,http://localhost:4200`; `*` allows every origin. Empty, the default, sets no CORS headers, because in the platform Kong sets them. Credentials are not allowed, since the token travels in the `Authorization` header.
- `OTEL_ENDPOINT`: OTLP collector (gRPC) for traces. Empty means the default of `gin-middleware/otelx` (`jaeger.logging.svc.cluster.local:4317`), `off` switches tracing off, for example in the fog or locally.

### Running locally with Docker Compose

For trying the engine by hand, `docker-compose.yml` starts it with Postgres and `devauth`, a stand-in for Keycloak (`tools/devauth`):

```sh
docker compose up --build
```

| Service | Port on localhost | |
|---|---|---|
| engine | 8080, 8081 | API; metrics and device answers |
| devauth | 8090 | `GET /token/{user}[?roles=admin]` returns a token to paste |
| webui | 8088 | test web interface (`tools/webui`) |
| postgres | 5433 | user, password and database `engine` |

The engine does not verify token signatures without `KEYCLOAK_CERT_URL`, so the unsigned tokens of devauth are accepted. Without permissions-v2, only owners and admins have access.

The web interface at http://localhost:8088 takes the token of the selected user from devauth and covers models, deployments with their selections, instances with variables, activities, debug state and the live stream, incidents, messages, and a console for any other route. It passes `/api/` to the engine and `/auth/` to devauth, so the browser talks to one origin. For the live stream it takes the token from the query parameter `access_token`, because a browser cannot set headers on a WebSocket; it is meant for this local setup only.

A walkthrough with the example model (needs `jq`):

```sh
TOKEN=$(curl -s localhost:8090/token/alice)
DEPLOYMENT=$(jq -Rs '{name: "Wait for go", xml: .}' examples/wait-for-message.bpmn \
  | curl -s -H "Authorization: $TOKEN" -H 'Content-Type: application/json' -d @- localhost:8080/namespaces/processes/deployments | jq -r .id)
INSTANCE=$(curl -s -H "Authorization: $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"deployment_id\": \"$DEPLOYMENT\", \"variables\": {\"level\": 21}}" localhost:8080/namespaces/processes/instances | jq -r .id)
curl -s -H "Authorization: $TOKEN" localhost:8080/namespaces/processes/instances/$INSTANCE/debug | jq .
curl -s -H "Authorization: $TOKEN" -H 'Content-Type: application/json' -d "{\"name\": \"go\", \"instance_id\": \"$INSTANCE\"}" localhost:8080/messages
curl -s -H "Authorization: $TOKEN" localhost:8080/namespaces/processes/instances/$INSTANCE/variables | jq .
```

The routes are in `docs/swagger.json`, which API tools such as Postman or Bruno import.

`docker-compose.platform.yml` adds MongoDB, Kafka (on `localhost:29092` for the host), permissions-v2 (8083), device-repository (8084), marshaller (8085), converter, the event role of the engine and `devicesim` (`tools/devicesim`), in the images the platform deploys:

```sh
docker compose -f docker-compose.yml -f docker-compose.platform.yml up --build
```

Sharing then goes through permissions-v2, e.g. `PUT localhost:8083/manage/processes_models/{id}` with the token of the owner; in the namespace `processes` only models are shareable, in `smart-services` all kinds. devauth also takes the messages permissions-v2 sends to developer-notifications and prints them (`docker compose logs devauth`).

Device commands go through the marshaller to the Kafka topic of the protocol handler. `devicesim` answers there in place of platform-connector and the devices: numbers get `NUMBER` (default 21.5), and what a setter receives shows in `docker compose logs devicesim`. `examples/seed-devices.sh` creates a thermostat of alice with a service that reads the room temperature and one that sets the target; `examples/thermostat.bpmn` uses both, with the device selected at deploy:

```sh
./examples/seed-devices.sh
TOKEN=$(curl -s localhost:8090/token/alice)
DEVICE=$(curl -s -H "Authorization: $TOKEN" "localhost:8084/devices/thermostat-1?as=local_id&owner_id=alice" | jq -r .id)
DEPLOYMENT=$(jq -Rs --arg d "$DEVICE" '{name: "Thermostat", xml: ., selections: {
    read: {device_id: $d, service_id: "urn:infai:ses:service:local-get-temperature"},
    set: {device_id: $d, service_id: "urn:infai:ses:service:local-set-temperature"}}}' examples/thermostat.bpmn \
  | curl -s -H "Authorization: $TOKEN" -H 'Content-Type: application/json' -d @- localhost:8080/namespaces/processes/deployments | jq -r .id)
INSTANCE=$(curl -s -H "Authorization: $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"deployment_id\": \"$DEPLOYMENT\", \"variables\": {\"target\": \"23\"}}" localhost:8080/namespaces/processes/instances | jq -r .id)
sleep 3   # two commands, each a round trip through marshaller, Kafka and devicesim
curl -s -H "Authorization: $TOKEN" localhost:8080/namespaces/processes/instances/$INSTANCE/variables | jq -c 'map({(.name): .value}) | add'
```

devicesim also sends an event every 10 seconds for each device in device-repository and each of its services with the interaction `event` or `event+request`, on the device data topic of the service. Numbers swing between 18.5 and 24.5 within two minutes (`NUMBER`, `AMPLITUDE`, `PERIOD`, `EVENT_INTERVAL`); answers to commands carry the same current value. `examples/room-temperature.bpmn` waits until the room is warmer than 23 °C and then until it is cooler than 20 °C; it completes within about two minutes:

```sh
S=urn:infai:ses:service:local-get-temperature
DEPLOYMENT=$(jq -Rs --arg d "$DEVICE" --arg s $S '{name: "Room temperature", xml: ., selections: {
    warm: {device_id: $d, service_id: $s}, cool: {device_id: $d, service_id: $s}}}' examples/room-temperature.bpmn \
  | curl -s -H "Authorization: $TOKEN" -H 'Content-Type: application/json' -d @- localhost:8080/namespaces/processes/deployments | jq -r .id)
INSTANCE=$(curl -s -H "Authorization: $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"deployment_id\": \"$DEPLOYMENT\"}" localhost:8080/namespaces/processes/instances | jq -r .id)
```

`examples/event-loop.bpmn` runs until it is stopped: it waits for every event of the thermostat (one every 10 seconds), counts them, and sets the target temperature to 20 °C when the room is warmer than 23 °C and to 23 °C when it is cooler than 20 °C. It gives the live debugging of the web interface something to show at any time. Deploy it like the room temperature example, with the get service selected for `reading` and the set service for `cool_down` and `heat_up`.

Commands to services that only send events need timescale-wrapper or mgw-last-value and are not part of the setup.

`docker compose down -v` removes the containers with their data.

## Smart services and modules

Smart services are the namespace `smart-services`. Their task types replace the eight module workers:
- `info`, `import`, `export`, `analytics`, `device_repository`: the behaviour of the previous workers, implemented in this repository and checked against their test cases (`pkg/modules/testdata`). Inputs with the prefix `<type>.`, Go templates against the instance variables (`{{.param}}`), `prescript*`/`postscript*` with `variables`, `inputs`, `outputs`, `util`, `deviceRepo`. The script API of both JavaScript environments (module scripts and BPMN scripts) is in [docs/scripts](docs/scripts/README.md), as TypeScript declarations, JSDoc and Ace completion; generated with `go generate ./pkg/scriptdoc`.
- `process_deployment`, `process_deployment_start`: deploy or start processes in the referenced namespace (`processes`) with the token of the owner. The deployment refers to the managing instance (`managed_by`). The engine reports completion with the message `deployment_done_<id>`, which the instance receives at once the next time it waits for it; waiting points name it with `senergy:messageName` (JavaScript). With `prefer_fog_deployment`, the process goes to the first gateway that carries every selected device; if the target namespace is `cloud_only`, it stays in the cloud without an error.
- `watcher`: checks device selections (or, with `ALLOW_GENERIC_WATCH_REQUESTS`, any request under the HTTP policy of the namespace) at intervals and starts a maintenance procedure on a change.

Configuration: `DEVICE_MANAGER_URL`, `DEVICE_SELECTION_URL`, `FLOW_ENGINE_URL`, `FLOW_PARSER_URL`, `IMPORT_DEPLOY_URL`, `SERVING_SERVICE_URL`, `DEVICE_REPOSITORY_URL`, plus the options of the workers (`ANALYTICS_*`, `DEVICE_GROUP_*`, `INFO_ADDITIONAL_MODULE_DATA_FIELDS`, `WATCHER_MIN_INTERVAL`, `WATCHER_DEFAULT_INTERVAL`, `MODULE_HEALTH_CHECK_INTERVAL`). A task type is registered only when its services are configured; otherwise a deploy rejects it.

API:
- Modules: `GET .../modules?instance_id=&module_type=&key=`, `GET|DELETE .../modules/{id}`. Resources are deleted through an outbox with retries; retention does not delete instances with modules, but trims their history like any other.
- Status of every instance (`status`, `status_message`): `running`, `ready`, `error`, from the run state and the module errors of the hourly health check.
- Start parameters with options: `GET .../deployments/{id}/parameters`; the engine fills `auto_select_all` fields on start.
- Changing parameters: `PUT .../instances/{id}/parameters`. Every new run removes the modules of the previous one; `smart-services` therefore rejects loops in the sequence flow (`forbid_cycles`).
- Maintenance procedures: `GET .../instances/{id}/maintenance-procedures[/{procedure}/parameters]`, `POST .../{procedure}/start`. Message starts in `smart-services` start only this way, not through `/messages`.
- Setting and deleting variables, also on ended instances: `PUT|DELETE .../instances/{id}/variables/{name}`.
- Deploy parameters per element: `parameters` on the deployment override inputs, event variables, timers and start defaults.
- Used resources: a version names the process models, flows and import types of its module tasks (`used_resources`) and what of them changed or was deleted since the deploy (`changed_resources`). The check runs every `RESOURCE_CHECK_CYCLE` (default `1h`, `off` switches it off) against `FLOW_REPO_URL` and `IMPORT_REPOSITORY_URL`. `GET /namespaces/{ns}/resource-usage/{kind}/{id}` counts the versions and instances that use a resource; only an admin deletes a model in use, with `?force=true`.
- Instances per version: `GET .../deployments/{id}/instance-count?version=` (administrators of the deployment).

## Debugging

- `GET .../instances/{id}/debug`: current state with active elements, reasons for waiting, variables, open incidents and pass counters of elements and sequence flows. Large values are cut; `GET .../instances/{id}/variables/{name}` returns them one by one.
- `GET .../instances/{id}/live`: WebSocket with snapshot, deltas and final state; `after=<seq>` resumes after a disconnect. The token comes in the `Authorization` header or, for browsers, as the first message `{"type":"auth","token":"..."}`. The second form exists only with `KEYCLOAK_CERT_URL`, because the token then does not pass Kong and the engine verifies the signature itself. Kong has to pass the upgrade through.
- `GET .../instances/{id}/history?seq=|time=`: the state after a step of the history, reconstructed from full states and activities; `first_seq` names the earliest step that can still be reconstructed.
- `GET .../instances/{id}/event-evaluations?element_id=`: evaluations of a device event, reconstructed from the values in Timescale, next to the actual correlations. Needs `TIMESCALE_WRAPPER_URL`, `DEVICE_REPOSITORY_URL` and `AUTH_ENDPOINT` (token of the owner); the same values allow `evaluate_events=true` on the debug state and on the live stream.
- In the Go client: `client.Follow` merges snapshot and deltas and resumes after disconnects, `client.OpenHistory` steps through the history.

Several replicas need no sticky sessions: every write operation of an instance lands in the change stream (table `changes`), Postgres announces it through LISTEN/NOTIFY, SQLite is polled.

## Tracing

As in the other platform services, through `gin-middleware/otelx`:
- Incoming requests take over the trace context and baggage of the caller; otelx puts `user_id` and `username` from the token into the baggage.
- Requests to platform services (permissions-v2, notifier, import-deploy, HTTP answer targets of device commands) carry trace context and baggage on. They are not passed to device-selection, device-repository and marshaller, whose clients take no context.
- The task type `http` passes nothing on: its targets lie outside the platform, and the baggage contains user ids.
- Every execution of a task and every evaluation of a device event is a span of its own; instance, deployment, namespace and owner are in span and baggage as `automation_instance_id`, `automation_deployment_id`, `automation_namespace` and `user_id`. Execution runs in the background, so its span starts a trace of its own instead of hanging off the starting request.
- Log entries with context (`InfoContext`, ...) carry the baggage and become events of the span.

## Roles

The same binary runs in two roles, scalable separately and bound to the same database:
- `ROLE=engine` (default): API, execution, tasks, schedules.
- `ROLE=event`: event worker. Reads the event descriptions from the database, subscribes to the related device data topics in the consumer group `EVENT_CONSUMER_GROUP` and correlates matches directly. Needs `KAFKA_URL` and, for the evaluation, `DEVICE_REPOSITORY_URL` with the `AUTH_*` values; unmarshalling runs in the process. If device-repository is not reachable, a QoS 1 message is read again instead of being reported to the owner.

## Gateway (fog)

With `MODE=fog`, the same binary runs on a gateway, as the only container of the process module and with SQLite:
- Device commands go to `command/{local device id}/{local service id}` of the gateway broker (`MQTT_BROKER`, e.g. `tcp://broker:1881`); answers come through `response/#`, errors through `error/command/#`, recognised by the prefix `LOCAL_COMMAND_PREFIX` (default `automation-engine-`).
- Device events come from `event/#`; the event role runs in the same process.
- `DEVICE_REPOSITORY_URL` is the device-repository mirror of the gateway, without a token; marshalling happens in the process.
- `LAST_VALUE_URL` is mgw-last-value, for services that only send events.
- `NOTIFIER_URL` points to the cloud proxy (`http://{gateway}/notifications-v2`; the engine appends `/notifications`).
- `STORAGE_BUDGET` limits the database in bytes (default in the fog 1 GiB, negative means unlimited). From 80 %, the history is trimmed beyond retention, oldest entries first, down to 70 %, with one warning per day. If the disk is full, execution stops and `/ready` reports an error until writing succeeds again.
- The local API is read-only and needs no token; deployments, starts and stops come from the cloud through the synchronisation.
- Namespaces come from the cloud with the deployments (NS-10). Whether a namespace may run on gateways (`cloud_only`) is decided in the cloud; the gateway runs what it receives and stores the setting without acting on it.
- `LEGACY_IO_POSTGRES` (DSN of the Postgres of process-io-worker of the old process module) takes over its values once on the first start, trying again with growing pauses while that Postgres is not reachable yet (the start waits up to two minutes for it, then the attempts go on in the background); values of instances are dropped, because the instances from Camunda do not continue. `LEGACY_IO_OWNER` sets the owner of the values, because the Camunda of the gateway knew no platform users. Deployments and starts of the gateway come from the cloud, which takes them over from process-sync during the migration.

Images exist for `linux/amd64` and `linux/arm64`, not for `arm/v7`. The Dockerfile compiles on the platform of the build host for the target platform, so arm64 needs no emulation.

## Synchronisation with gateways

In the cloud, `SYNC_MQTT_BROKER` connects the engine to the broker the gateways reach through mgw-device-cloud-connector. Deployments for a gateway are created through the same API with `network_id` (the hub id) and run there, not in the cloud; the cloud starts, stops and deletes the instances of a gateway, the gateway reports their state. The replicas share the messages of the gateways (`SYNC_SHARE_GROUP`); the protocol acknowledges and resends by itself (`SYNC_RESEND_AFTER`), gateways without contact since `SYNC_OFFLINE_AFTER` count as offline, and after `NETWORK_CLEANUP_MAX_AGE` their data is removed. `GET /networks` shows contact, versions and backlog per gateway, `GET /networks/{id}` the desired and the actual state per resource. permissions-v2 checks the rights on gateways (topic `hubs`: `rx` to read and start, `a` to deploy). The protocol is described in `pkg/fogsync/protocol.go`.

Namespaces with `"cloud_only": true` run only in the cloud: creating or updating one of their deployments with `network_id`, and preparing one for a gateway (`network_id` on `prepare`), is rejected with 400 naming the namespace. `smart-services` sets it, so smart services never run on a gateway; the processes they deploy into `processes` may. `processes` and every namespace without the field may run on gateways. If a namespace becomes `cloud_only` while it has deployments on gateways, the change is accepted and, as with other rules of a namespace, existing deployments are not checked again: they keep running, start, stop and can be deleted, but an update is rejected; the engine logs their ids with the change.

Debug routes and the live endpoint of a gateway instance ask the gateway through the same protocol and wait at most `SYNC_CALL_TIMEOUT` (default 10 s). If it is not reachable, the debug state answers with the last reported state and `offline: true`, the other routes with 503.

Deleting a deployment or instance of a gateway, removing the module that created it and deleting its owner all reach the gateway through the desired state. Restarts after an incident are the gateway's own; the cloud shows the planned time but never runs a gateway instance. Schedules start instances in the cloud and are rejected for deployments of a gateway. While a gateway is offline, retention and storage budget keep every instance whose end the cloud has not acknowledged, however long it takes; the change stream keeps the latest entry of each such instance. The cloud serves gateways of an older protocol version within `SYNC_PROTOCOL_WINDOW` in their version; a change of the protocol therefore updates the cloud first.

Switching deployments off: `PUT .../deployments/{id}/disabled` with `{"disabled": true}` stops running instances; after that the deployment starts nothing, not through messages and schedules either, until it is switched on again. Cloud deployments only.

Namespaces from `NAMESPACES` are created only if they do not exist yet; after that, `PUT /namespaces/{ns}` manages them (admins). A `smart-services` namespace stored by an earlier version lacks `cloud_only`; the schema migration 12 sets it once, and an admin may change it afterwards. An own `NAMESPACES` list replaces the defaults and has to set `cloud_only` for `smart-services` itself.

## API

OpenAPI in `docs/swagger.json` and `docs/swagger.yaml`, generated with `go generate ./...` from the Go types in `pkg/model` and the annotations in `pkg/api`. Regenerate after changes to the API or the types and check the files in.

AsyncAPI 3.0.0 in `docs/asyncapi.json`: the Kafka topics and MQTT topics the engine reads and writes (device commands, device and import data, platform signals, gateway, synchronisation). Also generated with `go generate ./...`, by `docs/asyncapi-gen`; the payload schemas come from the Go types of the messages, the topic names from the defaults of the configuration. Unlike other platform services, there is no conversion from 2.4.0 by hand, and CI checks the file as it does the OpenAPI.

Go client in `pkg/client`: `client.New(url)` for a running server, `client.NewTestClient(ctx)` for tests without network and containers; both implement the same interface.

## Models from Camunda

`POST /namespaces/{ns}/convert` rewrites a model written for Camunda 7 into the format of the engine without storing anything (MIG-04). JUEL becomes JavaScript (a single expression keeps its type, text with expressions becomes a template literal in which `null` is empty as in JUEL), Groovy with `println` becomes `log`, the topics `pessimistic`, `optimistic`, `process_io` and those of the modules become task types, the `http-connector` becomes the task type `notification` (target `notification` of the old process-deployment) or `http`, message names like `${done_event}` become `senergy:messageName`. The answer names what was rewritten, what has to be checked by hand and what is not supported (`mail-send`, conditions without a language), plus the findings under the rules of the namespace. Known differences to Camunda: a string `"false"` is a boolean in JUEL for `== false`, in JavaScript it is not; integral doubles appear in texts as `30` instead of `30.0`.

`/convert` is not needed before a deploy: deploying (create and update, by `xml` or by `model_id`), `prepared-deployments`, `validate` and saving a model convert on the fly. The rewrite itself tells the formats apart: a model in the format of the engine contains nothing it rewrites and stays byte for byte as it is, with an empty report; of a mixed model only the old parts are rewritten. Models are stored as they are submitted, because the designer of the web-ui recognizes device tasks by `camunda:type="external"` with `camunda:topic` and could not edit a converted model. The deployment version holds the converted BPMN, and the answer of the create or update names in `conversion` what was rewritten and what to check; it is not stored. Parts that cannot be converted reject the deploy with a validation error naming them. Saving and validating answer with the issues of the converted model and a hint (`"hint": true`) that the model is converted at deploy; hints do not keep a model from deploying. The payloads of device tasks of the old designer stay as they are: the engine reads their version 0 (function, aspect object, `device_class`, `characteristic_id`, `input`, `retries`; `label` is ignored) as the external-task-worker did. A version 2 payload with a device selected at deploy goes out as version 3, as process-deployment deployed it: the marshaller then finds the content variable by function and aspect, which the marshalling by characteristic of version 2 cannot when the characteristic is not set at the variable itself. The result is a list in both versions.


## Migration from Camunda

`automation-engine migrate` takes over the data of the old services (MIG-01 to MIG-09). The engine is configured as for operation (`-config`, environment); it needs `permissions_v2_url` and `auth_endpoint`, because the tool writes as admin and transfers rights. The sources:

- `MIGRATE_MONGO_URL`: the MongoDB of the old services; databases by the names of the platform, changeable with `-db-*`.
- `-camunda`: base URLs of the Camunda engines of the processes (all shards), each up to `/engine-rest`; `-camunda-user` and `MIGRATE_CAMUNDA_PASSWORD` for basic auth, or, with an empty `-camunda-user`, the credentials in each URL. The report names the engines without them. The Camunda of the smart services is not a source: designs, releases and instances come from the MongoDB of smart-service-repository, and its deployments would only show up as deployments that exist only in Camunda.

Without `-apply`, it is a dry run. A run creates only what is missing, keeps ids and derives necessary new ids from the old ones, so it can be repeated. The report names per step what was taken over, planned, already present, skipped, left to decide or failed; exit code 0 means complete, 1 open or partial, 2 not run. Left to decide are resources without a valid owner (`-owner old=new`), models with unsupported parts (e.g. `mail-send`) and deployments that exist only in Camunda (`-camunda-only <ids>`). `-no-restart` only lists running instances instead of restarting them; with `-json`, the report comes as JSON. For every model and every rewritten deployment the report also lists the findings of the rewrite with the kind `model`: `rewritten` (e.g. `R/P1D` became `P1D`), `check` (to look at by hand) and `unsupported`; for a model, they say what the conversion at deploy would do. They do not change the exit code, an unsupported part already leaves its deployment to decide.

Models stay in the format of the old designer, with the issues of a save; deployments are rewritten and keep their reference to the model when owner and BPMN match. Running instances restart with the current values of the variables named like the start fields. The business key becomes the instance id only if no other running instance, fog start or earlier step of the run has it; otherwise the id is generated from the Camunda id and the report says why. A later run keeps the ids of an earlier one: an instance that already exists under its key is recognised by owner, deployment, start time (read from the Camunda history) and start values, and is not restarted again; if that does not tell several candidates apart, none of them is restarted and they are left to decide. IO values follow their instance in key and binding; values of ended instances are dropped.

Smart service designs become models in the namespace `smart-services`, the releases of a design the versions of one deployment with the id of the newest release. Instances whose setup has ended are taken over with their modules and variables and not run again, because that would create every module a second time; an instance still in setup restarts. Delete references of modules to process-deployment and process-sync become deployments and instances of the engine, other URLs stay; a reference by a business key that several instances had keeps its URL and is left to decide. What process-sync holds for gateways becomes a deployment and a start of the network and runs as soon as the gateway has the engine; the owner is the one administrator of the hub or the one named with `-owner network:<id>=<user>`.

## Tests

```sh
go test ./...          # with the container variants of the integration tests
go test -short ./...   # without them, as CI runs
```

Store and engine tests run against SQLite and against Postgres in a container (testcontainers). Without a container runtime, the Postgres variants are skipped; `AE_TEST_POSTGRES=off` switches them off. The comparison of the rewrite with Camunda 7.24 (MIG-05) and the readers of the migration start Camunda and MongoDB in containers as well; `AE_TEST_CAMUNDA=off` and `AE_TEST_MONGO=off` switch them off. `AE_BPMN_EXPORT_DIR=<directory>` additionally checks parser and rewrite against exported models.

`pkg/integration` plays through the usual user flows over HTTP against a complete server: a process from model to completion, a schedule with switching off and on, an incident repaired through a new version, values through process-io, the path of the web-ui through the `deprecated/` routes, a smart service from design to upgrade and a smart service that manages a process. The event scenarios additionally start an event role of their own on the same database: a waiting process continued by a device or an import (with conversion from kelvin to celsius), and starts by the devices of a group, which stay away while the deployment is switched off. The tests of the other packages check single features. Every scenario runs twice: `fakes` with stand-ins in the process (permissions-v2 in the process, SQLite, for the events a Kafka stand-in and a fake for device-repository, import-deploy, import-repository and Keycloak) and `containers` against the real dependencies in testcontainers (Postgres, MongoDB, Kafka and the `:dev` images of permissions-v2, device-repository, import-repository and import-deploy; Keycloak and Rancher stay stand-ins, because the services do not verify signatures in tests and import-deploy would otherwise start import containers). The containers start once per test run and are shared; without Docker, the variant fails instead of skipping itself. `go test -short ./...` skips the container variants; that is how CI runs.

### Soak test (LONG-15)

`pkg/soak` runs a gateway engine with a cloud engine in the same process under steady load: the cloud starts instances of a process that waits for a device event, queries a device, waits one second and writes a variable of about 1 KiB; simulated devices answer through the broker of the gateway and send events. The connection to the cloud drops at times. At fixed intervals, the size of the database, the used space, heap, goroutines, open files, instances and the backlog of the synchronisation land in a CSV.

Without `SOAK_DURATION`, it runs for 5 s in the normal suite and only checks that all started instances complete. From 30 min on, it additionally checks that the resources stop growing after settling: database file, heap, goroutines and open files in the last quarter against the second. The median of the used space in the last quarter has to stay below the budget; peaks above it are allowed, because the budget is enforced only per cleanup round and instances stay after an offline phase until the cloud knows their end.

On an arm64 gateway, as a pre-built test binary:

```sh
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -o soak.test ./pkg/soak
# on the gateway
SOAK_DURATION=72h SOAK_DIR=/data/soak ./soak.test -test.run TestSoak -test.timeout 0 -test.v
```

Variables: `SOAK_DURATION`, `SOAK_DIR` (databases and report, default a temporary directory), `SOAK_REPORT` (CSV, default `SOAK_DIR/soak.csv`), `SOAK_SAMPLE_INTERVAL`, `SOAK_STARTS_PER_SECOND` (default 2), `SOAK_EVENT_INTERVAL` (default 500 ms), `SOAK_STORAGE_BUDGET` (default 32 MiB; the change stream and receipts are cleaned up only after 1 h and 24 h, a budget that is too small leaves no history), `SOAK_OFFLINE_EVERY` and `SOAK_OFFLINE_FOR` (default a quarter and a fortieth of the duration). `SOAK_MQTT_BROKER` with `SOAK_MQTT_USER` and `SOAK_MQTT_PASSWORD` uses a real broker instead of one in the process; the synchronisation then runs over its topics `processes/cmd/...` and `processes/state/...`, so use it on a gateway without a running mgw-device-cloud-connector, otherwise the messages reach the real cloud. Marshaller and device repository are simulated in the test; what is measured is the behaviour of the engine over time, not the conversion of values.
