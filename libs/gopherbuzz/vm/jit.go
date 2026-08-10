package vm

import (
	"os"
	"sync/atomic"
)

// JIT toggle (see jit_amd64.go). ON by default; disable with BUZZ_JIT=0 (or
// "false"/"off") or SetJIT(false). No effect where no backend is compiled in.
var jitFlag atomic.Bool

func init() {
	switch os.Getenv("BUZZ_JIT") {
	case "0", "false", "off":
		jitFlag.Store(false)
	case "1", "true", "on":
		jitFlag.Store(true) // opt in even on arches whose backend defaults off
	default:
		jitFlag.Store(jitArchDefault)
	}
}

// SetJIT enables or disables the baseline JIT at runtime.
func SetJIT(on bool) { jitFlag.Store(on) }

// JITEnabled reports whether the baseline JIT is currently enabled.
func JITEnabled() bool { return jitFlag.Load() }

// jitRuns counts how many times native JIT code was entered. Used by tests to
// confirm the JIT path actually engaged (rather than silently falling back).
var jitRuns atomic.Int64

// JITRunCount returns the number of native JIT entries so far.
func JITRunCount() int64 { return jitRuns.Load() }

// jitDeopts counts how many native runs handed control back to the interpreter
// mid-chunk. Engagement alone does not prove a backend COMPILED the arithmetic in
// a program: a backend that declines a shape still enters native code and then
// deopts, so a purely run-count assertion passes either way. Tests use this to pin
// which shapes each backend actually computes natively.
var jitDeopts atomic.Int64

// JITDeoptCount returns the number of deopts back to the interpreter so far.
func JITDeoptCount() int64 { return jitDeopts.Load() }

// jitCompileFails counts chunks the code generator PANICKED on, as opposed to the
// far more common (and silent) case of a backend declining a shape it does not
// compile. Both end in the same cached "ineligible" verdict, so without this
// counter a codegen defect is indistinguishable from an unsupported opcode: the
// JIT just quietly stops engaging and nothing says why. See safeCompileJIT.
var jitCompileFails atomic.Int64

// JITCompileFailCount returns the number of chunks whose codegen panicked. Any
// non-zero value is a bug in this package, not a property of the program run.
func JITCompileFailCount() int64 { return jitCompileFails.Load() }

// jitBadExits counts native runs discarded because their exit context was not
// resumable. Same reasoning as jitCompileFails: the recovery is silent and
// correct, so this is the only evidence a miscompile happened at all.
var jitBadExits atomic.Int64

// JITBadExitCount returns the number of native runs discarded as unresumable. Any
// non-zero value is a bug in this package. Reporting it (with BUZZ_JIT=0 changing
// the answer) is the most useful JIT bug report there is.
func JITBadExitCount() int64 { return jitBadExits.Load() }

// ResetJITStats zeroes the JIT counters (test helper).
func ResetJITStats() {
	jitRuns.Store(0)
	jitDeopts.Store(0)
	jitCompileFails.Store(0)
	jitBadExits.Store(0)
}
