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

-- change stream of instances (SYNC-03): every write of an instance adds a row in the same
-- commit. On Postgres the sequence follows the order of inserts, not of commits; readers that
-- need every row re-read a window, live debugging only takes them as wake-up calls.
CREATE TABLE changes (
    seq          {{IDENTITY}},
    instance_id  TEXT    NOT NULL,
    namespace    TEXT    NOT NULL,
    activity_seq BIGINT  NOT NULL,
    deleted      INTEGER NOT NULL DEFAULT 0,
    ts           BIGINT  NOT NULL
);
CREATE INDEX changes_ts ON changes (ts);

-- full states of instances in intervals of the activity log; a step of the history is the
-- latest of them plus the activities after it (DBG-14)
CREATE TABLE history_snapshots (
    instance_id TEXT    NOT NULL REFERENCES instances (id) ON DELETE CASCADE,
    seq         BIGINT  NOT NULL,
    run         INTEGER NOT NULL,
    ts          BIGINT  NOT NULL,
    state       TEXT    NOT NULL,
    PRIMARY KEY (instance_id, seq)
);

ALTER TABLE instances ADD COLUMN snapshot_seq BIGINT NOT NULL DEFAULT 0;
ALTER TABLE instances ADD COLUMN snapshot_at BIGINT NOT NULL DEFAULT 0;
