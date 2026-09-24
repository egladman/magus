// Package origin carries the MCP client a piece of work came from across goroutines via
// context. Lives in its own leaf package so magus.go can read it without an import cycle.
package origin

import "context"

// Client is the MCP client that triggered a piece of work, as it named itself. It is the
// host application, which types.Origin records as Host; it is not a subagent, and not the
// model driving the host.
type Client struct {
	// Name is the client identifier from the MCP initialize handshake's clientInfo, e.g.
	// "someclient/0.7.2". MCP carries no model field.
	Name string
	// UserAgent is the raw HTTP User-Agent header of the client, captured on
	// the Streamable-HTTP transport only (empty over stdio). It is a second,
	// out-of-band identity signal: some hosts encode a build or channel here
	// that clientInfo omits. It is still the host's UA, not a guaranteed model.
	UserAgent string
}

type ctxKey struct{}

// WithContext returns ctx carrying c. Retrieve with FromContext.
func WithContext(ctx context.Context, c Client) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}

// FromContext returns the Client stored by WithContext, or (zero, false).
func FromContext(ctx context.Context) (Client, bool) {
	c, ok := ctx.Value(ctxKey{}).(Client)
	return c, ok
}
