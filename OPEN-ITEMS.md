# Open items outside this repository

Collection point for tasks and questions that concern other repositories, operations or the platform configuration. They are gone through together once all milestones are done. Until then, new items are added here, not scattered across `PLAN.md`.

Every item names its occasion (requirement id or milestone) and is either a **task** (what has to be done there) or a **question** (what has to be settled before anyone acts). Done items are checked off, not deleted, until the list as a whole is through.

## Routing and access

### Kong, Ladon

- [ ] **Task:** One route for the new API plus `deprecated/`; redirect today's routes to `deprecated/` (API-08, M7), with path rewriting and without the `strip_path` of the old services: `/process/engine` → `/deprecated/engine`, `/process/repository` → `/deprecated/repository`, `/process-io` → `/deprecated/io`, `/process/incidents` → `/deprecated/incidents`, `/process/scheduler` → `/deprecated/scheduler`, `/process/deployment` → `/deprecated/deployment`, `/smart-services/repository` → `/deprecated/smart-services`; `/process-sync` → `/deprecated/process-sync` and `/process-fog-deployment` → `/deprecated/fog-deployment` only once no gateway runs Camunda any more, because the layer covers only gateways with the engine. Ladon rules of the old paths keep applying to the new targets if the rewrite takes effect after Ladon; otherwise they have to be changed as well.
- [ ] **Note (behaviour):** Under `deprecated/`, the status codes of the new API apply (403 instead of 401, 404 instead of 500 for unknown ids). A start with `business_key` creates the instance with this id; a second start with the same key and the same variables answers with it, with other variables with 409, instead of creating a second instance. Deleting a running instance through the engine routes stops it and keeps its history, as Camunda did. Deployments through `/deprecated/deployment` are rewritten on creation (MIG-04); a model with `mail-send` is rejected with 400. `GET /dependencies` no longer names devices, because the engine does not store their names; generic event sources (`selected_generic_event_source`) are rejected. Releases of a smart service design are versions of one deployment: a further release keeps the id, older versions are called `<id>:v<n>`, a release can only be deleted as a whole. The layer knows the rights in `permissions_info` only roughly (owner everything, otherwise read); `value_label` of parameters is not stored. Under `/deprecated/process-sync`, networks are named by their id (the engine does not know the names of the hubs), `POST /sync/deployments` does nothing any more.
- [ ] **Task:** Do not route the internal port (`INTERNAL_PORT`, default 8081). It carries `/metrics` and the HTTP answers of devices (`/responses`, `/errors`) (M3).
- [ ] **Task:** Pass the WebSocket upgrade through for `/namespaces/*/instances/*/live` (DBG-01, DBG-08, M4).
- [ ] **Task:** Routes and Ladon rules for the paths from M5: `/namespaces/*/modules[/*]`, `/namespaces/*/deployments/*/parameters`, `/namespaces/*/instances/*/parameters`, `/namespaces/*/instances/*/maintenance-procedures[/...]`, `DELETE /namespaces/*/instances/*/variables/*`.
- [ ] **Task:** Ladon rules for the admin endpoints `/users` and `PUT` and `DELETE /namespaces/{ns}` (SEC-14, NS-02, M2) and for the debug paths `debug`, `live`, `history`, `event-evaluations`, `variables/{name}` under `/namespaces/*/instances/*` (M4).
- [ ] **Task:** Remove the dead routes `/api-aggregator` and `/eventmanager`.
- [ ] **Task:** Routes and Ladon rules for the paths added after M5 (audit 2026-10-20): `/namespaces/*/convert`, `/networks[/*]`, `/namespaces/*/deployments/*/disabled`, `/namespaces/*/deployments/*/instance-count`, `/namespaces/*/resource-usage/*/*`, `/namespaces/*/instances/*/info`, `/namespaces/*/prepared-deployments`.

### Keycloak

- [ ] **Task:** The client of the engine needs the realm role `admin` (permissions-v2) and the permission for token exchange. Since the owner check through `CheckPermission`, token exchange is required as soon as permissions-v2 is configured; device commands and the event reconstruction use it as well (SEC-03, SEC-06, SEC-11).

## Platform services

### permissions-v2

- [ ] **Question:** Can the withdrawal of the owner right be prevented in permissions-v2 itself, e.g. through a topic option? Today the engine reacts to the rights signal over Kafka and restores the right (SEC-10, M3). A solution in permissions-v2 would also benefit devices and hubs.
- [ ] **Question (operations):** Is `PERMISSIONS_KAFKA_TOPIC` set in the engine? Without a value, an owner right withdrawn in permissions-v2 stays withdrawn there; in the engine, the owner keeps access (M3).

### user-management

- [ ] **Task:** Before deleting a user, call the dry run of the engine, then the delete call (`/users`), instead of addressing process-scheduler. Kafka `user` remains the fallback (SEC-14, M2).

### gin-middleware (`otelx`)

- [ ] **Question:** `otelx` puts `username` from the token into the baggage; the OpenTelemetry handler of struct-logger writes the baggage into every log entry with context. The user name thus ends up in the logs of all services instrumented this way. Is that intended, or should `otelx` set only `user_id` (tracing, after M3)?

### device-selection, device-repository, marshaller

- [ ] **Task:** The Go clients take no context, so no trace context is passed on. For device-selection this concerns its own client, for device-repository the access in `pkg/devices` (formerly from `external-task-worker/lib`, now in this repository and extensible with a context), for marshaller the client from `marshaller/lib` (tracing, after M3).

### timescale-wrapper, timescale-rule-manager, timescale-tableworker

- [ ] **Question:** To which tables does timescale-rule-manager apply retention rules (e.g. `retention-one-year`)? That limits how far back the reconstruction of event evaluations reaches (EVT-12).
- [ ] **Question:** Does import data land in Timescale? Whether import events can be reconstructed depends on it; until then, the engine shows only the correlations for them (EVT-12, M4).
- [ ] **Question:** Does the evaluation on Timescale values yield the same as on the Kafka messages? To be checked are paths, conversion, timestamps and lists with fixed entries, which come back from the columns as a map instead of a list (EVT-10, M4).
- [ ] **Task (coordination):** The engine derives the column names from the service outputs as timescale-tableworker does (names joined with a dot, lists with `*` as one column) and queries `/queries` with the token of the owner. Changes to the column naming or to the answer format of `/queries` break the event reconstruction and the evaluation of the current value; both repositories should know (M4).
- [ ] **Question:** Do the device events forwarded by the gateway arrive in Timescale completely, including the mapping of local ids to cloud ids (EVT-13, M6)?

### notifier, developer-notifications

- No open items; both are targets of notifications and incident reports (INC-02).

## Libraries and shared models

### external-task-worker, event-worker (packages)

- [x] Since 2026-10-12, the engine imports neither from external-task-worker nor from event-worker. The parts used are in `pkg/devices`, `pkg/tasks/devicecommand` and `pkg/events/unmarshal.go`.
- [ ] **Task:** device-command and live-data-worker still import from `external-task-worker/lib`. The repository stays for them until they are switched over; the engine is no longer affected.
- [ ] **Note:** The formats of the command payload and the protocol message are now defined twice, in `external-task-worker/lib/messages` and in `pkg/tasks/devicecommand/messages.go`. If platform-connector changes them, both sides have to follow.

### smart-service-module-worker-lib and the worker repositories

- [x] Script API, templates and the handlers of `info`, `import`, `export`, `analytics` and `device_repository` have been in the engine repository since 2026-10-12 (`pkg/modules`, `pkg/modules/compat`); `go.mod` no longer names the repositories. The test cases of the workers keep running there as golden tests (`pkg/modules/testdata`).
- [ ] **Task:** Archive the repositories once the worker deployments are shut down (see shutdowns). Changes to them no longer reach the engine.
- [x] The bugs found are fixed in the engine, not in the repositories: `info` keeps reading after an additional field that is not a string; `analytics` stops on an error of the module lookup instead of creating a second pipeline; a pipeline that was only updated is not deleted on undo. `persistData` as a boolean stays `false` as before.

### device-repository

- [x] Since 2026-10-12, the engine uses `device-repository/v3` (v3.4.0).
- [ ] **Task (device-selection):** `device-selection/v2` (up to v2.0.3) still pulls in `device-repository/v2` through `pkg/model`; it is therefore an indirect dependency in the `go.mod` of the engine. With a device-selection on `/v3`, it goes away.

### process-model-repository, analytics-flow-repo-v2, import-repository

- [ ] **Task:** Before deleting a resource, they ask smart-service-repository who uses it. After the redirect, their smart service URL points to `/deprecated/smart-services` (same format) or to `GET /namespaces/smart-services/resource-usage/{kind}/{id}` of the new API. The engine checks process models itself: deleting a model in use through `/deprecated/repository` answers with 409; with `?force=true`, an admin deletes it anyway (SMS-15, after M7).
- [ ] **Question (operations):** The check of changed resources reads flows and import types with the token of the owner and, on 401/403, with that of the engine. May the client of the engine read in analytics-flow-repo-v2 and import-repository (realm role `admin`)? Without that, a deleted resource the owner no longer sees stays unreported (after M7).

### web-ui

- [ ] **Task:** The switch for switching deployments off and on (camunda-engine-wrapper v1.1.0) keeps working through `/deprecated/engine/v2/deployments/{id}/disabled`; for the new API it is `PUT /namespaces/processes/deployments/{id}/disabled`. The display of changed resources on releases reads `changed_resources` as before (DEP-12, SMS-15, after M7).

### models (`models/go`)

- [ ] **Question:** Do the types of the engine belong there or in the engine repository? Today the engine repository keeps them in `pkg/model` (CLI-01).

### platform-connector-lib

- [ ] **Task:** Adapt `task_info` with process ids in the command envelope to the new model (DATA-04).

### smart-service-module-worker-lib

- [ ] **Question:** Is the library imported outside the eight workers? It goes away with the integration of the modules (M5).

## Consumers of the API and the metrics

### web-ui and process designer

- [ ] **Task:** Show what the conversion of a model of Camunda or the old designer does: deploy, prepare, validate and model save convert on the fly, without a call of `POST /namespaces/{ns}/convert`; the answer of the deploy and of `prepared-deployments` names it in `conversion` (rewritten, to check by hand), model save and `validate` add a hint (`hint: true`) to the issues (MIG-04, M7, 2026-10-20).
- [ ] **Task:** Fog deployments through the same API with `network_id` instead of through process-fog-deployment and process-sync; network overview through `GET /networks` and `GET /networks/{id}` with desired and actual state per resource; instances of a gateway are `pending` until its first report (DEP-09, SYNC-12, SYNC-13, M6). Instances of a gateway can be started, stopped and deleted from the cloud, not restarted or changed in variables and parameters. Versions in `GET /networks/{id}` are large numbers (they start at the time in milliseconds) and are only good for comparing desired and actual; the start of an ended instance no longer appears there.
- [ ] **Task:** Debug view of a gateway instance: `offline: true` in the debug state means the last reported state, the gateway is not reachable; variables, runs and history are then not available (503) (DBG-15, M6).

- [ ] **Task:** Switch over to the new API based on the OpenAPI in `docs/swagger.json` (API-04, API-07), with namespaces (NS-09), an update dialog with strategy (DEP-04), model versions (MOD-05) and retention settings (HIST-04).
- [ ] **Task:** Smart service designer: wait for the completion of a deployment with `senergy:messageName` (JavaScript, e.g. `done_event`) instead of a `${...}` message name; no loops in the sequence flow in `smart-services` (M5). Start and procedure dialogs through `.../parameters`, status through `status`/`status_message`, modules through `/modules`.
- [ ] **Task:** Deploy dialog: values of single elements (inputs, event variables, timers, start defaults) through `parameters` of the deployment (DEP-01, M5).
- [ ] **Task:** The designer writes only the supported BPMN subset (BPMN-18), only JavaScript (EXPR-01) and the model format of the engine (until then, the engine converts the old format at deploy and stores models as they are, 2026-10-20): `senergy:taskType` instead of topics, `senergy:retries` and `senergy:timeout` on the service task, `senergy:completionStrategy` on device commands, device and import events with `senergy:*` attributes (M1, M3). One designer for all namespaces (NS-09).
- [ ] **Task:** Live and historical debugging (section 13, M4): WebSocket `live` with the token as first message `{"type":"auth","token":"..."}`, close codes 4000 plus HTTP status (4401 means: get a new token and resume with `after`), apply deltas as described in `model.DebugMessage`, steps through `history`, event reconstruction through `event-evaluations`. Taken sequence flows are now in `element-stats` (DBG-07).
- [ ] **Task (operations):** If the web-ui sends the token as the first message, the engine needs `KEYCLOAK_CERT_URL`, because the token then does not pass Kong (DBG-08, M4).
- [ ] **Task:** Switch the widgets `data-table` and `device-status`, which today create hidden deployments (`source=generated`), over to the new API.
- [ ] **Note:** `POST /namespaces/{ns}/prepared-deployments` with `network_id` offers only the devices of the hub of that gateway, as process-fog-deployment did (`25d45ad`); the deploy dialog of a gateway should pass it.

### dashboard

- [ ] **Task:** Adapt stored widget types with a process reference to the new API.

### canary, snowflake-canary

- [ ] **Task:** Switch over to the Go client `pkg/client` (CLI-08).

### cost-calculator, opencost-wrapper

- [ ] **Task:** Query the engine pods instead of the external-task-worker pods. The metric names `external_task_worker_task_command_send_count_vec` and `external_task_worker_task_marshalling_latency` stay, and so do the labels; `process_definition_id` now carries the deployment id (TASK-12, M3).

## Migration (M7)

- [ ] **Task (operations):** Run the migration as a one-time job in the cluster: first as a dry run, check the report, make the decisions (`-owner`, `-camunda-only`), inform those affected by running instances (MIG-02), then with `-apply`. Needs read access to the MongoDB of the old services (`MIGRATE_MONGO_URL`), the REST API of all Camunda shards with basic auth (`MIGRATE_CAMUNDA_PASSWORD`) and the client of the engine with the admin role.
- [x] **Question:** Where does camunda-engine-wrapper v1.1.0 store the state "disabled" per deployment (`PUT /v2/deployments/{id}/disabled`)? Answered on 2026-10-13: it suspends the process definition in Camunda; the migration reads the `suspended` flag of the definition and takes the deployment over switched off (DEP-12).
- [ ] **Note:** Modules of the old watcher keep their URL; the migration does not take over the watches themselves, they are in the database of the watcher. Smart service instances whose setup was still running during the migration restart; modules already created may then exist twice (the report names them).
- [ ] **Question:** What is the collection of process-scheduler called in prod (`process_scheduler` as database is known, locally the collection is called `process_schedule`)? Changeable with `-collection-scheduler`.

## Shutdowns

- [ ] **Task:** Shut down smart-service-engine (Camunda), smart-service-repository with its Mongo, the eight module workers and the `watcher-db` once M7 has migrated the data (SMS-01).
- [ ] **Task:** Shut down event-worker and event-deployment; the event worker is a role of the engine (`ROLE=event`, M3). The engine no longer imports event-worker; the repository can be archived afterwards.
- [ ] **Task:** Remove the Kafka topic `process-deployment-done` once event-deployment is off; check the leftovers `deployments_v3`, `camunda_incident` and probably `processmodel` along with it.
- [ ] **Task:** Archive process-sync, mgw-process-sync-client and process-fog-deployment once no gateway runs Camunda any more. Until then, process-sync serves the old gateways and the engine only gateways with itself; both use separate topics under `processes/{nid}/` (M6, migration M7).
- [ ] **Task:** Switch off api-aggregator.
- [ ] **Task:** Replace the ArgoCD apps `process-engine-prod` and `smart-service-engine-prod`.

## Gateway (M6)

- [ ] **Task (operations):** Alerts on `automation_engine_network_last_contact_seconds` for gateways without contact, replacing the ping self-test of process-sync (SYNC-12), and the switch of today's process alerts to the metrics of the engine (OPS-05); neither exists yet (audit 2026-10-20).

- [ ] **Task (acceptance):** Soak test on an arm64 gateway with `pkg/soak` for at least 72 h, file the report (CSV) (LONG-15). Locally it ran for 30 min on amd64; whether flash storage, a slower CPU and the real broker change anything only the gateway shows.
- [ ] **Task (operations):** Create the workflow for prod images (`prod.yml` with tagging as in the other repositories) once the engine is to be released; until then there is only `dev.yml`. The secrets `DOCKERHUB_USERNAME`, `DOCKERHUB_TOKEN` and `CR_PAT` have to be available to the repository (FOG-02).

### VerneMQ and senergy-platform-connector (`lib/handler/process`)

- [ ] **Task (operations):** The replicas of the engine connect to `SYNC_MQTT_BROKER` as an admin client: they subscribe to `$share/automation-engine/processes/+/state/engine/v1/#` and publish to `processes/{nid}/cmd/engine/v1/...` of every network. The webhook allows wildcards and `$share` only for admins. Every replica needs a client id of its own (default `automation-engine-{hostname}`).
- [ ] **Question:** Does the VerneMQ in use support shared subscriptions with QoS 1 so that a message is delivered to exactly one replica? Double delivery would be harmless (the protocol detects it), the gateway resends lost ones.

### mgw-device-cloud-connector

- [x] Upstream with QoS 0 and a buffer in memory only: the sync protocol of the engine acknowledges and resends by itself (M6); the connector stays an unchanged MQTT relay for `processes/...`.

### mgw-module-repository (`process/Modfile.yaml`)

- [ ] **Task:** Switch the process module `main/process` (today v1.5.4 with seven containers: Camunda and Postgres, mgw-process-sync-client, mgw-external-task-worker, event-worker, process-io-worker and its Postgres) over to one container of the engine: `MODE=fog`, `MQTT_BROKER=tcp://{broker}:1881`, `DEVICE_REPOSITORY_URL=http://{device-repo-mirror}:8080`, `LAST_VALUE_URL=http://{mgw-last-value}:8080`, `NOTIFIER_URL=http://{gateway}/notifications-v2` (the engine appends `/notifications`; corrected 2026-10-20, FOG-08), `MQTT_CLIENT_ID` unique per gateway, a volume for `SQLITE_PATH`, optionally `STORAGE_BUDGET`. Dependencies stay mirror, cloud proxy, broker and mgw-last-value (M6). On the update to the engine, keep the Postgres of process-io-worker reachable for the first start and set `LEGACY_IO_POSTGRES` and `LEGACY_IO_OWNER` (owner of the network), so that the IO values are taken over; afterwards Camunda, its Postgres and that of process-io can go (MIG-06).
- [ ] **Note:** Local topics use the `LocalId` from device-repository unchanged. If mgw-device-cloud-connector sets `LDH_ID_PREFIX`, they do not match the topics of the connectors; that applies to mgw-external-task-worker and the event-worker today as well.
- [ ] **Question:** The cloud proxy authenticates with the certificate of the network. The engine sets the `userId` of the owner of the instance in notifications; does notifier reject a `userId` that is not the user of the certificate? Warnings about the storage budget go without `userId` and reach the user of the network.
