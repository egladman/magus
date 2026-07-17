// Package origin carries agent origin metadata across goroutines via context.
// Lives in its own leaf package so magus.go can read it without an import cycle.
package origin

import "context"

// Origin describes the agent that triggered a piece of work.
type Origin struct {
	// Agent is the client identifier captured from the MCP initialize
	// handshake's clientInfo, e.g. "claude-desktop/0.7.2". This names the
	// host application, not the model driving it - MCP carries no model field.
	Agent string
	// UserAgent is the raw HTTP User-Agent header of the client, captured on
	// the Streamable-HTTP transport only (empty over stdio). It is a second,
	// out-of-band identity signal: some hosts encode a build or channel here
	// that clientInfo omits. It is still the host's UA, not a guaranteed model.
	UserAgent string
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
