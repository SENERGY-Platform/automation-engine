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

-- the key with which the notifier replaces a notification of the user instead of adding one, empty
-- for none, and whether it is delivered without sound (BPMN-09). The defaults are the behavior of
-- every notification before, so that the inserts of engines without the columns keep it while both
-- run side by side. The boolean is INTEGER 0/1 as everywhere in the schema.
ALTER TABLE notifications ADD COLUMN tag TEXT NOT NULL DEFAULT '';
ALTER TABLE notifications ADD COLUMN silent INTEGER NOT NULL DEFAULT 0;
