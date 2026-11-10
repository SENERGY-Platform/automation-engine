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

-- display labels a client gave for the start variables of an instance, e.g. the names of selected
-- devices, which smart-service instances show as value_label (SNRGY-5107); the default keeps the
-- inserts of engines without the column valid while both run side by side
ALTER TABLE instances ADD COLUMN start_value_labels TEXT NOT NULL DEFAULT '{}';
