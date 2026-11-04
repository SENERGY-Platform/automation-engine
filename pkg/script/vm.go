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
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"modernc.org/quickjs"
)

// VM is a QuickJS runtime for one evaluation (EXPR-03). QuickJS keeps its heap and its stack
// apart from Go's: a script that recurses natively, e.g. through a built-in assigned as toString,
// or that allocates without end fails with an InternalError instead of killing the process, as
// it did on goja. A VM is not safe for concurrent use and is closed after the evaluation, so that
// nothing a script changed, a built-in included, reaches the next one.
//
// Values cross between Go and JavaScript as JSON. Go values a script receives from a bound
// method are shown as goja showed them (ToScript), so scripts written for goja see the same
// properties.
type VM struct {
	vm       *quickjs.VM
	timeout  time.Duration
	internal quickjs.Value
	// set by reusable: evaluates a script and resets the globals afterwards
	reused   quickjs.Value
	bound    map[string]reflect.Value
	onLog    func(string)
	onAssign func(name string, raw string) (string, error)
}

// prelude installs what every VM has and returns the functions the Go side calls. It runs before
// any script and takes the host function off the global object, so scripts see only the standard
// library and what the caller exposes. __evalIn is defined in global code, so that a script it
// evaluates sees only its scope and the globals, not the functions of the prelude.
const prelude = `globalThis.__evalIn = function (__scope, __src) { with (__scope) { return eval(__src); } };
(function (g, host, evalIn) {
	delete g.__host;
	delete g.__evalIn;
	var parse = JSON.parse, stringify = JSON.stringify, define = Object.defineProperty;
	var names = Object.getOwnPropertyNames, descriptor = Object.getOwnPropertyDescriptor, create = Object.create;
	// the variables of the evaluation running in a reused VM, and the names of the functions of the
	// sandbox, which variables do not hide
	var scope = null, functions = create(null);
	// Camunda handed some variables over as JSON text that the engine keeps as objects, e.g. the
	// value of a device event, and models parse them; parsing an object would read "[object Object]"
	JSON.parse = function (text, reviver) {
		return typeof text === "string" ? parse.call(JSON, text, reviver) : text;
	};
	function strict(key, value) {
		switch (typeof value) {
		case "number":
			if (!isFinite(value)) throw new TypeError("value is not JSON serializable: " + value);
			return value;
		case "function":
		case "symbol":
		case "bigint":
			throw new TypeError("value is not JSON serializable: " + typeof value);
		case "undefined":
			return null;
		}
		return value;
	}
	// arguments of Go methods: what JSON lacks arrives as text, as goja passed it in a form Go prints
	function lenient(key, value) {
		switch (typeof value) {
		case "number":
			return isFinite(value) ? value : String(value);
		case "function":
		case "symbol":
		case "bigint":
			return String(value);
		case "undefined":
			return null;
		}
		return value;
	}
	// NaN and Infinity are stored as strings, because JSON does not know them; Number(x) reads them back
	function encode(value) {
		if (value === undefined || value === null) return "null";
		if (typeof value === "number" && !isFinite(value)) return value !== value ? '"NaN"' : value > 0 ? '"+Inf"' : '"-Inf"';
		if (typeof value === "function") throw new TypeError("a function is not a valid value");
		return stringify(value, strict);
	}
	function text(value) {
		if (typeof value === "string") return value;
		if (value === undefined) return "undefined";
		try {
			var s = stringify(value, strict);
			if (s !== undefined) return s;
		} catch (e) {}
		return String(value);
	}
	function set(name, value) {
		define(g, name, {value: value, writable: true, enumerable: true, configurable: true});
	}
	function log() {
		var parts = [];
		for (var i = 0; i < arguments.length; i++) parts.push(text(arguments[i]));
		host("log", parts.join(" "));
	}
	return {
		encode: encode,
		globals: function (raw) {
			var values = parse(raw);
			for (var name in values) set(name, values[name]);
		},
		sandbox: function () {
			functions.log = functions.console = functions.setVariable = true;
			set("log", log);
			set("console", {log: log});
			set("setVariable", function (name, value) {
				var raw;
				try {
					raw = host("assign", String(name), encode(value));
				} catch (e) {
					throw new TypeError("setVariable(" + stringify(String(name)) + "): " + e.message);
				}
				var value = parse(raw);
				set(name, value);
				if (scope && name in scope) scope[name] = value;
			});
		},
		// reusable returns a function that evaluates a script with its variables in a scope of
		// their own and afterwards restores the global object as it was before the first one:
		// what a script declared, assigned or deleted there does not reach the next evaluation
		reusable: function () {
			// values are compared, not descriptors: creating a descriptor per global and evaluation
			// took more time than the evaluation itself
			var baseNames = names(g), base = create(null), values = [], i;
			for (i = 0; i < baseNames.length; i++) {
				var d = descriptor(g, baseNames[i]);
				base[baseNames[i]] = d;
				values.push(d.get || d.set ? base : d.value);
			}
			function reset() {
				var current = names(g), i;
				for (i = 0; i < current.length; i++) {
					if (!base[current[i]]) {
						try { delete g[current[i]]; } catch (e) {}
					}
				}
				for (i = 0; i < baseNames.length; i++) {
					var was = values[i], is = g[baseNames[i]];
					if (was !== base && is !== was && !(is !== is && was !== was)) {
						try { define(g, baseNames[i], base[baseNames[i]]); } catch (e) {}
					}
				}
			}
			return function (src, raw) {
				var values = parse(raw);
				scope = create(null);
				for (var name in values) {
					if (!functions[name]) scope[name] = values[name];
				}
				try {
					return encode(evalIn(scope, src));
				} finally {
					scope = null;
					reset();
				}
			};
		},
		bind: function (namespace, methods) {
			var object = {};
			parse(methods).forEach(function (method) {
				object[method] = function () {
					var args = [];
					for (var i = 0; i < arguments.length; i++) args.push(arguments[i]);
					return parse(host("call", namespace, method, stringify(args, lenient)));
				};
			});
			set(namespace, object);
		}
	};
})(globalThis, globalThis.__host, globalThis.__evalIn)`

// preludeCode is the prelude compiled once; compiling it took most of the time of a new VM
var preludeCode = sync.OnceValues(func() ([]byte, error) {
	vm, err := quickjs.NewVM()
	if err != nil {
		return nil, err
	}
	defer func() { _ = vm.Close() }()
	return vm.Compile(prelude, quickjs.EvalGlobal)
})

// NewVM returns a VM that interrupts an evaluation after timeout and fails an allocation beyond
// maxMemory bytes; zero disables either.
func NewVM(timeout time.Duration, maxMemory int) (result *VM, err error) {
	vm, err := quickjs.NewVM()
	if err != nil {
		return nil, err
	}
	result = &VM{vm: vm, timeout: timeout, bound: map[string]reflect.Value{}}
	defer func() {
		if err != nil {
			result.Close()
			result = nil
		}
	}()
	if maxMemory > 0 {
		vm.SetMemoryLimit(uintptr(maxMemory))
	}
	if err = vm.SetEvalTimeout(timeout); err != nil {
		return result, err
	}
	if err = vm.RegisterHostFunc("__host", result.host); err != nil {
		return result, err
	}
	code, err := preludeCode()
	if err != nil {
		return result, err
	}
	result.internal, err = vm.EvalBytecodeValue(code)
	return result, err
}

func (this *VM) Close() {
	for _, v := range []*quickjs.Value{&this.reused, &this.internal} {
		if v.VM() != nil {
			v.Free()
		}
	}
	_ = this.vm.Close()
}

// call invokes a function of the prelude
func (this *VM) call(name string, args ...any) (any, error) {
	atom, err := this.vm.NewAtom(name)
	if err != nil {
		return nil, err
	}
	fn, err := this.internal.GetPropertyValue(atom)
	if err != nil {
		return nil, err
	}
	defer fn.Free()
	result, err := fn.Call(this.internal, args...)
	return result, scriptError(err)
}

// reusable prepares the VM for EvalReused; the globals as they are now are what each evaluation
// starts with.
func (this *VM) reusable() error {
	atom, err := this.vm.NewAtom("reusable")
	if err != nil {
		return err
	}
	fn, err := this.internal.GetPropertyValue(atom)
	if err != nil {
		return err
	}
	defer fn.Free()
	this.reused, err = fn.CallValue(this.internal)
	return scriptError(err)
}

// EvalReused is Eval for a VM prepared by reusable: the variables are visible to src only, and the
// global object is reset afterwards.
func (this *VM) EvalReused(src string, variables map[string]any, maxBytes int) (any, error) {
	raw, err := json.Marshal(variables)
	if err != nil {
		return nil, err
	}
	encoded, err := this.reused.Call(this.internal, src, string(raw))
	if err != nil {
		return nil, this.failure(err)
	}
	return decode(encoded, maxBytes)
}

// SetGlobals defines the values, which must be JSON compatible, as global variables.
func (this *VM) SetGlobals(values map[string]any) error {
	if len(values) == 0 {
		return nil
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	_, err = this.call("globals", string(raw))
	return err
}

// Bind defines a global object named namespace whose methods call the exported methods of value,
// named as goja named them: the first letter lower case. Arguments arrive converted to the
// parameter types; a panic of the method or a non-nil error as its last result throws in the
// script, as on goja.
func (this *VM) Bind(namespace string, value any) error {
	v := reflect.ValueOf(value)
	methods := []string{}
	for i := 0; i < v.NumMethod(); i++ {
		methods = append(methods, MethodName(v.Type().Method(i).Name))
	}
	this.bound[namespace] = v
	raw, err := json.Marshal(methods)
	if err != nil {
		return err
	}
	_, err = this.call("bind", namespace, string(raw))
	return err
}

// Exec evaluates src as global code for its effects.
func (this *VM) Exec(src string) error {
	value, err := this.vm.EvalValue(src, quickjs.EvalGlobal)
	if err != nil {
		return this.failure(err)
	}
	value.Free()
	return nil
}

// Eval evaluates src as global code and returns the value of its last expression statement in
// its JSON form, at most maxBytes long if positive.
func (this *VM) Eval(src string, maxBytes int) (any, error) {
	value, err := this.vm.EvalValue(src, quickjs.EvalGlobal)
	if err != nil {
		return nil, this.failure(err)
	}
	defer value.Free()
	encoded, err := this.call("encode", value)
	if err != nil {
		return nil, err
	}
	return decode(encoded, maxBytes)
}

// decode reads what encode of the prelude returned
func decode(encoded any, maxBytes int) (any, error) {
	raw, _ := encoded.(string)
	if maxBytes > 0 && len(raw) > maxBytes {
		return nil, fmt.Errorf("value exceeds %v bytes", maxBytes)
	}
	var result any
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("value is not JSON serializable: %w", err)
	}
	return result, nil
}

func (this *VM) host(args []any) (result any, err error) {
	op, _ := args[0].(string)
	switch op {
	case "log":
		if this.onLog != nil {
			this.onLog(stringArg(args, 1))
		}
		return nil, nil
	case "assign":
		if this.onAssign == nil {
			return nil, errors.New("not available")
		}
		return this.onAssign(stringArg(args, 1), stringArg(args, 2))
	case "call":
		return this.invoke(stringArg(args, 1), stringArg(args, 2), stringArg(args, 3))
	}
	return nil, fmt.Errorf("unknown host operation %q", op)
}

func stringArg(args []any, i int) string {
	if i >= len(args) {
		return ""
	}
	s, _ := args[i].(string)
	return s
}

func (this *VM) invoke(namespace string, method string, rawArgs string) (result any, err error) {
	target, ok := this.bound[namespace]
	if !ok {
		return nil, fmt.Errorf("unknown namespace %v", namespace)
	}
	var fn reflect.Value
	for i := 0; i < target.NumMethod(); i++ {
		if MethodName(target.Type().Method(i).Name) == method {
			fn = target.Method(i)
			break
		}
	}
	if !fn.IsValid() {
		return nil, fmt.Errorf("%v.%v is not a function", namespace, method)
	}
	var args []json.RawMessage
	if err = json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return nil, err
	}
	in, err := arguments(fn.Type(), args)
	if err != nil {
		return nil, err
	}
	defer func() {
		if caught := recover(); caught != nil {
			if e, ok := caught.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("%v", caught)
			}
		}
	}()
	out := fn.Call(in)
	if n := len(out); n > 0 && fn.Type().Out(n-1) == reflect.TypeFor[error]() {
		if !out[n-1].IsNil() {
			return nil, out[n-1].Interface().(error)
		}
		out = out[:n-1]
	}
	if len(out) == 0 {
		return "null", nil
	}
	raw, err := json.Marshal(ToScript(out[0].Interface()))
	return string(raw), err
}

// arguments converts the arguments of a call to the parameter types of fn; missing ones are zero
// values, as on goja
func arguments(fn reflect.Type, args []json.RawMessage) ([]reflect.Value, error) {
	result := []reflect.Value{}
	for i := 0; i < fn.NumIn(); i++ {
		t := fn.In(i)
		if fn.IsVariadic() && i == fn.NumIn()-1 {
			for j := i; j < len(args); j++ {
				v, err := argument(t.Elem(), args[j])
				if err != nil {
					return nil, err
				}
				result = append(result, v)
			}
			break
		}
		var raw json.RawMessage
		if i < len(args) {
			raw = args[i]
		}
		v, err := argument(t, raw)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, nil
}

func argument(t reflect.Type, raw json.RawMessage) (reflect.Value, error) {
	ptr := reflect.New(t)
	if len(raw) == 0 || string(raw) == "null" {
		return ptr.Elem(), nil
	}
	err := json.Unmarshal(raw, ptr.Interface())
	if err == nil {
		return ptr.Elem(), nil
	}
	// goja converted between primitives as JavaScript does: 5 to "5", 2.5 to 2
	switch t.Kind() {
	case reflect.String:
		ptr.Elem().SetString(strings.Trim(string(raw), `"`))
		return ptr.Elem(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if f, ferr := strconv.ParseFloat(strings.Trim(string(raw), `"`), 64); ferr == nil {
			ptr.Elem().SetInt(int64(f))
			return ptr.Elem(), nil
		}
	case reflect.Float32, reflect.Float64:
		if f, ferr := strconv.ParseFloat(strings.Trim(string(raw), `"`), 64); ferr == nil {
			ptr.Elem().SetFloat(f)
			return ptr.Elem(), nil
		}
	}
	return reflect.Value{}, fmt.Errorf("cannot use %s as %v", raw, t)
}

// MethodName is the name a script calls a Go method by: the first letter lower case, as goja's
// TagFieldNameMapper("json", true) had it.
func MethodName(name string) string {
	return strings.ToLower(name[0:1]) + name[1:]
}

// FieldName is the name a script sees a struct field under: its json tag name if that is an
// identifier, otherwise none, as goja's TagFieldNameMapper("json", true) had it.
func FieldName(field reflect.StructField) string {
	tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	if !isIdentifier(tag) {
		return ""
	}
	return tag
}

func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '$' || r == '_' || unicode.IsLetter(r) || (i > 0 && (unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Pc, r))) {
			continue
		}
		return false
	}
	return true
}

// ToScript converts a Go value into the JSON compatible form a script sees: struct fields under
// FieldName, the first of a name winning as for reflect.VisibleFields, fields without a name left
// out, and every visible field present, also an empty one. A type with its own JSON or text form,
// e.g. time.Time, keeps it.
func ToScript(value any) any {
	return toScript(reflect.ValueOf(value))
}

var (
	jsonMarshaler = reflect.TypeFor[json.Marshaler]()
	textMarshaler = reflect.TypeFor[encoding.TextMarshaler]()
)

func toScript(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		if v.Kind() == reflect.Pointer && (v.Type().Implements(jsonMarshaler) || v.Type().Implements(textMarshaler)) {
			return v.Interface()
		}
		return toScript(v.Elem())
	case reflect.Struct:
		if v.Type().Implements(jsonMarshaler) || v.Type().Implements(textMarshaler) {
			return v.Interface()
		}
		result := map[string]any{}
		for _, field := range reflect.VisibleFields(v.Type()) {
			if !field.IsExported() {
				continue
			}
			name := FieldName(field)
			if _, shadowed := result[name]; name == "" || shadowed {
				continue
			}
			fv, err := v.FieldByIndexErr(field.Index)
			if err != nil {
				result[name] = nil
				continue
			}
			result[name] = toScript(fv)
		}
		return result
	case reflect.Map:
		if v.IsNil() {
			return nil
		}
		result := map[string]any{}
		iter := v.MapRange()
		for iter.Next() {
			result[fmt.Sprint(iter.Key().Interface())] = toScript(iter.Value())
		}
		return result
	case reflect.Slice:
		if v.IsNil() {
			return nil
		}
		fallthrough
	case reflect.Array:
		result := make([]any, v.Len())
		for i := range result {
			result[i] = toScript(v.Index(i))
		}
		return result
	case reflect.Func, reflect.Chan, reflect.UnsafePointer, reflect.Complex64, reflect.Complex128:
		return nil
	}
	return v.Interface()
}

// failure is the error of an evaluation: ErrTimeout for the interrupt of the timeout
func (this *VM) failure(err error) error {
	var jsErr *quickjs.Error
	if errors.As(err, &jsErr) && jsErr.Name == "InternalError" && jsErr.Message == "interrupted" {
		return fmt.Errorf("%w after %v", ErrTimeout, this.timeout)
	}
	return scriptError(err)
}

// scriptError adds where a script failed to the message of a QuickJS error, which carries it only
// in its stack
func scriptError(err error) error {
	var jsErr *quickjs.Error
	if !errors.As(err, &jsErr) {
		return err
	}
	first, _, _ := strings.Cut(strings.TrimSpace(jsErr.Stack), "\n")
	first = strings.TrimSpace(strings.TrimPrefix(first, "at "))
	if open := strings.LastIndex(first, "("); open >= 0 && strings.HasSuffix(first, ")") {
		first = first[open+1 : len(first)-1]
	}
	if first == "" {
		return jsErr
	}
	// a script evaluated by a reused VM runs through eval, which QuickJS names <input>
	return &locatedError{err: jsErr, at: strings.Replace(first, "<input>", "<eval>", 1)}
}

type locatedError struct {
	err *quickjs.Error
	at  string
}

func (this *locatedError) Error() string {
	return this.err.Error() + " at " + this.at
}

func (this *locatedError) Unwrap() error {
	return this.err
}
