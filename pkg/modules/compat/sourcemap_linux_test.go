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

package compat_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/SENERGY-Platform/automation-engine/pkg/modules/compat"
	enginescript "github.com/SENERGY-Platform/automation-engine/pkg/script"
)

// A module script neither follows a sourceMappingURL comment, which would block on a pipe, nor
// reaches goja's compiler when it is nested too deeply.
func TestModuleScriptsCompileSafely(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "map")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := scripts(nil, nil).Run(context.Background(), "user", compat.PreScriptPrefix,
			map[string]any{"prescript": "1\n//# sourceMappingURL=file://" + fifo}, nil, map[string]any{}, nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the module script reads the source map")
	}
	_, _, err := scripts(nil, nil).Run(context.Background(), "user", compat.PreScriptPrefix,
		map[string]any{"prescript": "1" + strings.Repeat(" + 1", 2*enginescript.MaxDepth)}, nil, map[string]any{}, nil)
	if !errors.Is(err, enginescript.ErrTooDeep) {
		t.Fatalf("%v", err)
	}
}
