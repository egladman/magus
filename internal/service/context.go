package service

import (
	"context"
	"sync"

	"github.com/egladman/magus/spells"
)

type sessionKey struct{}
type supervisionKey struct{}
type scopeKey struct{}

// Scope holds the service [Session] a `magus buzz` script's leases ride. The Session
// is opened on the first acquire, so a script that takes none never dials a broker.
type Scope struct {
	mu   sync.Mutex
	sess *Session
}

// WithScope stores a fresh Scope on ctx. The caller owns its end: call ReleaseAll once
// the script returns, however it returns.
func WithScope(ctx context.Context) (context.Context, *Scope) {
	sc := &Scope{}
	return context.WithValue(ctx, scopeKey{}, sc), sc
}

// ScopeFrom returns the Scope on ctx, or nil outside a script.
func ScopeFrom(ctx context.Context) *Scope {
	sc, _ := ctx.Value(scopeKey{}).(*Scope)
	return sc
}

// Session returns the scope's Session, calling open to make it on first use.
func (sc *Scope) Session(open func() *Session) *Session {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.sess == nil {
		sc.sess = open()
	}
	return sc.sess
}

// ReleaseAll releases every reference still held; see [Session.ReleaseAll] for ctx.
func (sc *Scope) ReleaseAll(ctx context.Context) {
	sc.mu.Lock()
	sess := sc.sess
	sc.mu.Unlock()
	if sess != nil {
		sess.ReleaseAll(ctx)
	}
}

// WithSession stores the run's service [Session] on ctx so service ops reached as
// dependencies can be supervised through it (routed to the broker or run in-process).
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
// blocking): the directly-run-service case.
func TrySupervise(ctx context.Context, key string, s spells.Service) (handled bool, err error) {
	sess := sessionFrom(ctx)
	if sess == nil || !supervisionActive(ctx) {
		return false, nil
	}
	_, _, err = sess.Acquire(ctx, key, s)
	return true, err
}
