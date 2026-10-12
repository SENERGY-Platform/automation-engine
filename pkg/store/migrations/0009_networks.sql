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

-- deployments and instances of gateways are resources of the engine with the network id
-- of their gateway (SYNC-09, API-05)
ALTER TABLE deployments ADD COLUMN network_id TEXT NOT NULL DEFAULT '';
ALTER TABLE instances ADD COLUMN network_id TEXT NOT NULL DEFAULT '';
CREATE INDEX deployments_network ON deployments (network_id);
CREATE INDEX instances_network ON instances (network_id);

-- the gateways the cloud synchronises with: last contact, versions, positions of the change
-- stream and storage (SYNC-12)
CREATE TABLE networks (
    id             TEXT PRIMARY KEY,
    engine_version TEXT    NOT NULL DEFAULT '',
    protocol       INTEGER NOT NULL DEFAULT 0,
    compatible     INTEGER NOT NULL DEFAULT 1,
    started_at     BIGINT  NOT NULL DEFAULT 0,
    last_contact   BIGINT  NOT NULL DEFAULT 0,
    latest         BIGINT  NOT NULL DEFAULT 0,
    acked          BIGINT  NOT NULL DEFAULT 0,
    storage        TEXT    NOT NULL DEFAULT '',
    created_at     BIGINT  NOT NULL
);

-- the desired state of a gateway per resource: what the cloud sent and what the gateway
-- applied (SYNC-01, SYNC-08, SYNC-13)
CREATE TABLE network_desired (
    network_id      TEXT    NOT NULL,
    kind            TEXT    NOT NULL,
    id              TEXT    NOT NULL,
    version         BIGINT  NOT NULL,
    deleted         INTEGER NOT NULL DEFAULT 0,
    spec            TEXT    NOT NULL DEFAULT '',
    applied_version BIGINT  NOT NULL DEFAULT 0,
    error           TEXT    NOT NULL DEFAULT '',
    applied_at      BIGINT  NOT NULL DEFAULT 0,
    sent_at         BIGINT  NOT NULL DEFAULT 0,
    updated_at      BIGINT  NOT NULL,
    PRIMARY KEY (network_id, kind, id)
);
CREATE INDEX network_desired_pending ON network_desired (sent_at);
