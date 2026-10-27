# Requirements: Automation-Engine as a replacement for Camunda 7

As of 2026-10-12, with the decisions from section 2.

The new component is called **Automation-Engine** (decided 2026-10-12). The text often says just "engine"; this always means the Automation-Engine. Camunda is always referred to explicitly as Camunda.

Sources:
- code of the local checkouts under `~/workspace/go`, `~/workspace/ts/web-ui` and `~/workspace/docker/process-engine`;
- deployment definitions in `rancher-2-defs`;
- notes in `senergy-wissen`;
- 63 models exported from the process-model-repository (55 distinct names), which may be outdated.

What is described here is today's usage, not the feature set of Camunda.

Priorities: **M** = must, **S** = should, **K** = may. Each requirement briefly states what it follows from.

This document is not updated for the decisions taken during the implementation. They are recorded in [`PLAN.md`](../PLAN.md), in the sections "Decisions" of each milestone; **where a decision there differs from a requirement here, `PLAN.md` takes precedence.** The audit of 2026-10-20 in `PLAN.md` lists the requirements whose text no longer matches the engine.

---

## 0. Starting point in brief

- **Two Camunda instances**:
  - the main engine (`process-engine`, Camunda 7.24, one pod, Postgres 11.2);
  - a second one for smart services (`smart-service-engine`, Camunda **7.11**, own DB, without wrapper, tenants and cleanup).
- **In addition, one Camunda 7.24 per gateway** with its own Postgres in the process module (`mgw-module-repository/main/process/Modfile.yaml`, ARM image only for arm/v7 with Java 8 and `-Xmx768m`).
- **Camunda is operated as a pure REST black box.** Cockpit and Tasklist are removed. Used are:
  - deployment and start via `submit-form`;
  - instances and history at instance level;
  - message correlation and external tasks;
  - incidents (read-only), timers and script evaluation.
- **Around the engine** are the wrapper, task worker, process-io, incidents, event-deployment, event-worker, process-deployment, process-model-repository, process-scheduler, process-history-cleanup, process-sync, process-fog-deployment and eight smart-service module workers.
- **A large part of their code compensates for gaps in Camunda**: Camunda assigns its own IDs, provides no change stream and does not know the tenant model of the platform. Examples:
  - the vid table in the wrapper;
  - the sharding, although only one shard exists in prod;
  - Postgres triggers on Camunda tables in the sync client;
  - schema migrations in two third-party services;
  - incident polling every 5 s;
  - restarting instances on two levels in the fog.

## 1. Scope of consolidation

| Component | Decision | Reason |
|---|---|---|
| Camunda (`process-engine`) | replace | goal |
| camunda-engine-wrapper | **integrate** | API facade, rights check and delete cascades become core functions of the engine; the vid mapping is dropped (DEP-02) |
| external-task-worker (optimistic, pessimistic) | **integrate** | device commands as a native task type |
| process-io-worker, process-io-api | **integrate** | variable store tied to the life cycle of instance and deployment |
| process-incident-api | **integrate** | incident handling (stop, restart, notify) is runtime behaviour |
| process-model-repository | **integrate** | model, deployment and update belong together (section 5) |
| process-deployment, process-fog-deployment | **integrate** | the only services that read and write BPMN; same parser and stringifier |
| event-deployment | **integrate** | event descriptions are an artefact of the deployment |
| event-worker | **own role** (variant A, decided) | scaling over the device data topics stays separate; no reports for debugging (section 10) |
| process-scheduler | **integrate** | any number of schedules per deployment (1:n); today an in-memory cron, 1 replica, schedules survive the deletion of the deployment |
| smart-service-engine | **replace with the same engine** | the required functionality is a subset |
| smart-service-module-worker-* (8 workers) | **integrate** (decided) | as internal task types; no external task API for third parties (section 8) |
| mgw-external-task-worker | **integrate** (fog build) | wrapper around the same worker lib |
| process-sync, mgw-process-sync-client | **integrate** (decided) | state mirroring becomes replication between two identical engines; on the gateway in the engine process, in the cloud in the code base of the engine (section 17) |
| process-history-cleanup (CronJob) | **dropped** | retention becomes an engine function |
| sharding DB, wrapper-db (viddb) | **dropped** | one shard in prod; the vid mapping is dropped with an own ID |
| smart-service-repository | **integrate** (decided) | designs, releases, instances, modules and maintenance procedures depend on the life cycle of the smart-service processes (section 18) |
| api-aggregator | **switch off** instead of porting | replaced since 2026-10-01, but still deployed and routed in Kong |

## 2. Decisions

| Question | Decision | Affects |
|---|---|---|
| Name of the component | Automation-Engine | whole document |
| Integrate event processing? | variant A: event-worker stays a role of its own | section 10 |
| Support or replace JUEL? | replace JUEL, JavaScript only | EXPR-01, MIG-04 |
| Running instances at the switchover | restart | MIG-02 |
| Load requirements | no special consideration | OPS-01 |
| Target hardware fog | no special consideration; arm64 or amd64, no arm/v7 | FOG-02, FOG-04 |
| Persistence cloud | Postgres | OPS-02 |
| Update of a deployment | the user decides at the update; also applies to smart services and to the processes they manage through the process module | DEP-04, SMS-03 |
| External task API | no user integration; smart-service module workers are integrated | section 8 |
| Retention of history and incidents | value on the deployment, limited by upper bounds at configuration level (global, namespace) and user level; the smallest of these values is effective (changed 2026-10-12 after stakeholder feedback, before "the longest value applies"); the same data serves historical debugging | HIST-04, HIST-05 |
| Debugging | marking in the client; step by step also on historical data; as detailed as possible | section 13 |
| Event debugging | not via reports of the event-worker, but implicitly from the Timescale data; also applies to fog processes | EVT-09 to EVT-12, DBG-18 |
| Memory measurement on gateways | not necessary once the problem is fixed | section 12 |
| process-model-repository | integrate | section 5 |
| smart-service-repository | integrate | section 18 |
| Topic `export` in the process designer | outdated, dropped | BPMN-08 |
| HTTP call from processes in the cloud | configurable | BPMN-10 |
| Gateways on arm/v7 | old versions are irrelevant | FOG-02, MIG-06 |
| Upper bounds for ring buffer and storage budget | are set during implementation | LONG-03, LONG-07 |
| Business keys | dropped; instances get stable IDs that the caller may set, like deployments without vid | RUN-01, RUN-11, RUN-12 |
| Process definition | dropped; there are models, deployments and instances | DATA-01 to DATA-04 |
| Old APIs and aliases | only under one path `deprecated/`; the new API is free of legacy | section 15 |
| Processes and smart services | the same in the background; separated by namespaces, freely configurable and extensible; allowed or forbidden modules or workers per namespace | NS-01 to NS-10, section 18 |
| Client libraries | only for Go; none for JS/TS, the frontends implement the API themselves, the web-ui is adapted | CLI-01 to CLI-11 |
| Rights | own topics in permissions-v2 per namespace and level (model, deployment, instance), so 6 today; default permissions configured per namespace | NS-05, MOD-02, MIG-08 |
| Owner | every resource has an owner in the engine, like devices and hubs; the owner necessarily has admin rights in permissions-v2 | SEC-09 to SEC-12 |
| Deleting a user with dependants of others | delete cascade and error message are both possible; a configuration decides | SEC-13, SEC-14 |
| process-sync and mgw-process-sync-client | integrate into the engine | SYNC-09 to SYNC-13 |
| Allowed BPMN elements | configurable on the namespace (allow or deny) | NS-06 |
| Time of the device selection | both possible (at deploy, via start parameters); configurable on the namespace whether offered | NS-11 |
| References between namespaces | yes, explicitly allowed | NS-08 |
| One designer for all namespaces | yes | NS-09 |
| Old routes, in particular copied start URLs | Kong redirects the old paths to `deprecated/` | API-08 |

---

## 3. Process model and BPMN scope

### What the exported models contain

63 files (55 distinct models) were evaluated. Counting was per element, not per file; duplicates in the export are counted.

| Element | Count | Remark |
|---|---|---|
| `serviceTask` external | 162 | topics `pessimistic` 72, `optimistic` 54, `process_io` 36 |
| `serviceTask` with `camunda:connector` | 39 | `http-connector` 29, of which 26 notifications (URL is inserted at deploy) and 3 HTTP calls defined by users (`POST http://${ip}/ta`, `GET ${url}`, outputs `statusCode`, `response`); `mail-send` 10 |
| `task` without type | 54 | carrier of input and output mappings; 4 with `camunda:asyncBefore` |
| `scriptTask` | 15 | all with `camunda:resultVariable` |
| `intermediateCatchEvent` | 152 | message 92, timer 60 |
| `startEvent` | 63 | without type 56, message 5 (of which 4 device events with `senergy:*`), timer 2 |
| `endEvent` | 20 | all without type |
| `intermediateThrowEvent` | 3 | without type and without outgoing flow, so effectively an end point |
| `exclusiveGateway` | 87 | 33 diverging, 52 converging, 2 both; default flow in 1 model |
| `parallelGateway` | 25 | 20 diverging, 5 converging; in 12 models |
| `inclusiveGateway` | 8 | 5 diverging, 3 converging; in 8 models |
| `eventBasedGateway` | 17 | in 12 models |
| `conditionExpression` | 80 | JavaScript 52, JUEL 28 |
| Timer `timeDuration` | 45 | ISO 8601 (`PT5S` to `P1D`), 5× `${cycle}`, 2× `R/P1D` |
| Timer `timeCycle` | 9 | cron with 6 and 7 fields (`0 0 11 * * * ?`), 4× from a variable (`${cron_0}`) |
| Timer `timeDate` | 8 | |
| `camunda:inputOutput` | 273 | 332 input, 177 output parameters; 127 `camunda:script` within them |
| Script languages | | JavaScript 129, Groovy 13; Groovy contains only `println` output |
| `camunda:formData` / `formField` | 26 / 67 | start parameters |
| `signal` | 6 | only as a definition at file level, referenced nowhere (3 copies of one model) |
| Pools, lanes, `group` | 3 / 5 / 1 | presentation only |

Scripts use `JSON.parse`/`stringify`, `Date` and `Math.*`. Accesses to `java.*` or `execution.*` do not occur in these exports; the smart-service fixtures use `execution.setVariable`.

Not present are: boundary events, subprocesses, call activities, user tasks, multi-instance, listeners, error, escalation, compensation, link, signal and terminate events.

### Required

- **BPMN-01 (M)** Process with exactly one executable `bpmn:process`. Pools, lanes, `collaboration`, `messageFlow`, `group`/`category` and unreferenced `signal` definitions are read and ignored. Today pools and lanes only serve grouping in the deploy dialog (`parser/groups.go`).
- **BPMN-02 (M)** Start event without type, with start parameters from `camunda:formData/formField` (`id`, `label`, `type`, `defaultValue`, `camunda:properties`, among them `ignore_on_start`, `order`, `characteristic_id`, `options`, `multiple`, `iot`, `criteria`).
- **BPMN-03 (M)** Message start event:
  - for device events;
  - for named messages with `messageRef` (smart-service maintenance procedures, start with a given instance ID).
- **BPMN-04 (M)** Timer start event.
- **BPMN-05 (M)** End event without type. An element without outgoing flow also ends its token, as in Camunda; this concerns the intermediate event without type (3 occurrences).
- **BPMN-06 (M)** Intermediate timer event (catch) with all three kinds of definition:
  - `timeDuration` as ISO 8601;
  - `timeDate` as ISO point in time;
  - `timeCycle` as Quartz cron with 6 or 7 fields including `?`.
  - The value may come from a variable or an expression (today `${cycle}`, `${cron_0}`); after the switch to JavaScript this becomes a JS expression.
- **BPMN-07 (M)** Intermediate message event (catch), see section 10.
- **BPMN-08 (M)** Service task with task type. Today it is selected via `camunda:type="external"` and `camunda:topic`. Task types:
  - today in processes: `optimistic`, `pessimistic`, `process_io`;
  - today in smart services: `info`, `import`, `process_deployment`, `process_deployment_start`, `analytics`, `device_repository`, `watcher`, `export`.
  - Which types a model may use is set by its namespace (NS-03).
  - The topic `export` in the process designer (`IotProps.js`) is outdated and dropped (decided). It has no worker in the main engine and does not occur in the exports.
- **BPMN-09 (M)** Task type "notification" instead of the `http-connector` with `deploymentIdentifier=notification`. Today process-deployment inserts URL and headers (`stringifier/notification.go`).
- **BPMN-10 (M)** Task type "HTTP call" for calls that users define themselves: method, URL, headers, body, outputs `statusCode` and `response`. Used in 2 models; the URL `http://${ip}/ta` points to a device in the local network.
  - In the fog the task type is allowed.
  - It is configurable (decided), per namespace (NS-04): on or off, plus allow and deny lists for targets (hosts, networks, ports), separately for cloud and fog.
  - Default in the cloud: off or only with an allow list. Otherwise a user process can reach internal services in the cluster. Today the network policy of the engine blocks it there anyway (egress only to notifier, DB, port 587, DNS).
  - Default in the fog: on, with targets in the local network.
  - On a gateway, the gateway module can set the policy itself; it then replaces the fog policy of every namespace (decided 2026-10-23). Processes taken over from Camunda gateways also call services on the internet, which the old module allowed without restriction; whether that is allowed is the owner's decision on their gateway, not the platform's.
  - Time limit and size limit for the response.
- **BPMN-11 (M)** Script task, JavaScript, with `camunda:resultVariable`.
- **BPMN-12 (M)** Exclusive gateway: diverging with conditions and default flow, converging without condition.
- **BPMN-13 (M)** Event-based gateway with message and timer events behind it, also with several incoming flows (jump back into the loop).
- **BPMN-14 (M)** Parallel gateway, diverging and converging. In the exports 20 gateways diverge and only 5 converge; common are parallel branches that each run endlessly on their own. For long-lived processes LONG-16 applies.
- **BPMN-15 (M)** Inclusive gateway, diverging and converging, with the semantics of Camunda 7: the join waits for all tokens that can still reach it. The semantics must be retested against Camunda, because it is not obvious with loops.
- **BPMN-16 (M)** `bpmn:task` without type as a pass-through node with input and output mappings. `camunda:asyncBefore`/`asyncAfter` is accepted and ignored, because the Automation-Engine persists every step anyway.
- **BPMN-17 (M)** Input and output mappings on tasks and events:
  - value as text, as JavaScript expression or as script;
  - nested target names (`inputs.r`, `io.read.x`, `outputs.r`);
  - map values (`camunda:map/entry`).
- **BPMN-18 (M)** Validation when saving the model and at deploy:
  - Elements outside the supported set and task types that the namespace does not allow (NS-03, NS-06) are rejected with an error message that names the element.
  - Today process-deployment and the designer let everything through.
  - The modeler in the web-ui is restricted to the same set, per namespace (NS-09, section 21).

### Not required

User task, Tasklist, receive task, send task and business rule task; subprocess, call activity, multi-instance; boundary events, signal (throw and catch), link, error, escalation, compensation, terminate and transaction; conditional event in the BPMN sense; execution and task listeners; `camunda:class`/`delegateExpression`; DMN, FEEL, JUEL and Groovy; instance migration between versions; process instance modification; sending mail (`mail-send`).

On `mail-send`: process-deployment already rejects the connector today (`deploymentmodel/validation.go`). The 10 occurrences are in models that therefore cannot be deployed today anyway.

The "conditional event" of the platform is a message event with `senergy:script`. The condition is evaluated by the event-worker, not by the engine.

---

## 4. Expressions and scripts

- **EXPR-01 (M)** The only expression and script language is JavaScript. This applies to:
  - conditions on sequence flows;
  - input and output mappings;
  - timer values;
  - script tasks;
  - event conditions.

  Models with `${...}` or another language are rejected when saved; existing models are rewritten during migration (MIG-04). In Go, goja is an obvious choice, which the event-worker already uses.
- **EXPR-02 (M)** A mapping value is either text or an expression, and explicitly so. Today only the occurrence of `${` decides. For text with embedded values (`http://${ip}/ta`) a template literal is used.
- **EXPR-03 (M)** Sandbox:
  - time limit per evaluation (the event-worker uses 2 s);
  - memory limit;
  - no access to engine internals, file system or network.
  - Today the only protection is the prefix `var java = {}; var execution = {};` in the wrapper; in addition, process-deployment rejects `execution.`.
- **EXPR-04 (M)** Defined functions instead of engine objects:
  - `setVariable(name, value)` for smart services; today they call `execution.setVariable`, because the repository deploys bypassing the wrapper;
  - `log(...)` for debug output, visible in debugging (section 13). It replaces the Groovy `println` scripts, which today write only to the container log.
- **EXPR-05 (M)** Value semantics compatible with what existing scripts expect:
  - JSON strings that scripts read with `JSON.parse`;
  - `result` as a list from payload version 2 on (`result[0]`);
  - numbers as double;
  - `null` for missing values (`distance_km == null`).
  - Camunda returns Java objects today (Spin, ArrayList); deviations are found with the migration tests (MIG-04).

---

## 5. Data model and process models

### Data model

- **DATA-01 (M)** There are three concepts: **model**, **deployment** and **instance**.
  - The **process definition** is dropped (decided). Camunda needs it because a deployment there can contain several resources and not necessarily a process.
  - With us a deployment contains exactly one executable process (BPMN-01). Today the wrapper assigns suffixes for further processes of a model (`deplid_<id>_i`); this is dropped with BPMN-01.
- **DATA-02 (M)** Relations:

  | Concept | Divided into | Refers to |
  |---|---|---|
  | Model | versions (MOD-05) | – |
  | Deployment | versions (DEP-04) | model and model version, if any |
  | Instance | runs (RUN-11) | deployment, plus model, if any |
  | Run | – | the deployment version it runs on |

  - **Deployment without model:** These are e.g. processes that widgets create (`source=generated`), and deployments created via the API with their own XML.
  - **Model reference of the instance:** It is taken from the deployment and stored on the instance, so that "all instances of a model" can be queried directly.
  - **Version per run:** The deployment version belongs to the run, not to the instance. A restart at an update (DEP-04) begins a new run on the new version, under the same instance ID.
- **DATA-03 (M)** Everything that today depends on the process definition depends on the deployment or the deployment version:
  - incident handling;
  - binding of IO values;
  - event descriptions, timers and schedules;
  - retention setting;
  - metric labels;
  - command metadata.
- **DATA-04 (M)** New names instead of definition IDs:
  - **API:** The new API knows only `deployment_id`. `processDefinitionId`, `process_definition_id`, `processDefinitionNameLike` and the routes `/v2/process-definitions/...` exist only under `deprecated/` (API-02).
  - **Messages to connectors:** `task_info` (`platform-connector-lib`) gets `deployment_id`. Until all connectors use the new library, the engine additionally fills `process_definition_id`, after that it is dropped.
  - **Metrics:** The label for the cost-calculator is called `deployment_id`. Engine and cost-calculator are switched over together (TASK-12).
  - **Model contents:** Placeholders `{{DefinitionId}}` in process-IO keys are rewritten to `{{DeploymentId}}` during migration (MIG-04).

### Namespaces

- **NS-01 (M)** Normal processes and smart services are the same in the background (decided): models, deployments, instances and runs with the same functions.
  - Every model, every deployment and every instance belongs to exactly one **namespace**.
  - Listings, search and counts are separate per namespace.
- **NS-02 (M)** Namespaces are freely configurable and extensible, not limited to `processes` and `smart-services` (decided).
  - Creating and changing works via configuration or an admin API, without code changes.
  - `processes` and `smart-services` are shipped, with today's behaviour.
- **NS-03 (M)** Per namespace it is configurable which modules or workers, i.e. task types (TASK-00), are allowed or forbidden (decided), as an allow or deny list. Example as of today:
  - `processes` allows device command, process-IO, notification and HTTP call;
  - `smart-services` allows the modules from TASK-20 to TASK-27.

  It is checked:
  - when saving a model, as a hint (MOD-04);
  - at deploy, as a rejection (BPMN-18);
  - at execution: a type forbidden in the meantime creates an incident. Existing deployments that no longer fit after a configuration change are marked.
- **NS-04 (M)** The settings of the HTTP call (BPMN-10) apply per namespace, separately for cloud and fog.
- **NS-05 (M)** Rights via **own topics in permissions-v2 per namespace and level** (decided), for models, deployments and instances.
  - With today's `processes` and `smart-services` these are **6 new topics**. Proposed names: `<namespace>_models`, `<namespace>_deployments`, `<namespace>_instances`.
  - The **default permissions** are configured per namespace (decided). Technically they are the `DefaultPermissions` of the respective topic in permissions-v2 (`model.Topic.DefaultPermissions`): owners, groups or roles receive these rights when a resource is created.
  - Every level can be shared, with read, write, execute and administrate.
  - Independently of the default permissions, the owner always has administrate (SEC-10).
  - Defaults that reproduce today's behaviour:
    - `processes`: models shareable, deployments and instances only for the owner;
    - `smart-services`: models (designs) only for the owner, deployments (releases) and instances shareable.
  - When a namespace is created, the engine creates its three topics via the admin API of permissions-v2 (`SetTopic`). When the namespace configuration changes, it updates the `DefaultPermissions` of the topics. When a namespace is removed, its topics are removed as soon as it has no resources any more.
  - Today's topics `processmodel`, `smart_service_releases` and `smart_service_instances` merge into the new ones (MIG-08).
- **NS-06 (M)** Allowing or forbidding BPMN elements is configurable on the namespace (decided), as a restriction of the set from section 3. For `smart-services` today's rule is taken over: no timer, signal and conditional start events (`smart-service-repository/pkg/controller/validation.go`).
- **NS-07 (S)** Further defaults per namespace:
  - default and upper bound of the retention (HIST-04);
  - incident handling;
  - update strategy;
  - limits from section 12.
- **NS-08 (M)** Relations across namespaces are explicitly allowed (decided). Example: an instance in `smart-services` creates a deployment in `processes` through a module.
  - Which namespaces a namespace may address this way is configurable.
  - The created deployment refers to the instance that manages it. This applies to the delete cascade and the update strategy (SMS-03).
- **NS-09 (M)** There is one designer for all namespaces (decided). It aligns palette and properties with the namespace: only allowed task types and elements (NS-03, NS-06), only offered forms of device selection (NS-11).
- **NS-10 (M)** Namespaces also apply on the gateway. The configuration is replicated with the deployments.
- **NS-11 (M)** For the device selection both ways are possible (decided):
  - **at deploy**, per task and event (today processes, DEP-01);
  - **via start parameters** of an instance, with criteria and options (today smart services, SMS-08).

  A model can mix both. Which way is allowed and offered is configurable on the namespace; designer, deploy dialog and start dialog follow it.

### Process models (formerly process-model-repository)

- **MOD-01 (M)** Store models: BPMN XML, SVG, name, description, owner, namespace, date, last change.
  - Listing with paging, search, sorting, ID list, owner filter and rights filter, i.e. in the functional scope of today's `/v2/processes`.
  - Today's routes `/processes`, `/v2/processes` and `/processes/{id}/publish` exist only under `deprecated/` (API-02).
- **MOD-02 (M)** Rights via the model topic of the respective namespace in permissions-v2 (NS-05), with read, write, execute and administrate.
  - Today process-deployment checks during preparation via `p=x` whether the user may execute the model. This check remains.
  - The cleanup every 6 h, which reconciles the rights and model inventory, is dropped as soon as both are written transactionally together or the engine keeps the rights.
- **MOD-03 (M)** Publishing (`publish` with description) is kept.
- **MOD-04 (M)** When saving, the same validation is applied as at deploy (BPMN-18, EXPR-01). A model that cannot be deployed is noticed already when saved, with a hint instead of a rejection, so that intermediate states can still be saved.
- **MOD-05 (M)** Models are versioned. Every save creates a version, and a deployment records model ID and model version. Today the deployment stores no model ID; assignments work only via name and tenant (vault: "Every tenant's deployments are only visible in the engine").
- **MOD-06 (S)** Per model the associated deployments can be retrieved, with the version distance. After a model change the user can update the deployments and chooses how to handle running instances (DEP-04).
- **MOD-07 (S)** Retention of old model versions: versions that a deployment refers to are kept. Otherwise a limit by count applies.
- **MOD-08 (M)** When a user is deleted, their models are removed together with their rights (SEC-08). Deployments of others that are based on such a model keep their copy of the BPMN in the deployment version; their model reference is marked as deleted. According to the vault note "Deleting a user leaves process models and releases behind", remnants were left in prod.
- **MOD-09 (K)** Kafka `processmodel` as `PublishToKafkaTopic` of the permissions-v2 topic. The only consumer found was permission-search, which is no longer deployed in `rancher-2-defs`. The topic is dropped if no further consumer is found.

---

## 6. Deployment

Integrates process-deployment, process-fog-deployment, event-deployment and the deploy part of the wrapper.

- **DEP-01 (M)** The preparation API is kept functionally:
  - `start-parameters`;
  - `prepared-deployments`: parse BPMN, selection options via device-selection, import types;
  - `deployments` with CRUD;
  - `dependencies`.

  Consumers are the web-ui, generated widget processes (`source=generated`) and canary. The round trip via smart-service-module-worker-process becomes internal with its integration.
- **DEP-02 (M)** **One ID per deployment.** The deployment UUID is also the engine ID. This drops:
  - the vid table;
  - the retry loop on `UnknownVid`;
  - the prefix `deplid_`;
  - in the fog, the asynchronous report of the Camunda ID and the `/metadata` endpoint.
- **DEP-03 (M)** Atomic deploy that combines in one step what is spread over three services today:
  - BPMN, deployment model, dependencies (devices, events);
  - event descriptions;
  - incident handling;
  - retention settings;
  - schedules.

  Today `SetDeployment` reports success even if the synchronisation fails, and the sync loop deletes the deployment afterwards.
- **DEP-04 (M)** At an update the user chooses what happens to running instances:
  - **stop**: today's behaviour, only announced now;
  - **drain**: running instances stay on their version, new starts use the new one;
  - **restart**: stop and begin a new run on the new version under the same instance ID with the stored start parameters (RUN-11).

  From this follows:
  - **Versions per deployment.** Several versions run at the same time; an old version is removed when its last instance ends. Event descriptions, schedules and incident settings depend on the version.
  - **A uniform choice across all ways.** The choice is offered in the update API, in the web-ui and for updates through smart services (SMS-03).
  - **Stable event IDs across updates.** Today every event gets a new `event_id` at every update.
- **DEP-05 (M)** The device assignment is stored typed on the task in the deployment: device, service, group, function, aspects, characteristic, paths, configurables, `prefer_event`. Today it lies as a JSON string in the process variable `payload`.
- **DEP-06 (M)** Rights check at deploy on the selected devices, device groups and imports via permissions-v2, as today in `EnsureDeploymentSelectionAccess`.
- **DEP-07 (M)** Event descriptions are created at deploy and stored as part of the deployment version:
  - group expansion to device × service;
  - device without service to all matching services;
  - import events.
- **DEP-08 (M)** When a device group changes (Kafka `device-groups`), the affected descriptions are recomputed. After that, only the affected entries in the event-worker are invalidated; today every deployment clears the caches of all event-worker pods.
- **DEP-09 (M)** Target of the deployment: cloud or a gateway (`networkId`), via the same API.
- **DEP-10 (M)** Signal "deployment changed/deleted" for the event-worker (today Kafka `process-deployment-done`).
- **DEP-11 (M)** Deletion cascades in one transaction over all versions:
  - instances, history, IO variables;
  - incidents and incident handlers;
  - event descriptions, schedules, dependencies, debug history.

---

## 7. Instances and runtime

- **RUN-01 (M)** Start an instance of a deployment, with typed variables, on the current or a given deployment version.
  - **The ID is stable and can be set by the caller.** Without it, the engine creates a UUID.
  - With a given ID the start is idempotent (`PUT /process-instances/{id}`): a second start with the same ID and the same content returns the existing instance instead of a second one; with different content there is a conflict.
  - IDs are unique per tenant. UUIDs are recommended.
  - The start variables are stored on the instance, for restart and debugging.
  - **Business keys are dropped** (decided), just as the vid is dropped for deployments (DEP-02). What they serve for today and what replaces them:
    - Smart services: instance ID of the smart service as business key → becomes the process instance ID.
    - Maintenance procedures: maintenance ID as business key → becomes the ID of the instance that starts the procedure.
    - Restart after incident: new instance with the same key → new run of the same instance (RUN-11).
    - Fog: prefix `wardened:` for restarts from the cloud → dropped, a restart is a new run.
    - Smart-service module `process_deployment_start`: state check via the history by key → query by instance ID.
    - Filter in the monitor (`processInstanceBusinessKey`) → filter by instance ID.
- **RUN-02 (M)** Query start parameters (`/parameter`), without fields with `ignore_on_start`.
- **RUN-03 (M)** List and count running instances and show them per deployment, **with paging**. Today there is no paging.
- **RUN-04 (M)** Stop instances, individually and several at once. A stop ends the current run; the history remains.
- **RUN-05 (M)** Set a variable of a running instance (`PUT .../variables/{name}`).
- **RUN-06 (M)** Persistent timers with resumption after restart. A timer fires exactly once; this also applies when several replicas run.
- **RUN-07 (M)** Time-controlled starts, taken over from the process-scheduler:
  - any number of schedules per deployment (1:n). That is already the case today: each entry refers to a `process_deployment_id`, and several entries may name the same deployment;
  - per schedule cron with an optional seconds field, alias, active/inactive;
  - a schedule depends on the deployment, not on a version, and starts the current version after an update;
  - every start creates a new instance with an ID assigned by the engine (RUN-01);
  - schedules are removed together with a deleted user (SEC-08). Today's call `/schedules` from user-management merges into the call from SEC-14.
  - Execution exactly once.
- **RUN-08 (M)** State transitions are transactional and survive a restart.
- **RUN-09 (M)** An instance ID has at most one active run. This drops today's case that two restart levels in the fog create two instances with the same business key.
- **RUN-10 (M)** Instances are not limited in their running time. Endless loops on sensor data are a main use case; the requirements on storage are in section 12.
- **RUN-11 (M)** Runs: an instance ID can have several runs one after the other, with a consecutive run number. Each run records the deployment version it runs on (DATA-02).
  - A new run is created by a restart after an incident (INC-02), by a restart at an update (DEP-04), at the migration (MIG-02) and by an explicit restart via the API.
  - History, incidents and debug history are separate per run and retrievable together under the instance ID. In debugging this shows how often an instance was restarted and why.
  - A new run begins at the start event with the stored start variables; process variables of the previous run are not carried over.
- **RUN-12 (M)** Business keys do not exist in the new API. For existing clients:
  - `business_key` at start and `DELETE .../process-instances-by-business-key/{key}` exist only under `deprecated/` (API-02). There the business key is mapped to the instance ID. If the ID is already taken, the idempotent behaviour from RUN-01 applies: whoever started several instances with the same key so far gets a conflict instead of another instance.
  - `business_key` in `task_info` (`platform-connector-lib`) stays empty and is dropped with the switchover of the connectors (DATA-04).

---

## 8. Task execution (integrated workers)

Takes over `external-task-worker` (optimistic, pessimistic), `process-io-worker`, the fog worker and the eight smart-service module workers. There is no external task API for third parties.

- **TASK-00 (M)** Task types are an internal interface of the engine (Go interface). Every type:
  - gets inputs and credentials of the process owner;
  - returns outputs or an error;
  - can wait asynchronously for an answer whose state the engine persists;
  - gets timeout and retries from the engine.

  New types are code in the engine, not a network protocol. Which types are allowed is set by the namespace (NS-03).

### Device commands

- **TASK-01 (M)** Task type "device command" with two completion strategies:
  - **optimistic**: complete, then send, without waiting for an answer;
  - **pessimistic**: send, wait for the answer, then complete.
- **TASK-02 (M)** Resolve device, service and protocol:
  - via the device-repository (`/devices`, `/device-types`, `/protocols`, `/device-groups`);
  - with the token of the process owner (token exchange);
  - with a cache that is invalidated via Kafka `devices`, `device-types`, `protocols` and `device-groups`.
- **TASK-03 (M)** Marshalling via the marshaller (v2) or in the process itself. In the fog it runs in the process, because there is no marshaller service there.
- **TASK-04 (M)** Sending as `ProtocolMsg`:
  - to the Kafka topic of the protocol handler (`connector`, `mqtt`, `nimbus` ...), with the device ID as key;
  - via HTTP, if the handler is a URL;
  - in the fog via MQTT `command/{localDeviceId}/{localServiceId}`.
- **TASK-05 (M)** Receiving answers:
  - via Kafka `response` and `errors`;
  - via HTTP callback (today `response_to`, `POST /responses`);
  - in the fog via MQTT `response/#`.

  The contract `task_info` (`task_id`, `process_instance_id`, `process_definition_id`, `tenant_id`, `completion_strategy` ...) is kept, because the connectors mirror it back (`platform-connector-lib`). `process_definition_id` carries the deployment ID (DATA-04).
- **TASK-06 (M)** Multi-device tasks:
  - device group resolved into device × matching service (function plus all aspects);
  - order parallel, sequential or round robin;
  - subtask IDs;
  - result as a list.

  The state of the subtasks is held by a single memcached today; in the future it lies **in the engine persistence**.
- **TASK-07 (M)** Timeout and retries are explicitly configurable, per task via `retries`. Today the Camunda lock duration (120 s) acts as the answer timeout.
- **TASK-08 (M)** Read a value instead of sending a command (`prefer_event`, services with interaction `EVENT`):
  - query at timescale-wrapper `/queries`, in the fog at mgw-last-value;
  - the result is treated like an answer.
- **TASK-09 (M)** Runtime inputs as before:
  - `inputs` and `inputs.<pfad>` with type conversion;
  - `overwrite` replaces device, service and protocol;
  - `incident` raises an incident by the user.
- **TASK-10 (M)** The result is available as the local variable `result` (a list from payload version 2 on) and then runs through the output mappings.
- **TASK-11 (M)** Every task is completed at most once, even if answers arrive twice or late.
- **TASK-12 (M)** Cost-relevant metric with the same label semantics: `external_task_worker_task_command_send_count_vec{user_id, process_definition_id}`, where `process_definition_id` carries the deployment ID (DATA-04). The cost-calculator uses it to distribute the process costs to users.

### Process-IO

- **IO-01 (M)** Task type "process-IO" with the input conventions:
  - `io.read.<var>` reads a value;
  - `io.write.<key>` writes a value, JSON is parsed;
  - `io.default.<key>` sets a default;
  - placeholders `{{InstanceId}}` and `{{DeploymentId}}`. `{{DefinitionId}}` is rewritten during migration (DATA-04).
- **IO-02 (M)** Persistent key-value store per user, optionally bound to an instance or deployment.
  - read, write, delete, count, bulk;
  - filter by key (regular expression), `deployment_id` and `instance_id`.
  - Today's routes (`/variables`, `/values`, `/bulk`) and the filter `process_definition_id` exist only under `deprecated/`.
- **IO-03 (M)** Values bound to an instance are deleted with the instance; the retention follows the history. Today they remain until the history is deleted explicitly.

### Modules (formerly smart-service module workers)

Today eight independent workers run against the smart-service-engine, all via `smart-service-module-worker-lib`. They write module information to the smart-service-repository and report errors there; they do not use Camunda's incident mechanism. With the integration of the repository (section 18) these calls become internal. Which of these task types a model may use is set by its namespace (NS-03).

- **TASK-20 (M)** Task type `process_deployment`: deploy or update a process, in the cloud or on a gateway, and register the module in the repository. Today the worker fetches a prepared deployment and sends it back. Integrated, this becomes an internal call, and the loss of fields in the round trip via old model versions is dropped (vault: Process-Deployment).
- **TASK-21 (M)** Task type `process_deployment_start`: start an instance of a deployment (cloud or gateway) and check its state.
- **TASK-22 (M)** Task type `analytics`: create an analytics pipeline (flow-engine, flow-parser, import-deploy, device-repository).
- **TASK-23 (M)** Task type `import`: create an import (import-deploy).
- **TASK-24 (M)** Task type `export`: create an export (serving).
- **TASK-25 (M)** Task type `device_repository`: create device groups (device-repository, device-manager, device-selection).
- **TASK-26 (M)** Task type `info`: write module information.
- **TASK-27 (M)** Task type `watcher`:
  - Watches device selections or HTTP targets for changes and starts a maintenance procedure on a change.
  - Today it has its own Mongo (`watcher-db`) and an `ADVERTISED_URL`. Integrated, this becomes a recurring task in the engine persistence.
- **TASK-28 (M)** For all modules:
  - module life cycle on the instance (SMS-12), internal instead of via the repository;
  - errors as an incident of the instance, visible in the instance status (SMS-11);
  - credentials of the instance owner via token exchange.
- **TASK-29 (M)** Every former worker has its own configuration values (URLs, `WORKER_PARAM_PREFIX`, `ALLOW_EVENTS_IN_FOG_PROCESSES`, `ALLOW_GENERIC_WATCH_REQUESTS`). They become configuration values of the engine.

---

## 9. Incidents

- **INC-01 (M)** Incidents arise directly in the engine:
  - from failed tasks;
  - from script errors;
  - from timer errors;
  - from errors on completion;
  - from limit violations (LONG-08, LONG-16).

  Today there are three ways: HTTP from the worker, polling of `/engine-rest/incident` every 5 s and Kafka `camunda_incident` (unused, legacy).
- **INC-02 (M)** Handling per deployment (`incident_handling`), as today:
  - stop the instance;
  - with `notify`, a notification to the notifier and to developer-notifications; the copy to developer-notifications names the namespace (tag and line `Namespace:`), so that processes and smart services of the same name can be told apart;
  - with `restart`, a new run of the same instance (RUN-11).

  The default stays `restart: false, notify: true`.
- **INC-03 (M)** Restart also for processes with start parameters, with the stored start variables (RUN-01). Today this fails (`restart of processes with start-parameters not supported`).
- **INC-04 (M)** Protection against restart loops:
  - limit per deployment, persistent;
  - backoff;
  - from a threshold on, the deployment is marked as faulty.

  Today there is a 5 min lock per instance in the cloud and 3 restarts per hour in the fog, in memory only. A restart loop has hidden a permanent error in prod (32 restarts in one day, vault note).
- **INC-05 (M)** List incidents with filters by namespace, instance, run, deployment and element, sorting and paging. That is the functional scope that the web-ui and dashboard widgets use today via `/incidents`; the old route exists only under `deprecated/`.
- **INC-06 (M)** For every incident, element, error message, time and the state of the variables at the time of the error are stored. The retention follows HIST-04.

---

## 10. Events and message correlation

### What happens today

- The event-worker reads **all** device data topics (`urn_infai_ses_service_.*`, `urn_infai_ses_import_.*`).
- For each message it looks up the matching descriptions by (device, service) or import.
- It evaluates `senergy:script` in goja.
- Then it calls `POST /message` via the wrapper, with `messageName = event_id`, `all: true`, `tenantId` and the local variables `event`, `age`, `msg_id`.

### Decision: variant A

The event-worker stays a role of its own, preferably as a second operating mode of the same binary:
- It keeps scaling separately via the topic distribution.
- It reads the event descriptions from the engine (API or change stream).
- It calls the correlation API of the engine.
- event-deployment is integrated into the deploy (DEP-07).

### Requirements

- **EVT-01 (M)** Correlation by message name to all waiting catch events and message starts of a tenant (`all`), with local variables.
- **EVT-02 (M)** Correlation by message name **to a given instance ID**, e.g. for `deployment_done_<id>`. Maintenance procedures are a message start with a given instance ID (BPMN-03).
- **EVT-03 (M)** Event sources:
  - device + service;
  - device without service, expanded at deploy;
  - device group, expanded at deploy;
  - import.

  The analytics events (`MessageEvent` with flow_id) are dead and are dropped.
- **EVT-04 (M)** Condition evaluation in the event-worker:
  - unmarshalling to the characteristic of the event, via path or function + aspect;
  - script with `value_variable` and `variables`;
  - it triggers on true, "true" or a number ≥ 1.
- **EVT-05 (M)** QoS per event as today (at-most-once or at-least-once), plus a **unique idempotency key** per message. `msg_id` is `partition:offset` without topic today and therefore not unique.
- **EVT-06 (M)** Messages above a maximum age are discarded (today 1 h).
- **EVT-07 (M)** Errors in the evaluation go to the user throttled (today via the notifier with a duplicate window of 1 h) and do not block consumption.
- **EVT-08 (S)** A correlation that finds no waiting instance is not an error; it is counted as a metric.
- **EVT-09 (M)** The event-worker reports nothing for debugging (decided). Its load stays unchanged. For events, the engine stores only what reaches it anyway (HIST-05):
  - begin and end of waiting for an event;
  - every correlation with time, message ID and the variables `event` and `age`.
- **EVT-10 (M)** The condition evaluations that did **not** trigger are reconstructed on demand from the Timescale data for debugging:
  - For the waiting period (begin of waiting until the correlation or until now), the debug view queries the values of the event source via timescale-wrapper `/queries`, with the token of the owner.
  - For group events it queries per expanded device × service.
  - It applies to each value the same evaluation as the event-worker: unmarshalling, conversion to the characteristic, condition script with `value_variable` and `variables`.
  - The basis is the event description of the deployment version that applied at that time.
  - For each value it shows time, raw value, converted value, result, errors and script output.
  - The evaluation uses the same code as the event-worker (shared library), so that reconstruction and operation do not drift apart.
  - Costs arise only when someone debugs; the query is paged and limited in time per request.
- **EVT-11 (M)** Reconstruction and actual correlations are shown side by side, and the reconstruction is marked as such. Deviations are a diagnostic hint, such as "would have triggered, but no correlation". Possible causes that the view names:
  - message older than the maximum age (EVT-06);
  - discarded with `qos=0`;
  - event-worker disturbed;
  - script not deterministic, for example through `Date`.
- **EVT-12 (M)** Limits of the reconstruction:
  - It reaches back as far as Timescale holds the data. The retention per table is governed by timescale-rule-manager; there is a template `retention-one-year`, to which tables it is applied has not been checked.
  - If values are missing in Timescale, the view shows only the actual correlations.
  - For **import events** it has to be checked whether import data lands in Timescale. If not, only the correlations remain.
- **EVT-13 (M)** Fog processes:
  - Device events on the gateway also go to the cloud via mgw-device-cloud-connector (`handler/message_hdl/device_event*`) and thus land in Timescale.
  - The reconstruction for fog instances therefore runs in the cloud, with the correlations from the gateway (DBG-15) and the mapping of local IDs to cloud IDs from the deployment.
  - Gaps arise while the gateway is offline: the connector buffers only in memory, up to 50 000 messages, and sends with QoS 0. The view marks such periods.
  - On the gateway itself there is no time series, only mgw-last-value. Without a cloud connection only the current value can therefore be evaluated (DBG-18).

---

## 11. History and retention

- **HIST-01 (M)** History at instance level: instance ID, run number, deployment and deployment version of the run, model and model version (if any), name, start, end, end state (completed, externally terminated, incident) and start variables. This is used by the web-ui, canary, widgets and smart services.
- **HIST-02 (M)** History query in the functional scope of today's monitor:
  - finished/running;
  - namespace, deployment ID, deployment name (substring), model ID, instance ID;
  - sorting, paging, total count.

  Today's parameter names (`processDefinitionId`, `processDefinitionNameLike`, `processInstanceBusinessKey`, `maxResults`, `firstResult`, `with_total`) exist only under `deprecated/`.
- **HIST-03 (M)** Delete history, individually and several at once, with a cascade to IO, incidents and debug history.
- **HIST-04 (M)** The retention is governed by a value and upper bounds (decided 2026-10-12, replaces "the longest value applies"). The goal is that nobody sets very long periods and thereby creates large amounts of data.
  - **Upper bounds** are set by admins: global, per namespace (NS-07) and per user.
  - **Default:** It applies when nothing is set on the deployment; globally 7 days, can be overridden per namespace.
  - **Value on the deployment:** The owner sets it, within the upper bounds. A higher value is rejected when saved.
  - **Effective** is the smallest of the values: deployment value or default, upper bound global/namespace, upper bound of the owner of the instance (SEC-12). Examples:
    - user limit 7 days, deployment 1 day results in 1 day.
    - user limit 1 day, deployment 7 days results in 1 day.
    - no settings, default 7 days results in 7 days.
  - When an upper bound is lowered, it applies immediately, also to existing data.
  - This applies to instance history, activity history, incidents and debug history (section 13), because they are the same data.
  - The engine enforces the retention itself; CronJob and batch window are dropped.
  - Limits also apply with long retention, see LONG-03 and LONG-07.
- **HIST-05 (M)** Activity history per instance, for running and finished instances:
  - which element was entered and left when;
  - chosen outgoing flow at gateways and evaluated conditions;
  - variable changes with element;
  - commands and answers;
  - correlations and timers;
  - script output, incidents.

  It is the basis for historical debugging (DBG-13).
- **HIST-06 (M)** History data of running instances has a retention that does not depend on the end of the instance (section 12). Despite a 7-day TTL, the history in prod reaches back about 8 months, probably because of instances that never end.

---

## 12. Long-lived processes and limited storage

Processes that run for months or years are a main use case: a process waits for sensor data, switches something and returns to waiting. Examples are the fan process from the vault notes "A process incident ends the instance unless restart is set" and "Comparing two sensors inside a process". Such processes must run without intervention, without the database growing with running time or the number of loop iterations. On gateways with limited storage this does not work today.

### Why it grows today

- **Camunda cleans up history only for finished instances.** The removal time (`REMOVAL_TIME_`) is only set at the end of the instance. Every loop iteration writes one row per passed element into `ACT_HI_ACTINST`, and for an instance that never ends nothing of it is deleted (vault: "History cleanup never runs without a batch window", section "Not this if").
- **The gateway has the same engine configuration.** The cleanup in the sync client (every 24 h, maximum age 168 h) likewise deletes only finished instances.
- **Postgres does not return deleted space to the file system.** Only `VACUUM FULL` lets the file shrink.
- **Parallel branches without a join** are frequent in the exports (20 diverging, 5 converging parallel gateways). If such a branch leads back in a loop before the split, the number of tokens multiplies.

### Requirements

- **LONG-01 (M)** The storage needed by a running instance is bounded. It depends on the number of active elements and variables, not on running time or the number of iterations.
- **LONG-02 (M)** Runtime state is overwritten, not appended. Completed tasks, fired timers, correlated subscriptions and finished subtasks are deleted on completion.
- **LONG-03 (M)** History data of running instances (HIST-05, incidents) is cleaned up independently of the end of the instance:
  - by age according to HIST-04;
  - additionally by count per instance as a ring buffer, with a configurable upper bound; the default values are set during implementation.

  The upper bound prevents a long retention from accumulating unlimited data with fast loops. When it is reached, the user sees this in debugging ("older entries displaced").
- **LONG-04 (M)** Counters instead of individual rows where only a count is needed: passes per element, last execution, last error.
- **LONG-05 (M)** The cleanup runs in the engine continuously and in small steps, without a batch window and without a CronJob.
- **LONG-06 (M)** Deleted space becomes free again; the file does not grow further after a settling phase. With SQLite e.g. with `auto_vacuum=incremental` and regular `incremental_vacuum`.
- **LONG-07 (M)** Storage budget on the gateway configurable; the default is set during implementation.
  - When the usage approaches the budget, a warning goes to the cloud and the user, and history data is cut beyond the configured retention, oldest first.
  - On the gateway the budget therefore takes precedence over HIST-04.
  - Runtime state takes precedence over history.
  - If the storage medium is full, the engine enters a defined error state and leaves no corrupted state behind.
- **LONG-08 (M)** Size limits per variable and per instance. Exceeding them creates an incident. Typical case: a script that appends to a list in every iteration.
- **LONG-09 (M)** The synchronisation does not send one entry per loop iteration upwards. Sent upwards are:
  - changes at instance level;
  - step details only while someone is watching (DBG-12) or when they are fetched for historical debugging in the cloud (DBG-15).

  Today the sync client reports every change to `ACT_RU_EXECUTION` upwards via a trigger.
- **LONG-10 (M)** The offline buffer of the synchronisation is bounded. It is folded per instance to the latest state; above an upper bound a snapshot is sent after reconnecting.
- **LONG-11 (M)** Side stores are bounded:
  - IO values without instance binding per user or per gateway, by count and size;
  - start parameters are deleted with the instance or deployment;
  - on the cloud side of the synchronisation the same retention applies as in the engine.
- **LONG-12 (M)** Running instances survive restarts, updates of the engine and schema changes. For timers after downtime there is a catch-up rule: execute missed cycles at most once, do not catch up all of them.
- **LONG-13 (S)** Instances without progress are recognisable: metric and display "no element change since period X".
- **LONG-14 (M)** Metrics on storage:
  - size by runtime state, history, outbox and IO;
  - the instances with the most history entries.

  In the fog these values go to the cloud.
- **LONG-15 (M)** Acceptance through a long-running test on an arm64 gateway.
  - Representative loop processes from the exported models are tested, among them parallel and inclusive gateways, device commands, timer cycles and IO.
  - The event rate is high, so that many months of operation are run through in a short time; offline phases and restarts are added.
  - Criterion: database size and memory use stay stable after the settling phase.
- **LONG-16 (M)** Maximum number of simultaneous tokens per instance, configurable. Exceeding it creates an incident instead of unbounded growth (see parallel branches above).

---

## 13. Debugging

Today there is nothing for this. Cockpit is removed, the wrapper reads no variables, and the monitor's detail view shows only the diagram without state. Decided is:
- marking happens in the client;
- step by step also on historical data;
- as detailed as possible;
- event evaluations are reconstructed implicitly from the Timescale data and not reported by the event-worker (EVT-09 to EVT-13).

Live and history data have the same form: the live stream is the end of the activity history (HIST-05).

### Live endpoint

- **DBG-01 (M)** WebSocket endpoint per instance, e.g. `GET /v2/process-instances/{id}/live`.
  - First comes a snapshot, then deltas with a consecutive sequence number.
  - After a connection loss the client continues from a sequence; if the gap is too large, a new snapshot comes.
  - When the instance ends, the end state comes, then the connection is closed.
- **DBG-02 (M)** The snapshot contains:
  - **Instance**: ID, run number and earlier runs, deployment and version, state, start time, start variables.
  - **Active elements**: BPMN ID, type, name, active since. Several are possible, through parallel branches and event-based gateways.
  - **Reason for waiting** per element:
    - Device command: target device and service, strategy, subtasks with status, attempt n of `retries`, timeout.
    - Message event: `event_id`, source, condition script, current value of the source with evaluation (DBG-18), on request the reconstructed evaluations since waiting began (EVT-10).
    - Timer: definition and next due time.
    - Script task: running since.
    - Smart-service module: module type and progress.
  - **Variables**: instance and local variables, each with type, value, last change and changing element.
  - **Open incidents**.
- **DBG-03 (M)** Deltas for all entries from HIST-05.
- **DBG-04 (M)** Large values are transmitted truncated and can be loaded individually via HTTP.

### Historical debugging

- **DBG-13 (M)** Finished and running instances can be traced step by step within the retention:
  - forwards and backwards step by step;
  - jump to a point in time or an entry;
  - per step the state as in the snapshot (active elements, variables, reason for waiting).
- **DBG-14 (M)** For this, variable states are stored so that every step can be reconstructed: changes as deltas plus periodic full states. The limits from LONG-03 apply. If the beginning has been displaced, the look back begins at the oldest remaining full state.
- **DBG-15 (M)** Fog instances can be debugged historically:
  - The history stays on the gateway.
  - The cloud fetches it on demand, in sections via the synchronisation.
  - If the gateway is offline, only the state the cloud already has is available; it is marked as such.
  - Event evaluations are reconstructed also for fog instances from the Timescale data in the cloud (EVT-13).
- **DBG-16 (S)** Search in the history, e.g. "when did variable X get the value Y", "when did event Z trigger", "all incidents".
- **DBG-18 (M)** For a waiting event, the live view shows the current value of the source and whether the condition would be fulfilled with it now.
  - The source is the last value via timescale-wrapper, in the fog mgw-last-value.
  - It is queried on opening and, while someone is watching, in a throttled interval; without a watcher no costs arise.

### Diagram

- **DBG-05 (M)** The client marks the SVG of the matching deployment version stored at deploy.
  - bpmn-js writes `data-element-id` for every element on export; checked on `smart-service-repository/pkg/tests/resources/process_deployment.svg` and the fixtures in process-deployment.
  - The engine delivers the SVG once and after that only element IDs.
- **DBG-06 (M)** Distinguished markings: active, passed (with count, LONG-04), incident, waiting for an event with evaluation of the current value (DBG-18).
- **DBG-07 (S)** Passed sequence flows are marked, so that the path taken through gateways is visible.

### Access, load, operations

- **DBG-08 (M)** Access with the read right on the instance (NS-05) or as admin.
  - With the WebSocket the token goes as a subprotocol or as the first message, not as a query parameter, otherwise it ends up in access logs.
  - Kong must pass the upgrade through, the Ladon rule must cover the path.
  - When the token expires, the connection is closed or a new one is requested.
- **DBG-09 (M)** With several replicas the deltas arrive without requiring sticky sessions; the source is the change stream (SYNC-03).
- **DBG-10 (M)** Limits:
  - connections per user;
  - throttling with fast loops: combine deltas, variables as latest state;
  - a slow client gets a new snapshot instead of missed deltas.
- **DBG-11 (M)** Script output from `log(...)` (EXPR-04) appears in the history at the element that produced it.
- **DBG-12 (S)** Live debugging of fog instances via the same endpoint with `networkId`. The live stream from the gateway runs only while someone is watching.
- **DBG-17 (K)** Interventions from the debug view, separate from the read stream and with their own authorisation: set a variable (RUN-05), trigger an event manually, make a timer due immediately.

---

## 14. Tenants, authentication, permissions

- **SEC-01 (M)** Queries and actions are filtered by the rights in permissions-v2 (NS-05, SEC-07), i.e. own and shared resources. Who owns a resource is stated in the owner field of the engine (SEC-09). Today the wrapper filters only by tenant (`tenantIdIn`).
- **SEC-02 (M)** An admin role sees and acts across tenants, for housekeeping and internal services. Today this works only via the Camunda REST API with basic auth.
- **SEC-03 (M)** Internal calls run with client credentials or token exchange and valid tokens. Today the wrapper calls the incident-api with a hard-coded, expired admin token.
- **SEC-04 (M)** JWT handling according to the platform standard (Kong plus Ladon). Today the wrapper uses `ParseUnverified`.
- **SEC-05 (M)** Gateway permission via permissions-v2, topic `hubs`: read and start with `rx`, deploy and delete with `a`.
- **SEC-06 (M)** Every instance runs with the rights of its owner, independently of the namespace: token exchange for device commands (TASK-02) and for modules (TASK-28). Sharing is governed by the namespace (NS-05). Smart-service instances run today in a separate engine without tenants.
- **SEC-07 (M)** Models, deployments and instances are resources in permissions-v2 (NS-05). What is shared on creation is determined by the default permissions of the namespace; after that users with administrate can grant further rights. Every access is checked against permissions-v2, not only against the tenant.
- **SEC-08 (M)** When a user is deleted, everything they own is removed: models, deployments and instances in all namespaces including modules, history, debug history, IO data, incidents and schedules. Rights shared with them on resources of others are removed. Today remnants are left (vault: "Deleting a user leaves process models and releases behind").

  What happens to dependants of others is governed by SEC-13.
- **SEC-13 (M)** Dependants of others when a user is deleted are resources of other owners that depend on their resources:
  - instances of other users on their shared deployments, e.g. smart-service instances on their release;
  - deployments of other users that one of their instances manages through a module (NS-08).

  Two behaviours are possible; a configuration decides (decided):
  - **Delete cascade**: the dependants of others are deleted as well. Their owners get a notification with the reason.
  - **Error message**: the deletion is rejected, with a list of the dependants of others (resource, namespace, owner). The admin can change owners beforehand (SEC-11) or remove the dependants.

  Proposal: the configuration applies per namespace, with a platform default, like the other namespace settings (NS-07). Deployments of others on deleted models are not dependants (MOD-08).
- **SEC-14 (M)** Deleting a user runs via a synchronous admin call of the engine, e.g. `DELETE /users/{id}`. In addition there is a dry-run check without effect, e.g. `GET /users/{id}/dependents`.
  - Today user-management calls the services one after the other synchronously (`pkg/ctrl/user.go` `DeleteUser`). On an error it aborts, Keycloak comes last.
  - The error message from SEC-13 therefore stops the deletion before Keycloak. Services called before have already deleted their data, though, the device-repository first.
  - user-management therefore calls the dry-run check **before** all delete calls and aborts with the message if the engine would reject.
  - Deleting via Kafka `user` (today process-deployment, process-model-repository) remains only as a safeguard for users deleted outside of user-management.

### Owner

With devices and hubs there were problems as long as the assignment to the user was only in permissions-v2. Therefore an owner was introduced there (`device-repository`: `Device.OwnerId`, `Hub.OwnerId`, `Graph.Owner`). The same applies here (decided).

- **SEC-09 (M)** Every model, every deployment and every instance has an owner field in the engine. It is not derived from permissions-v2. The owner is the basis for:
  - distinguishing own and shared resources in lists;
  - deletion when a user is deleted (SEC-08);
  - the rights with which an instance works (SEC-06);
  - cost attribution: `user_id` in the metric label is the owner of the instance (TASK-12);
  - limits per user (LONG-11);
  - the synchronisation with gateways.
- **SEC-10 (M)** The owner **necessarily** has administrate in permissions-v2 (decided), as with devices.
  - On creation the engine sets the default permissions of the namespace and gives the owner administrate. The model is `device-repository/lib/controller/security.go` `EnsureInitialRights`.
  - Whoever wants to withdraw the right from the owner is rejected. If that is not possible because the change happens directly in permissions-v2, the engine restores the right immediately.
  - According to the code, device-repository checks the admin right of the owner only on creation and on an owner change. permissions-v2 itself knows no owner. A direct withdrawal via permissions-v2 is therefore not caught there (section 22).
- **SEC-11 (M)** Owner change following the pattern of device-repository (`hub.go`, `device.go`):
  - It is allowed only for users with administrate on the resource or platform admins.
  - The new owner must already have administrate.
  - Without a value, the owner is the requesting user; only admins may set another owner on creation.
- **SEC-12 (M)** Who becomes the owner of an instance:
  - at a start by a user, the starting user, also on a shared deployment;
  - at starts without a user (schedule, timer start event, message start by a device), the owner of the deployment;
  - for instances that a module creates (NS-08), the owner of the managing instance.

  A new run (RUN-11) keeps the owner of the instance. An owner change on the deployment does not change the owner of existing instances.

  Deployments of a gateway and their instances are the exception: they always belong to the owner of the network (FOG-11).

---

## 15. API

- **API-01 (M)** A new, uniform API without legacy (decided).
  - **Resources:** namespaces, models (with versions), deployments (with versions), instances (with runs), history, incidents, IO values, schedules, modules, live and debug access.
  - **Terms** according to the data model (section 5): `deployment_id`, `instance_id`, `run`, `namespace`. There are no business keys, no process definitions and no vid.
  - **One base URL** instead of today's ten routes of wrapper, process-deployment, process-model-repository, process-io, incidents, scheduler, smart-service-repository, process-sync and process-fog-deployment.
  - **Namespace in every resource:** it is in the path, e.g. `/namespaces/{ns}/deployments`, or a mandatory filter.
- **API-02 (M)** Old APIs and all aliases are reachable **only under one path `deprecated/`** (decided), e.g. `.../deprecated/engine/v2/...`, `.../deprecated/deployment/v3/...`. This covers:
  - the wrapper routes v2, among them `/v2/process-definitions/...`, start via `GET` with `business_key` and deletion by business key;
  - process-deployment `/v3/...`;
  - process-model-repository `/processes` and `/v2/processes`;
  - process-io, incident-api and process-scheduler;
  - smart-service-repository, among them `/designs`, `/releases`, `/extended-releases`, `/instances`, `/modules` and `/instances-by-process-id/...`;
  - process-sync and process-fog-deployment;
  - the old parameter and field names (`processDefinitionId`, `process_definition_id`, `processInstanceBusinessKey`, `maxResults`/`firstResult`).
- **API-03 (M)** The layer under `deprecated/` only translates to the new API. It has no logic and no storage of its own, so that it can be removed without side effects. Every call is counted per route (metric); from this one can read when it can be switched off.
- **API-04 (M)** The own clients are switched over to the new API: web-ui (monitor, deployments, models, smart services, fog views, widgets), canary and snowflake-canary, user-management. After the switchover no platform service uses `deprecated/` any more.
- **API-05 (M)** Cloud and fog have one API: the same resources with an optional `networkId`.
- **API-06 (M)** REST semantics:
  - starting with `POST`, or `PUT` with a given ID (RUN-01); no state-changing `GET`;
  - uniform paging;
  - permission errors with 403, not 401.
- **API-07 (M)** OpenAPI according to the platform standard. The `deprecated/` routes are marked as deprecated in it.
- **API-08 (M)** Kong redirects today's paths to `deprecated/` (decided). Example: `/process/engine/v2/deployments/{id}/start` becomes `.../deprecated/engine/v2/deployments/{id}/start`.
  - This way start URLs that users have built in outside the platform via "copy start URL" also keep working.
  - The redirect is pure Kong configuration (path rewrite). The engine does not know the old paths.
  - Ladon rules for the old paths are switched over as well.
  - The redirect is dropped together with the `deprecated/` layer when its metric (API-03) shows no more calls.

### Go client library

Today every service brings its own handwritten Go client, e.g. `camunda-engine-wrapper/lib/client`, `process-incident-api/lib/client`, `event-deployment/lib/client` and `permissions-v2/pkg/client`. Fields that one side does not know are lost without error when decoding. This is documented twice:
- vault: "Moving a type into the shared model has a publish order";
- vault: "A consumer that copied the type is invisible to the sibling build"; an example is the round trip in the smart-service worker that lost `aspect_ids`.

A client library exists only for Go. For JavaScript/TypeScript there is none (decided): the frontends implement the API themselves, based on the OpenAPI (API-07). The web-ui is adapted for this (API-04). The client covers only the new API, not `deprecated/`.

- **CLI-01 (M)** One source for the types. Go types and OpenAPI are not maintained independently of each other: either the Go types of the server are the source and OpenAPI is generated from them, or vice versa. The generation runs in CI. If the client deviates from the server, the build fails. The frontends use the same OpenAPI.
- **CLI-02 (M)** The client covers the entire new API:
  - namespaces, models, deployments, instances and runs;
  - history, incidents, IO values, schedules, modules;
  - live and historical debugging;
  - queries with `networkId` for gateways.
- **CLI-03 (M)** Errors arrive typed. At least distinguished are: not found, no permission, conflict (e.g. idempotent start with different content, RUN-01), validation error with element reference (BPMN-18) and not reachable.
- **CLI-04 (M)** Helpers for paging: iterators over all pages.
- **CLI-05 (M)** A client for the debug stream (DBG-01):
  - continue from the last sequence after a disconnect;
  - merge snapshot and deltas into a current state object.

  For historical debugging it offers the same state form with step forward, step back and jump (DBG-13).
- **CLI-06 (M)** Versioning and publishing:
  - The client is versioned with the API and published together with the server, via the release pipeline of the platform.
  - Under Apache-2.0 like the other SENERGY repos.
  - An incompatible API break needs a new major path (`/vN`) in the Go module. Otherwise the pipeline assigns a major tag that Go rejects as an invalid version (`senergy-wissen/03 Werkzeuge/CI/Die Version vergibt die Pipeline.md`).
- **CLI-07 (M)** The client is tested against a running engine, with the test client from CLI-09.
- **CLI-08 (M)** Go client in the repo of the engine under `pkg/client`, according to platform convention (like `permissions-v2/pkg/client`, `device-selection/pkg/client`):
  - an interface `Client` with constructor `New(url)`;
  - types from the package of the server, no copies.
- **CLI-09 (M)** A test client, e.g. `NewTestClient(ctx)`, starts the engine in the process with an embedded database (SQLite) and fulfils the same interface. Consumers test with it without network and without containers. The model is `permissions-v2/pkg/client.NewTestClient`.
- **CLI-10 (M)** Authentication:
  - token per call (user token);
  - token provider for client credentials and token exchange, like `permissions-v2/pkg/client.NewTokenProvider`.
- **CLI-11 (S)** The library parts that other repos import today from `external-task-worker/lib` (marshaller, messages; device-command, live-data-worker) get a stable place. They do not belong in the API client.

## 16. Fog: engine on the gateway

- **FOG-01 (M)** The same engine binary runs in cloud and fog. The fog operating mode uses:
  - MQTT instead of Kafka;
  - local device IDs;
  - the marshaller in the process;
  - the device-repository mirror;
  - mgw-last-value.
- **FOG-02 (M)** Images for amd64 and arm64. arm/v7 is dropped; old gateway versions are irrelevant (decided).
- **FOG-03 (M)** Embedded persistence on the gateway. It replaces two Postgres 11.2 containers (engine, process-io) and the bolt DB of the sync client.
  - Recommendation: SQLite, because the cloud uses Postgres and schema and queries can largely be shared.
- **FOG-04 (M)** No target hardware is prescribed. The storage budget is configurable (LONG-07), the long-running test runs on an arm64 gateway (LONG-15).
- **FOG-05 (M)** Full function without a cloud connection: instances, timers, events, incidents with restart, history, debug history.
- **FOG-06 (M)** Local event evaluation on MQTT `event/{localDeviceId}/{localServiceId}`, with the event descriptions from the deployment. The event-worker in the fog fetches them from the engine; today it queries the HTTP API of the sync client (`GET /event-descriptions`).
- **FOG-07 (M)** Incident handling with a persistent restart counter, on **one** level. Today there are two levels, gateway and cloud reconciler, which get in each other's way.
- **FOG-08 (M)** Notifications go via the cloud-proxy.
- **FOG-09 (M)** Task type "HTTP call" into the local network (BPMN-10).
- **FOG-10 (S)** The engine brings its schema changes itself. Today wrapper and sync client migrate the Camunda tables from outside (vault: "Camunda does not apply its own schema updates").
- **FOG-11 (M)** Deployments of a gateway and their instances belong to the owner of the network (decided 2026-10-27). The gateway reaches the cloud with the token of the user its certificate names (FOG-08), and the notifier accepts a notification only for the user of the token, so a different owner received no notifications from the gateway (401 "You may only send messages to yourself").
  - The cloud takes `owner_id` of the hub in device-repository: on creation and start, an `owner` other than that is rejected (400), and so is an owner change to someone else (SEC-11). A pass after the start corrects existing deployments and instances and registers the rights of the network owner; earlier owners keep their rights in permissions-v2.
  - The gateway takes `user_id` of the network from the cert-manager of the cloud proxy (`NETWORK_INFO_URL`), sets it on what the cloud sends and rewrites its existing deployments, event descriptions and instances. Without the setting, or while the network is unknown, the gateway keeps the owners it received.
  - Instances a gateway reports get the owner of their deployment in the cloud, whatever owner the gateway names, so that a gateway with an older process module does not turn the owner back.
  - Whoever deploys or starts on a gateway without being the owner of its network gets rights on the new resource in permissions-v2, so that the user keeps access to it (decided 2026-10-27). The namespace sets them in `gateway_creator_rights`; not set means read, write and administrate, which let the user see, stop and delete it, and no right set grants nothing. Without execute, starting a deployment someone else made needs a share of the owner. The rights are written together with the administrate of the owner before the resource is created; if that fails, nothing is created (503). For resources of gateways, the engine evaluates rights in permissions-v2 also where the namespace does not share the kind (`processes`), in single access and in the lists of deployments, instances and incidents.
  - Incidents keep the owner they were recorded with. IO values of instances follow the owner, so values written under a previous owner are no longer read by them.

---

## 17. Synchronisation cloud–gateway (formerly process-sync and mgw-process-sync-client)

process-sync (cloud) and mgw-process-sync-client (gateway) are integrated into the engine (decided). Both sides run the same engine, the same data model, the same IDs and the same versions. The synchronisation is thus replication between two identical engines.

Today both services depend on Camunda internals: Postgres triggers on `ACT_RU_EXECUTION` and other tables, schema migrations from outside, mapping of the Camunda deployment IDs. What the sync client does beyond that is already an engine function in this document:
- deploy (DEP-09);
- event descriptions for the event-worker (FOG-06);
- restart logic (FOG-07);
- history cleanup (section 12);
- metadata in bolt (FOG-03).


- **SYNC-01 (M)** Downwards a desired state is replicated, idempotently and with the cloud UUID as ID:
  - deployments with version and chosen update strategy (DEP-04);
  - deletions;
  - starts with a given instance ID and parameters, idempotent (RUN-01).
- **SYNC-02 (M)** Upwards a change stream at instance level is replicated, with sequence numbers and acknowledgement:
  - deployments and versions;
  - instances and history;
  - incidents and errors.

  After an offline phase it catches up from the last acknowledged position. It replaces the Postgres triggers, the `known` lists every 3 h and sending with QoS 0 and a buffer only in memory.
- **SYNC-03 (M)** The engine provides the change stream as an outbox with a sequence. It is used by the synchronisation, the live endpoint (DBG-09) and the event-worker.
- **SYNC-04 (M)** The transport keeps running over MQTT via mgw-device-cloud-connector, scheme `processes/{networkId}/...`, extended by:
  - fetching history sections (DBG-15);
  - live stream on request (DBG-12).
- **SYNC-05 (M)** The cloud side provides a read and command facade per `networkId` as part of the engine API (API-05), with permissions-v2 `hubs`.
- **SYNC-06 (M)** Event descriptions for gateways are created in the cloud and delivered again on changes to device groups.
- **SYNC-07 (S)** Data of a network that has had no contact for longer than `cleanup_max_age` (today 730 h) is removed.
- **SYNC-08 (M)** The ideas from the unmerged branch `feat/platform-source-of-truth` in process-sync move into the engine: desired state per deployment, reconciler, resending after a period. Fingerprints and duplicate cleanup are dropped, because IDs are stable and deploys idempotent (DEP-02, RUN-01). The branch itself is no longer merged.
- **SYNC-09 (M)** No separate services any more:
  - **Gateway:** The synchronisation is part of the engine process, without its own container and without its own database. The bolt DB of the sync client is dropped.
  - **Cloud:** The synchronisation is part of the code base of the engine. It runs either in the engine replicas or as a separate role "sync" of the same binary, analogous to the event-worker (section 10). Fog data lies in the engine database as resources with `networkId`. Rights, history, debugging and namespaces apply to it without a separate implementation; the Mongo of process-sync is dropped.
- **SYNC-10 (M)** MQTT with several replicas. Today process-sync has one replica and a persistent MQTT session (`MQTT_CLEAN_SESSION=false`). In the future:
  - Every message from a gateway is processed exactly once, either via shared subscriptions in the broker (VerneMQ: `$share/<gruppe>/...`) or via a leader election for the sync part.
  - A message is acknowledged only when its effect is committed to the database. The restart of a replica loses no messages.
  - Which way is taken is settled before the implementation. A separate role "sync" with one replica and leader election is the simplest way; shared subscriptions allow several active replicas.
- **SYNC-11 (M)** The sync protocol is versioned.
  - Gateways are updated later than the cloud, so cloud and gateway temporarily run with different engine versions.
  - Every message carries a protocol version. The cloud supports the current and at least the previous gateway version; the window is configurable.
  - A gateway outside the window is marked as incompatible and not silently served wrongly.
  - Every engine release states whether it changes the protocol.
- **SYNC-12 (M)** Network overview per gateway:
  - last contact;
  - engine and protocol version;
  - replication backlog (unacknowledged sequences in both directions);
  - offline since.

  It replaces `/networks`, `last_network_contact` and the self-test via `processes/ping`, which today ends process-sync with a fatal error after 10 min without a ping. In the future this is a metric with an alert.
- **SYNC-13 (M)** Desired and actual state per deployment and gateway are visible: pending, applied or failed with an error message. This replaces `state/error` and the query via `/metadata`.

---

## 18. Smart services as a namespace

Smart services are the namespace `smart-services` (NS-01, NS-02). What smart-service-engine, smart-service-repository and the eight module workers do today becomes general functions of the engine. Every namespace can use them, as far as its configuration allows.

| Today (smart services) | In future (all namespaces) |
|---|---|
| Design | model (section 5) |
| Release | deployment version |
| Smart-service instance | instance |
| Release info from form fields | start parameters with options, characteristic and device selection criteria (BPMN-02) |
| Module | resource that an instance created through a task type (SMS-12) |
| Maintenance procedure | message start with a given instance ID (SMS-13) |
| Instance variable | process variable |
| Module worker | task type, allowed per namespace (TASK-20 to TASK-29, NS-03) |

Today the smart-service-repository (Mongo, 1 replica) stores designs, releases, instances, instance variables, modules and maintenance procedures. It deploys into the smart-service-engine itself and provides callbacks to the workers (`/instances-by-process-id/...`). With the integration these callbacks are dropped.

- **SMS-01 (M)** The smart-service-engine (Camunda 7.11), the smart-service-repository and the eight module workers are replaced by the engine. A namespace `smart-services` is shipped, with today's behaviour (NS-03, NS-05, NS-06).
- **SMS-02 (M)** Stable IDs for deployments and messages. This drops today's renaming at release deploy (`id_<releaseId>`, internal event ID).
- **SMS-03 (M)** At the update of a deployment in `smart-services` (today release) the same choice applies as in DEP-04: stop, drain, restart. The choice applies in two stages:
  - for the instances themselves;
  - for the deployments that these instances manage in other namespaces (NS-08). They receive the strategy when the module redeploys them.

  Today a new release replaces the older ones of the same design, also across owners, and deletes their deployments (vault: "Creating a release rewrites the releases before it"). This behaviour is not taken over implicitly.
- **SMS-04 (M)** The completion of a deployment that a module created is reported directly to the managing instance. Today the worker sends `deployment_done_<id>` 5 s after the deploy.
- **SMS-05 (K)** Clarify the catch `permission_done_*`: there is no sender in the workspace, it is probably dead.
- **SMS-06 (M)** There are no orphaned instances any more, because everything is in the same database and is deleted together. Today's cleanup (`smart-service-repository/pkg/controller/cleanup.go`) is dropped.
- **SMS-07 (M)** Sharing of deployments and instances via permissions-v2, as far as the namespace provides for it (NS-05). Whoever may execute a shared deployment creates their own instances of it. This is how releases work today.
- **SMS-08 (M)** Start parameters when creating an instance. The basis are the form fields: type, options, multiple selection, characteristic, device selection criteria, order (today `releaseinfo.go`). Selectable options are resolved for the user, e.g. devices via device-selection.
- **SMS-09 (M)** Change parameters of a running instance. This triggers the update choice from SMS-03 (today `PUT /instances/{id}/parameters`).
- **SMS-10 (M)** Read, set and delete variables of an instance. They are the process variables of the instance (RUN-05); a separate store as in the repository today is dropped.
- **SMS-11 (M)** Status of an instance: ready, running, error, with an error message. It is derived from the run state and incidents, instead of being reported by the workers as today.
- **SMS-12 (M)** Modules: resources that an instance creates through a task type (deployment, import, analytics pipeline, device group ...), with type, data and delete reference.
  - They can be retrieved per instance and collectively.
  - When the instance is deleted, they are deleted as well, also deployments on gateways.
  - This function applies in every namespace whose task types create resources.
- **SMS-13 (M)** Maintenance procedures: additional message start events of a model with their own start parameters.
  - List per instance, query parameters, start.
  - A start creates an instance with a given ID that refers to the instance the procedure belongs to.
  - The watcher (TASK-27) triggers them internally.
- **SMS-14 (M)** Debugging (section 13) also applies to smart-service instances. Today users there see only status and error message.

---

## 19. Operations

- **OPS-01 (S)** Several replicas are possible without anything being executed twice: timers, schedules, watchers and answer processing. There are no concrete load targets (decided). Several replicas serve mainly availability during updates.
- **OPS-02 (M)** Persistence in the central Postgres with backup (decided). The four Camunda Postgres instances (11.2, without backup) and the Mongo databases of process-io, incidents, process-deployment, process-model-repository, smart-service-repository, event-deployment, scheduler and watcher merge into it.
- **OPS-03 (M)** The engine brings its schema migrations itself, versioned and forward compatible, for Postgres and SQLite.
- **OPS-04 (M)** Resource requests and limits, liveness and readiness probe.
- **OPS-05 (M)** Prometheus metrics:
  - instances (started, finished, aborted) per deployment and tenant;
  - incidents;
  - correlations;
  - commands and response times;
  - timer delay;
  - storage (LONG-14);

  Today's alerts are switched over.
- **OPS-06 (M)** Cost attribution: the cost-calculator uses pod names in the namespace `process-engine` and the metric from TASK-12. The smart-service engine is not attributed today; after the merge it is included.
- **OPS-07 (S)** Structured logging with instance, run, deployment and element ID, plus tracing along the path engine → connector → answer.
- **OPS-08 (M)** canary and snowflake-canary check the Automation-Engine end to end.

---

## 20. Migration from Camunda

- **MIG-01 (M)** All deployments are transferred from the stored deployment models, without users having to redeploy. Process models go into the namespace `processes`, smart-service designs, releases and instances into the namespace `smart-services`. The `delete_info` of the smart-service modules, today URLs to process-sync with `wardened:` business keys, are rewritten to internal references. Models are created with version 1, and the model–deployment assignment is reconstructed via name and tenant where it is unambiguous.
- **MIG-02 (M)** Running instances are **restarted** after the switchover (decided), with the same start parameters.
  - The previous business key serves as instance ID if it is unique per tenant; this mainly concerns smart-service instances. Otherwise a new UUID is assigned.
  - `wardened:` prefixes are removed.
  - Process variables are lost, as with today's restart.
  - Users learn beforehand which instances are affected.
- **MIG-03 (M)** Deployments that exist only in Camunda are recorded: keys without `deplid_`, in prod 37 in the engine compared to 9 in the wrapper (vault: "Every tenant's deployments are only visible in the engine"). For each it is decided: migrate or discard.
- **MIG-04 (M)** Models are rewritten automatically to the new expression language:
  - JUEL conditions: remove `${...}`, resolve HTML entities. In the exports all 20 distinct JUEL conditions use only comparisons, `&&`, `||`, `!` and index access, i.e. a syntax that JavaScript understands directly. JUEL keywords (`eq`, `and`, `empty`) do not occur.
  - Mappings: `${var}` becomes the expression `var`, text with embedded `${...}` becomes a template literal.
  - Timer values from expressions (`${cycle}`, `${cron_0}`) become JS expressions.
  - `scriptFormat="groovy"` with `println` becomes JavaScript with `log`.
  - `camunda:type="external"` with topic becomes a task type.
  - `http-connector` becomes notification or HTTP call.

  Per model a report is produced: what was rewritten, what is not supported (`mail-send`, `R/P1D` in `timeDuration`) and what has to be checked manually.
- **MIG-05 (M)** The rewrite is tested against Camunda. Conditions and mappings run with representative variable assignments in both engines and must give the same result. The reason are differences in type conversion between JUEL and JavaScript, for example with `==` between number and string or with `null`.
- **MIG-06 (M)** For the gateways there is a rollout via an update of the process module, taking over local deployments and IO data.
  - The accumulated Camunda history is not taken over.
  - The old Postgres volumes are removed.
  - Parallel operation with old gateway versions is not intended; old versions and arm/v7 are irrelevant (decided).
- **MIG-07 (S)** Parallel operation in the cloud: new deployments go to the Automation-Engine, existing ones drain in Camunda. Since running instances are restarted anyway (MIG-02), a cut-over date is also sufficient.
- **MIG-08 (M)** Rights are taken over. The shares from the topics `processmodel`, `smart_service_releases` and `smart_service_instances` pass into the new topics (NS-05), same users, groups and rights: `processmodel` to `processes_models`, `smart_service_releases` to `smart-services_deployments`, `smart_service_instances` to `smart-services_instances`. Smart-service designs have no topic today, they are visible only to the owner (`smart-service-repository/pkg/controller/designs.go` filters by user ID). They receive the default permissions of `smart-services_models`. Deployments and instances in the namespace `processes` receive the default permissions, so today only the owner.
- **MIG-09 (M)** Owners are taken over from today's data:
  - models: field `owner` in the process-model-repository;
  - deployments: `owner` in process-deployment, or the Camunda `tenantId` for deployments that exist only in Camunda (MIG-03);
  - smart-service designs, releases and instances: their user ID or their `creator`.

  In prod there are releases with an empty `creator` and with `placeholder-for-invalid-data` (vault: "A release creator can be empty or a placeholder"). Such resources get no invented owner. They are listed and assigned by hand or deleted. If a taken-over owner does not have administrate in permissions-v2, it is set (SEC-10).
---

## 21. Further components that are affected

| Component | How affected |
|---|---|
| **web-ui** | monitor, deployments, process models, widgets (switch, range slider, process state, charts, incident list, scheduler), fog views, Ladon admin with hard-coded paths; widgets `data-table` and `device-status` create hidden deployments (`source=generated`). New: switchover to the new API (API-04), namespaces, update dialog with strategy (DEP-04), model versions (MOD-05), live and historical debugging (section 13), retention settings (HIST-04); the web-ui implements the API itself, based on the OpenAPI (API-07), without its own client library |
| **Process designer in the web-ui** | today writes the Camunda dialect via `camunda-bpmn-moddle` with the full palette; in future only the supported set (BPMN-18), only JavaScript (EXPR-01), task types instead of topics, notification and HTTP call as types of their own. One designer for all namespaces, configured via the namespace (NS-09), instead of process and smart-service designers |
| **dashboard** | stores widget types with a process reference |
| **canary, snowflake-canary** | external API consumers, monitoring; switch to the Go client (CLI-08) |
| **cost-calculator, opencost-wrapper** | pod names and metric labels |
| **models** (`models/go`) | shared deployment model; to be clarified with CLI-01 whether the types of the engine live there or in the engine repo |
| **platform-connector-lib** | `task_info` with process IDs in the command envelope |
| **device-command, live-data-worker** | import packages from `external-task-worker/lib`; the packages move to a stable place (live-data-worker is not deployed) |
| **smart-service-module-worker-lib** | dropped with the integration; check whether other repos import it |
| **timescale-wrapper, timescale-rule-manager** | source for the reconstruction of event evaluations (EVT-10) and the current value (DBG-18); their retention limits event debugging |
| **analytics-flow-engine, flow-parser, import-deploy, serving, device-manager, device-selection** | targets of the integrated smart-service modules; their clients move into the engine |
| **mgw-device-cloud-connector** | stays as the general MQTT relay of the gateway for `processes/...`; upstream QoS 0 and buffer only in memory must be changed for SYNC-02 |
| **mgw-module-repository** (`process/Modfile.yaml`) | gateway module with 6 containers plus 2 Postgres today; in future engine (with synchronisation, task types and IO) and event-worker, without database containers of their own |
| **process-sync, mgw-process-sync-client** | merge into the engine (section 17); repos are archived |
| **user-management** | today calls process-scheduler on deletion, otherwise Kafka `user`; in future the dry-run check and delete call of the engine (SEC-14) |
| **notifier, developer-notifications** | target of notifications and incident reports |
| **Kong, Ladon** | one route for the new API plus `deprecated/`; today's routes `/process/engine`, `/process/deployment`, `/process/repository`, `/process/scheduler`, `/process/incidents`, `/process-io`, `/process-sync`, `/process-fog-deployment`, `/smart-services/repository` redirect to `deprecated/` (API-08); WebSocket upgrade for DBG-01; dead routes `/api-aggregator`, `/eventmanager` |
| **Kafka topics** | `process-deployment-done` stays or is replaced; `deployments_v3`, `camunda_incident` and probably `processmodel` are legacy |
| **ArgoCD** | apps `process-engine-prod`, `smart-service-engine-prod` |
| **api-aggregator** | switch off |

---

## 22. To be checked during implementation

There are currently no open decisions. The following points have not been checked:

- **Owner protection in permissions-v2 (SEC-10):** Can a withdrawal of the owner right in permissions-v2 be prevented, e.g. with a topic option that protects the owner? Or must the engine react to rights changes, for example via the Kafka topic from `PublishToKafkaTopic`, and restore the right? Devices and hubs would also benefit from this.

- **Timescale retention:** To which tables does timescale-rule-manager apply retention rules (e.g. `retention-one-year`)? This limits how far the reconstruction of event evaluations reaches back (EVT-12).
- **Import data:** Does import data land in Timescale? This decides whether import events can be reconstructed (EVT-12).
- **Fog device data:** Do the device events forwarded by the gateway arrive completely in Timescale? This includes the mapping of local IDs to cloud IDs (EVT-13).
- **Same evaluation in reconstruction and operation:** Does the evaluation on Timescale values give the same result as on the Kafka messages? Paths, conversion and timestamps are to be checked (EVT-10).
- **Further users of `smart-service-module-worker-lib`:** Do they exist outside the eight workers?

## Notes on the sources

- **Exported models** (`~/Downloads/bpmn`, 63 files): possibly outdated. Whether the models with `mail-send` and Groovy are still deployed is open. Deployed versions (`xml_deployed`) and smart-service releases were not evaluated.
- **process-scheduler**: The local checkout dates from 2026-08-27 and lacks the prod tag `v1.0.0`. The statements are based on HEAD.
- **event-deployment**: The local branch `dev` is behind prod (`v0.2.0`). `v0.2.0` was read.
- **process-sync**: Checked out is the unmerged branch `feat/platform-source-of-truth`. Prod runs on `v1.0.2`.
- **mgw-process-module**: The repo is outdated (v1.3.1). Authoritative is `mgw-module-repository/main/process/Modfile.yaml` (v1.5.4).
- **Vault notes that contradict the code**:
  - Process-Deployment in the private and in the shared vault: there it says the deployment command runs via `deployments_v3`. In fact it runs via HTTP; Kafka is switched off (`DEPLOYMENT_TOPIC="-"`, `DISABLE_KAFKA=true`).
  - "Creating a process deployment through the api": there it says the engine learns of the deployment asynchronously. In fact the wrapper deploys synchronously within the request.
