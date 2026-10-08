// Package crash prints a hint ahead of Go's trace when a magus goroutine panics: the
// panic is a defect in magus, and the hint says where this build's code can be read.
//
// A panic exits the process from whichever goroutine raised it, so a recover in main
// sees only main's own. Every goroutine therefore starts through Go, or defers Report
// as its first statement when something else starts it (errgroup.Group.Go).
package crash

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

var (
	hint    atomic.Pointer[func() string]
	printed sync.Once
)

// SetHint installs the text Report prints. main calls it once at startup; until then
// Report prints nothing and only re-panics.
func SetHint(f func() string) { hint.Store(&f) }

// Report, deferred first in a goroutine, prints the hint to stderr when that goroutine
// panics, then re-panics with the same value so the trace and exit status stay Go's.
// The hint prints once per process however many goroutines panic together.
func Report() {
	r := recover()
	if r == nil {
		return
	}
	if f := hint.Load(); f != nil {
		printed.Do(func() { fmt.Fprint(os.Stderr, (*f)()) })
	}
	panic(r)
}

// Go starts fn on a new goroutine that reports a panic through Report. fn's arguments
// are whatever its closure captures, so a caller replacing `go f(x)` evaluates x when
// fn runs rather than at the go statement.
func Go(fn func()) {
	go func() {
		defer Report()
		fn()
	}()
}
