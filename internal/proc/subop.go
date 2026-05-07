package proc

import (
	"context"
	"sync/atomic"
)

// SubOp holds the current sub-operation label for an inflight request.
// Concurrent-safe; all methods are nil-safe.
type SubOp struct {
	p atomic.Pointer[string]
}

// Set records s as the current sub-operation label, or clears it when s is empty.
func (o *SubOp) Set(s string) {
	if o == nil {
		return
	}
	if s == "" {
		o.p.Store(nil)
		return
	}
	o.p.Store(&s)
}

// Load returns the current sub-operation label, or "" if none is set.
func (o *SubOp) Load() string {
	if o == nil {
		return ""
	}
	if p := o.p.Load(); p != nil {
		return *p
	}
	return ""
}

type subOpKey struct{}

// WithSubOp injects op into ctx for display in magus status.
func WithSubOp(ctx context.Context, op *SubOp) context.Context {
	return context.WithValue(ctx, subOpKey{}, op)
}

// SubOpFromContext returns the SubOp in ctx, or nil.
func SubOpFromContext(ctx context.Context) *SubOp {
	op, _ := ctx.Value(subOpKey{}).(*SubOp)
	return op
}
