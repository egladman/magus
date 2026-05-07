package cache

import "context"

// Tracer opens a child span for an internal cache phase — hashing inputs,
// replaying a hit, or snapshotting outputs. It is the cache's only window onto a
// tracing backend: the observability layer implements it and installs it on the
// run context with [ContextWithTracer], so this package keeps no OpenTelemetry
// dependency of its own. StartSpan returns a context carrying the new span and a
// func that ends it, recording err as the span's status.
type Tracer interface {
	StartSpan(ctx context.Context, name string) (context.Context, func(err error))
}

type tracerKey struct{}

// ContextWithTracer returns a copy of ctx carrying t. A nil t is stored as a
// no-op so callers can wire it through unconditionally.
func ContextWithTracer(ctx context.Context, t Tracer) context.Context {
	if t == nil {
		t = noopTracer{}
	}
	return context.WithValue(ctx, tracerKey{}, t)
}

// tracerFromContext returns the Tracer installed by [ContextWithTracer], or a
// no-op when none is present — so the build hot path never nil-checks.
func tracerFromContext(ctx context.Context) Tracer {
	if t, ok := ctx.Value(tracerKey{}).(Tracer); ok {
		return t
	}
	return noopTracer{}
}

type noopTracer struct{}

func (noopTracer) StartSpan(ctx context.Context, _ string) (context.Context, func(error)) {
	return ctx, func(error) {}
}
