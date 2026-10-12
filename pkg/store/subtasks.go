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
	"encoding/json"
	"time"
)

// Subtask is the command for one device and service of a device command (TASK-06); it
// replaces the sub-task state the external-task-worker kept in memcached.
type Subtask struct {
	JobId     string
	SubId     string
	Position  int
	Command   json.RawMessage
	TaskInfo  json.RawMessage
	HasResult bool
	Result    any
	Tries     int
	LastTry   time.Time
}

// EnsureSubtasks stores the sub-tasks of a job; results of existing ones are kept.
func (this *Tx) EnsureSubtasks(subtasks []Subtask) error {
	for _, s := range subtasks {
		_, err := this.exec("INSERT INTO command_subtasks (job_id, sub_id, position, command, task_info) VALUES (?, ?, ?, ?, ?) "+
			"ON CONFLICT (job_id, sub_id) DO UPDATE SET position = excluded.position, command = excluded.command, task_info = excluded.task_info",
			s.JobId, s.SubId, s.Position, string(s.Command), string(s.TaskInfo))
		if err != nil {
			return err
		}
	}
	return nil
}

func (this *Tx) ListSubtasks(jobId string) ([]Subtask, error) {
	rows, err := this.query("SELECT job_id, sub_id, position, command, task_info, has_result, result, tries, last_try FROM command_subtasks WHERE job_id = ? ORDER BY position", jobId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Subtask
	for rows.Next() {
		var s Subtask
		var command, taskInfo, result string
		var hasResult int
		var lastTry int64
		if err = rows.Scan(&s.JobId, &s.SubId, &s.Position, &command, &taskInfo, &hasResult, &result, &s.Tries, &lastTry); err != nil {
			return nil, err
		}
		s.Command = json.RawMessage(command)
		s.TaskInfo = json.RawMessage(taskInfo)
		s.HasResult = hasResult == 1
		s.Result = fromJson[any](result)
		s.LastTry = fromMillis(lastTry)
		list = append(list, s)
	}
	return list, rows.Err()
}

// SetSubtaskResult stores the result of a sub-task; false if the sub-task does not exist,
// e.g. because its job has already been completed.
func (this *Tx) SetSubtaskResult(jobId string, subId string, value any) (bool, error) {
	result, err := this.exec("UPDATE command_subtasks SET has_result = 1, result = ? WHERE job_id = ? AND sub_id = ?", toJson(value), jobId, subId)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (this *Tx) CountSubtaskTry(jobId string, subId string, at time.Time) error {
	_, err := this.exec("UPDATE command_subtasks SET tries = tries + 1, last_try = ? WHERE job_id = ? AND sub_id = ?", millis(at), jobId, subId)
	return err
}

// JobExists reports whether a job still waits for its result.
func (this *Tx) JobExists(id string) (bool, error) {
	n, err := this.count("SELECT COUNT(*) FROM jobs WHERE id = ?", id)
	return n > 0, err
}
