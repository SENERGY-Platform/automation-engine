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
	"errors"
	"regexp"
	"strings"
)

var println = regexp.MustCompile(`^println\s*(\((.*)\)|(.*?))\s*;?$`)

// groovyToJS translates the Groovy scripts in use, which only log with println, into
// JavaScript with log(...). Double-quoted strings interpolate ${...} in Groovy and become
// template literals. Anything else is not translated.
func groovyToJS(src string) (string, error) {
	var lines []string
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			lines = append(lines, line)
			continue
		}
		m := println.FindStringSubmatch(trimmed)
		if m == nil {
			return "", errors.New("only println is translated from Groovy")
		}
		argument := m[2]
		if m[1] != "" && !strings.HasPrefix(m[1], "(") {
			argument = m[3]
		}
		js, err := groovyStrings(strings.TrimSpace(argument))
		if err != nil {
			return "", err
		}
		lines = append(lines, "log("+js+");")
	}
	return strings.Join(lines, "\n"), nil
}

// groovyStrings turns double-quoted strings with ${...} into template literals
func groovyStrings(expr string) (string, error) {
	var result strings.Builder
	for i := 0; i < len(expr); i++ {
		c := expr[i]
		if c != '"' && c != '\'' {
			result.WriteByte(c)
			continue
		}
		j := i + 1
		for j < len(expr) && expr[j] != c {
			if expr[j] == '\\' {
				j++
			}
			j++
		}
		if j >= len(expr) {
			return "", errors.New("unterminated string literal")
		}
		literal := expr[i+1 : j]
		if c == '"' && strings.Contains(literal, "${") {
			result.WriteString("`" + strings.ReplaceAll(literal, "`", "\\`") + "`")
		} else {
			result.WriteString(expr[i : j+1])
		}
		i = j
	}
	return result.String(), nil
}
