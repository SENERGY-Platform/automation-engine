--
-- Copyright 2026 InfAI (CC SES)
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.
-- You may obtain a copy of the License at
--
--    http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software
-- distributed under the License is distributed on an "AS IS" BASIS,
-- WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
-- See the License for the specific language governing permissions and
-- limitations under the License.

-- shared by Postgres and SQLite: times are unix milliseconds, JSON is TEXT, booleans are INTEGER 0/1

CREATE TABLE models (
    id                  TEXT PRIMARY KEY,
    namespace           TEXT    NOT NULL,
    name                TEXT    NOT NULL,
    description         TEXT    NOT NULL,
    owner               TEXT    NOT NULL,
    version             INTEGER NOT NULL,
    published           INTEGER NOT NULL DEFAULT 0,
    publish_description TEXT    NOT NULL DEFAULT '',
    published_at        BIGINT,
    created_at          BIGINT  NOT NULL,
    updated_at          BIGINT  NOT NULL
);
CREATE INDEX models_namespace_owner ON models (namespace, owner);

CREATE TABLE model_versions (
    model_id   TEXT    NOT NULL REFERENCES models (id) ON DELETE CASCADE,
    version    INTEGER NOT NULL,
    xml        TEXT    NOT NULL,
    svg        TEXT    NOT NULL,
    issues     TEXT    NOT NULL,
    created_at BIGINT  NOT NULL,
    PRIMARY KEY (model_id, version)
);

CREATE TABLE deployments (
    id            TEXT PRIMARY KEY,
    namespace     TEXT    NOT NULL,
    name          TEXT    NOT NULL,
    description   TEXT    NOT NULL,
    owner         TEXT    NOT NULL,
    model_id      TEXT    NOT NULL DEFAULT '',
    model_version INTEGER NOT NULL DEFAULT 0,
    model_deleted INTEGER NOT NULL DEFAULT 0,
    version       INTEGER NOT NULL,
    source        TEXT    NOT NULL DEFAULT '',
    created_at    BIGINT  NOT NULL,
    updated_at    BIGINT  NOT NULL
);
CREATE INDEX deployments_namespace_owner ON deployments (namespace, owner);
CREATE INDEX deployments_model ON deployments (model_id);

CREATE TABLE deployment_versions (
    deployment_id   TEXT    NOT NULL REFERENCES deployments (id) ON DELETE CASCADE,
    version         INTEGER NOT NULL,
    model_version   INTEGER NOT NULL,
    xml             TEXT    NOT NULL,
    svg             TEXT    NOT NULL,
    update_strategy TEXT    NOT NULL,
    created_at      BIGINT  NOT NULL,
    PRIMARY KEY (deployment_id, version)
);

-- message and timer start events of the current deployment version
CREATE TABLE start_events (
    deployment_id TEXT    NOT NULL REFERENCES deployments (id) ON DELETE CASCADE,
    version       INTEGER NOT NULL,
    element_id    TEXT    NOT NULL,
    kind          TEXT    NOT NULL,
    message_name  TEXT    NOT NULL DEFAULT '',
    due_at        BIGINT,
    PRIMARY KEY (deployment_id, element_id)
);
CREATE INDEX start_events_message ON start_events (message_name);
CREATE INDEX start_events_due ON start_events (due_at);

CREATE TABLE instances (
    id                 TEXT PRIMARY KEY,
    namespace          TEXT    NOT NULL,
    owner              TEXT    NOT NULL,
    deployment_id      TEXT    NOT NULL REFERENCES deployments (id) ON DELETE CASCADE,
    deployment_name    TEXT    NOT NULL,
    deployment_version INTEGER NOT NULL,
    model_id           TEXT    NOT NULL DEFAULT '',
    model_version      INTEGER NOT NULL DEFAULT 0,
    run                INTEGER NOT NULL,
    state              TEXT    NOT NULL,
    end_reason         TEXT    NOT NULL DEFAULT '',
    start_variables    TEXT    NOT NULL,
    created_at         BIGINT  NOT NULL,
    started_at         BIGINT  NOT NULL,
    ended_at           BIGINT,
    last_activity_at   BIGINT  NOT NULL,
    activity_seq       BIGINT  NOT NULL DEFAULT 0,
    activity_min_seq   BIGINT  NOT NULL DEFAULT 1
);
CREATE INDEX instances_namespace_owner ON instances (namespace, owner);
CREATE INDEX instances_deployment_state ON instances (deployment_id, state);
CREATE INDEX instances_model ON instances (model_id);

CREATE TABLE runs (
    instance_id        TEXT    NOT NULL REFERENCES instances (id) ON DELETE CASCADE,
    run                INTEGER NOT NULL,
    deployment_version INTEGER NOT NULL,
    cause              TEXT    NOT NULL,
    state              TEXT    NOT NULL,
    end_reason         TEXT    NOT NULL DEFAULT '',
    started_at         BIGINT  NOT NULL,
    ended_at           BIGINT,
    PRIMARY KEY (instance_id, run)
);

-- runtime state is overwritten and deleted, never appended (LONG-02)
CREATE TABLE tokens (
    id          TEXT PRIMARY KEY,
    instance_id TEXT    NOT NULL REFERENCES instances (id) ON DELETE CASCADE,
    run         INTEGER NOT NULL,
    element_id  TEXT    NOT NULL,
    state       TEXT    NOT NULL,
    arrived_via TEXT    NOT NULL DEFAULT '',
    locals      TEXT    NOT NULL,
    created_at  BIGINT  NOT NULL
);
CREATE INDEX tokens_instance ON tokens (instance_id);

CREATE TABLE variables (
    instance_id TEXT   NOT NULL REFERENCES instances (id) ON DELETE CASCADE,
    name        TEXT   NOT NULL,
    value       TEXT   NOT NULL,
    updated_at  BIGINT NOT NULL,
    element_id  TEXT   NOT NULL DEFAULT '',
    PRIMARY KEY (instance_id, name)
);

CREATE TABLE subscriptions (
    id           TEXT PRIMARY KEY,
    instance_id  TEXT    NOT NULL REFERENCES instances (id) ON DELETE CASCADE,
    run          INTEGER NOT NULL,
    token_id     TEXT    NOT NULL,
    element_id   TEXT    NOT NULL,
    kind         TEXT    NOT NULL,
    message_name TEXT    NOT NULL DEFAULT '',
    due_at       BIGINT,
    group_id     TEXT    NOT NULL DEFAULT '',
    definition   TEXT    NOT NULL DEFAULT '',
    created_at   BIGINT  NOT NULL
);
CREATE INDEX subscriptions_instance ON subscriptions (instance_id);
CREATE INDEX subscriptions_message ON subscriptions (message_name);
CREATE INDEX subscriptions_due ON subscriptions (due_at);

CREATE TABLE jobs (
    id          TEXT PRIMARY KEY,
    instance_id TEXT    NOT NULL REFERENCES instances (id) ON DELETE CASCADE,
    run         INTEGER NOT NULL,
    token_id    TEXT    NOT NULL,
    element_id  TEXT    NOT NULL,
    task_type   TEXT    NOT NULL,
    input       TEXT    NOT NULL,
    state       TEXT    NOT NULL,
    attempt     INTEGER NOT NULL DEFAULT 0,
    retries     INTEGER NOT NULL,
    timeout_ms  BIGINT  NOT NULL,
    due_at      BIGINT  NOT NULL,
    lock_owner  TEXT    NOT NULL DEFAULT '',
    lock_until  BIGINT  NOT NULL DEFAULT 0,
    last_error  TEXT    NOT NULL DEFAULT '',
    created_at  BIGINT  NOT NULL
);
CREATE INDEX jobs_instance ON jobs (instance_id);
CREATE INDEX jobs_due ON jobs (state, due_at);
CREATE INDEX jobs_lock ON jobs (state, lock_until);

-- activity log, bounded per instance as ring buffer (HIST-05, LONG-03)
CREATE TABLE activities (
    instance_id  TEXT    NOT NULL REFERENCES instances (id) ON DELETE CASCADE,
    seq          BIGINT  NOT NULL,
    run          INTEGER NOT NULL,
    ts           BIGINT  NOT NULL,
    kind         TEXT    NOT NULL,
    element_id   TEXT    NOT NULL DEFAULT '',
    element_type TEXT    NOT NULL DEFAULT '',
    data         TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (instance_id, seq)
);

-- counters instead of rows per pass (LONG-04)
CREATE TABLE element_stats (
    instance_id TEXT    NOT NULL REFERENCES instances (id) ON DELETE CASCADE,
    run         INTEGER NOT NULL,
    element_id  TEXT    NOT NULL,
    count       BIGINT  NOT NULL,
    last_at     BIGINT  NOT NULL,
    last_error  TEXT    NOT NULL DEFAULT '',
    error_at    BIGINT,
    PRIMARY KEY (instance_id, run, element_id)
);

CREATE TABLE incidents (
    id                 TEXT PRIMARY KEY,
    namespace          TEXT    NOT NULL,
    owner              TEXT    NOT NULL,
    instance_id        TEXT    NOT NULL REFERENCES instances (id) ON DELETE CASCADE,
    run                INTEGER NOT NULL,
    deployment_id      TEXT    NOT NULL,
    deployment_version INTEGER NOT NULL,
    element_id         TEXT    NOT NULL DEFAULT '',
    element_name       TEXT    NOT NULL DEFAULT '',
    message            TEXT    NOT NULL,
    ts                 BIGINT  NOT NULL,
    variables          TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX incidents_namespace_owner ON incidents (namespace, owner);
CREATE INDEX incidents_instance ON incidents (instance_id);
CREATE INDEX incidents_deployment ON incidents (deployment_id);

-- idempotency of message correlation per target (EVT-05)
CREATE TABLE message_receipts (
    message_id  TEXT   NOT NULL,
    target      TEXT   NOT NULL,
    received_at BIGINT NOT NULL,
    PRIMARY KEY (message_id, target)
);
CREATE INDEX message_receipts_received ON message_receipts (received_at);
