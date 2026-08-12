package vm

// FaultKind classifies a VM fault reported to a fault hook. See VM.SetFaultHook.
type FaultKind int

const (
	// FaultPanic is a Go panic recovered inside Exec: an internal VM error (a bug
	// or a malformed chunk), not a Buzz-level throw. It surfaces to the embedder as
	// "buzz: internal error".
	FaultPanic FaultKind = iota
	// FaultHostError is an error returned by a host (direct) callable, raised into
	// Buzz as a catchable throw. It fires whether or not a try/catch handler
	// ultimately catches it, but never for a control-flow sentinel (a fiber yield
	// or a context cancellation), which are not faults. See raiseHostError.
	FaultHostError
	// FaultJITCompile is a panic out of the JIT code generator: the chunk hit a
	// codegen defect and was marked permanently ineligible, so it runs interpreted.
	// It fires ONCE per chunk (the ineligible verdict is cached), and never for a
	// chunk the backend merely declines to compile - declining is routine and
	// silent, a codegen panic is a bug. See safeCompileJIT.
	FaultJITCompile
	// FaultJITBadExit is a native JIT run that returned a status, resume ip, or
	// stack height the interpreter cannot resume from. The run is discarded and the
	// chunk re-executed interpreted from the top, so the answer stays right; the
	// fault is how a miscompile becomes visible instead of silently degrading. Like
	// FaultJITCompile it fires once per chunk, which then runs interpreted. See
	// VM.jitRun.
	FaultJITBadExit
)

// String names the fault kind for logs and metric labels (plain ASCII).
func (k FaultKind) String() string {
	switch k {
	case FaultPanic:
		return "panic"
	case FaultHostError:
		return "host-error"
	case FaultJITCompile:
		return "jit-compile-fail"
	case FaultJITBadExit:
		return "jit-bad-exit"
	default:
		return "unknown"
	}
}

// StructuredError is an optional interface a host error may implement to reach a Buzz
// `catch` as a MAP rather than a flat string.
//
// Without it, `catch (e)` binds err.Error() and a magusfile can only substring-match to
// find out what went wrong - the same stringly-typed fragility that a typed error exists
// to remove. With it, the embedder decides the shape, and a caller writes
// `if (e.code == "MGS2001")`.
//
// Deliberately a map of primitives rather than a Value: this package must not require an
// embedder to build VM values, and the embedder must not need to know how a Value is
// represented. Keys map to Buzz map entries; a nil or empty map falls back to the string
// form, so implementing this badly degrades rather than breaks.
//
// The "message" key is reserved: when absent, the raiser fills it from Error(), so every
// caught value carries something printable no matter what the embedder supplied.
type StructuredError interface {
	error
	// BuzzError returns the fields to expose on the caught value.
	BuzzError() map[string]string
}

// TypedError is a StructuredError that also names the OBJECT TYPE its caught value
// should present as, so `catch (e: ffi\FFITypeMismatchError)` matches it.
//
// A plain StructuredError becomes a map, and a map satisfies no named type -
// `is` compares an object instance's definition name (see buzzIsType). Without
// this a host module could raise a richly-shaped error that no typed catch could
// ever select, which is the one thing typed errors exist for. The type is
// synthesized per raise from the field names supplied; nothing has to declare it
// in Buzz source first.
type TypedError interface {
	StructuredError
	// BuzzErrorType is the object type name a typed catch will match.
	BuzzErrorType() string
}
