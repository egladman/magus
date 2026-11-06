package types

import "context"

type traceKey struct{}

// WithTrace marks ctx as a dry-run tracing context. Effectful host operations
// (subprocess exec, filesystem writes, network requests, environment mutation)
// detect it via Tracing and trace their intent then return a benign result rather
// than performing the side effect, so a dry run never touches the system. Reads
// are left alone, so a dry run can still inspect the workspace to compute its plan.
//
// Caveat: because the magusfile body still evaluates, a target that branches on a
// command's output or a network response sees a stubbed (empty) result, so the
// traced plan can diverge from a real run. That is inherent to dry-run-by-evaluation.
func WithTrace(ctx context.Context) context.Context {
	return context.WithValue(ctx, traceKey{}, true)
}

// Tracing reports whether ctx is a dry-run tracing context (see WithTrace).
func Tracing(ctx context.Context) bool {
	on, _ := ctx.Value(traceKey{}).(bool)
	return on
}

// Stubbed is Tracing for an effectful host operation named op: under a trace
// the caller stubs op, and a [StepLog] on ctx notes it among its Effects.
func Stubbed(ctx context.Context, op string) bool {
	if !Tracing(ctx) {
		return false
	}
	if r := StepLogFromContext(ctx); r != nil {
		r.effect(op)
	}
	return true
}
