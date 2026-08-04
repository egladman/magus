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
)

// String names the fault kind for logs and metric labels (plain ASCII).
func (k FaultKind) String() string {
	switch k {
	case FaultPanic:
		return "panic"
	case FaultHostError:
		return "host-error"
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
