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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/maphash"
	"math/rand/v2"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"
)

const crashing = `var o = {}; o.toString = String.prototype.trim; String(o)`

// inMode computes fingerprints in mode until the test ends
func inMode(t *testing.T, mode string) {
	t.Helper()
	if err := SetFingerprintMode(mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = SetFingerprintMode(FingerprintText) })
}

// The text of a copy without // comments, trailing whitespace and empty lines has one fingerprint.
func TestTextFingerprintIgnoresLineComments(t *testing.T) {
	inMode(t, FingerprintText)
	for _, src := range []string{
		crashing + " // a copy",
		"// a copy\n\n" + crashing,
		"// a copy\r\n" + crashing + "\t\r\n",
		crashing + "\u2028// a copy",
		"//no space after the slashes\n" + crashing,
	} {
		if Fingerprint(src) != Fingerprint(crashing) {
			t.Errorf("%q differs from the original", src)
		}
	}
	for _, src := range []string{
		// spacing, indentation and line ends inside the script count
		"var o={};o.toString=String.prototype.trim;String(o)",
		"var o = {};\n\to.toString = String.prototype.trim; String(o)",
		// a comment with a quote or a slash stays, e.g. with a URL
		crashing + " // see http://example.org",
		crashing + " // it's a copy",
	} {
		if Fingerprint(src) == Fingerprint(crashing) {
			t.Errorf("%q has the fingerprint of the original", src)
		}
	}
}

// textFingerprint gives what a line by line reference gives, also for random text of the
// characters that matter.
func TestTextFingerprintAgainstAReference(t *testing.T) {
	reference := func(src string) string {
		out := "text\n"
		for src != "" {
			end, next := lineEnd(src)
			line, terminator := src[:end], src[end:next]
			src = src[next:]
			for from := 0; ; {
				i := strings.Index(line[from:], "//")
				if i < 0 {
					break
				}
				i += from
				if (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') && !strings.ContainsAny(line[i+2:], "'\"`/") {
					line = strings.TrimRight(line[:i], " \t")
					if line == "" {
						terminator = ""
					}
					break
				}
				from = i + 1
			}
			out += line + terminator
		}
		out = "text\n" + strings.Trim(out[len("text\n"):], blankAround)
		sum := sha256.Sum256([]byte(out))
		return hex.EncodeToString(sum[:])
	}
	alphabet := []string{"a", " ", "\t", "/", "/", "/", "\"", "'", "`", "\n", "\r", "\u2028", "\u2029", "\u00e2", "x"}
	random := rand.New(rand.NewPCG(1, 2))
	for n := 0; n < 20000; n++ {
		var src strings.Builder
		for i := random.IntN(40); i > 0; i-- {
			src.WriteString(alphabet[random.IntN(len(alphabet))])
		}
		if got, expected := textFingerprint(src.String()), reference(src.String()); got != expected {
			t.Fatalf("%q", src.String())
		}
	}
}

// Copies of a script that differ only in formatting have one fingerprint of the syntax tree.
func TestTreeFingerprintIgnoresFormatting(t *testing.T) {
	inMode(t, FingerprintTree)
	for _, src := range []string{
		"var o={};o.toString=String.prototype.trim;String(o)",
		"// harmless\nvar   o = {};\n\n/* really */ o.toString =\tString.prototype.trim;\r\nString(o)   ",
		// without semicolons, inserted by the parser
		"var o = {}\no.toString = String.prototype.trim\nString(o)",
		// whitespace a lexer may not know: no-break space, byte order mark, line separator
		"var\u00a0o = {};\ufeffo.toString = String.prototype.trim;\u2028String(o)",
		// a source map comment, which is not followed to a file
		crashing + "\n//# sourceMappingURL=/dev/zero",
	} {
		if Fingerprint(src) != Fingerprint(crashing) {
			t.Errorf("%q differs from the original", src)
		}
	}
	if Fingerprint("value > 20 //\u2029"+crashing) != Fingerprint("value > 20\n"+crashing) {
		t.Error("a line comment does not end at a paragraph separator")
	}
	if Fingerprint("var f = function () { /* a */ return 1 }") != Fingerprint("var f=function(){return 1}") {
		t.Error("the source text of a function counts")
	}
}

// A change of the code, also inside a literal, makes another fingerprint, and so does code that
// could be taken for a comment: a crashing script must not get the fingerprint of a harmless one,
// which would block that everywhere.
func TestFingerprintKeepsCode(t *testing.T) {
	scripts := []string{
		crashing,
		`var p = {}; p.toString = String.prototype.trim; String(p)`,
		`var o = {}; o.toString = String.prototype.concat; String(o)`,
		`"a  b"`, `"a b"`, `'a b'`, "`a  ${x}  b`", "`a ${x} b`", `x = /a  b/g`, `x = /a b/g`, `typeof x`, `typeofx`,
		`x = 0x10`, `x = 16`,
		"value > 20",
		// a line comment ends at a line separator
		"value > 20 //\u2028" + crashing,
		// the slash after the condition of an if starts a regular expression
		"if (true) /\\//.test(''); " + crashing,
		"if(true)/\\",
		// // in a string, a regular expression, a template, after a comment with a quote
		`x = "http://example.org"`, `x = "http://example.org" + ` + crashing,
		`x = /[ //]/;`, `x = /[ //]/; ` + crashing,
		`x = /a\//;`, `x = /a\//; ` + crashing,
		"/* it's */ u = 'http://h';", "/* it's */ u = 'http://h'; " + crashing,
		"t = `a //b`;", "t = `a //b`; " + crashing,
		"x = a + ++b", "x = a++ + b",
		"a.b", "a[b]", "a + b", "a - b", "x++", "++x", "x = true", "x = false", "x = 1", "x = 2",
		"var ab, c", "var a, bc", "f(a, b)", "f(a)(b)",
		"a = b\n++c", "a = b++\nc",
		"function f() { return\nx }", "function f() { return x }",
		// a template inside a substitution, a comment inside a substitution
		"t = `a ${ `b ${c}` } d`", "t = `a ${ `b ${c} ` } d`", "t = `${0 /* `${ */}`",
		// syntax errors
		"x = (", "x = (;",
	}
	for _, mode := range []string{FingerprintText, FingerprintTree} {
		t.Run(mode, func(t *testing.T) {
			inMode(t, mode)
			seen := map[string]string{}
			for _, src := range scripts {
				f := Fingerprint(src)
				if other, ok := seen[f]; ok {
					t.Errorf("%q and %q have one fingerprint", src, other)
				}
				seen[f] = src
			}
		})
	}
}

// The fingerprint of a deep or wide script neither overflows the stack nor takes long: it is
// computed before a script runs, outside of the crash brake, and on every save of a model.
func TestFingerprintOfDeepScripts(t *testing.T) {
	defer debug.SetMaxStack(debug.SetMaxStack(64 << 20))
	for _, mode := range []string{FingerprintText, FingerprintTree} {
		t.Run(mode, func(t *testing.T) {
			inMode(t, mode)
			for name, src := range map[string]string{
				"chain":              strings.Repeat("a+", 500000) + "a",
				"templates":          "`${0 /*" + strings.Repeat("`${", 500000) + "*/}`",
				"nesting":            strings.Repeat("(", 500000) + "a" + strings.Repeat(")", 500000),
				"unclosed templates": strings.Repeat("`${", 500000),
				"comments":           strings.Repeat("a // b\n", 200000),
				// many // on one line, none a comment: each line is scanned once
				"slashes":        strings.Repeat("/", 2000000),
				"quoted slashes": strings.Repeat(" //'", 500000),
			} {
				start := time.Now()
				Fingerprint(src)
				if elapsed := time.Since(start); elapsed > 5*time.Second {
					t.Errorf("%v: %v", name, elapsed)
				}
			}
		})
	}
}

// The cache holds one fingerprint per script, and computes it again after it was cleared, also
// after the mode changed.
func TestFingerprintCache(t *testing.T) {
	inMode(t, FingerprintText)
	expected := textFingerprint(crashing)
	for i := 0; i < maxCached+10; i++ {
		Fingerprint("x = " + strings.Repeat("1", i%50) + "; " + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26)))
	}
	if n := fingerprints.count.Load(); n > maxCached {
		t.Errorf("%v cached fingerprints", n)
	}
	fingerprints.clear()
	if cached, ok := CachedFingerprint(crashing); ok {
		t.Errorf("cached before it was computed: %v", cached)
	}
	if Fingerprint(crashing) != expected || Fingerprint(crashing) != expected {
		t.Error("the cached fingerprint differs")
	}
	if cached, ok := CachedFingerprint(crashing); !ok || cached != expected {
		t.Errorf("%v %v", cached, ok)
	}
	inMode(t, FingerprintTree)
	if Fingerprint(crashing) != treeFingerprint(crashing) {
		t.Error("the fingerprint of the other mode is cached")
	}
	if err := SetFingerprintMode("other"); err == nil {
		t.Error("an unknown mode is refused")
	}
}

// A script deeper than MaxDepth is refused before goja's compiler recurses over it, which would
// overflow the stack and end the process, also when a model is only validated; a script just
// within the limit compiles.
func TestDeepScriptsAreRefused(t *testing.T) {
	defer debug.SetMaxStack(debug.SetMaxStack(64 << 20))
	deep := "level" + strings.Repeat(" + 1", 1000000)
	if err := Check(deep); !errors.Is(err, ErrTooDeep) {
		t.Fatalf("%v", err)
	}
	if _, err := NewSandbox().Run(deep, nil); !errors.Is(err, ErrTooDeep) {
		t.Fatalf("%v", err)
	}
	inMode(t, FingerprintTree)
	if Fingerprint(deep) == Fingerprint(deep+" + 1") {
		t.Error("a script too deep is identified by its source")
	}
	within := "var level = 1; level" + strings.Repeat(" + 1", MaxDepth/2)
	if result, err := NewSandbox().Run(within, nil); err != nil || fmt.Sprint(result.Value) != fmt.Sprint(MaxDepth/2+1) {
		t.Fatalf("%v %v", result.Value, err)
	}
}

// A script is parsed and compiled once: the program, or the error, is cached by its source, up to
// a sum of sources; a large script is compiled at every run.
func TestCompiledProgramsAreCached(t *testing.T) {
	first, err := Compile("value > 20")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := Compile("value > 20"); again != first {
		t.Error("compiled again")
	}
	if other, _ := Compile("value > 21"); other == first {
		t.Error("another script has the same program")
	}
	_, failed := Compile("value >")
	if _, again := Compile("value >"); failed == nil || again != failed {
		t.Errorf("the error is cached: %v %v", failed, again)
	}
	large := "var x = 1;" + strings.Repeat(" ", maxProgramSource)
	if a, _ := Compile(large); a == nil {
		t.Fatal("not compiled")
	} else if b, _ := Compile(large); a == b {
		t.Error("a large script is cached")
	}
	for i := 0; i < 2*maxProgramSources/(maxProgramSource/2); i++ {
		if _, err = Compile(fmt.Sprintf("var x%v = 1;", i) + strings.Repeat(" ", maxProgramSource/2)); err != nil {
			t.Fatal(err)
		}
		if programs.size > maxProgramSources {
			t.Fatalf("%v bytes of source cached", programs.size)
		}
	}
}

// A cached program is found by the hash of its source and the source: on a collision of the hash,
// the other script does not run.
func TestCachedProgramsCompareTheSource(t *testing.T) {
	other, err := Compile("'other'")
	if err != nil {
		t.Fatal(err)
	}
	src := "'mine'"
	programs.put(maphash.String(seed, src), compiled{src: "'other'", program: other})
	result, err := NewSandbox().Run(src, nil)
	if err != nil || result.Value != "mine" {
		t.Fatalf("%v %v", result.Value, err)
	}
}

// With tree fingerprints, compiling a script computes its fingerprint in the same parse.
func TestCompilingComputesTheTreeFingerprint(t *testing.T) {
	inMode(t, FingerprintTree)
	src := "var compiledOnce = 1; compiledOnce + 1"
	if _, err := Compile(src); err != nil {
		t.Fatal(err)
	}
	if cached, ok := CachedFingerprint(src); !ok || cached != treeFingerprint(src) {
		t.Fatalf("%v %v", cached, ok)
	}
}

// A cached program runs in several runtimes at once, each with its own variables.
func TestCachedProgramsRunConcurrently(t *testing.T) {
	src := "var doubled = JSON.parse(input).map(function (v) { return v * factor }); doubled[2]"
	var wait sync.WaitGroup
	failures := make(chan string, 64)
	for g := 0; g < 8; g++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for i := 0; i < 50; i++ {
				factor := g*100 + i
				result, err := NewSandbox().Run(src, map[string]any{"input": "[1, 2, 3]", "factor": factor})
				if err != nil || fmt.Sprint(result.Value) != fmt.Sprint(3*factor) {
					failures <- fmt.Sprintf("%v: %v %v", factor, result.Value, err)
					return
				}
			}
		}()
	}
	wait.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
}
