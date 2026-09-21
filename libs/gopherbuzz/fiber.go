package buzz

import (
	"context"

	vmpackage "github.com/egladman/magus/libs/gopherbuzz/vm"
)

// Host fiber driving.
//
// A Go embedder can drive a fiber itself rather than letting Buzz's own `foreach`
// drive it. That is what makes deterministic finalization possible: `foreach`
// ABANDONS a fiber when its body breaks or throws, so the code after the fiber's
// `yield` never runs. MEASURED, and pinned by TestForeachAbandonsAFiberWhenTheBodyThrows:
// an acquire/yield/release fiber leaks on both paths. A host that owns the loop can
// finalize in a Go `defer` however the body exited, which serves the PURPOSE of Python's
// generator close() with a different mechanism: close() throws GeneratorExit at the
// suspension point, while ResolveFiber keeps running the fiber's own code to completion.
//
// These export the SAME drivers the `resume` and `resolve` keywords already bind to
// and add no behavior. Buzz semantics are untouched and no Buzz program can observe
// that they exist, so upstream parity is unaffected: this is embedding-API surface,
// the axis on which a Go implementation is expected to differ from a Zig one with a
// C API.

// ResumeFiber advances fiber to its next yield, exactly as `resume fiber` does. It
// reports the yielded value, or null when the fiber completed. A fiber that is
// already done returns null and re-surfaces its terminal error, if it had one.
//
// WARNING: the result does NOT distinguish "yielded null" from "the fiber finished".
// Both are (null, nil). That ambiguity is upstream's, carried deliberately for keyword
// parity with `resume`; the only reliable completion test is the fiber's own status,
// vm.AsFiber(v).Status() == vm.FiberDone. A loop that treats a null result as the end
// stops one iteration early against a fiber that yields null.
//
// The fiber is the argument rather than the receiver, so the name carries the noun:
// s.Resume(ctx, f) would read as resuming the session. Same shape as Session.CallValue,
// which also takes its callable as an argument.
func (s *Session) ResumeFiber(ctx context.Context, fiber vmpackage.Value) (vmpackage.Value, error) {
	return s.builtinResume(ctx, []vmpackage.Value{fiber})
}

// ResolveFiber runs fiber to completion, dismissing every yield, exactly as
// `resolve fiber` does, and reports the fiber function's return value.
//
// "Resolve" here is the Buzz keyword, not this package's module resolution
// (moduleResolver, DefaultSearchPaths), which is the one collision worth naming.
//
// This is the finalizer: calling it in a `defer` guarantees the code after a
// fiber's `yield` runs even when the body threw. Idempotent on a completed fiber,
// so finalizing one that already finished costs nothing and reports no error.
func (s *Session) ResolveFiber(ctx context.Context, fiber vmpackage.Value) (vmpackage.Value, error) {
	return s.builtinResolve(ctx, []vmpackage.Value{fiber})
}

// NewFiber builds a suspended fiber over fn, ready for ResumeFiber. It is CallValue's
// shape for a body the host intends to DRIVE rather than run to completion: same fresh
// VM per invocation, same argument convention.
//
// Nothing runs until the first resume, so a caller can arm its finalizer before any of
// the body has executed.
func (s *Session) NewFiber(ctx context.Context, fn vmpackage.Value, args []vmpackage.Value) (vmpackage.Value, error) {
	return vmpackage.NewVM(ctx).NewFiber(fn, args)
}
