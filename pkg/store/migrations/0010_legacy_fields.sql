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
-- the widget that created a schedule; the web-ui finds the schedules of a widget by it
ALTER TABLE schedules ADD COLUMN created_by TEXT NOT NULL DEFAULT '';
CREATE INDEX schedules_created_by ON schedules (namespace, created_by);
