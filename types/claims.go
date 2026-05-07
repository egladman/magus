package types

import "context"

type claimsContextKey struct{}

// WithEffectiveClaims returns a context carrying the effective claims
// for the spell currently being dispatched.
func WithEffectiveClaims(ctx context.Context, claims []string) context.Context {
	return context.WithValue(ctx, claimsContextKey{}, claims)
}

// EffectiveClaimsFromContext returns the effective claims set by the
// orchestrator, or nil if none were injected.
func EffectiveClaimsFromContext(ctx context.Context) []string {
	if v, ok := ctx.Value(claimsContextKey{}).([]string); ok {
		return v
	}
	return nil
}
