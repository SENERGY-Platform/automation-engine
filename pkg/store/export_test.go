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

import "time"

// ClaimNotificationsSkipLockedForTest reads and claims due notifications in one statement, the
// alternative to DueNotifications and ClaimNotification that BenchmarkClaims measures
// (SNRGY-5134); Postgres only.
func ClaimNotificationsSkipLockedForTest(tx *Tx, now time.Time, until time.Time, limit int) ([]string, error) {
	rows, err := tx.query("UPDATE notifications SET due_at = ? WHERE id IN "+
		"(SELECT id FROM notifications WHERE due_at <= ? ORDER BY due_at LIMIT ? FOR UPDATE SKIP LOCKED) RETURNING id",
		millis(until), millis(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

// CountForTest counts the rows of a table; for tests only.
func CountForTest(tx *Tx, table string) (int, error) {
	return tx.count("SELECT COUNT(*) FROM " + table)
}

// ExecForTest runs a statement; for tests only.
func ExecForTest(tx *Tx, statement string) error {
	_, err := tx.exec(statement)
	return err
}

// MaxOpenConnsForTest returns the connection limit of the store; for tests only.
func MaxOpenConnsForTest(s *Store) int {
	return s.db.Stats().MaxOpenConnections
}

// SetIdsPerQueryForTest changes how many ids and versions one query of a list reads, so that a
// test covers several queries with a few rows; it returns the restore.
func SetIdsPerQueryForTest(n int) func() {
	ids, refs := idsPerQuery, versionRefsPerQuery
	idsPerQuery, versionRefsPerQuery = n, n
	return func() { idsPerQuery, versionRefsPerQuery = ids, refs }
}
