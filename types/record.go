package types

import "context"

type recordKey struct{}

// WithRecord marks ctx as a dry-run recording context. Effectful host operations
// (subprocess exec, filesystem writes, network requests, environment mutation) detect
// it via Recording and record their intent then return a benign result instead of
// performing the side effect, so a dry run never touches the system. Reads are left
// alone, so a dry run can still inspect the workspace to compute its plan.
//
// Caveat: because the magusfile body still evaluates, a target that branches on a
// command's output or a network response sees a stubbed (empty) result, so the
// recorded plan can diverge from a real run. That is inherent to dry-run-by-evaluation.
func WithRecord(ctx context.Context) context.Context {
	return context.WithValue(ctx, recordKey{}, true)
}

// Recording reports whether ctx is a dry-run recording context (see WithRecord).
func Recording(ctx context.Context) bool {
	on, _ := ctx.Value(recordKey{}).(bool)
	return on
}
