package main

import (
	"context"
	"time"

	"github.com/egladman/magus/internal/service"
	"github.com/egladman/magus/types"
)

// defaultServiceIdle is how long the daemon keeps a shared service warm after its
// last dependent releases, unless the service overrides it via Service.Idle. Shorter
// than the workspace idle TTL: a shared service is cheap to restart but costly to
// leave running all day.
const defaultServiceIdle = 30 * time.Minute

// serviceHost adapts a service.Registry to proc.ServiceHost: the daemon's Acquire
// returns nothing (the client only needs to know the service is up), so the Handle
// is dropped. Release maps straight through.
type serviceHost struct{ reg *service.Registry }

func (h serviceHost) Acquire(ctx context.Context, key string, svc types.Service) error {
	_, err := h.reg.Acquire(ctx, key, svc)
	return err
}

func (h serviceHost) Release(key string) { h.reg.Release(key) }

// StopAll stops every hosted service and returns how many were stopped, leaving the
// daemon running (the registry stays usable).
func (h serviceHost) StopAll() int {
	n := h.reg.Held()
	h.reg.Shutdown()
	return n
}
