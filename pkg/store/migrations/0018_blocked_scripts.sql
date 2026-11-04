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

-- scripts that ended the process, by the fingerprint of their token sequence (EXPR-03); a blocked
-- script is refused on save and deploy and not run. scanned: when the stored deployments were
-- searched for it, 0 not yet. Replicas reload the list when the counter moves.
CREATE TABLE blocked_scripts (
    fingerprint TEXT PRIMARY KEY,
    source      TEXT NOT NULL,
    reason      TEXT NOT NULL,
    origin      TEXT NOT NULL,
    created_at  BIGINT NOT NULL,
    scanned_at  BIGINT NOT NULL DEFAULT 0
);
INSERT INTO counters (name, value) VALUES ('blocked_scripts', 0);
