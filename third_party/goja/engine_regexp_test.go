package goja

import (
	"errors"
	"testing"
	"time"
)

// A match that backtracks catastrophically ends the run after the timeout, through every method
// that matches, and a script cannot catch it; a normal match is unaffected.
func TestEngineRegExpTimeout(t *testing.T) {
	SetRegExpMatchTimeout(200 * time.Millisecond)
	t.Cleanup(func() { SetRegExpMatchTimeout(0) })
	// the lookahead needs regexp2, which backtracks; RE2 would not
	prefix := `var re = /^(a+)+(?!x)$/; var g = /(a+)+(?!x)$/g; var s = "a".repeat(28) + "b"; `
	for _, src := range []string{
		`re.test(s)`,
		`re.exec(s)`,
		`s.match(re)`,
		`s.match(g)`,
		`Array.from(s.matchAll(g))`,
		`s.replace(re, "x")`,
		`s.replace(g, "x")`,
		`s.split(re)`,
		`s.search(re)`,
		`try { re.test(s) } catch (e) { "caught" }`,
	} {
		start := time.Now()
		_, err := New().RunString(prefix + src)
		var timeout *RegExpTimeoutError
		if !errors.As(err, &timeout) {
			t.Errorf("%v: %v", src, err)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("%v: %v", src, elapsed)
		}
	}
	value, err := New().RunString(`/^(a+)+(?!x)$/.test("aaaa") && "a1b2".replace(/(?<=a)\d/g, "#")`)
	if err != nil || value.String() != "a#b2" {
		t.Errorf("%v %v", value, err)
	}
}
