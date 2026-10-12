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

-- event descriptions of device events of the current deployment version (DEP-07); event
-- workers keep them in memory and read changes by revision instead of a cache signal
CREATE TABLE event_descriptions (
    id                TEXT PRIMARY KEY,
    deployment_id     TEXT    NOT NULL REFERENCES deployments (id) ON DELETE CASCADE,
    version           INTEGER NOT NULL,
    element_id        TEXT    NOT NULL,
    namespace         TEXT    NOT NULL,
    owner             TEXT    NOT NULL,
    message_name      TEXT    NOT NULL,
    device_id         TEXT    NOT NULL DEFAULT '',
    service_id        TEXT    NOT NULL DEFAULT '',
    device_group_id   TEXT    NOT NULL DEFAULT '',
    import_id         TEXT    NOT NULL DEFAULT '',
    topic             TEXT    NOT NULL,
    script            TEXT    NOT NULL,
    value_variable    TEXT    NOT NULL,
    variables         TEXT    NOT NULL DEFAULT '',
    qos               INTEGER NOT NULL DEFAULT 0,
    characteristic_id TEXT    NOT NULL DEFAULT '',
    function_id       TEXT    NOT NULL DEFAULT '',
    aspect_ids        TEXT    NOT NULL DEFAULT '',
    path              TEXT    NOT NULL DEFAULT '',
    service           TEXT    NOT NULL DEFAULT '',
    revision          BIGINT  NOT NULL
);
CREATE INDEX event_descriptions_deployment ON event_descriptions (deployment_id);
CREATE INDEX event_descriptions_revision ON event_descriptions (revision);
CREATE INDEX event_descriptions_group ON event_descriptions (device_group_id);

-- removed descriptions, so that workers drop them; purged after a while, workers reload fully
CREATE TABLE event_description_tombstones (
    id         TEXT   NOT NULL,
    revision   BIGINT NOT NULL,
    removed_at BIGINT NOT NULL
);
CREATE INDEX event_description_tombstones_revision ON event_description_tombstones (revision);

-- device groups the events of a deployment depend on, also when a group has no devices yet (DEP-08)
CREATE TABLE deployment_device_groups (
    deployment_id TEXT NOT NULL REFERENCES deployments (id) ON DELETE CASCADE,
    group_id      TEXT NOT NULL,
    PRIMARY KEY (deployment_id, group_id)
);
CREATE INDEX deployment_device_groups_group ON deployment_device_groups (group_id);

-- the row lock on a counter orders concurrent revisions by commit
CREATE TABLE counters (
    name  TEXT PRIMARY KEY,
    value BIGINT NOT NULL
);
INSERT INTO counters (name, value) VALUES ('event_descriptions', 0);
