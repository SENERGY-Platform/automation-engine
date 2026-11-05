package goja

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

func init() {
	enginePatches = append(enginePatches, "regexp-timeout")
}

var regExpMatchTimeout atomic.Int64

// SetRegExpMatchTimeout bounds every match of a regular expression that needs backtracking, which
// goja runs with regexp2: such a match ignores Interrupt, and one that backtracks catastrophically,
// e.g. /^(a+)+$/ against "aaaa...b", runs for minutes. Past the bound, the run ends with a
// *RegExpTimeoutError, which a script cannot catch; without this patch, goja reported no match.
// It applies to expressions compiled after the call, so it is set before any script runs. Zero or
// less leaves matches unbounded, the default.
func SetRegExpMatchTimeout(timeout time.Duration) {
	regExpMatchTimeout.Store(int64(timeout))
}

// RegExpTimeoutError ends a run whose regular expression did not finish its match in the time
// SetRegExpMatchTimeout gives.
type RegExpTimeoutError struct {
	baseUncatchableException
	pattern string
	timeout time.Duration
}

func (e *RegExpTimeoutError) Error() string {
	return fmt.Sprintf("the regular expression /%v/ did not finish its match within %v", truncatePattern(e.pattern), e.timeout)
}

// RegExpMatchError ends a run whose regular expression regexp2 failed to match for another reason
// than the timeout, an error of regexp2 itself.
type RegExpMatchError struct {
	baseUncatchableException
	pattern string
	err     string
}

func (e *RegExpMatchError) Error() string {
	return fmt.Sprintf("the regular expression /%v/ failed to match: %v", truncatePattern(e.pattern), e.err)
}

func truncatePattern(pattern string) string {
	if len(pattern) > 100 {
		return pattern[:100] + "..."
	}
	return pattern
}

// matchFailed ends the run after regexp2 failed a match, mostly because it timed out. goja
// reported no match before, which a script cannot tell from a real one. regexp2's message of a
// timeout carries the whole input and is left out.
func (r *regexp2Wrapper) matchFailed(err error) {
	if strings.HasPrefix(err.Error(), "match timeout") {
		panic(&RegExpTimeoutError{pattern: r.rx.String(), timeout: r.rx.MatchTimeout})
	}
	panic(&RegExpMatchError{pattern: r.rx.String(), err: err.Error()})
}
