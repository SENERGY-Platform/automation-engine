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

package modules

import (
	"testing"
)

// Criteria whose aspect_ids is not a list fail the task, as in the watcher worker, instead of
// every later check at device-selection.
func TestWatchRejectsAspectIdsThatAreNotAList(t *testing.T) {
	for _, variable := range []string{"watcher.watch_devices_by_criteria", "watcher.watch_modified_devices_by_criteria"} {
		t.Run(variable, func(t *testing.T) {
			if _, err := saveWatch(t, WatcherSettings{}, watcherInputs(variable, `[{"function_id":"fid","aspect_ids":"aid"}]`)); err == nil {
				t.Error("expected an error")
			}
		})
	}
}
