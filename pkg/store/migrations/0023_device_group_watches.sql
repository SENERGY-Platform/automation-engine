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

-- watches of a device group (SNRGY-5132) are checked when device-repository announces a change of
-- the group or the deletion of one of its devices; watch_devices holds the members of the last
-- check, by which a deleted device finds the watches of the groups it was in. The default keeps
-- the inserts of engines without the column valid while both run side by side.
ALTER TABLE watches ADD COLUMN device_group_id TEXT NOT NULL DEFAULT '';
CREATE INDEX watches_device_group ON watches (device_group_id);
CREATE TABLE watch_devices (
    watch_id  TEXT NOT NULL REFERENCES watches (id) ON DELETE CASCADE,
    device_id TEXT NOT NULL,
    PRIMARY KEY (watch_id, device_id)
);
CREATE INDEX watch_devices_device ON watch_devices (device_id);
