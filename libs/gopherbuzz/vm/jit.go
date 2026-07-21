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

// ResetJITStats zeroes the JIT engagement counter (test helper).
func ResetJITStats() { jitRuns.Store(0) }
