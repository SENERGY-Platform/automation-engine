package goja

import (
	"errors"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"testing"
)

// shapes a script builds to make goja's own Go code recurse past the goroutine stack; each runs
// with a stack cap of 64 MB, which they overflow by far without a bound on that recursion
var nativeDepthShapes = map[string]string{
	"native as own toString":          `var o = {}; o.toString = String.prototype.trim; String(o)`,
	"native as own toPrimitive":       `var o = {}; o[Symbol.toPrimitive] = String.prototype.trim; String(o)`,
	"error named by itself":           `var e = new Error(); e.name = e; String(e)`,
	"nested array to string":          `var a = []; for (var i = 0; i < 300000; i++) a = [a]; String(a)`,
	"nested array joined":             `var a = []; for (var i = 0; i < 300000; i++) a = [a]; a.join()`,
	"nested array in template":        "var a = []; for (var i = 0; i < 300000; i++) a = [a]; `${a}`",
	"nested array sorted":             `var a = []; for (var i = 0; i < 300000; i++) a = [a]; [a, []].sort()`,
	"nested array toLocaleString":     `var a = []; for (var i = 0; i < 300000; i++) a = [a]; a.toLocaleString()`,
	"nested array flat":               `var a = []; for (var i = 0; i < 1000000; i++) a = [a]; a.flat(Infinity)`,
	"nested array stringified":        `var a = []; for (var i = 0; i < 300000; i++) a = [a]; JSON.stringify(a)`,
	"yield* chain":                    `var it = (function* () { yield 1 })(); for (var i = 0; i < 300000; i++) it = (function* (inner) { yield* inner })(it); it.next()`,
	"proxy chain set":                 `var p = {}; for (var i = 0; i < 500000; i++) p = new Proxy(p, {}); p.x = 1`,
	"prototype chain set":             `var o = {}; for (var i = 0; i < 1500000; i++) o = Object.create(o); o.x = 1`,
	"reviver injects a deep value":    `JSON.parse('{"a": 1, "b": 2}', function (k, v) { if (k === "a") { var d = {}; for (var i = 0; i < 1000000; i++) d = {d: d}; this.b = d } return v })`,
	"deep object exported":            `var o = {}; for (var i = 0; i < 1500000; i++) o = {o: o}; o`,
	"deep array exported":             `var a = []; for (var i = 0; i < 1500000; i++) a = [a]; a`,
	"prototype chain get":             `var o = {}; for (var i = 0; i < 1500000; i++) o = Object.create(o); o.x`,
	"prototype chain has":             `var o = {}; for (var i = 0; i < 1500000; i++) o = Object.create(o); "x" in o`,
	"prototype chain index set":       `var o = {}; for (var i = 0; i < 1500000; i++) o = Object.create(o); o[1] = 1`,
	"prototype chain symbol get":      `var o = {}; for (var i = 0; i < 1500000; i++) o = Object.create(o); o[Symbol.iterator]`,
	"proxy chain get":                 `var p = {}; for (var i = 0; i < 500000; i++) p = new Proxy(p, {}); p.x`,
	"proxy chain has":                 `var p = {}; for (var i = 0; i < 500000; i++) p = new Proxy(p, {}); "x" in p`,
	"proxy chain delete":              `var p = {}; for (var i = 0; i < 500000; i++) p = new Proxy(p, {}); delete p.x`,
	"proxy chain keys":                `var p = {}; for (var i = 0; i < 500000; i++) p = new Proxy(p, {}); Object.keys(p)`,
	"proxy chain call":                `var f = function () {}; for (var i = 0; i < 500000; i++) f = new Proxy(f, {}); f()`,
	"proxy chain isArray":             `var p = []; for (var i = 0; i < 500000; i++) p = new Proxy(p, {}); Array.isArray(p)`,
	"proxy chain prototype":           `var p = {}; for (var i = 0; i < 500000; i++) p = new Proxy(p, {}); Object.getPrototypeOf(p)`,
	"bound function chain call":       `var f = function () {}; for (var i = 0; i < 1000000; i++) f = f.bind(null); f()`,
	"bound function chain instanceof": `var f = function () {}; for (var i = 0; i < 1000000; i++) f = f.bind(null); ({}) instanceof f`,
	"bound function chain new":        `var f = function () {}; for (var i = 0; i < 1000000; i++) f = f.bind(null); new f()`,
}

// Every shape ends its run with a StackOverflowError instead of the process, or is refused when it
// builds a chain of proxies or bound functions; the export of a deep result panics with one, which
// the caller recovers from.
func TestEngineNativeDepth(t *testing.T) {
	if name := os.Getenv("GOJA_DEPTH_SHAPE"); name != "" {
		debug.SetMaxStack(64 << 20)
		vm := New()
		value, err := vm.RunString(nativeDepthShapes[name])
		if strings.HasSuffix(name, "exported") && err == nil {
			err = func() (err error) {
				defer func() {
					if p := recover(); p != nil {
						err, _ = p.(error)
					}
				}()
				value.Export()
				return nil
			}()
		}
		var overflow *StackOverflowError
		var exception *Exception
		refused := errors.As(err, &exception) && strings.Contains(exception.Error(), "Maximum chain of 10000 proxies and bound functions exceeded")
		if !errors.As(err, &overflow) && !refused {
			t.Fatalf("expected a StackOverflowError or a refused chain, got %v", err)
		}
		return
	}
	for name := range nativeDepthShapes {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestEngineNativeDepth$")
			cmd.Env = append(os.Environ(), "GOJA_DEPTH_SHAPE="+name)
			out, err := cmd.CombinedOutput()
			if err != nil {
				lines := strings.SplitN(string(out), "\n", 4)
				t.Errorf("%v: %v", err, strings.Join(lines[:min(3, len(lines))], " | "))
			}
		})
	}
}
