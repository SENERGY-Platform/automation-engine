# Plan: Automation-Engine

Basis: `docs/requirements.md` (status 2026-10-12). The IDs in parentheses refer to it. Decisions recorded here take precedence over the text of the requirements where the two differ; the requirements are not rewritten for them. What is still open after the milestones is in "Audit against the requirements (2026-10-20)" at the end.

The requirements describe a programme, not a single change. They are implemented in milestones, each of which runs and is tested on its own.

| # | Milestone | Core IDs | Status |
|---|---|---|---|
| M1 | Execution core: BPMN subset, JS sandbox, token execution, timers, correlation, runs, history, namespaces from configuration, new REST API, Go client with test client | BPMN-01–18, EXPR-01–05, DATA-01–03, NS-01–03/06/11 (configuration), MOD-01/03–05, DEP-02/04, RUN-01–12 (without 07), HIST-01–03/05, LONG-01–05/08/12/16, EVT-01/02/05, CLI-01/03/04/08/09 | implemented, see below |
| M2 | permissions-v2 (topics per namespace, initial rights, owner protection), incident handling with restart and backoff, retention with upper limits, deleting users, admin API for namespaces, Prometheus metrics, token provider in the client | NS-02/05/07, SEC-01–14, INC-02–04, HIST-04/06, LONG-03 (age), LONG-13/14, MOD-02/07/08, OPS-05, CLI-10 | implemented, see below |
| M3 | Task types device command, process IO, notification, HTTP call; schedules; event worker as a role of the binary, event descriptions, group expansion; preparation API; Kafka `user` and rights signal | TASK-01–12, IO-01–03, BPMN-09/10, RUN-07, DEP-01/05–10, EVT-01–08, NS-04/11 | implemented, see below |
| M4 | Debugging: outbox, WebSocket live stream, historical debugging, reconstruction of event evaluations | SYNC-03, DBG-*, EVT-09–13, CLI-05 | implemented, see below |
| M5 | Smart service modules, watcher, maintenance procedures | TASK-20–29, SMS-* | implemented, see below |
| M6 | Fog mode and synchronisation | FOG-*, SYNC-*, LONG-06/07/09–11/14/15 | implemented, acceptance on an arm64 gateway open |
| M7 | `deprecated/` layer, migration from Camunda, model rewrite | API-02/03/08, MIG-* | implemented, Kong redirect and migration run open |

What concerns other repos, operations or the platform configuration is collected in `OPEN-ITEMS.md` and is gone through together after the milestones.

## M1: Decisions

**Persistence.** Postgres and SQLite from the start (OPS-02, FOG-03, OPS-03), both through `database/sql` with shared SQL. Placeholders are written as `?` and rewritten for Postgres. Times as Unix milliseconds (`BIGINT`), JSON as `TEXT`, so that there are no dialect differences in types. Drivers: `modernc.org/sqlite` (pure Go, no cgo, important for arm64 images), `pgx/v5/stdlib`. Rejected: Mongo as the platform default, because the requirements specify Postgres; SQLite only at first, because the locking semantics for timers with several replicas (RUN-06, OPS-01) would otherwise be checked only late.

**Concurrency.** Every state transition of an instance runs in a transaction that locks the instance row (Postgres `FOR UPDATE`, SQLite `BEGIN IMMEDIATE`). A timer or job is deleted in the same transaction in which its effect is committed. A second replica no longer finds it afterwards. This gives "exactly once" without leader election.

**Task types.** A service task creates a job. It is executed outside the transaction and completed in a new transaction that checks whether the job still exists and still belongs to the same execution (TASK-11). Timeout and retries are set by the engine (TASK-07). M1 only delivers the interface (TASK-00), no platform types yet. A type that is allowed but not present in this engine is rejected at deploy.

**Model format.** The Camunda dialect remains the basis, so that the designer and bpmn-js keep fitting. Deviations:
- Task type via `senergy:taskType` instead of `camunda:type="external"` with `camunda:topic` (MIG-04 rewrites).
- Conditions need `language="javascript"`. Timer values are text, an expression with `language="javascript"`.
- Mapping values are text or `camunda:script` with JavaScript (EXPR-02). Text with `${` is rejected (EXPR-01).

**Variable scopes** as in Camunda (`AbstractVariableScope.setVariable`): input mappings create local variables on the element. `setVariable`, `resultVariable` and output mappings write locally if the variable exists there, otherwise at instance level.

**Gateways.** The parallel join counts tokens as Camunda 7 does (number of incoming flows), not per flow. The inclusive join waits as long as another active token can reach it (Camunda 7). Both are to be retested against Camunda (BPMN-15); this was not done in M1.

**IDs.** Instance IDs are globally unique, which is stricter than "per tenant" (RUN-01). A `PUT` on a foreign, invisible ID gives a conflict without details.

**Rights in M1.** Only owner and admin role. The check sits behind an interface (`controller.Authorizer`), into which M2 plugs permissions-v2. JWT as in service-commons, with signature verification if a Keycloak URL is configured (SEC-04).

**API.** `net/http` with the pattern routing of the standard library. Resources under `/namespaces/{ns}/...`, correlation under `/messages`. Paging via `limit` and `offset`, the answer always contains `total`. OpenAPI via swag from the Go types of the server (CLI-01).

**Limits (defaults, configurable).** 100 tokens per instance (LONG-16); 1 MiB per variable, 4 MiB per instance (LONG-08); 10 000 history entries per instance as a ring buffer (LONG-03); 2 s per script evaluation (EXPR-03).

**Further definitions in the model format** (affect the designer in the web-ui):
- `senergy:retries` and `senergy:timeout` (ISO-8601 duration) on the service task (TASK-07).
- Message events with `senergy:*` event attributes or without `messageRef` are device or import events. Their message name is `<deployment_id>_<element_id>`, stable across updates (DEP-04). Named messages (`messageRef` with `name`) keep their name.
- Conditions must yield a boolean; otherwise an incident arises.
- `NaN` and `Infinity` from scripts are stored as strings, because JSON does not know them; `Number(x)` reads them back.

## M1: Status

Checked with `go test -race ./...` against SQLite and Postgres 16 (testcontainers). The 63 exported models are all read; their findings are exclusively ones that MIG-04 rewrites or reports (test `TestExportedModelsParseWithOnlyMigrationIssues`, needs `AE_BPMN_EXPORT_DIR`, the models are deliberately not in the repo).

Partially implemented:
- DEP-03: BPMN, version and start events atomically; event descriptions, incident settings, retention and schedules follow with M2/M3.
- MOD-06: deployments of a model via the filter `model_id`; the version distance is not computed.
- MOD-08: the model reference is marked as deleted on delete; deleting users follows in M2.
- INC-01/02: incidents arise in the engine and end the run (today's default "stop"). Notification and restart are missing until M2.
- EVT-08: no metric for correlations without a target.
- SEC: only owner and admin role; sharing via permissions-v2 in M2.
- CLI-02: covers the existing API; debugging, IO, schedules and modules come with their milestones.

## M1: Known gaps

- **Memory limit of the sandbox (EXPR-03):** goja has no memory limit. What takes effect is the time limit and the size limit for results. A script that allocates a lot of memory within 2 s is not caught. A hard limit needs a separate process or a different runtime.
- **Camunda comparison (BPMN-15, MIG-05)** is pending.
- **Retention by age** is missing until M2. Until then only the ring buffer limits the history.
- **CI** is missing in the repo, and with it the drift check for the OpenAPI files in `docs/`.

## M2: Decisions

**Default permissions (NS-05), decided 2026-10-12.** In permissions-v2 the `DefaultPermissions` of a topic apply on every check to all resources of the topic (`pkg/controller/check.go`, v1.0.0); they are not initial rights. NS-05 assumes otherwise. Implemented therefore, per namespace and level (`models`, `deployments`, `instances`):
- `shareable`: rights of others from permissions-v2 count. Otherwise only owner and admin role have access, even if something is shared in permissions-v2.
- `initial_rights`: groups and roles that every new resource gets on creation, like `InitialGroupRights` in device-repository.
- `topic_default_permissions`: the real topic-wide rights, default empty.
- `register`: register resources of non-shareable levels in permissions-v2 anyway, default yes (decided). If a level is registered that was not before, the engine registers the existing ones afterwards.

Rejected: taking `DefaultPermissions` literally, because then a grant applies to all resources of a topic and "only for the owner" with initial rights cannot be expressed.

**Owner (SEC-09, SEC-10).** The engine checks the owner itself; the owner always has all rights, independent of permissions-v2. A direct withdrawal in permissions-v2 therefore has no effect. The entry there is only restored at the next registration (owner change, new registration). An ongoing repair via the Kafka signal of permissions-v2 follows with M3.

**Asynchronous registration.** New resources carry `perm_registered = 0`; `permissions.Service.Sync` writes them to permissions-v2 and sets the flag. Deletions write a row into `permission_removals` in the same commit, which Sync works off. This way no HTTP call runs in an open transaction, and an outage of permissions-v2 only delays sharing. Rejected: synchronous writing before the commit, because then every start, including every timer start, depends on the availability of permissions-v2.

**Restart after incident (INC-02 to 04).** The incident only sets `restart_at` with backoff (default 1 min, doubled up to 1 h, by number of consecutive incident runs). Counting and restarting happen at the due time in a separate transaction that locks first the deployment and then the instance, in the same order as an update. Counting in the incident would have meant the reverse order and with it deadlocks under Postgres. Above `max_restarts` (default 10) per window (24 h) the deployment is marked as faulty and the owner is notified; it is reset by an update or `POST .../reset-restarts`. A stop cancels a planned restart.

**Notifications** go through an outbox (`notifications`) and are sent after the commit, with retry and discarding after 24 h. Format as process-incident-api today: title "Process-Incident in <Deployment>", topic `incident`, `ignore_duplicates_within_seconds=3600`, copy to developer-notifications.

**Retention (HIST-04).** What takes effect is the smallest of the deployment value or default (namespace, otherwise global 7 days), upper limit of the namespace, global upper limit and upper limit of the instance owner. A deployment value above the limits of the owner is rejected. Cleanup runs in the housekeeping round (default every minute) for 50 deployments per round and then for every user with a limit. Finished instances are deleted completely; for all others the history before the limit is removed (HIST-06). Instances with a planned restart stay.

**Namespaces (NS-02)** are in the database. At start the configuration only creates missing ones; after that the admin API applies (`PUT`/`DELETE /namespaces/{ns}`). Topics are written before saving. A namespace with resources cannot be deleted. Every replica reloads the namespaces every 10 s.

**Deleting users (SEC-08, SEC-13, SEC-14).** `GET /users/{id}/dependents` and `DELETE /users/{id}`, for admins only. Foreign dependents are currently instances of others on deployments of the user. The default is rejecting with 409 and a list (decided); with `user_deletion: cascade` on the namespace they are deleted and their owners notified. The rights of the user on foreign resources are removed first; if permissions-v2 fails, nothing is deleted. The engine lists the user's resources with an unsigned token of the user, as device-repository does, because permissions-v2 does not verify signatures.

**Internal calls (SEC-03)** to permissions-v2 run with client credentials (`AUTH_ENDPOINT`, `AUTH_CLIENT_ID`, `AUTH_CLIENT_SECRET`). The client needs the realm role `admin`, because it creates topics and sets initial rights. Without `PERMISSIONS_V2_URL` only owner and admin role apply.

**Metrics** on a separate port (since M3 `INTERNAL_PORT`, before `METRICS_PORT`, default 8081), so that Kong does not route them: `automation_engine_runs_started_total`, `_runs_ended_total`, `_incidents_total`, `_automatic_restarts_total`, `_correlations_total{result}` (EVT-08), `_task_executions_total`, `_task_execution_seconds`, `_timer_delay_seconds`, `_storage_rows`, `_database_bytes`, `_stalled_instances` (LONG-13, `STALLED_AFTER`), `_instance_activities_top` (LONG-14).

## M2: Gaps

- Kafka fallback `user` (SEC-14) and the rights signal of permissions-v2 need Kafka and follow with M3.
- The rights from `processmodel`, `smart_service_releases` and `smart_service_instances` move into the new topics only with the migration (MIG-08, M7).
- Dependents via modules (NS-08) exist only with M5; schedules and IO data when deleting a user with M3.
- If several replicas run `Sync` at the same time, a rare sequence (delete during a registration running in another replica) can leave an orphaned entry in permissions-v2. It grants no access, because the resource is missing in the engine.
- Lists on shareable levels fetch all granted IDs from permissions-v2 and filter with `IN`. With very many grants per user this becomes expensive.

## M3: Decisions (2026-10-12)

**Stages.** a) Device commands with device selection and preparation API, b) process IO, notification, HTTP call, schedules, c) event worker and Kafka signals.

**Reuse of external-task-worker v0.1.2 (decided).** Imported are the contract types (`lib/messages`: `Command`, `ProtocolMsg`, `TaskInfo`), the device-repository access with token exchange and cache (`lib/devicerepository`), the marshaller client (`lib/marshaller`) and the Timescale client (`lib/timescale`). The orchestration (protocol message, subtasks for groups, inputs on paths, evaluation of the response) sits there in methods of the Camunda worker and is ported to the job model of the engine; the origin is stated in the files. As soon as CLI-11 creates a stable place, the imports switch there. Superseded on 2026-10-12: the parts in use were pulled into the repo, see "After M5".

**Model format device command.** `senergy:taskType="device_command"`, `senergy:completionStrategy` (`optimistic` or `pessimistic`, default pessimistic). The task definition remains the `payload` JSON in the model as today; the device selection is stored typed per element in the deployment version (DEP-05) and inserted at execution.

**Event worker (decided).** The event-worker becomes part of this repo as a separate role of the same binary, separately startable and scalable, and works coupled through the database:
- Event descriptions are in the engine database with a running revision; deletions leave a tombstone. Every worker keeps all descriptions in memory and fetches only changes since its last revision. This replaces `process-deployment-done` and invalidates selectively (DEP-08).
- A worker subscribes only to topics for which descriptions exist, instead of all device data topics by regex.
- Correlation in-process against the database; with QoS 1 the offset is acknowledged only after the commit; idempotency key `topic:partition:offset` (EVT-05).
- Evaluation (unmarshalling, characteristic, script) in a shared package that the reconstruction in M4 also uses (EVT-10).
- Rejected: decoupled through Kafka (compacted topic plus HTTP correlation) and decoupled through the API, because both need tokens, a publisher and a second solution in fog. The price of the decision: every worker pod is a database client.

## M3: Status

- **Device command** (`pkg/tasks/devicecommand`): protocol message via Kafka or HTTP, subtasks per device for device groups (`GROUP_SCHEDULER` sequential or parallel), optimistic and pessimistic completion strategy, responses and errors via the topics `response`/`errors` or `/responses` and `/errors` on the internal port, timeout as job timeout. Metrics with the names of the external-task-worker (`external_task_worker_task_command_send_count_vec`, `external_task_worker_task_marshalling_latency`), so that the cost-calculator keeps reading them (TASK-12).
- **Device selection and preparation** (`pkg/prepare`, `POST /namespaces/{ns}/prepared-deployments`): criteria from the model, options via device-selection, typed selection per element in the deployment version; a missing or inadmissible selection is a validation error before the rights check.
- **Process IO** (`/io/values`, task type `process_io`), **notification** (`notification`), **HTTP call** (`http`, policy per namespace and mode, checked when the connection is established).
- **Schedules** (`/namespaces/{ns}/schedules`): cron with optional seconds and descriptors, firing in the scheduler of the engine.
- **Event descriptions** are created anew at deploy and on changes to device groups (Kafka `device-groups`); `GET .../deployments/{id}/event-descriptions` shows them.
- **Event worker** (`ROLE=event`, `pkg/events`, `pkg/server/events.go`): keeps the descriptions in memory, fetches changes by revision every second and reloads everything every 10 minutes; subscribes only to the topics with descriptions in the consumer group `EVENT_CONSUMER_GROUP`; QoS 1 correlates before the offset acknowledgement, QoS 0 concurrently after it; messages older than one hour are discarded; errors go throttled as a notification to the owner.
- **Kafka signals**: `user` (deletion as fallback, SEC-14) and optionally the rights signal of permissions-v2 (`PERMISSIONS_KAFKA_TOPIC`, SEC-10).
- Configuration: `Default()` is guarded by a test against its own validation. The smoke test of the binary ran for both roles (engine with empty configuration; the event role aborts without `KAFKA_URL` with a clear message).

## M3: Gaps

- With `update_strategy=drain` the event descriptions of the current version apply. Instances of an old version that wait for a device event removed or changed in the new version no longer get it.
- Fog (MQTT instead of Kafka for commands and device data) follows with M6; so far `MODE=fog` only selects the HTTP policy. (Implemented in M6, stage a.)
- The imports from external-task-worker v0.1.2 and event-worker v1.2.0 have been replaced by code in the repo since 2026-10-12 (see "After M5").
- `PERMISSIONS_KAFKA_TOPIC` is switched off without a value; without the signal an administrate right of the owner withdrawn in permissions-v2 stays withdrawn there. The owner still keeps access in the engine, because it checks the owner locally.

## After M3 (2026-10-12)

- **Owner change**: `HasAdministrate` asks permissions-v2 with `CheckPermission` and a token of the new owner (Keycloak token exchange) instead of evaluating the rights of the resource itself. This way rights via groups and roles count as well.
- **Tracing** following the pattern of smart-service-repository (commit 573979f): `pkg/tracing`, description in the README. Open: database accesses are not instrumented (for `database/sql` there would be otelsql, another dependency); Kafka messages carry no trace context, because the other sides neither write nor read it; the span of a task is not linked to the request that started the instance (for that the trace context would have to be stored with the instance).

## M4: Decisions (2026-10-12)

**Scope.** Fog parts (DBG-12, DBG-15, EVT-13) come with M6, DBG-17 (K) is dropped for now. Stages: a) change stream, debug state, historical debugging, b) live endpoint and client, c) reconstruction of event evaluations and current value.

**Change stream (SYNC-03, decided: outbox plus LISTEN/NOTIFY).** Every write operation on an instance creates a row in `changes` in the same commit (sequence, instance, activity sequence). Postgres additionally announces it with `pg_notify` on commit; every replica keeps a LISTEN connection. SQLite and Postgres poll the outbox as a fallback while someone is watching. Wake-ups are only hints: a watcher then reads the state of the instance itself, lost or duplicate wake-ups do no harm. The synchronisation (M6) reads the same table with its own positions; until then it is cleaned up by age.

**One state model for live and historical (DBG-02, DBG-13).** `model.DebugState` describes the instance, active elements with wait reason, variables with type, last change and changing element, open incidents and element counters. Live it is built from the runtime state, historically by replaying the activity history.

**Full snapshots (DBG-14).** At fixed intervals of the activity history (upper limit / 20) the engine writes the live state as a full snapshot into `history_snapshots`, with full variable values. A step is reconstructed from the last full snapshot before it and the activities in between. Full snapshots below the oldest remaining entry are displaced with the ring buffer. Values above `MaxActivityValueBytes` are truncated in the history; between two full snapshots the reconstruction shows them as truncated.

**Reconstruction on the server (decided).** `GET .../instances/{id}/history?seq=<seq>` (or `time=`) returns the state in snapshot form; Go client and web-ui only navigate.

**Live endpoint (DBG-01, decided: coder/websocket v1.8.15, ISC).** After the snapshot the server computes deltas as the difference to the last sent state plus the new activities; the client applies them without deriving anything itself. Coalescing in fast loops follows from this (DBG-10). Token via `Authorization` header (Go client, via Kong) or as the first message (browser); in the second case the engine verifies the signature itself, and `KEYCLOAK_CERT_URL` is then mandatory.

**Event reconstruction (EVT-10).** Values via timescale-wrapper `/queries` with the token of the owner (token exchange), columns from the outputs of the service as in the timescale-tableworker, evaluation with `events.Evaluator` as in the event worker. The basis is the event description, which is created anew from the deployment version of the run, because only that of the current version is stored; device groups are expanded with their current composition.

## M4: Status

- **Change stream** (`changes`, SYNC-03): one row per write operation of an instance in the same commit, Postgres additionally announces it via `pg_notify`. `pkg/live.Hub` wakes the live streams of a replica via LISTEN and polls the outbox as a fallback (Postgres every 2 s, SQLite every 250 ms, only while someone is watching). Cleaned up after one hour.
- **Debug state** (`pkg/debug`, `GET .../debug`, DBG-02): active elements, wait reasons with device command subtasks, event sources, timers and joins, variables with type, last change and changing element, open incidents, counters for elements and taken sequence flows (DBG-06, DBG-07). Values above 4 KiB are truncated and loaded individually (DBG-04).
- **Historical debugging** (`GET .../history`, DBG-13, DBG-14): full snapshots every `MaxActivities/20` entries (at least 50) and at least daily, at most 30 per instance; reconstruction from the last full snapshot and the activities after it. A test compares reconstruction and live state at every step, also after displacement in the ring buffer.
- **Live endpoint** (`GET .../live`, DBG-01, DBG-03, DBG-08–10): snapshot, deltas as the difference to the last sent state plus new activities, final state; resuming from a sequence; new snapshots on a large gap or a slow client; at most 5 connections per user and replica (HTTP 429); termination with code 4401 on an expired token.
- **Event reconstruction** (`GET .../event-evaluations`, EVT-10–12) and current value of waiting events (`evaluate_events`, DBG-18, in the live stream every 30 s): values via timescale-wrapper with the token of the owner, evaluation with `events.Evaluator`. Deviations between reconstruction and correlation are named with possible causes, only if all values fit one side.
- **Client** (CLI-05): `Watch` in both clients, `Follow` and `OpenHistory` on top of it.
- Element counters now also contain sequence flows; `GET .../element-stats` returns correspondingly more entries.

## M4: Gaps

- Fog parts (DBG-12, DBG-15, EVT-13) follow with M6 (implemented in stage d); DBG-17 (K) is not implemented.
- The search in the history (DBG-16, S) is limited to the existing filters of the activity list (kind, element); a search by variable values is missing.
- Values above 4 KiB are truncated in the activity history. Between two full snapshots the reconstruction shows them truncated, and `history/variables` then reports them as unavailable.
- The time of an active element comes from the last pass of the element; with several tokens on the same element it is that of the most recent one.
- On Postgres the sequence of the change stream follows the insertion order, not the commit order. For live debugging this is sufficient, because wake-ups are only hints; the synchronisation (M6) runs on SQLite with one writer.
- Import events are not reconstructed as long as it is open whether import data lands in Timescale (EVT-12). Long lists with fixed entries come back from Timescale as a map; an unmarshalling that expects a list then fails and shows the error on the evaluation.
- How far back the reconstruction reaches is governed by the retention in Timescale (timescale-rule-manager); the engine does not know it.

## M5: Decisions (2026-10-12)

The basis are the analyses of the eight workers, of `smart-service-module-worker-lib` and of smart-service-repository (status `origin/main` 2026-10-06).

**Compatibility of the modules (decided: full).** Every string in the inputs of a module task is rendered as today as a Go template against the variables (`{{.param}}`, `brl`/`brr`), `prescript*` and `postscript*` inputs run with the same script API (`variables`, `inputs`, `outputs`, `util`, `deviceRepo`, `console`). Existing designs run without rewriting.

**Reuse instead of porting.** Middleware, script environment and templates come from `smart-service-module-worker-lib`, the handlers of `info`, `import`, `export`, `analytics` and `device_repository` from the worker repos (pseudo-versions on `origin/main` of 2026-10-06). They take only small interfaces (`GetInstanceUser`, `ListExistingModules`, `UseModuleDeleteInfo`), which the engine fulfils against its own database. Their Camunda part stays unused. Newly written are `process_deployment`, `process_deployment_start` and `watcher`, because they address services that the engine replaces (process-deployment, process-engine-wrapper, watcher-db). The libraries remain a dependency until CLI-11 creates a place for them; the price is a build coupling to six repos. Superseded on 2026-10-12: the code was pulled into the repo, see "After M5".

**Modules (SMS-12).** Table `modules` on the instance, keys in `module_keys`. The delete info is either a URL (DELETE with the token of the owner, 2xx and 404 count as deleted, as today) or a resource of the engine (deployment, instance, watch). Deletion goes through the outbox `module_removals` with retry, not in the request: nothing is left orphaned (SMS-06). Instances with modules are not subject to retention, because their resources keep running.

**Modules on every new run (decided, corrected).** At first a policy per namespace with a deviation per deployment was planned (`delete` or `keep_keyed`). Corrected: every new run of an instance removes all of its modules, whether after a parameter change (SMS-09), an update with `restart` or a restart after an incident. Reason: only short-lived processes are meant to create modules. In return a namespace with `forbid_cycles` rejects cycles in the sequence flow at deploy, because a loop cannot be shown statically to be finite; `smart-services` sets this, `processes` does not. Keys remain for maintenance procedures that update modules of the same instance.

**Maintenance procedures (SMS-13, decided: shared variables).** Message start events with their own form fields. A start creates an instance with `parent_id`. Module inputs, templates and script variables read and write the variables of the parent instance, modules attach to it. The message name is the name of the message, without renaming (SMS-02).

**Health check of the modules (decided: keep).** Hourly per module type as today (pipeline running, import and export exist, deployment exists, started instance running); a finding becomes a module error and appears in the instance status, not an incident.

**Status (SMS-11).** `status` on every instance: `running`, `ready` (run completed), `error` with a message from the end of the run, an incident or a module error.

**Completion of a deployment (SMS-04).** `process_deployment` deploys synchronously in the same step. The message `deployment_done_<id>` is queued for the executing instance and delivered immediately the next time it waits for this name, instead of sending it 5 s later. For this a message event optionally gets `senergy:messageName`, a JavaScript expression for the name; M7 rewrites `${done_event}` to it. `permission_done_<gid>` from `device_repository` is queued in the same way; there is no sender today (SMS-05), so waiting designs never continued.

**Deploy parameters.** `process_deployment` sets inputs, event variables and timers of individual elements (`{bpmnId}.parameter.*`, `.variables.*`, `.time`). For this the deployment version gets `parameters` per element, which override the input mappings or `senergy:variables` and timer definitions; the web-ui can set them in the deploy dialog as well (DEP-01). Analytics events (`.event.flow_id`) are dead and are dropped.

**Without gateway.** `prefer_fog_deployment` deploys in the cloud until M6; M6 adds the gateway.

**Stages.** a) Modules, outbox, status, parent instances, message buffer, cycle check, API; b) module runtime with reused handlers and health check; c) `process_deployment`, `process_deployment_start`, `watcher`, maintenance procedures; d) start parameters with options (SMS-08), parameter change (SMS-09), deleting variables (SMS-10).

## M5: Status

- **Modules** (SMS-12): table `modules` with keys, API for listing, reading and deleting, deleting the resources via the outbox `module_removals` (URL with owner token or a resource of the engine in the matching namespace). Instances with modules are not subject to retention; when a user is deleted no removals are queued.
- **Effects of tasks**: modules, variables of the root instance and queued messages arise in the transaction that completes the job. If the engine cannot take over a result (instance stopped in the meantime, commit failed), the task type undoes the resources; previously existing modules stay untouched.
- **A new run removes modules** and maintenance procedures of the previous one (SMS-03, SMS-09); `forbid_cycles` rejects loops in `smart-services`.
- **Runtime of the modules** (`pkg/modules`): middleware, script API and templates of the worker library; `info`, `import`, `export`, `analytics`, `device_repository` with the behaviour of the workers (in the repo since 2026-10-12, see below); `process_deployment`, `process_deployment_start` and `watcher` newly written against the engine. Health check hourly per type.
- **Status** (SMS-11) on every instance, format of the module errors as before.
- **Maintenance procedures** (SMS-13) with shared variables and modules; message starts in `smart-services` only via the instance.
- **Start parameters** (SMS-08) with options from device-selection, value format of the releases, `auto_select_all`; `json_string_parameters` stores lists in string fields as JSON like Camunda.
- **Parameter change** (SMS-09), setting and deleting variables in any state (SMS-10).
- **Deploy parameters** per element (inputs, event variables, timers, start defaults) for modules and the deploy dialog.
- **Message buffer** and `senergy:messageName` (SMS-04); `done_event` of every module task is queued (SMS-05).

## M5: Gaps

- Gateway deployments (`prefer_fog_deployment`, gateway instances of `process_deployment_start`) come with M6; until then cloud.
- The old REST API of smart-service-repository (releases, designs, `/instances-by-process-id`) follows with the `deprecated/` layer in M7, as does the migration of existing designs, releases, instances and modules.
- Modules are named `<instance>.<job>` instead of `<process instance>.<camunda task>`; the migration has to take over existing modules without changing their IDs.
- As before, `import` and `export` are not idempotent: if the engine crashes between the creation at the service and the commit, the resource stays there without a module.
- The errors of the worker handlers were taken over at first; with the integration two are fixed (see below).
- Generic watches (`watch_request`) run under the HTTP policy of the namespace instead of the isolated client of the watcher; in the cloud it is off by default.
- `process_deployment_start` no longer records `state: completed` in the module data; the status of the started instance is available through the engine.
- Start values of 3000 characters and more were replaced by placeholders so far (limit of Camunda); the engine stores them directly up to the variable limit.

## After M5: Worker code integrated (2026-10-12)

**Decision.** The repos of the module workers (`smart-service-module-worker-lib`, `-info`, `-import`, `-export`, `-analytics`, `-device-repository`) and the event-worker are abolished. Their code is no longer in `go.mod`; it is reimplemented or taken over here. This removes the build coupling to six repos that M5 had accepted for the reuse. Also replaced are the packages from external-task-worker v0.1.2 that M3 had imported; device-command and live-data-worker keep using that repo, though.

**Where.**
- `pkg/modules/compat`: script API (`variables`, `inputs`, `outputs`, `util`, `deviceRepo`, `console`), templates and model formats of the worker library.
- `pkg/modules/{info,imports,export,analytics,devicegroups}.go`: the handlers, as `Handler` via `Task` without Camunda types. Undo is generic in `runtime.go`: existing modules stay, new ones are removed via their delete info.
- `pkg/events/unmarshal.go`: marshaller and cached access to device-repository with client credentials and the cache invalidation signals of service-commons.
- `pkg/devices/repository.go`, `modifier.go`: access to devices, device types, protocols and device groups with the token of the owner and cache (formerly `lib/devicerepository`), `SplitModifier` (formerly `lib/devicegroups`). The model types come directly from `models/go`; `lib/devicerepository/model` were only aliases for them.
- `pkg/tasks/devicecommand/{messages,marshaller,timescale}.go`: command payload and protocol message (formerly `lib/messages`, fields and JSON names compared mechanically against the original), client of the marshaller service, last values via timescale-wrapper.

**device-repository `/v3`.** The engine uses `device-repository/v3` v3.4.0 instead of `/v2`; the clients and models are the same for the calls in use, the golden tests run unchanged. `/v2` remains indirectly via `device-selection/v2/pkg/model`.

**Documentation of the script environments.** `pkg/scriptdoc` generates into `docs/scripts` with `go generate ./pkg/scriptdoc`: for pre- and postscripts, like smart-service-module-worker-lib, `script-env.d.ts`, `jsdoc.js` and `ace-code-completer.ts`, plus TypeScript declarations of the BPMN scripts and a Markdown reference of both environments. Methods and comments come from the source of `pkg/modules/compat`, types and field names via reflection with the name mapping of goja, deprecations from the comments of the models. Tests check that the files are current, that documented and callable methods and globals match, and that every referenced type is declared; `tsc --strict` compiles the declarations. Compared to the library the output differs only in that multi-line comments in `jsdoc.js` carry a `*` on every line.

**Evidence.** The test cases of the workers are under `pkg/modules/testdata` (analytics 13, device_repository 14, info 9) and run as golden tests through the engine: the same requests to flow-engine, device-manager and device-selection, the same modules and variables. The tests of marshaller and device repo of the event-worker are carried over to `pkg/events/unmarshal_test.go`. For `export` there were no tests; `export_test.go` checks the behaviour from the source. From external-task-worker the aspect tests of `lib/messages` are carried over; repo access, `SplitModifier` and the timescale query have their own tests. The command execution itself had already been ported and tested since M3.

**Deviations from the behaviour of the workers.**
- `info` skips an additional field that is not a string and keeps reading the others; the worker stopped at it (`break`).
- `analytics` with a key aborts with the error when the module lookup fails; the worker ignored the error and created a second pipeline. The message for a `pipeline_id` that is not a string now names the pipeline instead of the device group.
- `analytics` does not read the device group and devices for the service criteria a second time; the result is the same.
- `persistData` remains effective only as JSON text; a boolean counts as `false`, as with the worker.
- Device commands: an answer of device-repository other than 2xx is an error with path and status. So far the content was read anyway, and follow-up errors such as an empty device or "service not found" followed.
- Device commands with `prefer_event`: if timescale-wrapper returns fewer queries or columns than requested, that is an error. So far the access ran into a panic.
- Event role: if device-repository is not reachable (5xx or transport error), a QoS 1 message is read again and the owner is not notified. This corresponds to the event-worker. Since M3 the engine had instead notified and discarded the message.

## M6: Decisions (2026-10-12)

The basis are the analyses of process-sync (`master` and branch `feat/platform-source-of-truth`), mgw-process-sync-client, mgw-external-task-worker, event-worker in fog mode, mgw-device-cloud-connector, mgw-last-value, the device-repository mirror and the process module `main/process` (v1.5.4) in mgw-module-repository.

**Protocol (decided: only the new one).** The cloud engine speaks only the new, versioned sync protocol. Gateways with Camunda stay with process-sync until their process module is updated and migrated (M7); both protocols run in parallel without interfering. Rejected: serving the old protocol as well, because the engine would then inherit Camunda IDs, `known` lists and warden logic.

**Transport.** As before via mgw-device-cloud-connector, which bridges only `processes/{nid}/cmd/#` (cloud → gateway, locally `processes/cmd/...`) and `processes/state/#` (gateway → cloud, in the cloud `processes/{nid}/state/...`). The new messages lie below it in `engine/v1/...`, separate from the topics of process-sync. The connector delivers at most once (clean session, QoS 0 upwards, buffer in memory); reliability therefore arises in the protocol: sequences, acknowledgements and resending after a deadline (SYNC-02, SYNC-08).

**Cloud side with several replicas (decided: shared subscriptions, SYNC-10).** All replicas subscribe to `$share/<group>/processes/+/state/engine/v1/#`. A message of a gateway is taken over in a transaction that locks the network, and only without gaps from the last acknowledged sequence; older ones are acknowledged again, gaps discarded, and the gateway resends from the acknowledgement. Acknowledgement happens only after the commit. Order and duplicate delivery across several replicas are thus solved in the protocol, not in the broker. Rejected: a separate role with leader election, because it allows only one active replica.

**API on the gateway (decided: read locally, write via sync).** Locally there are only read routes, health and metrics, without token verification as with the other gateway services. Deployments, starts, stops and deletions come only via the synchronisation from the cloud. A device in the LAN therefore cannot deploy processes with HTTP calls into the local network. Rejected: full API without verification; no local API.

**Fog runtime (FOG-01).** The same binary with `MODE=fog`:
- Device commands on `command/{local device ID}/{local service ID}` of the gateway broker with `{"command_id","data"}`; responses on `response/#`, errors on `error/command/#`, matched via a separate prefix of the `command_id`. The subtask is in the database, the protocol message is formed anew for the response; a restart therefore loses no correlation (mgw-external-task-worker kept it only in memory and did not listen on `error/command`).
- Device events from `event/#` with local IDs. Event descriptions carry the local IDs of device and service, which the describer reads from device-repository; the gateway needs no mapping tables of its own for this (process-sync sent them along separately, with an error in the service mapping).
- device-repository mirror instead of device-repository, marshaller in-process (library marshaller v2 and v1 with a concept repository from the mirror, like mgw-external-task-worker), mgw-last-value instead of timescale-wrapper (local IDs, a missing value is an error).
- Notifications via mgw-cloud-proxy, same format.
- The engine takes over process IO itself; process-io-worker and its Postgres are dropped.

**Storage budget (LONG-07).** `STORAGE_BUDGET` limits the database. From 80 % the engine shortens history beyond the retention, oldest entries first, down to 70 %, and reports this once per day to the user of the network; runtime state is never shortened. If a write fails because of a full disk, SQLite rolls back the transaction; the engine pauses execution, reports itself as unhealthy and continues as soon as writing succeeds again.

**Stages.** a) Fog runtime; b) sync protocol and gateway side; c) cloud side with networks, reconciler, network overview, gateway targets for `process_deployment`; d) debugging of fog instances; e) images for arm64, long-running test harness, modfile as an open item. Each stage is committed and pushed after green tests (decided).

## M6: Status

- **a) Fog runtime** (2026-10-12): `MODE=fog` with gateway broker via `pkg/mqtt` (paho.mqtt.golang under EDL-1.0, decided), device commands via MQTT with matching from the database (`pkg/tasks/devicecommand/local.go`), marshaller in-process with a concept repository from the mirror (`localmarshaller.go`), mgw-last-value, device events with local topics in the same process (`pkg/events/local.go`, decoding like platform-connector), read API without token, storage budget with shortening of the history and pause on a full disk. Fixed along the way: `debug.Apply` ordered new element statistics differently than the database, which is why the test failed sporadically.
- **b) Sync protocol and gateway side** (2026-10-12): `pkg/fogsync/protocol` (version 1, topics under `engine/v1/`, separate from process-sync) and `pkg/fogsync/gateway`. Downwards, desired state per resource with version (namespace, deployment with version and event descriptions of the cloud, start, stop, delete instance), idempotent and acknowledged per resource, errors with a message (SYNC-01, SYNC-13). Upwards, batches of the change stream after the acknowledged position, folded per instance to the latest state, one at a time, without acknowledgement again after 30 s with backoff up to 5 min; if the continuation has been purged, a snapshot is sent (SYNC-02, LONG-09, LONG-10). Status at start and every 5 min with versions, positions, applied resources and storage (SYNC-11, SYNC-12, LONG-14). The gateway does not apply messages of another protocol version.
- **c) Cloud side** (2026-10-12): `pkg/fogsync/cloud` in the engine replicas with a shared subscription; batches of the change stream are taken over under a lock of the network only without gaps and acknowledged after the commit; desired state per network and resource in `network_desired` with sent and applied version, resending after `SYNC_RESEND_AFTER`, immediately after a status; gateways outside the protocol window are marked and not served; networks without contact since `NETWORK_CLEANUP_MAX_AGE` are removed with their resources. Deployments and instances carry `network_id`; a deployment for a gateway is stored in the cloud but not activated, its event descriptions read the local topics and stay away from the event worker of the cloud; starts appear as `pending` until the gateway reports them. Rights via permissions-v2 `hubs` (SEC-05). `GET /networks`, `GET /networks/{id}`, filter `network_id` on deployments and instances, metrics `network_last_contact_seconds` and `network_backlog`. `process_deployment` with `prefer_fog_deployment` chooses, as before, the first gateway that carries all selected devices.
- **d) Debugging of fog instances** (2026-10-12): read debug routes of a gateway instance (debug state, historical state, runs, activities, element counters, variables) ask the gateway via the sync protocol (`request`/`response/{replica}`, answer to the asking replica, deadline `SYNC_CALL_TIMEOUT`); if the gateway is not reachable, the debug state returns the last reported state with `offline: true` (DBG-12, DBG-15). The live endpoint subscribes to the changes of the instance on the gateway with a lease of 1 min, which the cloud renews while someone is watching. The event reconstruction reads runs and activities from the gateway and the device values, as in the cloud, from Timescale; data from times without connection is missing there, and the engine names this as a limitation (EVT-13).
- **e) Images and long-running test** (2026-10-13): the Dockerfile compiles for the target platform, images for amd64 and arm64 without arm/v7 (FOG-02); workflows `tests.yml` (format, vet, comparison of the generated files in `docs/`, build, tests) and `dev.yml`. Harness for the long-running test in `pkg/soak` with a CSV report and a check that resources do not keep growing after settling (LONG-15, LONG-06, LONG-07); acceptance on an arm64 gateway is pending. `mqtt.Memory` can discard messages instead of recording them, otherwise memory would grow along in the long-running test.
  The first run over 30 min (budget 4 MiB, two connection drops) found errors that are now fixed: the budget shortened instances on the gateway whose end the cloud did not know yet, and they stayed `pending` there; a snapshot would have deleted in the cloud the history of all instances shortened on the gateway; `permission_removals` grew without limit without permissions-v2; desired state in the cloud, applied resources on the gateway and with them every status message grew with every start. Now retention and budget keep an instance until the cloud has acknowledged its end; an ended instance missing from a snapshot stays in the cloud; without permissions-v2 nothing is queued for removal; the start of an ended instance is forgotten in the cloud immediately, on the gateway as soon as the instance is removed there. So that a later resource with the same ID is still newer than what was applied before, versions of the desired state start at the time in milliseconds instead of at 1. This also fixes that a deployment deleted and recreated with the same ID would have been skipped on the gateway. The runtime tables reach their steady state only after the retentions of change stream (1 h) and receipts (24 h); a budget below that shortens the whole history. After the corrections the same test ran green for 30 min with a 6 MiB budget: 3599 instances started and completed, none stays `pending`, file last 4.7 MB, heap and goroutines without increase. The check against the budget takes the median of the used space in the last quarter, not the peak: the budget is enforced per cleanup round, and after an offline phase instances deliberately stay until the cloud knows their end.

## M7: Decisions (2026-10-13)

The basis are the routes and data models of camunda-engine-wrapper, process-deployment, process-model-repository, process-io-api, process-incident-api, process-scheduler, smart-service-repository, process-sync (`master`) and process-fog-deployment, their callers in the workspace and in the web-ui, and 63 exported models (57 distinct).

**Scope of the `deprecated/` layer (decided: routes in use).** Reproduced are the routes with known callers: web-ui, canary and snowflake-canary, copied start URLs (v2 and v1 `/deployment/{id}/start`) and api-aggregator, with the filters they send. Camunda filters are translated only as far as used (`maxResults`, `firstResult`, `sortBy`, `sortOrder`, `processInstanceBusinessKey`, `processDefinitionId`, `processDefinitionNameLike`, `finished`, `unfinished`, `with_total`). The internal routes of the workers (`/instances-by-process-id/...`, `variables-map`, module upserts, `/v2/event-trigger`, admin routes of incident-api and process-io) are dropped, because workers, event worker, incidents and IO are in the engine. Rejected: all registered routes, because there are no callers for the rest.

**Translation.** The layer calls only the controller of the new API, with the token of the caller, and holds no state (API-03). Old identifiers are mapped to new ones without a table: deployment ID = vid or process-deployment ID (the migration takes it over unchanged), process definition ID = `deplid_<id>:<version>:<id>`, business key = instance ID. A start with `business_key` becomes a `PUT` with this ID, i.e. idempotent instead of a second instance with the same key. Deviating status codes of the old services (401 instead of 403, 500 for an unknown ID) are not reproduced. Every route is counted (`automation_engine_deprecated_requests_total{route}`).

**Source of the migration (decided: direct).** The tool reads the Mongo databases of the old services, the vid mapping and the shards from the Postgres databases of the wrapper, and the deployments, definitions and running instances via the REST API of every Camunda shard and of the smart service Camunda. New dependency: `go.mongodb.org/mongo-driver` (Apache-2.0). Rejected: reading via the old APIs, because process-deployment has no admin access and deployments that exist only in Camunda are missing there.

**Form.** Subcommand `migrate` of the binary. Without `-apply` a dry run; it creates only what is missing and is therefore repeatable; report on stdout, exit code 0 (complete), 1 (partial), 2 (not executable), like `moses/tools/migratelegacy`.

**Start values on restart (decided).** Camunda stores no original start values at history level `activity`. For every form field of the start event, the current value of the runtime variable of the same name is taken; smart service instances take their stored parameters, gateway instances the `start_parameters` from process-sync. The report names where a process may have changed the variable.

**Gateways (decided: the cloud takes over).** The migration reads `deployment_metadata` and wardens from process-sync and creates deployments and starts as desired state of the network. Once a gateway has the new process module, it applies them via the synchronisation. Until then process-sync serves the old gateways; `deprecated/process-sync` represents only gateways with the engine. The gateway engine reads local IO values at its first start from the old process-io database, if it is mounted.

**Rewriting models (MIG-04).** A library used by the migration and an import endpoint. Covered is what the exports contain: JUEL in conditions, mappings, timers and message names; Groovy with `println`; topics `pessimistic`, `optimistic`, `process_io` and the module topics; `http-connector` (notification or HTTP call); `{{DefinitionId}}` in IO keys. Reported instead of rewritten: `mail-send`, `R/P1D` as a duration, cron with 5 fields, conditions without language such as `was_active || true`. The comparison with Camunda (MIG-05) runs against Camunda 7 in a container and can be switched off like the Postgres tests.

**Stages.** a) Rewriting the models with report and Camunda comparison; b) `deprecated/` layer; c) migration tool; d) gateway takeover and IO on the gateway. Each stage is committed and pushed after green tests.

## M7: Status

- **a) Rewriting models** (2026-10-13): `pkg/migrate/rewrite` works on the XML and leaves layout and diagram unchanged; `POST /namespaces/{ns}/convert` offers it without saving. All 63 exported models are rewritten completely except for `mail-send` (10 tasks) and two conditions without language, and pass the check at deploy afterwards. Deviating from the list in MIG-04, the rewrite rewrites two cases instead of only reporting them, both with a note to check: `R/P1D` as a duration becomes `P1D`, because Camunda also waited only once there; a cron with 5 fields gets seconds, because Camunda had rejected it and the model never ran. JavaScript conditions with `return` at the top level are wrapped in a function. The comparison with Camunda 7.24 (MIG-05, `TestRewrittenExpressionsBehaveLikeCamunda`) evaluates conditions and mappings from the exports with numbers, strings, booleans and `null` in both engines. They are equal except for two documented cases: a string `"false"` is a boolean in JUEL with `== false`, and Java writes integral doubles as `30.0`. Lists and JSON objects as variables are not compared, because their representation in Camunda depends on the way they were set.
- **b1) `deprecated/` for engine wrapper, repository, IO, incidents, scheduler** (2026-10-13): `pkg/deprecated` under `/deprecated/`, only in the cloud, with counter `automation_engine_deprecated_requests_total{route}` and `Deprecation` header; marked as deprecated in the OpenAPI (API-07). For this the new API got two fields that the web-ui uses today and that must not have storage in the layer: `created_by` on schedules (widget reference) and the filter `not_owner` on models. A start with `business_key` is a `PUT` with this ID; deviating variables give 409 instead of a second instance.
- **b2) `deprecated/` for process-deployment v3** (2026-10-13): inbound via `Convert` and the device selection per element; changed inputs, timers, notification texts and start defaults become parameters of the deployment version, but only if they differ from the model and contain no expression, otherwise an override would replace the expressions of the rewritten model. Outbound the element list is built from the deployed process (criteria via `prepare.CriteriaOf`, options only with `with_options`). An update stops running instances, as process-deployment did by redeploying in Camunda.
- **b3) `deprecated/` for smart-service-repository** (2026-10-13): designs are models in the format of the old designer; a release rewrites the design and deploys it with a reference to the model, for which the new API now accepts `model_id` together with `xml` (content of the model in another form, MIG-04). The releases of a design are the versions of a deployment (SMS-03); the release ID is the deployment ID, older versions `<id>:v<n>`, `new_release_id` follows from the version difference, `PUT .../parameters?release_id` moves the instance (SMS-09). For this, instances now have `name` and `description` (`PUT .../instances/{id}/info`); for gateway instances they belong to the cloud and are not overwritten by the replication. Rejected: a separate deployment per release, because the engine cannot move an instance to another deployment and SMS-03 describes the release as an update.
- **b4) `deprecated/` for process-sync and process-fog-deployment** (2026-10-13): only gateways with the engine (decided). A deployment ID also counts as the Camunda ID of the old routes; `is_placeholder` follows from the desired state of the network, `sync_date` from the last contact. Kong redirects these paths only once no gateway uses Camunda any more.
- **c1) Migration tool, process side** (2026-10-13): subcommand `migrate` with dry run, report and exit code; readers for the MongoDB of the old services (structs with the same field names as there, so that the driver reads symmetrically to writing; documents in `interface{}` fields as a map) and for the REST API of the Camunda shards. Taken over are models (format of the old designer, rights from `processmodel`), deployments (rewritten via the same mapping as `deprecated/deployment`, model reference via owner and identical BPMN), deployments only in Camunda on explicit request, restart of running instances, schedules and IO values (Camunda IDs in keys are replaced). The dry run remembers planned deployments, so that dependent steps are assessed as with `-apply`. New dependency: `go.mongodb.org/mongo-driver/v2` v2.6.0 (Apache-2.0), so far indirectly in the module graph. The migration takes over the disabled state of the wrapper v1.1.0 since the addendum below.
- **c2) Migration tool, smart services and gateways** (2026-10-13): designs as models, releases of a design as versions of a deployment with the ID of the newest release (rights of all releases merged). Instances with completed setup are taken over as ended with modules and variables instead of being restarted, because a new run would create every module again; an instance with an error ends with this error, so that the status shows it. Delete references to process-deployment and process-sync become resources of the engine. Fog deployments and wardens from process-sync become deployments and starts of the network; because process-sync stores no owner, the owner is the only administrator of the hub in permissions-v2.
- **d) Gateways** (2026-10-13): the cloud takes over deployments and starts of a gateway during the migration from process-sync (c2); the gateway applies them as soon as its process module has the engine. The gateway engine takes over the IO values of the old process module once at its first start from the Postgres of process-io-worker (`LEGACY_IO_POSTGRES`, `pkg/migrate/legacyio`), with the owner from `LEGACY_IO_OWNER`; values of instances are dropped. The Camunda history is not taken over (MIG-06).

## After M7: Changes of the source repos ported (2026-10-13)

The source repos were checked for changes since their analysis. Ported:

- **camunda-engine-wrapper v1.1.0: switch deployments off and on (DEP-12).** The wrapper suspends the process definition for this. In the engine it is the field `disabled` on the deployment (`PUT .../deployments/{id}/disabled`, old `PUT /deprecated/engine/v2/deployments/{id}/disabled`). A switched-off deployment starts nothing: no start via the API, no message starts, timer starts advance without starting. Running instances are stopped on switching off, as Camunda halted them on suspending; a restart is rejected. An update keeps the state. As in the wrapper it applies only to cloud deployments; deployments of a network answer with 400 (fog is missing there as well, SNRGY-4929). The migration takes over suspended definitions as switched-off deployments.
- **smart-service-repository (up to `275ffe4`, 2026-10-12).** Instance count per version for administrators (`GET .../deployments/{id}/instance-count?version=`); which versions use a resource (`GET .../resource-usage/{kind}/{id}`, kinds `process-models`, `flows`, `import-types`), counted across all users, deployments only as far as readable; a model that modules deploy is deleted only by an admin with `force`; the periodic check of changed and deleted resources (`changed_resources` on the version, `RESOURCE_CHECK_CYCLE`, default 1 h); a release ID in use is rejected with 409. As there, the current version and older ones with instances are counted, resources only as literal text in the inputs of the module tasks. For import types the check remembers at the first read a hash of the fields that an import uses; flows it compares via `dateUpdated`, models via `updated_at`. If the owner can no longer read a resource, the engine asks with its own token; 404 then means deleted.
- **Not to be ported:** `f9a0c2f` (check the target release before tearing down the instance). `PUT .../parameters` checks target version and parameters in the same transaction before a run ends; the error did not exist in the engine. The restart now additionally checks the disabled state before the end of the run.

Rejected: an index of the used resources per version as in smart-service-repository. The engine reads them from the stored process when asked; the query is rare (before a delete), and an index would have to be maintained on every parameter change.

## After M7: Cleanup (2026-10-13)

Packages that had only one user or shared the same task were merged; older entries above still name the old paths.

- `pkg/fogsync/{cloud,gateway,protocol}` → `pkg/fogsync` (`cloud.go`, `gateway.go`, `protocol.go`; `cloud.New` is called `NewCloud`, `gateway.Agent` is called `Gateway`). The files keep the separation of the two sides and the protocol.
- `pkg/live` → `pkg/debug` (`Hub`): only wakes the live streams of the debugger.
- `pkg/parameters` → `pkg/prepare` (`Describer`): both describe what a user chooses before deploy or start, via device-selection.
- `pkg/messaging` → `pkg/tasks/devicecommand` (`Transport`, next to `LocalTransport`): the device commands were the only user.
- `pkg/tasks/notify` → `pkg/notification` (`Task`): the task type only hands over to the notifier.
- `pkg/metrics` → `pkg/server` (`Metrics`): only the server wires them; engine and device commands address them through their own interfaces.
- The versions of a deployment that are in use are determined once by `engine.VersionsInUse` for resource usage and the resource check, with the process cache of the engine.
- Test servers are started by `client.NewTestServer`; five test packages had copied the setup.

Deliberately kept as separate packages: `auth`, `tracing`, `script`, `mqtt`, `timer`, `devices` (used by many packages; merged they would create cycles or catch-all packages), `namespaces` (own rules, used by controller and gateway through interfaces), `migrate/legacyio` (runs in the server; `migrate` imports the server), `migrate/rewrite` (also used by the controller, without the dependencies of the migration), `tasks/httpcall` and `tasks/processio` (one task type each, `processio` also the store behind the IO API), `scriptdoc/generate` (program for `go generate`), `bpmn/bpmntest` and `store/storetest` (test helpers, not in production code), `client`, `model`, `api` (interface for consumers).

## Audit against the requirements (2026-10-20)

Every requirement id of `docs/requirements.md` (242) was checked against code and tests, together with the gaps of the milestones above and the engine items of `OPEN-ITEMS.md`. None is missing entirely: 186 are implemented, 47 partly, 3 differ by a decision recorded above, 6 are tasks of other repos or of operations. Defects 1, 2, 5, 6 and 7 were reproduced with tests in a copy of the repository, the others read from the code.

### Defects

All fixed on 2026-10-20, each with a test that failed before the fix; the decisions the fixes needed are in "Decisions of the fixes" below.

1. **The cloud runs the restarts of gateway instances (FOG-07).** `store.Tx.UpsertReplicatedInstance` copies the `restart_at` of the gateway into the cloud row, and `Tx.DueRestarts` has no `network_id` filter, so `Engine.restartInstance` executes the instance in the cloud as well. The comments in `pkg/fogsync/protocol.go` and `gateway.go` ("restarts are the gateway's own") describe the intent, not the behaviour.
2. **Removed modules leave their resources on the gateway (SMS-12, SMS-06).** `modules.Remover` deletes deployments and instances through `store.Tx.RemoveResource`, which removes only the cloud rows; the desired state of the gateway (`network_desired`) never learns of the deletion, unlike `Controller.desireDeletedDeployment` and `desireInstance(..., true)`.
3. **A gateway offline for more than an hour can lose the end of an instance (LONG-07, LONG-10).** The protection of not yet synchronised instances (`unsynced`, `pkg/store/sync.go`) rests on rows in `changes`, which `PurgeChanges` removes after `ChangeRetention` (1 h) whether the cloud acknowledged them or not. Retention or storage budget may then delete an ended instance whose end never reached the cloud, and the snapshot after the reconnect makes the cloud delete its copy, which it still holds as running. `TestTheCloudLearnsTheEndBeforeTheGatewayTrims` covers only short offline periods.
4. **A gateway one protocol version behind gets nothing from the cloud (SYNC-11).** The cloud accepts gateways in `[Version-window, Version]`, but stamps its own `Version` on every downward message (`cloud.go`), and the gateway drops every message whose version is not its own (`gateway.go`). After a protocol change, `SYNC_PROTOCOL_WINDOW` therefore does not let cloud and gateways be updated one after the other.
5. **An update that changes only the deploy parameters is ignored (DEP-04, DEP-01).** The "nothing changed" check of `Controller.PutDeployment` compares XML, SVG, name, description, model version, incident handling, retention and selections, not `Parameters`. It also hits updates through `deprecated/deployment`, which turn changed timers and inputs into parameters, and the updates of `process_deployment` modules.
6. **An update with `restart` fails as a whole if a running instance cannot be restarted (RUN-11, DEP-04).** `Engine.restartInstance` restarts at the start event without definition; an instance of a process that starts only through a message or a timer makes the whole update fail with 400. A restart after an incident fails the same way and only notifies the owner.
7. **The migration checks business keys per Camunda deployment, not per tenant (MIG-02).** `pkg/migrate/process.go` counts the uniqueness of a key per deployment; two running instances of one user in different deployments with the same key get the same id, the second is reported as existing, not restarted, and its IO values attach to the first.
8. **Deleting a user leaves their deployments and instances on gateways (SEC-08).** `Tx.DeleteOwned` deletes the cloud rows directly, without the desired deletions on the gateways.
9. **Cleaning up a network leaves its rights in permissions-v2 (SYNC-07).** `Tx.DeleteNetwork` removes rows with plain SQL, past `queueRemovals` and the change stream.
10. **The migration report drops rewritten and to-be-checked findings (MIG-04).** Only `Unsupported` is kept (`deprecated/deployment.go`, `migrate/process.go`), so rewrites such as `R/P1D` → `P1D` or five-field cron pass silently; only `POST /namespaces/{ns}/convert` returns the full report.

11. **The cloud ran the schedules of gateway deployments (FOG-05, RUN-07).** Found while fixing 1: `Engine.fireSchedules` started instances of a gateway deployment in the cloud, and nothing rejected such a schedule.

A documentation defect was corrected right away: `NOTIFIER_URL` on a gateway is `http://{gateway}/notifications-v2`, the notifier client appends `/notifications` itself (FOG-08); README and `OPEN-ITEMS.md` named the full path.

### Decisions of the fixes

- **Restarts of gateway instances** (1): the cloud keeps `restart_at` of a gateway instance only for display; `DueRestarts` and `NextRestartDue` consider cloud instances only.
- **Change stream on a gateway** (3): `TrimChanges` replaces `PurgeChanges` in the housekeeping. Up to the acknowledged position it removes by age as before; after it, only entries followed by a later one of the same instance. The cost: while offline, ended instances keep their runtime rows and the budget can trim only their history, so a gateway that ends more instances offline than its budget holds fills up instead of losing ends the cloud has not seen. Rejected: a fog-specific longer `ChangeRetention`, which only moves the limit.
- **Protocol window** (4): the cloud writes to each gateway in the version that gateway reported, if it lies in the window; a gateway accepts only its own version. All downward messages pass `encode` in `protocol.go`, where a future version converts to older formats. Consequence: a protocol change updates the cloud first, then the gateways one by one.
- **Network cleanup** (9): resources of a removed network are deleted one by one through `DeleteDeployment` and `DeleteInstance`, so rights, module removals and tombstones follow as for any deletion.
- **Deletions reaching gateways** (2, 8): module removal and the undo of a module go through `Controller.RemoveResource`, user deletion desires the deletions in the same transaction as `DeleteOwned`; both share `desireDeleted` with the deletion through the API. Instances of a user's own gateway deployments are not desired separately, the gateway deletes them with the deployment.
- **What counts as a change of a deployment** (5): name, description, model id and version, XML and SVG as resolved, incident handling, retention, selections, parameters, and a given source. Not: the update strategy, owner and network (refused), `managed_by`. Parameters and selections compare by effect, so empty and missing maps are equal. Such updates now need an `update_strategy`; the old deploy route sets `stop`, modules set their own.
- **Restarts** (6): a restart, after an update, an incident or through the API, begins at the start event that began the run, read from the element statistics of the run, if the version still has it; otherwise at the start event without definition. An instance that still cannot be restarted is stopped at an update with the reason in its run, and its owner is notified; the update goes ahead. After an incident it stays ended with its incident, as before. Rejected: failing the update, which let one instance block the new version. Open: the start element should be recorded on the run itself; replicated gateway instances have no element statistics and fall back to the start event without definition.
- **Schedules of gateway deployments** (11): rejected with 400 on create and on move; existing ones (e.g. from a migration) are skipped by `DueSchedules`. Rejected for now: sending schedules to the gateway, a new feature; process-scheduler had none for gateways either.
- **Instance ids of the migration** (7): a business key becomes an id only if exactly one running instance or fog start has it and no earlier step of the run took it; otherwise the id is generated from the Camunda or process-sync ids. A later run recognises an instance migrated under its key by namespace, owner, deployment, network, start time from the Camunda history and start values; candidates it cannot tell apart are left to decide, none is restarted. Fog starts now run right after the running instances, because module delete references resolve business keys through the ids assigned in the run; an ambiguous reference keeps its URL and is left to decide. Remaining limits: smart-service instances keep their old ids without a check against those the instance steps took, and without Camunda history the start time is not compared.
- **Migration report** (10): `DeploymentInput` returns the conversion with the input; every migrated deployment and release reports its findings as `rewritten`, `check` and `unsupported` items of kind `model`, which do not change the exit code.

### Partly implemented

Open tasks of the engine, not yet decided whether and when. The id says where the requirement is.

- **Validation:** the condition of a device or import event (`senergy:script`) is neither syntax- nor JUEL-checked on save or deploy, only at evaluation (BPMN-18, EXPR-01). The sandbox has no memory limit (EXPR-03, known since M1). The join semantics of the inclusive gateway are not compared with Camunda, in particular with loops (BPMN-15, known since M1).
- **Namespaces:** a changed namespace does not mark deployments that no longer fit (NS-03); there is no default update strategy per namespace (NS-07); the setting `device_selection` is stored but enforced nowhere (NS-11).
- **Models and deployments:** no rights filter `p` on model lists as in `/v2/processes` (MOD-01); no version distance (MOD-06, known since M1); old deployment versions are never removed when their last instance ends, and `drain` uses the event descriptions of the new version for the old instances (DEP-04, the latter known since M3); `task_info` has no `deployment_id` field yet, `{{DefinitionId}}` is not rewritten but accepted as alias at runtime, untested (DATA-04).
- **Tasks and incidents:** module workers' flags `allow_msg_events_in_fog_processes` and `allow_imports_in_fog_processes` were not taken over; processes with device events now always go to the gateway (TASK-29); incident variables above 4 KiB are cut like the history (INC-06).
- **Events:** offline periods of a gateway are not marked in the reconstruction, and on a gateway `evaluate_events` evaluates nothing, because event debugging is wired only with Timescale (EVT-13).
- **History and long-lived operation:** incidents, runs and element statistics are limited by age only, not by count per instance (LONG-03); the storage report counts rows instead of sizes, misses IO values, runs, element statistics and subtasks, and the top instances do not reach the cloud (LONG-14).
- **Debugging:** the wait of a device command lacks the completion strategy, module tasks show no progress (DBG-02); wait reasons at historical steps are thinner than live ones, without event sources and subtasks (DBG-13); no search by variable value (DBG-16, known since M4).
- **Rights and users:** module-managed deployments of others are not counted as dependants on user deletion (SEC-13, known since M2); the owner right is restored only with `PERMISSIONS_KAFKA_TOPIC` (SEC-10); signatures are verified only with `KEYCLOAK_CERT_URL`, and that path has no test (SEC-04).
- **API:** paging is not uniform, activities use `after_seq` without total, several lists return plain arrays (API-06; the comment in `pkg/model/common.go` claims otherwise).
- **Synchronisation:** a deployment the gateway marks faulty is not reported upwards (SYNC-02); no "offline since" and no alert for gateways without contact (SYNC-12).
- **Smart services:** stop and restart in an update apply only to running instances, so instances whose setup has finished are drained (SMS-03).
- **Operations:** no metric of the response time of device commands (OPS-05); spans carry no run id (OPS-07); no release pipeline (`prod.yml`, CLI-06, FOG-02); acceptance on an arm64 gateway open (LONG-15, FOG-04).
- **Migration:** watches of smart-service instances are not migrated, maintenance procedures of migrated instances stop with the old watcher (MIG-01); owners can be assigned only per old owner value, not per resource (MIG-09).

### Behaviour that differs from the requirements without a recorded decision

Recorded here so that it is a decision; changing it would be a new task.

- Optimistic device commands send first and complete afterwards; a failed send is retried or becomes an incident instead of being only logged (TASK-01).
- Groups with `sequential` or `round_robin` take the task timeout as the answer window of one try and count retries per device (TASK-06, TASK-07; `docs/device-command-groups.md`).
- `MIN_TIMER_DURATION` (default 5 s) restores the minimum of process-deployment.
- A timer that the old designer leaves empty gets the time of the old deploy dialog written into the model on deploy (`pkg/deprecated/timers.go`), instead of becoming a parameter.

### Superseded statements above

- M1 gaps: CI with the drift check exists (`.github/workflows/tests.yml`), the client covers the whole API (`client.Client` embeds `api.Controller`), EVT-08 has its metric, the MIG-05 comparison exists; open from that list are the memory limit, the BPMN-15 comparison and MOD-06.
- M2 gaps: the ongoing repair of the owner right came with M3 (only with `PERMISSIONS_KAFKA_TOPIC`); dependants via modules did not come with M5 and are open (SEC-13 above).
- M3 status: `GROUP_SCHEDULER` also knows `round_robin`, and the timeout is no longer the job timeout for groups (`docs/device-command-groups.md`).
- M4 decisions name `pkg/live.Hub`; it is `pkg/debug.Hub` (see "Cleanup").
- README: instances with modules are exempt from deletion by retention, not from trimming their history; on a full disk `/ready` reports the error, `/health` does not. Both corrected on 2026-10-20.

### Before this file is removed

This file is to be dissolved once the open items above are settled. Its decisions and rejected alternatives move to `docs/` or the README first; recorded only here are, among others: Postgres and SQLite from the start (millisecond `BIGINT`, JSON as `TEXT`, Mongo rejected), variable scopes after Camunda's `AbstractVariableScope`, globally unique instance ids, the NS-05 interpretation of permissions-v2 (`shareable`, `initial_rights`, `topic_default_permissions`), the event worker coupled through the database instead of Kafka or the API, the library choices with their licences (coder/websocket ISC, paho EDL-1.0, mongo-driver Apache-2.0, modernc SQLite without cgo), the rejected per-namespace module policy, the deviations of device commands from the workers, the rejected old sync protocol and sync role with leader election, the read-only gateway API, command correlation in the database, the dropped internal worker routes, the rejected migration through the old APIs, the rejected deployment per release, and the package consolidation. README and `OPEN-ITEMS.md` point to this file in their first lines.
