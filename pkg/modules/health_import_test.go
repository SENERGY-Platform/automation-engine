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
	"encoding/json"
	"testing"
)

// The status of an import instance as import-deploy v0.5.1 reports it, from Kubernetes and from
// Docker, judged by the health check (SNRGY-4539).
func TestImportProblemJudgesTheContainerOfAnImport(t *testing.T) {
	for name, c := range map[string]struct{ instance, problem string }{
		"no status, backend without one":    {`{"id":"i"}`, ""},
		"running deployment":                {`{"restart":true,"status":{"running":true}}`, ""},
		"restart unset means a deployment":  {`{"status":{"running":false,"transitioning":true}}`, "import i is not running"},
		"crash loop with condition message": {`{"restart":true,"status":{"running":false,"transitioning":true,"message":"Deployment does not have minimum availability."}}`, "import i is not running: Deployment does not have minimum availability."},
		"docker restarting":                 {`{"restart":true,"status":{"running":false,"transitioning":true,"message":"restarting"}}`, "import i is not running: restarting"},
		"deployment gone":                   {`{"restart":true,"status":{"message":"not deployed"}}`, "import i is not deployed"},
		"job running":                       {`{"restart":false,"status":{"running":true}}`, ""},
		"job pending":                       {`{"restart":false,"status":{"transitioning":true,"message":"pending"}}`, ""},
		"job completed":                     {`{"restart":false,"status":{"message":"completed"}}`, ""},
		"docker job exited":                 {`{"restart":false,"status":{"message":"exited"}}`, ""},
		"job failed":                        {`{"restart":false,"status":{"message":"failed"}}`, "import i failed"},
		"job failed with condition message": {`{"restart":false,"status":{"message":"Job has reached the specified backoff limit"}}`, "import i failed: Job has reached the specified backoff limit"},
		"docker job dead":                   {`{"restart":false,"status":{"message":"dead"}}`, "import i failed: dead"},
		"job gone":                          {`{"restart":false,"status":{"message":"not deployed"}}`, "import i is not deployed"},
	} {
		t.Run(name, func(t *testing.T) {
			var instance importInstance
			if err := json.Unmarshal([]byte(c.instance), &instance); err != nil {
				t.Fatal(err)
			}
			if problem := importProblem("i", instance); problem != c.problem {
				t.Errorf("%q, want %q", problem, c.problem)
			}
		})
	}
}
