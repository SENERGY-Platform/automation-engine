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

-- versions of smart service designs are releases, which users delete themselves (keep_versions,
-- DEP-04, SMS-03). As with cloud_only in migration 12, the seed does not overwrite a stored
-- namespace, so the shipped smart-services is marked once here; an admin may change it.
UPDATE namespaces SET config = '{"keep_versions":true,' || substr(config, 2)
WHERE name = 'smart-services' AND config LIKE '{"%' AND config NOT LIKE '%"keep_versions"%';
