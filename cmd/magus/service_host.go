package main

import (
	"github.com/egladman/magus/types/daemon"
	"context"
	"time"

	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/internal/service"
)

// defaultServiceIdle is how long the broker keeps a shared service warm after its
// last dependent releases, unless the service overrides it via Service.Idle. Shorter
// than the workspace idle TTL: a shared service is cheap to restart but costly to
// leave running all day.
const defaultServiceIdle = 30 * time.Minute

// serviceHost adapts a service.Registry to broker.ServiceHost: the client only needs
// to know the service is up and whether the broker owns it, so the Handle is reduced
// to that. Release maps straight through.
type serviceHost struct{ reg *service.Registry }

func (h serviceHost) Snapshot() []daemon.Service { return serviceStatuses(h.reg) }

func (h serviceHost) Acquire(ctx context.Context, key string, spec broker.ServiceSpec) (bool, error) {
	handle, err := h.reg.Acquire(ctx, key, spec.Service())
	return err == nil && handle != service.Adopted, err
}

func (h serviceHost) Release(key string) { h.reg.Release(key) }

// StopAll stops every hosted service and returns how many were stopped, leaving the
// broker running (the registry stays usable). context.Background: the RPC handler
// that calls this has no per-request ctx to thread through today, and unlike broker
// teardown there is no already-cancelled parent ctx to work around here; Shutdown's
// own per-victim bounds (readiness timeout, stop grace) still apply regardless.
func (h serviceHost) StopAll() int {
	n := h.reg.Held()
	h.reg.Shutdown(context.Background())
	return n
}

// serviceStatuses adapts the registry's introspection snapshot (service.ServiceStatus)
// to the status-wire shape (daemon.Service) the console and the Status RPC serve.
func serviceStatuses(reg *service.Registry) []daemon.Service {
	snap := reg.Snapshot()
	out := make([]daemon.Service, 0, len(snap))
	for _, s := range snap {
		out = append(out, daemon.Service{
			ID:         s.ID,
			Label:      s.Label,
			Command:    s.Command,
			Ports:      s.Ports,
			State:      daemon.ServiceState(s.State),
			Dependents: s.Dependents,
			StartedAt:  s.StartedAt,
		})
	}
	return out
}
