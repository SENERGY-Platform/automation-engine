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
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/maphash"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/dop251/goja"
	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/file"
	"github.com/dop251/goja/parser"
)

// The ways to compute a fingerprint (SCRIPT_FINGERPRINT).
const (
	// FingerprintText compares the text without // comments: fast, but another indentation or
	// spacing is another script
	FingerprintText = "text"
	// FingerprintTree compares the syntax tree goja parses: copies that differ in any formatting
	// match, at the cost of a parse per script
	FingerprintTree = "tree"
)

var treeFingerprints atomic.Bool

// SetFingerprintMode chooses how Fingerprint compares scripts, FingerprintText or
// FingerprintTree; empty is FingerprintText. It is set once at the start, before any script is
// fingerprinted: entries of the blocklist computed in the other mode match only through their
// source.
func SetFingerprintMode(mode string) error {
	switch mode {
	case "", FingerprintText:
		treeFingerprints.Store(false)
	case FingerprintTree:
		treeFingerprints.Store(true)
	default:
		return fmt.Errorf("unknown script fingerprint %q, expected %v or %v", mode, FingerprintText, FingerprintTree)
	}
	fingerprints.clear()
	return nil
}

// Fingerprint identifies a script for the blocklist (EXPR-03), in the mode SetFingerprintMode
// chose: by its text without // comments (textFingerprint) or by its syntax tree
// (treeFingerprint). It is cached by a 64-bit hash of the source, not by the source, which a
// module script makes kilobytes long.
func Fingerprint(src string) string {
	key := maphash.String(seed, src)
	if cached, ok := fingerprints.get(key); ok {
		return cached
	}
	var result string
	if treeFingerprints.Load() {
		result = treeFingerprint(src)
	} else {
		result = textFingerprint(src)
	}
	fingerprints.put(key, result)
	return result
}

// CachedFingerprint returns the fingerprint of src if it is known already, without computing it.
func CachedFingerprint(src string) (string, bool) {
	return fingerprints.get(maphash.String(seed, src))
}

var (
	seed         = maphash.MakeSeed()
	fingerprints = &cache[string]{}
	depths       = &cache[bool]{}
)

// cache holds values per script, up to maxCached; then it starts over instead of growing, e.g.
// with generated scripts
type cache[V any] struct {
	values sync.Map
	count  atomic.Int64
}

const maxCached = 10000

func (this *cache[V]) get(key uint64) (V, bool) {
	value, ok := this.values.Load(key)
	if !ok {
		var zero V
		return zero, false
	}
	return value.(V), true
}

func (this *cache[V]) put(key uint64, value V) {
	if this.count.Add(1) > maxCached {
		this.clear()
		this.count.Store(1)
	}
	this.values.Store(key, value)
}

func (this *cache[V]) clear() {
	this.values.Clear()
	this.count.Store(0)
}

// textFingerprint hashes the text of a script without its // comments, with the spaces and tabs
// before them, without lines that held only a comment, and without blank space around it.
// Everything else counts as written, also indentation, spacing and line ends. Only lines with // are looked at; each is scanned once,
// also with many // in it.
//
// Without a lexer, it takes // for a comment only where goja must read one too: at the start of
// the line or after a space or tab, and with no quote and no slash after it on the line. In a
// string or a regular expression, the end of the literal follows on the line, with a quote or a
// slash, so that the // is left as it is; removed, the code after the literal would no longer
// count, and a crashing script could get the fingerprint of a harmless one. A comment with a quote
// or a slash, e.g. with a URL, stays as well: a copy with another one is then another script,
// which the quarantine and the stopped smart service still catch.
func textFingerprint(src string) string {
	out := make([]byte, 0, len(src)+8)
	out = append(out, "text\n"...)
	copied := 0 // src[:copied] is handled
	// the line of the last //: start, end, start of the next, last quote or slash in it
	var line struct{ start, end, next, special int }
	line.end, line.next = -1, 0
	for search := 0; ; {
		j := strings.Index(src[search:], "//")
		if j < 0 {
			break
		}
		i := search + j
		if i >= line.end {
			// a new line; the text between the last one and i was not scanned yet
			line.start = line.next + lastLineEnd(src[line.next:i])
			end, next := lineEnd(src[i:])
			line.end, line.next = i+end, i+next
			line.special = line.start + strings.LastIndexAny(src[line.start:line.end], "'\"`/")
		}
		// a // starts a comment where it begins the line or follows a space or tab, and nothing
		// after it on the line is a quote or a slash
		if (i == line.start || src[i-1] == ' ' || src[i-1] == '\t') && line.special < i+2 {
			before := strings.TrimRight(src[line.start:i], " \t")
			out = append(out, src[copied:line.start]...)
			if before == "" {
				copied = line.next
			} else {
				out = append(out, before...)
				copied = line.end
			}
			search = line.next
			continue
		}
		search = i + 1
	}
	out = append(out, src[copied:]...)
	// blank space around the script does not count, e.g. a line end after the last line
	body := bytes.Trim(out[len("text\n"):], blankAround)
	out = append(out[:len("text\n")], body...)
	sum := sha256.Sum256(out)
	return hex.EncodeToString(sum[:])
}

const blankAround = " \t\r\n\u2028\u2029"

// lineEnd returns the end of the first line of s and the start of the next. JavaScript ends a
// line, and a // comment, also at \r and at the separators U+2028 and U+2029: a comment taken to
// run to \n would hide the code behind them, and give a crashing script the fingerprint of a
// harmless one.
func lineEnd(s string) (end int, next int) {
	end, next = len(s), len(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		end, next = i, i+1
	}
	if i := strings.IndexByte(s[:end], '\r'); i >= 0 {
		end, next = i, i+1
		if next < len(s) && s[next] == '\n' {
			next++
		}
	}
	// U+2028 and U+2029 are E2 80 A8 and E2 80 A9 in UTF-8
	for from := 0; ; {
		i := strings.IndexByte(s[from:end], 0xE2)
		if i < 0 {
			return end, next
		}
		i += from
		if i+2 < len(s) && s[i+1] == 0x80 && (s[i+2] == 0xA8 || s[i+2] == 0xA9) {
			return i, i + 3
		}
		from = i + 1
	}
}

// lastLineEnd returns the start of the last line of s: the position after its last line end, 0
// without one
func lastLineEnd(s string) int {
	start := 0
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		start = i + 1
	}
	if i := strings.LastIndexByte(s[start:], '\r'); i >= 0 {
		start += i + 1
	}
	// U+2028 and U+2029, as in lineEnd
	for end := len(s); ; {
		i := strings.LastIndexByte(s[start:end], 0xE2)
		if i < 0 {
			return start
		}
		i += start
		if i+2 < len(s) && s[i+1] == 0x80 && (s[i+2] == 0xA8 || s[i+2] == 0xA9) {
			return i + 3
		}
		end = i
	}
}

// treeFingerprint hashes the syntax tree goja parses, without positions: comments, whitespace,
// the semicolons the parser inserts and redundant parentheses do not count; names, literals as
// written and the structure do. Because the tree is the one goja runs, code hidden from a lexer
// still counts. A script goja cannot parse, or nested deeper than MaxDepth, cannot run either; it
// is identified by its exact source.
func treeFingerprint(src string) string {
	// without the option, a sourceMappingURL comment makes the parser read a file
	program, err := parser.ParseFile(nil, "", src, 0, parser.WithDisableSourceMaps)
	if err != nil {
		return sourceFingerprint(src)
	}
	fingerprint, tooDeep := fingerprintTree(src, program)
	// Compile, which parses the script again to run it, needs the depth only
	depths.put(maphash.String(seed, src), tooDeep)
	return fingerprint
}

// fingerprintTree hashes a parsed script and reports whether it is deeper than MaxDepth; a script
// too deep is identified by its source
func fingerprintTree(src string, program *ast.Program) (fingerprint string, tooDeep bool) {
	sum := sha256.New()
	out := bufio.NewWriter(sum)
	_, _ = out.WriteString("tree\n")
	if walkTree(program, out) != nil {
		return sourceFingerprint(src), true
	}
	_ = out.Flush()
	return hex.EncodeToString(sum.Sum(nil)), false
}

func sourceFingerprint(src string) string {
	sum := sha256.New()
	_, _ = io.WriteString(sum, "source\n")
	_, _ = io.WriteString(sum, src)
	return hex.EncodeToString(sum.Sum(nil))
}

// MaxDepth is the deepest syntax tree a script may have. goja's compiler recurses over the tree
// without a limit, and its parser limits nesting, not a chain like a+a+...+a: a few million terms
// overflow the stack of the goroutine, which ends the process, also when a model is only
// validated. Compiling a chain takes time quadratic in its length besides.
const MaxDepth = 5000

// ErrTooDeep refuses a script whose syntax tree is deeper than MaxDepth.
var ErrTooDeep = fmt.Errorf("the script is nested more than %v levels deep", MaxDepth)

// Compile compiles a script as goja.Compile, but without following a sourceMappingURL comment:
// goja would read the file it names before the script runs, without a limit, which makes
// file:///dev/zero end the process when its memory runs out, and before the crash brake knows.
// A script deeper than MaxDepth is refused before goja's compiler recurses over it.
//
// The result is cached: a program is bound to no runtime and runs in several at once, so that a
// script is parsed and compiled once, not at every run. With tree fingerprints, the walk over the
// parsed script that measures the depth computes the fingerprint as well, which the blocklist
// then finds cached.
func Compile(src string) (*goja.Program, error) {
	key := maphash.String(seed, src)
	if cached, ok := programs.get(key, src); ok {
		return cached.program, cached.err
	}
	program, err := compileUncached(key, src)
	programs.put(key, compiled{src: src, program: program, err: err})
	return program, err
}

func compileUncached(key uint64, src string) (*goja.Program, error) {
	parsed, err := goja.Parse("", src, parser.WithDisableSourceMaps)
	if err != nil {
		return nil, err
	}
	tooDeep, ok := depths.get(key)
	if !ok {
		if treeFingerprints.Load() {
			var fingerprint string
			fingerprint, tooDeep = fingerprintTree(src, parsed)
			fingerprints.put(key, fingerprint)
		} else {
			tooDeep = walkTree(parsed, nil) != nil
		}
		depths.put(key, tooDeep)
	}
	if tooDeep {
		return nil, ErrTooDeep
	}
	return goja.CompileAST(parsed, false)
}

// compiled is a script with its program, or the error compiling it gave
type compiled struct {
	src     string
	program *goja.Program
	err     error
}

// programCache holds compiled scripts up to a sum of their sources; then it starts over. A
// program takes 12 to 20 times the memory of its source.
type programCache struct {
	mux     sync.RWMutex
	entries map[uint64]compiled
	size    int
}

const (
	maxProgramSources = 2 << 20
	// a larger script is compiled at every run, rather than taking much of the cache
	maxProgramSource = 64 << 10
)

var programs = &programCache{entries: map[uint64]compiled{}}

// get returns the cached program of src. The source is compared, not only its 64-bit hash: on a
// collision, another script would run.
func (this *programCache) get(key uint64, src string) (compiled, bool) {
	this.mux.RLock()
	entry, ok := this.entries[key]
	this.mux.RUnlock()
	return entry, ok && entry.src == src
}

func (this *programCache) put(key uint64, entry compiled) {
	if len(entry.src) > maxProgramSource {
		return
	}
	this.mux.Lock()
	defer this.mux.Unlock()
	if old, ok := this.entries[key]; ok {
		this.size -= len(old.src)
		delete(this.entries, key)
	}
	if this.size+len(entry.src) > maxProgramSources || len(this.entries) >= maxCached {
		this.entries = map[uint64]compiled{}
		this.size = 0
	}
	this.entries[key] = entry
	this.size += len(entry.src)
}

var (
	idxType  = reflect.TypeOf(file.Idx(0))
	fileType = reflect.TypeOf(&file.File{})
)

// walkTree writes a syntax tree to out without positions and without the source text the parser
// keeps for functions and classes; with out nil, it only measures the depth. It returns
// ErrTooDeep for a tree deeper than MaxDepth. It walks with its own stack of one frame per level,
// not recursively, so that neither a deep tree overflows the goroutine stack nor a wide one, e.g.
// millions of statements, fills the memory.
func walkTree(root *ast.Program, out *bufio.Writer) error {
	type frame struct {
		value reflect.Value // a struct or a slice
		next  int           // its next field or element
		depth int           // structs above it
	}
	write := func(s string) {
		if out != nil {
			_, _ = out.WriteString(s)
		}
	}
	var stack []frame
	// open writes a leaf, or the start of a struct or a slice and pushes its frame
	open := func(v reflect.Value, depth int) error {
		for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
			if v.IsNil() {
				write("nil;")
				return nil
			}
			v = v.Elem()
		}
		switch v.Kind() {
		case reflect.Struct:
			if depth > MaxDepth {
				return ErrTooDeep
			}
			write(v.Type().Name())
			write("{")
			stack = append(stack, frame{value: v, depth: depth})
			return nil
		case reflect.Slice:
			write("[")
			stack = append(stack, frame{value: v, depth: depth})
			return nil
		}
		if out == nil {
			return nil
		}
		switch v.Kind() {
		case reflect.String:
			// with its length, so that the end of one string cannot be read as the start of the next
			s := v.String()
			write(strconv.Itoa(len(s)))
			write(":")
			write(s)
		case reflect.Bool:
			write(strconv.FormatBool(v.Bool()) + ";")
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			write(strconv.FormatInt(v.Int(), 10) + ";")
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			write(strconv.FormatUint(v.Uint(), 10) + ";")
		case reflect.Float32, reflect.Float64:
			write(strconv.FormatFloat(v.Float(), 'g', -1, 64) + ";")
		default:
			// a kind the syntax tree does not use; its type keeps the fingerprint deterministic
			write(v.Type().String() + ";")
		}
		return nil
	}
	if err := open(reflect.ValueOf(root), 0); err != nil {
		return err
	}
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		v, depth := top.value, top.depth
		var child reflect.Value
		if v.Kind() == reflect.Struct {
			for top.next < v.NumField() && skipped(v.Type().Field(top.next)) {
				top.next++
			}
			if top.next == v.NumField() {
				write("}")
				stack = stack[:len(stack)-1]
				continue
			}
			child, depth = v.Field(top.next), depth+1
		} else {
			if top.next == v.Len() {
				write("]")
				stack = stack[:len(stack)-1]
				continue
			}
			child = v.Index(top.next)
		}
		top.next++
		// top is invalid from here on, open may grow the stack
		if err := open(child, depth); err != nil {
			return err
		}
	}
	return nil
}

// skipped reports the fields that do not count: positions, the file, and the source text of
// functions and classes, which the tree has as well
func skipped(field reflect.StructField) bool {
	return field.Type == idxType || field.Type == fileType || (field.Name == "Source" && field.Type.Kind() == reflect.String)
}

// JoinParts joins the string inputs whose name starts with prefix, in the order of their names,
// as the pre- and postscripts of module tasks run (TASK-28).
func JoinParts(prefix string, inputs map[string]any) string {
	names := []string{}
	for name, value := range inputs {
		if _, ok := value.(string); ok && strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var result strings.Builder
	for _, name := range names {
		result.WriteString(inputs[name].(string))
	}
	return result.String()
}
