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
	"fmt"
	"strings"
)

// part of a JUEL composite: literal text or the body of ${...} / #{...}
type part struct {
	text string
	expr bool
}

// hasExpression reports whether text contains a JUEL expression.
func hasExpression(text string) bool {
	return strings.Contains(text, "${") || strings.Contains(text, "#{")
}

// splitComposite splits text into literal parts and expressions; braces and quotes inside an
// expression are respected, so that "${a == '}'}" stays one expression.
func splitComposite(text string) ([]part, error) {
	var result []part
	var literal strings.Builder
	for i := 0; i < len(text); i++ {
		if (text[i] == '$' || text[i] == '#') && i+1 < len(text) && text[i+1] == '{' {
			end, err := expressionEnd(text, i+2)
			if err != nil {
				return nil, err
			}
			if literal.Len() > 0 {
				result = append(result, part{text: literal.String()})
				literal.Reset()
			}
			result = append(result, part{text: text[i+2 : end], expr: true})
			i = end
			continue
		}
		literal.WriteByte(text[i])
	}
	if literal.Len() > 0 {
		result = append(result, part{text: literal.String()})
	}
	return result, nil
}

// expressionEnd returns the index of the brace that closes an expression starting at start
func expressionEnd(text string, start int) (int, error) {
	depth := 0
	var quote byte
	for i := start; i < len(text); i++ {
		c := text[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '{':
			depth++
		case c == '}':
			if depth == 0 {
				return i, nil
			}
			depth--
		}
	}
	return 0, errors.New("unterminated expression")
}

// word operators of JUEL and their JavaScript form
var wordOperators = map[string]string{
	"and": "&&", "or": "||", "not": "!", "eq": "==", "ne": "!=", "lt": "<", "gt": ">", "le": "<=", "ge": ">=", "div": "/", "mod": "%",
}

// emptyCheck is the JavaScript form of the JUEL operator empty: true for null, an empty text
// and an empty list or map, false for everything else. It is written out in the model instead
// of calling a function of the engine, so that an older engine on a gateway runs it as well.
const emptyCheck = `((v) => v == null || v === "" || (typeof v === "object" && Object.keys(v).length === 0))`

// jsExpression translates the body of a JUEL expression. The expressions in use are
// comparisons, &&, ||, ! and index access, which JavaScript reads the same way; word
// operators are replaced outside string literals. empty, which has no counterpart, applies
// emptyCheck to its operand.
func jsExpression(juel string) (string, error) {
	var result strings.Builder
	var quote byte
	for i := 0; i < len(juel); i++ {
		c := juel[i]
		if quote != 0 {
			result.WriteByte(c)
			if c == '\\' && i+1 < len(juel) {
				i++
				result.WriteByte(juel[i])
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			result.WriteByte(c)
			continue
		}
		if isIdentStart(c) {
			j := i
			for j < len(juel) && isIdentPart(juel[j]) {
				j++
			}
			word := juel[i:j]
			previous := strings.TrimRight(result.String(), " \t\r\n")
			property := strings.HasSuffix(previous, ".")
			switch {
			case word == "empty" && !property:
				end, err := operandEnd(juel, j)
				if err != nil {
					return "", fmt.Errorf("the operand of the JUEL operator empty: %w", err)
				}
				operand, err := jsExpression(juel[j:end])
				if err != nil {
					return "", err
				}
				result.WriteString(emptyCheck + "(" + operand + ")")
				j = end
			case wordOperators[word] != "" && !property:
				result.WriteString(wordOperators[word])
			default:
				result.WriteString(word)
			}
			i = j - 1
			continue
		}
		result.WriteByte(c)
	}
	if quote != 0 {
		return "", errors.New("unterminated string literal")
	}
	return strings.TrimSpace(result.String()), nil
}

// operandEnd returns the end of the operand of a prefix operator that starts at start: a value
// with its property accesses, index accesses and calls, e.g. a.b['c'].d(1), a parenthesized
// expression or a literal. A further prefix operator is not supported as operand.
func operandEnd(juel string, start int) (int, error) {
	i := start
	for i < len(juel) && (juel[i] == ' ' || juel[i] == '\t' || juel[i] == '\r' || juel[i] == '\n') {
		i++
	}
	if i == len(juel) {
		return 0, errors.New("missing")
	}
	switch c := juel[i]; {
	case c == '(':
		end, err := closing(juel, i)
		if err != nil {
			return 0, err
		}
		i = end + 1
	case c == '\'' || c == '"':
		end, err := closing(juel, i)
		if err != nil {
			return 0, err
		}
		i = end + 1
	case c >= '0' && c <= '9':
		for i < len(juel) && (juel[i] == '.' || (juel[i] >= '0' && juel[i] <= '9')) {
			i++
		}
		return i, nil
	case isIdentStart(c):
		j := i
		for j < len(juel) && isIdentPart(juel[j]) {
			j++
		}
		if word := juel[i:j]; word == "not" || word == "empty" {
			return 0, fmt.Errorf("%v as operand is not supported", word)
		}
		i = j
	default:
		return 0, fmt.Errorf("%q as operand is not supported", string(c))
	}
	for i < len(juel) {
		switch juel[i] {
		case '.':
			j := i + 1
			for j < len(juel) && isIdentPart(juel[j]) {
				j++
			}
			if j == i+1 {
				return 0, errors.New("a property name is missing after .")
			}
			i = j
		case '[', '(':
			end, err := closing(juel, i)
			if err != nil {
				return 0, err
			}
			i = end + 1
		default:
			return i, nil
		}
	}
	return i, nil
}

// closing returns the index of the bracket or quote that closes the one at open; brackets
// inside string literals and nested brackets are skipped
func closing(text string, open int) (int, error) {
	if q := text[open]; q == '\'' || q == '"' {
		for i := open + 1; i < len(text); i++ {
			if text[i] == '\\' {
				i++
			} else if text[i] == q {
				return i, nil
			}
		}
		return 0, errors.New("unterminated string literal")
	}
	depth := 0
	var quote byte
	for i := open; i < len(text); i++ {
		c := text[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return 0, errors.New("unterminated bracket")
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// toScript turns a text with JUEL expressions into a JavaScript expression with the same
// result: a single expression keeps its type, a composite becomes a template literal in which
// null is empty, as in JUEL.
func toScript(text string) (string, error) {
	parts, err := splitComposite(text)
	if err != nil {
		return "", err
	}
	if len(parts) == 1 && parts[0].expr {
		return jsExpression(parts[0].text)
	}
	var b strings.Builder
	b.WriteByte('`')
	for _, p := range parts {
		if !p.expr {
			b.WriteString(escapeTemplate(p.text))
			continue
		}
		js, err := jsExpression(p.text)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "${(%v) ?? \"\"}", js)
	}
	b.WriteByte('`')
	return b.String(), nil
}

func escapeTemplate(text string) string {
	return strings.NewReplacer("\\", "\\\\", "`", "\\`", "${", "\\${").Replace(text)
}
