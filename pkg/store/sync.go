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
)

// Applied is a desired resource of the cloud as a gateway applied it (SYNC-13).
type Applied struct {
	Kind    string
	Id      string
	Version int64
	Error   string
	At      time.Time
}

// PositionUpAcked is the position of the change stream of a gateway the cloud has taken.
const PositionUpAcked = "up_acked"

// unsynced excludes instances with changes the cloud has not taken; without a position, on
// the cloud, it excludes nothing. Retention and budget keep them, so that the cloud learns how
// they ended (LONG-07, LONG-10).
const unsynced = "id NOT IN (SELECT c.instance_id FROM changes c JOIN sync_positions p ON p.name = '" + PositionUpAcked + "' AND c.seq > p.position)"

// InitSyncPosition sets a position to 0 unless it is set.
func (this *Tx) InitSyncPosition(name string) error {
	_, err := this.exec("INSERT INTO sync_positions (name, position) VALUES (?, 0) ON CONFLICT (name) DO NOTHING", name)
	return err
}

// PruneAppliedInstances forgets instances applied without error that are gone; the cloud
// forgets them when they end, so that neither side grows with every start.
func (this *Tx) PruneAppliedInstances() (int64, error) {
	result, err := this.exec("DELETE FROM sync_applied WHERE kind = 'instance' AND error = '' AND id NOT IN (SELECT id FROM instances)")
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// SyncPosition reads a position of the synchronisation, 0 if it was never set.
func (this *Tx) SyncPosition(name string) (result int64, err error) {
	err = this.queryRow("SELECT COALESCE((SELECT position FROM sync_positions WHERE name = ?), 0)", name).Scan(&result)
	return result, err
}

func (this *Tx) SetSyncPosition(name string, position int64) error {
	_, err := this.exec("INSERT INTO sync_positions (name, position) VALUES (?, ?) ON CONFLICT (name) DO UPDATE SET position = excluded.position", name, position)
	return err
}

// GetApplied reads the applied version of a resource; found is false if none was applied.
func (this *Tx) GetApplied(kind string, id string) (result Applied, found bool, err error) {
	var at int64
	err = this.queryRow("SELECT kind, id, version, error, applied_at FROM sync_applied WHERE kind = ? AND id = ?", kind, id).Scan(&result.Kind, &result.Id, &result.Version, &result.Error, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return result, false, nil
	}
	result.At = fromMillis(at)
	return result, err == nil, err
}

func (this *Tx) SetApplied(a Applied) error {
	_, err := this.exec("INSERT INTO sync_applied (kind, id, version, error, applied_at) VALUES (?, ?, ?, ?, ?) "+
		"ON CONFLICT (kind, id) DO UPDATE SET version = excluded.version, error = excluded.error, applied_at = excluded.applied_at",
		a.Kind, a.Id, a.Version, a.Error, millis(a.At))
	return err
}

func (this *Tx) ListApplied() (result []Applied, err error) {
	rows, err := this.query("SELECT kind, id, version, error, applied_at FROM sync_applied ORDER BY kind, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a Applied
		var at int64
		if err = rows.Scan(&a.Kind, &a.Id, &a.Version, &a.Error, &at); err != nil {
			return nil, err
		}
		a.At = fromMillis(at)
		result = append(result, a)
	}
	return result, rows.Err()
}

// OldestChangeSeq is the sequence of the oldest entry of the change stream, 0 if it is empty;
// a position before it cannot be continued, the entries in between are purged.
func (this *Tx) OldestChangeSeq() (result int64, err error) {
	err = this.queryRow("SELECT COALESCE(MIN(seq), 0) FROM changes").Scan(&result)
	return result, err
}

// InstanceIds lists the ids of all instances, for a snapshot of a gateway (LONG-10).
func (this *Tx) InstanceIds() (result []string, err error) {
	rows, err := this.query("SELECT id FROM instances ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}
