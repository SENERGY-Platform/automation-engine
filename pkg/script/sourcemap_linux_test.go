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

package script

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A sourceMappingURL comment does not make goja read the file it names: a pipe without writer
// would block forever, file:///dev/zero would fill the memory, both before the script runs.
func TestSourceMapCommentsAreNotFollowed(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "map")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	src := "1\n//# sourceMappingURL=file://" + fifo
	for name, run := range map[string]func() error{
		"run":   func() error { _, err := (&Sandbox{Timeout: time.Second}).Run(src, nil); return err },
		"check": func() error { return Check(src) },
		"eval": func() error {
			_, err := (&Sandbox{Timeout: time.Second}).Run("eval("+jsString(src)+")", nil)
			return err
		},
		"new function": func() error {
			_, err := (&Sandbox{Timeout: time.Second}).Run("new Function("+jsString(src)+")()", nil)
			return err
		},
		"fingerprint": func() error { Fingerprint(src); return nil },
	} {
		done := make(chan error, 1)
		go func() { done <- run() }()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%v: %v", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%v reads the source map", name)
		}
	}
}

func jsString(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, "\\", "\\\\"), "\n", "\\n") + "'"
}
