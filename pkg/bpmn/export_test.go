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

package bpmn

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

// Reads exported production models, which are not part of the repository. Every model must
// parse into a graph, and every issue must be one the migration rewrite (MIG-04) resolves or
// reports, so that the supported element set covers what the models use.
// Run with AE_BPMN_EXPORT_DIR=<dir with *.bpmn>.
func TestExportedModelsParseWithOnlyMigrationIssues(t *testing.T) {
	dir := os.Getenv("AE_BPMN_EXPORT_DIR")
	if dir == "" {
		t.Skip("AE_BPMN_EXPORT_DIR not set")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.bpmn"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no models in %v: %v", dir, err)
	}
	// migration rewrites (MIG-04) or reports as unsupported or to be checked manually
	expected := regexp.MustCompile(`JUEL|camunda:type|camunda:topic|camunda:connector|script format "groovy"|needs a task type|` +
		`message name "\$\{|must have 6 or 7 fields|repeating interval .* not allowed as duration|condition needs language|Illegal return statement`)
	counts := map[string]int{}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		process, issues, err := ParseAndValidate(string(raw), Rules{})
		if err != nil {
			t.Errorf("%v: %v", filepath.Base(file), err)
			continue
		}
		if len(process.Elements) == 0 {
			t.Errorf("%v: no elements", filepath.Base(file))
		}
		for _, issue := range issues {
			if !expected.MatchString(issue.Message) {
				t.Errorf("%v: unexpected issue %v", filepath.Base(file), issue)
			}
			counts[regexp.MustCompile(`"[^"]*"`).ReplaceAllString(issue.Message, `"…"`)]++
		}
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("%5d %v", counts[k], k)
	}
}
