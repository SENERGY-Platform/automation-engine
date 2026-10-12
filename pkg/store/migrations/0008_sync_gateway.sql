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

-- the synchronisation with the cloud on a gateway (SYNC-01, SYNC-02): positions of the change
-- stream the cloud has acknowledged, and the versions of desired resources applied here
CREATE TABLE sync_positions (
    name     TEXT PRIMARY KEY,
    position BIGINT NOT NULL
);

CREATE TABLE sync_applied (
    kind       TEXT   NOT NULL,
    id         TEXT   NOT NULL,
    version    BIGINT NOT NULL,
    error      TEXT   NOT NULL DEFAULT '',
    applied_at BIGINT NOT NULL,
    PRIMARY KEY (kind, id)
);
