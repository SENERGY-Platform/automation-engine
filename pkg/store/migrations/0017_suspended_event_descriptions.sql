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

-- the condition of a description that ended the process with a fatal error is not evaluated again
-- (EXPR-03, crash brake); the column holds the error, empty is not suspended
ALTER TABLE event_descriptions ADD COLUMN suspended TEXT NOT NULL DEFAULT '';

-- a deployment whose start timer expression, or the first run of a new instance, ended the process
-- (EXPR-03): the crash rolled back the instance or the deployment itself, so there is nothing else
-- to stop. The deployment is disabled, also when it is created only later, e.g. when a gateway
-- applies it again; a later version or a reset of its restarts lifts it.
CREATE TABLE quarantined_deployments (
    deployment_id TEXT PRIMARY KEY,
    version       INTEGER NOT NULL,
    element_id    TEXT NOT NULL,
    error         TEXT NOT NULL,
    since         BIGINT NOT NULL
);
