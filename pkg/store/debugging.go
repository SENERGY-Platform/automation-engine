/*
 * Copyright 2026 InfAI (CC SES)
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *    http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package store

import (
	"database/sql"
	"errors"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/model"
)

// ChangesChannel is the Postgres channel that announces changes of instances on commit;
// the payload is the instance id.
const ChangesChannel = "automation_engine_changes"

// Change is an entry of the change stream (SYNC-03).
type Change struct {
	Seq         int64
	InstanceId  string
	Namespace   string
	ActivitySeq int64
	Deleted     bool
	Time        time.Time
}

// changed adds a write of an instance to the change stream; Postgres announces it on commit
func (this *Tx) changed(instanceId string, namespace string, activitySeq int64, deleted bool) error {
	_, err := this.exec("INSERT INTO changes (instance_id, namespace, activity_seq, deleted, ts) VALUES (?, ?, ?, ?, ?)",
		instanceId, namespace, activitySeq, boolInt(deleted), millis(time.Now()))
	if err != nil || this.dialect != Postgres {
		return err
	}
	_, err = this.exec("SELECT pg_notify(?, ?)", ChangesChannel, instanceId)
	return err
}

// ChangesAfter lists entries of the change stream after seq in order of their sequence.
func (this *Tx) ChangesAfter(seq int64, limit int) (result []Change, err error) {
	rows, err := this.query("SELECT seq, instance_id, namespace, activity_seq, deleted, ts FROM changes WHERE seq > ? ORDER BY seq LIMIT ?", seq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Change
		var deleted int
		var ts int64
		if err = rows.Scan(&c.Seq, &c.InstanceId, &c.Namespace, &c.ActivitySeq, &deleted, &ts); err != nil {
			return nil, err
		}
		c.Deleted = deleted == 1
		c.Time = fromMillis(ts)
		result = append(result, c)
	}
	return result, rows.Err()
}

// LatestChangeSeq is the sequence of the newest entry of the change stream, 0 if it is empty.
func (this *Tx) LatestChangeSeq() (result int64, err error) {
	err = this.queryRow("SELECT COALESCE(MAX(seq), 0) FROM changes").Scan(&result)
	return result, err
}

// PurgeChanges removes entries older than before, at most limit at a time (LONG-05).
func (this *Tx) PurgeChanges(before time.Time, limit int) (int64, error) {
	result, err := this.exec("DELETE FROM changes WHERE seq IN (SELECT seq FROM changes WHERE ts < ? ORDER BY seq LIMIT ?)", millis(before), limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// SaveSnapshot stores a full state of an instance at an activity sequence (DBG-14).
func (this *Tx) SaveSnapshot(instanceId string, seq int64, run int, at time.Time, state model.DebugState) error {
	_, err := this.exec("INSERT INTO history_snapshots (instance_id, seq, run, ts, state) VALUES (?, ?, ?, ?, ?) "+
		"ON CONFLICT (instance_id, seq) DO UPDATE SET run = excluded.run, ts = excluded.ts, state = excluded.state",
		instanceId, seq, run, millis(at), toJson(state))
	return err
}

// SnapshotAtOrBefore returns the latest snapshot with a sequence up to seq; false if there is none.
func (this *Tx) SnapshotAtOrBefore(instanceId string, seq int64) (result model.DebugState, found bool, err error) {
	var state string
	err = this.queryRow("SELECT state FROM history_snapshots WHERE instance_id = ? AND seq <= ? ORDER BY seq DESC LIMIT 1", instanceId, seq).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	return fromJson[model.DebugState](state), true, nil
}

// OldestSnapshotSeq is the sequence of the oldest snapshot of an instance; false if there is none.
func (this *Tx) OldestSnapshotSeq(instanceId string) (seq int64, found bool, err error) {
	var value sql.NullInt64
	err = this.queryRow("SELECT MIN(seq) FROM history_snapshots WHERE instance_id = ?", instanceId).Scan(&value)
	return value.Int64, value.Valid, err
}

// TrimSnapshots removes snapshots that cannot start a reconstruction anymore, because the
// activities after them were evicted, and keeps at most keep of the others.
func (this *Tx) TrimSnapshots(instanceId string, activityMinSeq int64, keep int) error {
	if _, err := this.exec("DELETE FROM history_snapshots WHERE instance_id = ? AND seq < ?", instanceId, activityMinSeq-1); err != nil {
		return err
	}
	_, err := this.exec("DELETE FROM history_snapshots WHERE instance_id = ? AND seq NOT IN (SELECT seq FROM history_snapshots WHERE instance_id = ? ORDER BY seq DESC LIMIT ?)",
		instanceId, instanceId, keep)
	return err
}

// ListJobs lists the jobs of an instance.
func (this *Tx) ListJobs(instanceId string) (result []Job, err error) {
	rows, err := this.query("SELECT "+jobColumns+" FROM jobs WHERE instance_id = ? ORDER BY created_at, id", instanceId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, j)
	}
	return result, rows.Err()
}

// ActivitySeqAt is the sequence of the last activity entry at or before a time; false if there is none.
func (this *Tx) ActivitySeqAt(instanceId string, at time.Time) (seq int64, found bool, err error) {
	var value sql.NullInt64
	err = this.queryRow("SELECT MAX(seq) FROM activities WHERE instance_id = ? AND ts <= ?", instanceId, millis(at)).Scan(&value)
	return value.Int64, value.Valid, err
}
