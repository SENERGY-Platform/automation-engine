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

-- device selection per element of a deployment version (DEP-05)
ALTER TABLE deployment_versions ADD COLUMN selections TEXT NOT NULL DEFAULT '';

-- sub-tasks of a device command, one per device and service (TASK-06); replaces memcached
CREATE TABLE command_subtasks (
    job_id     TEXT    NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    sub_id     TEXT    NOT NULL,
    position   INTEGER NOT NULL,
    command    TEXT    NOT NULL,
    task_info  TEXT    NOT NULL,
    has_result INTEGER NOT NULL DEFAULT 0,
    result     TEXT    NOT NULL DEFAULT '',
    tries      INTEGER NOT NULL DEFAULT 0,
    last_try   BIGINT  NOT NULL DEFAULT 0,
    PRIMARY KEY (job_id, sub_id)
);
