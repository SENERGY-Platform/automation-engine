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
-- a deployment switched off keeps its versions, but nothing starts and nothing runs (DEP-12)
ALTER TABLE deployments ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0;

-- the result of the check of the resources a deployment version uses, e.g. a process model
-- edited after the version was deployed; a hint for its owner
CREATE TABLE resource_checks (
    deployment_id TEXT    NOT NULL REFERENCES deployments (id) ON DELETE CASCADE,
    version       INTEGER NOT NULL,
    changed       TEXT    NOT NULL DEFAULT '[]',
    -- import types have no change time; the hash of the first seen content is the baseline
    hashes        TEXT    NOT NULL DEFAULT '{}',
    checked_at    BIGINT  NOT NULL,
    PRIMARY KEY (deployment_id, version)
);
