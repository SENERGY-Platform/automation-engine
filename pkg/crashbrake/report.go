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

package crashbrake

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"strings"
)

// reportLines is how much of the report a decision keeps: the error and the top of the stack.
const reportLines = 8

// readCrashReport returns whether the previous process ended with a Go fatal error or panic, the
// first lines of its report and the id of the goroutine that crashed. A signal dump such as
// SIGQUIT is not a crash.
func readCrashReport(path string) (bool, string, uint64) {
	content, err := os.ReadFile(path)
	if err != nil || !isFatalReport(content) {
		return false, "", 0
	}
	return true, firstLines(string(content), reportLines), crashingGoroutine(content)
}

func isFatalReport(report []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(report))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		for _, prefix := range []string{"panic:", "fatal error:", "runtime:", "runtime stack:"} {
			if strings.HasPrefix(line, prefix) {
				return true
			}
		}
		return false
	}
	return false
}

// crashingGoroutine reads the id from the first "goroutine N [running]:" line, else from the first
// "goroutine N [" line; zero means none, which names nothing.
func crashingGoroutine(report []byte) uint64 {
	scanner := bufio.NewScanner(bytes.NewReader(report))
	var first uint64
	for scanner.Scan() {
		rest, ok := strings.CutPrefix(scanner.Text(), "goroutine ")
		if !ok {
			continue
		}
		space := strings.IndexByte(rest, ' ')
		if space < 0 {
			continue
		}
		id, err := strconv.ParseUint(rest[:space], 10, 64)
		if err != nil {
			continue
		}
		if strings.Contains(rest, "[running]:") {
			return id
		}
		if first == 0 {
			first = id
		}
	}
	return first
}

// errorOf names the error of a report in one line. The runtime writes "fatal error: stack overflow"
// to stderr only; the crash output starts with the stack, where runtime.newstack marks an overflow.
func errorOf(report string) string {
	for _, line := range strings.Split(report, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "fatal error:") || strings.HasPrefix(line, "panic:") {
			return line
		}
	}
	if strings.Contains(report, "runtime.newstack") {
		return "fatal error: stack overflow"
	}
	return "fatal error"
}

func firstLines(text string, n int) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// readPending reads decisions an earlier start could not apply; a missing or unreadable file is
// none.
func readPending(path string) []Decision {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var result []Decision
	_ = json.Unmarshal(content, &result)
	return result
}

// writePending stores decisions to apply, or removes the file when there are none.
func writePending(path string, decisions []Decision) {
	if len(decisions) == 0 {
		_ = os.Remove(path)
		return
	}
	if content, err := json.Marshal(decisions); err == nil {
		_ = os.WriteFile(path, content, 0o644)
	}
}

func mergeDecisions(pending []Decision, fresh []Decision) []Decision {
	seen := map[Unit]bool{}
	var result []Decision
	for _, d := range append(append([]Decision{}, fresh...), pending...) {
		if !seen[d.Unit] {
			seen[d.Unit] = true
			result = append(result, d)
		}
	}
	return result
}
