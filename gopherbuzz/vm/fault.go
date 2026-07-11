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
