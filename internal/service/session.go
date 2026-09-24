package service

import (
	"context"
	"log/slog"
	"sync"

	"github.com/egladman/magus/spells"
)

// Session is the per-run routing layer over service supervision. It acquires each
// service either from a cross-invocation host (the broker, kept warm across runs) or
// from the in-process [Registry] (this run only), and releases everything it took
// when the run ends. It keeps this package free of any RPC dependency by taking the
// host's acquire/release as plain closures, which the caller wires to the broker
// client.
type Session struct {
	reg *Registry // in-process host; also the fallback when no broker is reachable

	// brokerAcquire/brokerRelease route to the cross-invocation host when non-nil;
	// nil means no broker is reachable, so services run in-process for this run only.
	brokerAcquire func(ctx context.Context, key string, svc spells.Service) error
	// brokerRelease takes a ctx for the reason ReleaseAll does: a wedged broker socket
	// would otherwise hang every run at exit, with no bound anywhere on the path.
	brokerRelease func(ctx context.Context, key string)

	mu sync.Mutex
	// brokerKeys counts acquires per key, not just membership: the broker-side
	// Registry ref-counts per Acquire, so two acquires of the same key in one run
	// need two releases in ReleaseAll or the ref never returns to zero.
	brokerKeys map[string]int
}

// NewSession returns a Session backed by reg. brokerAcquire/brokerRelease may be nil
// (no cross-invocation host), in which case every service runs in-process.
func NewSession(reg *Registry, brokerAcquire func(context.Context, string, spells.Service) error, brokerRelease func(context.Context, string)) *Session {
	return &Session{
		reg:           reg,
		brokerAcquire: brokerAcquire,
		brokerRelease: brokerRelease,
		brokerKeys:    map[string]int{},
	}
}

// acquire starts (or reuses) the service for key, routing to the broker when one is
// reachable, else to the in-process Registry. If the broker acquire fails (it was
// reachable at run start but has since died or wedged) the service is hosted
// in-process for this run rather than aborting (the design's "degrade to
// per-invocation"), so a broker hiccup does not fail an otherwise-fine run.
func (s *Session) acquire(ctx context.Context, key string, svc spells.Service) error {
	if s.brokerAcquire != nil {
		if err := s.brokerAcquire(ctx, key, svc); err != nil {
			slog.WarnContext(ctx, "magus: the broker could not host a service; hosting it in-process for this run",
				slog.String("key", key), slog.String("err", err.Error()))
			_, ierr := s.reg.Acquire(ctx, key, svc)
			return ierr
		}
		s.mu.Lock()
		s.brokerKeys[key]++
		s.mu.Unlock()
		return nil
	}
	_, err := s.reg.Acquire(ctx, key, svc)
	return err
}

// ReleaseAll releases everything the session acquired: broker-hosted services are
// released back to the broker (which keeps them warm and reaps them later), and the
// in-process ones are stopped. Call once at run end. ctx bounds the in-process
// Shutdown; pass a ctx that can still make progress even if the run's own ctx is
// already cancelled (see [Registry.Shutdown]), since a cancelled run still has to
// release what it acquired. ctx bounds the broker releases too: it is the RPC to a
// possibly-wedged socket, so leaving it unbounded would hang teardown outright.
func (s *Session) ReleaseAll(ctx context.Context) {
	s.mu.Lock()
	keys := s.brokerKeys
	s.brokerKeys = map[string]int{}
	s.mu.Unlock()

	for k, n := range keys {
		for range n {
			s.brokerRelease(ctx, k)
		}
	}
	s.reg.Shutdown(ctx)
}
