package goja

import (
	"github.com/dop251/goja/unistring"
)

func init() {
	enginePatches = append(enginePatches, "native-depth")
}

// DefaultMaxNativeDepth bounds the recursion of goja's own Go code over values a script builds
// unless SetMaxNativeDepth sets another bound. A level takes up to a few kilobytes of stack.
const DefaultMaxNativeDepth = 10000

// SetMaxNativeDepth bounds the recursion of goja's own Go code over values a script builds: a
// native as an object's own toString, a nested array turned into text or JSON, a long prototype,
// proxy or bound function chain, a yield* chain, a deep value exported to Go. SetMaxCallStackSize
// counts JavaScript frames only, so that such a recursion overflowed the goroutine stack, a fatal
// error that ends the process. Past the bound, the run ends with a *StackOverflowError, which a
// script cannot catch; Export panics with it. Chains of proxies and bound functions longer than
// the bound are refused when created, with a RangeError. Zero or less is DefaultMaxNativeDepth.
func (r *Runtime) SetMaxNativeDepth(depth int) {
	r.maxNativeDepth = depth
}

func (r *Runtime) nativeDepthLimit() int {
	if r.maxNativeDepth <= 0 {
		return DefaultMaxNativeDepth
	}
	return r.maxNativeDepth
}

// enterNative counts a level of native recursion; the caller defers leaveNative
func (r *Runtime) enterNative() {
	r.nativeDepth++
	if r.nativeDepth > r.nativeDepthLimit() {
		r.nativeDepth--
		ex := &StackOverflowError{}
		ex.stack = r.vm.captureStack(nil, 0)
		panic(ex)
	}
}

func (r *Runtime) leaveNative() {
	r.nativeDepth--
}

// wrapperDepth is the length of the chain of proxies and bound functions an object starts, which
// every operation on it follows natively; their targets cannot change, so that it is exact
func wrapperDepth(o *Object) int {
	switch w := o.self.(type) {
	case *proxyObject:
		return w.wrapperDepth
	case *boundFuncObject:
		return w.wrapperDepth
	}
	return 0
}

// nextWrapperDepth returns the depth of a proxy or bound function around target, and refuses a
// chain longer than the bound
func (r *Runtime) nextWrapperDepth(target *Object) int {
	depth := wrapperDepth(target) + 1
	if depth > r.nativeDepthLimit() {
		panic(r.newErrorf(r.getRangeError(), "Maximum chain of %d proxies and bound functions exceeded", r.nativeDepthLimit()))
	}
	return depth
}

// The steps to a prototype, counted: a script can build a chain of millions of prototypes, which
// every lookup, assignment and in check on it walks recursively.

func protoGet(p *Object, name, receiver Value) Value {
	r := p.runtime
	r.enterNative()
	defer r.leaveNative()
	return p.get(name, receiver)
}

func protoGetStr(p *Object, name unistring.String, receiver Value) Value {
	r := p.runtime
	r.enterNative()
	defer r.leaveNative()
	return p.self.getStr(name, receiver)
}

func protoGetIdx(p *Object, idx valueInt, receiver Value) Value {
	r := p.runtime
	r.enterNative()
	defer r.leaveNative()
	return p.self.getIdx(idx, receiver)
}

func protoGetSym(p *Object, s *Symbol, receiver Value) Value {
	r := p.runtime
	r.enterNative()
	defer r.leaveNative()
	return p.self.getSym(s, receiver)
}

func protoSetForeignStr(p *Object, name unistring.String, val, receiver Value, throw bool) (bool, bool) {
	r := p.runtime
	r.enterNative()
	defer r.leaveNative()
	return p.self.setForeignStr(name, val, receiver, throw)
}

func protoSetForeignIdx(p *Object, idx valueInt, val, receiver Value, throw bool) (bool, bool) {
	r := p.runtime
	r.enterNative()
	defer r.leaveNative()
	return p.self.setForeignIdx(idx, val, receiver, throw)
}

func protoSetForeignSym(p *Object, s *Symbol, val, receiver Value, throw bool) (bool, bool) {
	r := p.runtime
	r.enterNative()
	defer r.leaveNative()
	return p.self.setForeignSym(s, val, receiver, throw)
}

func protoHasPropertyStr(p *Object, name unistring.String) bool {
	r := p.runtime
	r.enterNative()
	defer r.leaveNative()
	return p.self.hasPropertyStr(name)
}

func protoHasPropertyIdx(p *Object, idx valueInt) bool {
	r := p.runtime
	r.enterNative()
	defer r.leaveNative()
	return p.self.hasPropertyIdx(idx)
}

func protoHasPropertySym(p *Object, s *Symbol) bool {
	r := p.runtime
	r.enterNative()
	defer r.leaveNative()
	return p.self.hasPropertySym(s)
}
