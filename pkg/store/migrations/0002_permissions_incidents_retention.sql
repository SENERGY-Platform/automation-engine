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

-- namespaces are stored, the configuration only seeds missing ones (NS-02)
CREATE TABLE namespaces (
    name       TEXT PRIMARY KEY,
    config     TEXT   NOT NULL,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);

-- limits an admin sets per user (HIST-04)
CREATE TABLE user_settings (
    user_id            TEXT PRIMARY KEY,
    max_retention_days INTEGER NOT NULL DEFAULT 0
);

-- a resource is registered in permissions-v2 asynchronously; 0 means pending
ALTER TABLE models ADD COLUMN perm_registered INTEGER NOT NULL DEFAULT 0;
ALTER TABLE deployments ADD COLUMN perm_registered INTEGER NOT NULL DEFAULT 0;
ALTER TABLE instances ADD COLUMN perm_registered INTEGER NOT NULL DEFAULT 0;
CREATE INDEX models_perm ON models (perm_registered);
CREATE INDEX deployments_perm ON deployments (perm_registered);
CREATE INDEX instances_perm ON instances (perm_registered);

-- removals from permissions-v2, written in the transaction that deletes the resource
CREATE TABLE permission_removals (
    topic       TEXT    NOT NULL,
    resource_id TEXT    NOT NULL,
    attempts    INTEGER NOT NULL DEFAULT 0,
    due_at      BIGINT  NOT NULL,
    last_error  TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (topic, resource_id)
);
CREATE INDEX permission_removals_due ON permission_removals (due_at);

-- notifications to send after commit (INC-02)
CREATE TABLE notifications (
    id         TEXT PRIMARY KEY,
    user_id    TEXT    NOT NULL,
    title      TEXT    NOT NULL,
    message    TEXT    NOT NULL,
    topic      TEXT    NOT NULL,
    attempts   INTEGER NOT NULL DEFAULT 0,
    due_at     BIGINT  NOT NULL,
    last_error TEXT    NOT NULL DEFAULT '',
    created_at BIGINT  NOT NULL
);
CREATE INDEX notifications_due ON notifications (due_at);

ALTER TABLE deployments ADD COLUMN retention_days INTEGER NOT NULL DEFAULT 0;
-- restart protection per deployment (INC-04)
ALTER TABLE deployments ADD COLUMN faulty_since BIGINT;
ALTER TABLE deployments ADD COLUMN restart_window_start BIGINT NOT NULL DEFAULT 0;
ALTER TABLE deployments ADD COLUMN restart_count INTEGER NOT NULL DEFAULT 0;

ALTER TABLE deployment_versions ADD COLUMN incident_restart INTEGER NOT NULL DEFAULT 0;
ALTER TABLE deployment_versions ADD COLUMN incident_notify INTEGER NOT NULL DEFAULT 1;

ALTER TABLE instances ADD COLUMN restart_at BIGINT;
CREATE INDEX instances_restart ON instances (restart_at);
CREATE INDEX instances_ended ON instances (state, ended_at);
CREATE INDEX incidents_ts ON incidents (ts);
