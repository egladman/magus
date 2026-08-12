package vm

import (
	"log/slog"
	"sync/atomic"
)

// Logging, scoped deliberately to the FFI/zdef boundary.
//
// gopherbuzz is a library, so it has no opinion about where output goes and emits
// nothing until an embedder asks. Two consequences shape everything here:
//
//   - Nothing is logged at INFO or above. Choosing what a program's operator sees
//     is the HOST's editorial call, and a library that logs at INFO has made it for
//     them. Errors are returned, not logged: logging one this code also returns
//     reports it twice.
//   - The scope is the FFI boundary only -- layout decisions, and the alloc/free
//     lifecycle. Not the VM dispatch loop, which already has the nil-gated observer
//     seam, heap profiling and JIT counters, and where a per-instruction check would
//     be a real cost. The FFI boundary earns it because it is where a wrong answer
//     is silent: a bad struct offset marshals plausible garbage rather than raising,
//     and there is nothing to inspect after the fact.
//
// slog has no TRACE, so LevelTrace below is the customary negative offset. The
// layout and lifecycle records use it because one zdef-heavy program emits a great
// many of them; the zdef bind itself is DEBUG, being once per call.

// LevelTrace is the level for per-operation FFI records: struct and union layout
// decisions, and each alloc and free. It sits below slog.LevelDebug because a
// program that marshals in a loop emits one per operation, which is too much for a
// host that merely turned debug logging on.
const LevelTrace = slog.LevelDebug - 4

// ffiLogger is the embedder-installed logger, nil until SetFFILogger is called.
// Package-level rather than per-VM because the functions that report -- the layout
// calculators and the allocator -- are package-level and reachable with no VM in
// hand. Atomic because an embedder may install one while other goroutines are
// already running Buzz.
var ffiLogger atomic.Pointer[slog.Logger]

// SetFFILogger installs the logger the FFI boundary reports to. Passing nil
// restores the default of emitting nothing. See the package comment above for what
// is reported and at which levels; note that nothing is emitted at INFO or above.
func SetFFILogger(l *slog.Logger) { ffiLogger.Store(l) }

// ffiLog reports one FFI event, and is a no-op when no logger is installed or the
// installed one is not enabled for level. Callers must pass attrs rather than
// interpolating into msg: msg stays a constant so records group, and the values
// stay queryable.
func ffiLog(level slog.Level, msg string, attrs ...slog.Attr) {
	l := ffiLogger.Load()
	if l == nil || !l.Enabled(nil, level) {
		return
	}
	l.LogAttrs(nil, level, msg, attrs...)
}

// ffiLogEnabled reports whether an FFI record at level would be emitted. Call it
// before building attrs that cost something to assemble -- a layout's offset slice
// is the case that matters -- so an unobserved run pays nothing for them.
func ffiLogEnabled(level slog.Level) bool {
	l := ffiLogger.Load()
	return l != nil && l.Enabled(nil, level)
}
