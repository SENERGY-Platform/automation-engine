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

// UsedBytes is the size of the data; free pages of SQLite are reused and not counted (LONG-07).
func (this *Tx) UsedBytes() (int64, error) {
	if this.dialect == Postgres {
		return this.DatabaseBytes()
	}
	var pages, free, pageSize int64
	if err := this.queryRow("PRAGMA page_count").Scan(&pages); err != nil {
		return 0, err
	}
	if err := this.queryRow("PRAGMA freelist_count").Scan(&free); err != nil {
		return 0, err
	}
	err := this.queryRow("PRAGMA page_size").Scan(&pageSize)
	return (pages - free) * pageSize, err
}

// TrimOldestHistory removes the oldest history beyond any retention, when the storage budget
// is near (LONG-07): finished instances first, then the oldest activity entries and
// incidents of all instances. Runtime state stays; instances with modules stay, as with the
// retention. It returns the number of removed rows; 0 means there is no history left.
func (this *Tx) TrimOldestHistory(limit int) (int64, error) {
	var total int64
	instances := "id IN (SELECT id FROM instances WHERE state <> 'running' AND restart_at IS NULL " +
		"AND NOT EXISTS (SELECT 1 FROM modules m WHERE m.instance_id = instances.id) ORDER BY ended_at LIMIT ?)"
	if err := this.queueRemovals("instances", instances, max(limit/10, 1)); err != nil {
		return 0, err
	}
	statements := []struct {
		sql   string
		limit int
	}{
		{"DELETE FROM instances WHERE " + instances, max(limit/10, 1)},
		{"DELETE FROM activities WHERE (instance_id, seq) IN (SELECT instance_id, seq FROM activities ORDER BY ts LIMIT ?)", limit},
		{"DELETE FROM incidents WHERE id IN (SELECT id FROM incidents ORDER BY ts LIMIT ?)", max(limit/10, 1)},
	}
	for _, statement := range statements {
		result, err := this.exec(statement.sql, statement.limit)
		if err != nil {
			return total, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
	}
	if total == 0 {
		return 0, nil
	}
	// the truncation marker and the snapshots follow the activity log, as with the retention
	if _, err := this.exec("UPDATE instances SET activity_min_seq = COALESCE((SELECT MIN(a.seq) FROM activities a WHERE a.instance_id = instances.id), activity_seq + 1)"); err != nil {
		return total, err
	}
	_, err := this.exec("DELETE FROM history_snapshots WHERE seq < (SELECT i.activity_min_seq - 1 FROM instances i WHERE i.id = history_snapshots.instance_id)")
	return total, err
}
