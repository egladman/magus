package service

import (
	"context"

	"github.com/egladman/magus/types"
)

type sessionKey struct{}
type supervisionKey struct{}

// WithSession stores the run's service [Session] on ctx so service ops reached as
// dependencies can be supervised through it (routed to the daemon or run in-process).
// Set once per run.
func WithSession(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, sessionKey{}, s)
}

func sessionFrom(ctx context.Context) *Session {
	s, _ := ctx.Value(sessionKey{}).(*Session)
	return s
}

// WithSupervision marks ctx as a scope where a service op should be supervised in
// the background (started, readiness-gated, not blocked on) rather than run in the
// foreground. Dependency dispatch sets it, so a service reached via magus.needs is
// supervised while a directly-run service target still foregrounds and blocks.
func WithSupervision(ctx context.Context) context.Context {
	return context.WithValue(ctx, supervisionKey{}, true)
}

func supervisionActive(ctx context.Context) bool {
	on, _ := ctx.Value(supervisionKey{}).(bool)
	return on
}

// TrySupervise starts (or reuses) the service for key under the run's [Session] when
// supervision is active, returning handled=true so the caller does not fork it in
// the foreground. When there is no Session or supervision is not active it returns
// handled=false (a no-op probe) and the caller runs the service inline (foreground,
// blocking) - the directly-run-service case.
func TrySupervise(ctx context.Context, key string, s types.Service) (handled bool, err error) {
	sess := sessionFrom(ctx)
	if sess == nil || !supervisionActive(ctx) {
		return false, nil
	}
	return true, sess.acquire(ctx, key, s)
}
