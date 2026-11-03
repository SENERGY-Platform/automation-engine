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

-- the device groups the event descriptions of a deployment depend on, per version: the
-- descriptions of an older version stay while instances still run on it (DEP-04), and a
-- changed group derives them again for every version that has them (DEP-08)
CREATE TABLE deployment_device_groups_versioned (
    deployment_id TEXT    NOT NULL REFERENCES deployments (id) ON DELETE CASCADE,
    version       INTEGER NOT NULL,
    group_id      TEXT    NOT NULL,
    PRIMARY KEY (deployment_id, version, group_id)
);
INSERT INTO deployment_device_groups_versioned (deployment_id, version, group_id)
    SELECT g.deployment_id, d.version, g.group_id FROM deployment_device_groups g JOIN deployments d ON d.id = g.deployment_id;
DROP TABLE deployment_device_groups;
ALTER TABLE deployment_device_groups_versioned RENAME TO deployment_device_groups;
CREATE INDEX deployment_device_groups_group ON deployment_device_groups (group_id);

CREATE INDEX event_descriptions_version ON event_descriptions (deployment_id, version);
