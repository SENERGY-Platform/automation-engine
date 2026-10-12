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

-- key-value store of process-io per user, optionally bound to a deployment or an instance and
-- deleted with it (IO-02, IO-03)
CREATE TABLE io_values (
    user_id       TEXT   NOT NULL,
    io_key        TEXT   NOT NULL,
    value         TEXT   NOT NULL,
    deployment_id TEXT REFERENCES deployments (id) ON DELETE CASCADE,
    instance_id   TEXT REFERENCES instances (id) ON DELETE CASCADE,
    updated_at    BIGINT NOT NULL,
    PRIMARY KEY (user_id, io_key)
);
CREATE INDEX io_values_deployment ON io_values (deployment_id);
CREATE INDEX io_values_instance ON io_values (instance_id);

-- time schedules of deployments (RUN-07)
CREATE TABLE schedules (
    id            TEXT PRIMARY KEY,
    namespace     TEXT    NOT NULL,
    deployment_id TEXT    NOT NULL REFERENCES deployments (id) ON DELETE CASCADE,
    owner         TEXT    NOT NULL,
    cron          TEXT    NOT NULL,
    alias         TEXT    NOT NULL DEFAULT '',
    disabled      INTEGER NOT NULL DEFAULT 0,
    next_due      BIGINT,
    created_at    BIGINT  NOT NULL,
    updated_at    BIGINT  NOT NULL
);
CREATE INDEX schedules_due ON schedules (disabled, next_due);
CREATE INDEX schedules_deployment ON schedules (deployment_id);
CREATE INDEX schedules_owner ON schedules (namespace, owner);
