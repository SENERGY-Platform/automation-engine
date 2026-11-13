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

-- a maintenance procedure names the procedure it runs, so that a watch does not start a second run
-- of it beside a running one (SNRGY-5143); procedures of older engines keep the default
ALTER TABLE instances ADD COLUMN procedure_name TEXT NOT NULL DEFAULT '';
CREATE INDEX instances_parent_procedure ON instances (parent_id, procedure_name);
-- a run of a procedure owed to an instance: a watch fired while the procedure ran. Started once
-- no run of it is running; next_at is the end of a claim, 0 for at once. The hash of the
-- variables keeps different inputs apart.
CREATE TABLE maintenance_reruns (
    instance_id    TEXT   NOT NULL REFERENCES instances (id) ON DELETE CASCADE,
    procedure_name TEXT   NOT NULL,
    variables_hash TEXT   NOT NULL,
    variables      TEXT   NOT NULL,
    requested_at   BIGINT NOT NULL,
    next_at        BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (instance_id, procedure_name, variables_hash)
);
CREATE INDEX maintenance_reruns_next ON maintenance_reruns (next_at);
