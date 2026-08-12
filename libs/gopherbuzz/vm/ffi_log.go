package vm

import (
	"context"
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
// slog has no TRACE, so FFILevelTrace below is the customary negative offset. The
// layout and lifecycle records use it because one zdef-heavy program emits a great
// many of them; the zdef bind itself is DEBUG, being once per call.

// FFILevelTrace is the level for per-operation FFI records: struct and union layout
// decisions, and each alloc and free. It sits below slog.LevelDebug because a
// program that marshals in a loop emits one per operation, which is too much for a
// host that merely turned debug logging on.
//
// FFI-qualified deliberately, like AllocFFI and SetFFILogger beside it: this is not
// a trace level for the vm package, and magus's own config.LevelTrace has the same
// value, so an unqualified name would need an alias wherever both are imported.
const FFILevelTrace = slog.LevelDebug - 4

// ffiLogger is the embedder-installed logger, nil until SetFFILogger is called.
// Package-level rather than per-VM because the functions that report -- the layout
// calculators and the allocator -- are package-level and reachable with no VM in
// hand. Atomic because an embedder may install one while other goroutines are
// already running Buzz.
var ffiLogger atomic.Pointer[slog.Logger]

// ffiLogCtx is the context handed to the installed handler. It is Background and not
// a caller's ctx because the reporting sites are package-level functions - the layout
// calculators and the allocator - which take none and cannot without changing their
// exported signatures. Passing a literal nil would also work, since slog substitutes
// Background, but it hides the decision. A context-aware handler therefore gets no
// correlation from this seam; wiring one is a signature change worth making
// deliberately rather than by threading a TODO through six call sites.
var ffiLogCtx = context.Background()

// SetFFILogger installs the logger the FFI boundary reports to. Passing nil restores
// the default of emitting nothing.
//
// What is reported: the zdef bind at slog.LevelDebug (once per zdef call), and struct
// and union layout decisions plus each alloc and free at FFILevelTrace (once per
// operation). NOTHING is emitted at INFO or above - choosing what an operator sees is
// the host's call - and an error this package returns is never also logged.
func SetFFILogger(l *slog.Logger) { ffiLogger.Store(l) }

// ffiLog reports one FFI event, and is a no-op when no logger is installed or the
// installed one is not enabled for level. Callers must pass attrs rather than
// interpolating into msg: msg stays a constant so records group, and the values
// stay queryable.
func ffiLog(level slog.Level, msg string, attrs ...slog.Attr) {
	l := ffiLogger.Load()
	if l == nil || !l.Enabled(ffiLogCtx, level) {
		return
	}
	l.LogAttrs(ffiLogCtx, level, msg, attrs...)
}

// ffiLogEnabled reports whether an FFI record at level would be emitted. Call it
// before building attrs that cost something to assemble -- a layout's offset slice
// is the case that matters -- so an unobserved run pays nothing for them.
func ffiLogEnabled(level slog.Level) bool {
	l := ffiLogger.Load()
	return l != nil && l.Enabled(ffiLogCtx, level)
}
