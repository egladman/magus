package service

import (
	"context"
	"log/slog"
	"sync"

	"github.com/egladman/magus/spells"
)

// Session is the per-run routing layer over service supervision. It acquires each
// service either from a cross-invocation host (the daemon, kept warm across runs) or
// from the in-process [Registry] (this run only), and releases everything it took
// when the run ends. It keeps this package free of any daemon/RPC dependency by
// taking the daemon acquire/release as plain closures, which the caller wires to the
// proc client.
type Session struct {
	reg *Registry // in-process host; also the fallback when no daemon is reachable

	// daemonAcquire/daemonRelease route to the cross-invocation host when non-nil;
	// nil means no daemon is reachable, so services run in-process for this run only.
	daemonAcquire func(ctx context.Context, key string, svc spells.Service) error
	daemonRelease func(key string)

	mu sync.Mutex
	// daemonKeys counts acquires per key, not just membership: the daemon-side
	// Registry ref-counts per Acquire, so two acquires of the same key in one run
	// need two releases in ReleaseAll or the ref never returns to zero.
	daemonKeys map[string]int
}

// NewSession returns a Session backed by reg. daemonAcquire/daemonRelease may be nil
// (no cross-invocation host), in which case every service runs in-process.
func NewSession(reg *Registry, daemonAcquire func(context.Context, string, spells.Service) error, daemonRelease func(string)) *Session {
	return &Session{
		reg:           reg,
		daemonAcquire: daemonAcquire,
		daemonRelease: daemonRelease,
		daemonKeys:    map[string]int{},
	}
}

// acquire starts (or reuses) the service for key, routing to the daemon when one is
// reachable, else to the in-process Registry. If the daemon acquire fails (it was
// reachable at run start but has since died or wedged) the service is hosted
// in-process for this run rather than aborting - the design's "degrade to
// per-invocation" - so a daemon hiccup does not fail an otherwise-fine run.
func (s *Session) acquire(ctx context.Context, key string, svc spells.Service) error {
	if s.daemonAcquire != nil {
		if err := s.daemonAcquire(ctx, key, svc); err != nil {
			slog.WarnContext(ctx, "magus: daemon service acquire failed; hosting in-process for this run",
				slog.String("key", key), slog.String("err", err.Error()))
			_, ierr := s.reg.Acquire(ctx, key, svc)
			return ierr
		}
		s.mu.Lock()
		s.daemonKeys[key]++
		s.mu.Unlock()
		return nil
	}
	_, err := s.reg.Acquire(ctx, key, svc)
	return err
}

// ReleaseAll releases everything the session acquired: daemon-hosted services are
// released back to the daemon (which keeps them warm and reaps them later), and the
// in-process ones are stopped. Call once at run end.
func (s *Session) ReleaseAll() {
	s.mu.Lock()
	keys := s.daemonKeys
	s.daemonKeys = map[string]int{}
	s.mu.Unlock()

	for k, n := range keys {
		for range n {
			s.daemonRelease(k)
		}
	}
	s.reg.Shutdown()
}
