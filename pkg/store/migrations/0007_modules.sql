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

-- resources an instance created through a task type (SMS-12); they belong to the instance,
-- for maintenance procedures to the instance the procedure belongs to
CREATE TABLE modules (
    id          TEXT PRIMARY KEY,
    namespace   TEXT    NOT NULL,
    instance_id TEXT    NOT NULL REFERENCES instances (id) ON DELETE CASCADE,
    created_by  TEXT    NOT NULL DEFAULT '',
    element_id  TEXT    NOT NULL DEFAULT '',
    owner       TEXT    NOT NULL,
    module_type TEXT    NOT NULL,
    data        TEXT    NOT NULL,
    delete_info TEXT    NOT NULL DEFAULT '',
    error       TEXT    NOT NULL DEFAULT '',
    checked_at  BIGINT  NOT NULL DEFAULT 0,
    created_at  BIGINT  NOT NULL,
    updated_at  BIGINT  NOT NULL
);
CREATE INDEX modules_instance ON modules (instance_id);
CREATE INDEX modules_namespace_owner ON modules (namespace, owner);
CREATE INDEX modules_checked ON modules (module_type, checked_at);

CREATE TABLE module_keys (
    module_id TEXT NOT NULL REFERENCES modules (id) ON DELETE CASCADE,
    module_key TEXT NOT NULL,
    PRIMARY KEY (module_id, module_key)
);
CREATE INDEX module_keys_key ON module_keys (module_key);

-- removal of the resources of deleted modules, retried until it succeeds (SMS-06)
CREATE TABLE module_removals (
    module_id   TEXT PRIMARY KEY,
    namespace   TEXT    NOT NULL,
    owner       TEXT    NOT NULL,
    module_type TEXT    NOT NULL,
    delete_info TEXT    NOT NULL,
    attempts    INTEGER NOT NULL DEFAULT 0,
    next_at     BIGINT  NOT NULL,
    last_error  TEXT    NOT NULL DEFAULT '',
    created_at  BIGINT  NOT NULL
);
CREATE INDEX module_removals_next ON module_removals (next_at);

-- messages for an instance that arrived before it waits for them, e.g. the completion of a
-- deployment a module created (SMS-04); consumed when the instance subscribes
CREATE TABLE instance_messages (
    instance_id  TEXT   NOT NULL REFERENCES instances (id) ON DELETE CASCADE,
    message_name TEXT   NOT NULL,
    variables    TEXT   NOT NULL DEFAULT '',
    created_at   BIGINT NOT NULL,
    PRIMARY KEY (instance_id, message_name)
);

-- periodic checks of the watcher task type (TASK-27); a change starts a maintenance procedure
CREATE TABLE watches (
    id          TEXT PRIMARY KEY,
    instance_id TEXT    NOT NULL REFERENCES instances (id) ON DELETE CASCADE,
    namespace   TEXT    NOT NULL,
    owner       TEXT    NOT NULL,
    procedure_name TEXT NOT NULL,
    parameters  TEXT    NOT NULL DEFAULT '',
    request     TEXT    NOT NULL,
    hash_type   TEXT    NOT NULL,
    interval_ms BIGINT  NOT NULL,
    last_hash   TEXT    NOT NULL DEFAULT '',
    next_at     BIGINT  NOT NULL,
    last_error  TEXT    NOT NULL DEFAULT '',
    created_at  BIGINT  NOT NULL
);
CREATE INDEX watches_next ON watches (next_at);

-- maintenance procedures refer to the instance they belong to (SMS-13)
ALTER TABLE instances ADD COLUMN parent_id TEXT NOT NULL DEFAULT '';
CREATE INDEX instances_parent ON instances (parent_id);
-- deployments a module created refer to the instance that manages them (NS-08)
ALTER TABLE deployments ADD COLUMN managed_by TEXT NOT NULL DEFAULT '';
-- per element overrides of inputs, event variables and timers set at deploy time
ALTER TABLE deployment_versions ADD COLUMN parameters TEXT NOT NULL DEFAULT '';
