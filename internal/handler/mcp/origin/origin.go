// Package origin carries agent origin metadata across goroutines via context.
// Lives in its own leaf package so magus.go can read it without an import cycle.
package origin

import "context"

// Origin describes the agent that triggered a piece of work.
type Origin struct {
	// Agent is the client identifier captured from the MCP initialize
	// handshake, e.g. "claude-desktop/0.7.2".
	Agent string
}

type ctxKey struct{}

// WithContext returns ctx carrying o. Retrieve with FromContext.
func WithContext(ctx context.Context, o Origin) context.Context {
	return context.WithValue(ctx, ctxKey{}, o)
}

// FromContext returns the Origin stored by WithContext, or (zero, false).
func FromContext(ctx context.Context) (Origin, bool) {
	o, ok := ctx.Value(ctxKey{}).(Origin)
	return o, ok
}
