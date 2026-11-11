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

package server

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Task executions are counted and timed per namespace and owner, so that the cost of task
// execution can be shared by user; the histogram of the durations keeps only the task type
// (SNRGY-3077).
func TestTaskMetricsAreSplitByOwner(t *testing.T) {
	m := NewMetrics(time.Hour)
	m.Task("device_command", "completed", "processes", "alice", 1500*time.Millisecond)
	m.Task("device_command", "failed", "processes", "alice", 500*time.Millisecond)
	m.Task("device_command", "completed", "smart-services", "bob", 250*time.Millisecond)
	recorder := httptest.NewRecorder()
	m.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body, _ := io.ReadAll(recorder.Body)
	for _, expected := range []string{
		`automation_engine_task_executions_total{namespace="processes",result="completed",task_type="device_command",user_id="alice"} 1`,
		`automation_engine_task_executions_total{namespace="processes",result="failed",task_type="device_command",user_id="alice"} 1`,
		`automation_engine_task_executions_total{namespace="smart-services",result="completed",task_type="device_command",user_id="bob"} 1`,
		`automation_engine_task_execution_time_seconds_total{namespace="processes",task_type="device_command",user_id="alice"} 2`,
		`automation_engine_task_execution_time_seconds_total{namespace="smart-services",task_type="device_command",user_id="bob"} 0.25`,
		`automation_engine_task_execution_seconds_count{task_type="device_command"} 3`,
	} {
		if !strings.Contains(string(body), expected) {
			t.Errorf("missing %v", expected)
		}
	}
	if strings.Contains(string(body), `automation_engine_task_execution_seconds_bucket{task_type="device_command",user_id`) {
		t.Errorf("the histogram has no owner label")
	}
}
