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

package rewrite

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

// The exported models of the platform rewrite completely, except for what the engine does not
// offer: mail-send and conditions that were constant text in Camunda (MIG-04). The models are
// not in the repository; AE_BPMN_EXPORT_DIR names their directory.
func TestExportedModelsRewriteExceptForMailAndConstantConditions(t *testing.T) {
	dir := os.Getenv("AE_BPMN_EXPORT_DIR")
	if dir == "" {
		t.Skip("AE_BPMN_EXPORT_DIR not set")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.bpmn"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no models in %v: %v", dir, err)
	}
	expected := regexp.MustCompile(`connector mail-send|neither JUEL nor has a language`)
	counts := map[string]int{}
	out := os.Getenv("AE_BPMN_REWRITE_OUT")
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		result, report, err := Rewrite(string(raw))
		if err != nil {
			t.Errorf("%v: %v", filepath.Base(file), err)
			continue
		}
		if out != "" {
			_ = os.WriteFile(filepath.Join(out, filepath.Base(file)), []byte(result), 0o644)
		}
		for _, f := range report.Unsupported {
			if !expected.MatchString(f.Message) {
				t.Errorf("%v: unexpected %v: %v", filepath.Base(file), f.ElementId, f.Message)
			}
		}
		for kind, findings := range map[string][]Finding{"rewritten": report.Rewritten, "check": report.Check, "unsupported": report.Unsupported} {
			for _, f := range findings {
				counts[kind+": "+regexp.MustCompile(`"[^"]*"|[\w.]+_\w+|\$\{[^}]*\}`).ReplaceAllString(f.Message, "…")]++
			}
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
