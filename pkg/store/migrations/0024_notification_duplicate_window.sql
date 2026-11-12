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

-- the seconds within which the notifier drops a notification equal to one the user already has
-- (SNRGY-3911), 0 for none. The default is the window every notification had before, so that the
-- inserts of engines without the column keep their behavior while both run side by side.
ALTER TABLE notifications ADD COLUMN duplicate_window INTEGER NOT NULL DEFAULT 3600;
