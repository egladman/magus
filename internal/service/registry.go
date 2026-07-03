// Package service supervises long-running shared services and their lifecycle.
//
// The Registry is the host for Decision 2 (one instance shared across dependents)
// and Decision 3 (keep-warm teardown): it deduplicates services by a caller-chosen
// key (the service fingerprint, so two targets with the same config share one
// instance), reference-counts dependents, and keeps an instance warm for an idle
// window after its last dependent releases before reaping it. Starting and stopping
// the actual OS process is delegated to a [Runner], so the lifecycle policy is
// testable without spawning anything.
//
// This is the in-process core. Hosting a service ACROSS separate `magus run`
// invocations puts a Registry inside the daemon and drives it over RPC; that
// integration, and the cross-restart orphan reaper, layer on top of this policy.
package service

import (
	"context"
	"sync"
	"time"

	"github.com/egladman/magus/types"
)

// Runner starts and stops the OS process behind a service. The Registry owns
// lifecycle policy (dedup, ref-count, idle keep-warm); the Runner owns the process.
type Runner interface {
	// Start launches the service and returns once it is ready (Runner-defined), or
	// an error if it could not be started or never became ready.
	Start(ctx context.Context, s types.Service) (Handle, error)
	// Stop terminates a running service. It is called at most once per Handle.
	Stop(h Handle)
}

// Handle is an opaque Runner-owned reference to a running service.
type Handle any

// Registry deduplicates, ref-counts, and idle-reaps shared services. The zero
// value is not usable; construct one with [New].
type Registry struct {
	runner      Runner
	defaultIdle time.Duration
	journal     *Journal // nil for the in-process registry; set on the daemon for crash reaping

	mu      sync.Mutex
	entries map[string]*entry
}

// Option configures a [Registry].
type Option func(*Registry)

// WithJournal makes the registry persist each hosted service's stop command via j so
// a later daemon can reap orphans it left on a crash. Daemon-only; the in-process
// registry passes no journal.
func WithJournal(j *Journal) Option { return func(r *Registry) { r.journal = j } }

type entry struct {
	// ready is closed once Start has completed and handle/startErr are set. It is
	// the happens-before between the starting goroutine and every reader (other
	// acquirers, reap, Shutdown), so teardown can WAIT for an in-flight Start rather
	// than race it and orphan a just-started process. handle/startErr are written
	// before ready closes and read only after, so they need no further locking.
	ready    chan struct{}
	handle   Handle
	startErr error

	refs  int
	idle  time.Duration
	timer *time.Timer // pending idle reap while refs == 0; nil otherwise
}

// New returns a Registry that reaps a service defaultIdle after its last dependent
// releases (unless the service overrides it via Service.Idle). A defaultIdle of 0
// means reap immediately at ref-count zero (no keep-warm).
func New(runner Runner, defaultIdle time.Duration, opts ...Option) *Registry {
	r := &Registry{runner: runner, defaultIdle: defaultIdle, entries: map[string]*entry{}}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Acquire returns the shared service for key, starting it once if it is not already
// running (or is warm from a previous acquire) and incrementing its dependent
// count. Concurrent acquires of the same key share one Start (the others block on
// its completion). A failed Start is not cached: the entry is discarded so a later
// Acquire retries.
func (r *Registry) Acquire(ctx context.Context, key string, s types.Service) (Handle, error) {
	r.mu.Lock()
	if e := r.entries[key]; e != nil {
		if e.timer != nil {
			e.timer.Stop() // cancel a pending reap; this dependent keeps it warm
			e.timer = nil
		}
		e.refs++
		r.mu.Unlock()
		<-e.ready // an already-running or still-starting instance
		return e.handle, e.startErr
	}
	// This goroutine owns the start for a fresh entry.
	e := &entry{ready: make(chan struct{}), idle: r.idleFor(s), refs: 1}
	r.entries[key] = e
	r.mu.Unlock()

	// Start outside the lock so a slow readiness probe does not block other keys.
	e.handle, e.startErr = r.runner.Start(ctx, s)
	close(e.ready)
	if e.startErr != nil {
		r.mu.Lock()
		if r.entries[key] == e {
			delete(r.entries, key)
		}
		r.mu.Unlock()
		return nil, e.startErr
	}
	// Record how to stop it so a later daemon can reap it if this one crashes.
	r.journal.record(key, s.Stop)
	return e.handle, nil
}

// Release drops one dependent of key. When the last dependent releases, the service
// is kept warm for its idle window and then reaped, unless a new Acquire arrives
// first. Releasing an unknown or already-zero key is a no-op.
func (r *Registry) Release(key string) {
	r.mu.Lock()
	e := r.entries[key]
	if e == nil || e.refs == 0 {
		r.mu.Unlock()
		return
	}
	e.refs--
	if e.refs > 0 {
		r.mu.Unlock()
		return
	}
	if e.idle <= 0 {
		delete(r.entries, key)
		r.mu.Unlock()
		r.journal.forget(key)
		r.stop(e) // ref-count-zero teardown: no keep-warm
		return
	}
	e.timer = time.AfterFunc(e.idle, func() { r.reap(key, e) })
	r.mu.Unlock()
}

// reap stops and removes an idle entry, unless it was re-acquired or replaced while
// the idle timer was pending.
func (r *Registry) reap(key string, e *entry) {
	r.mu.Lock()
	if e.refs > 0 || r.entries[key] != e {
		r.mu.Unlock()
		return
	}
	delete(r.entries, key)
	r.mu.Unlock()
	r.journal.forget(key)
	r.stop(e)
}

// stop tears down one entry's process, run outside the lock so it cannot block other
// keys. It waits for the entry's Start to have completed (ready) so it never stops a
// nil handle while a fork is still in flight - the fix for the Shutdown/reap-vs-Start
// race.
func (r *Registry) stop(e *entry) {
	<-e.ready
	if e.handle != nil {
		r.runner.Stop(e.handle)
	}
}

// Shutdown reaps every service regardless of ref-count or idle window, for daemon
// teardown. It waits for any in-flight Start so no just-started process is orphaned.
// After Shutdown the Registry is empty and reusable.
func (r *Registry) Shutdown() {
	r.mu.Lock()
	victims := make([]*entry, 0, len(r.entries))
	keys := make([]string, 0, len(r.entries))
	for key, e := range r.entries {
		if e.timer != nil {
			e.timer.Stop()
		}
		victims = append(victims, e)
		keys = append(keys, key)
	}
	r.entries = map[string]*entry{}
	r.mu.Unlock()
	for i, e := range victims {
		r.journal.forget(keys[i])
		r.stop(e)
	}
}

// Held returns the number of services the registry is holding, whether running with
// live dependents or kept warm awaiting idle reap.
func (r *Registry) Held() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// idleFor resolves a service's idle window: its own Service.Idle override when it
// parses to a positive duration, else the Registry default.
func (r *Registry) idleFor(s types.Service) time.Duration {
	if s.Idle != "" {
		if d, err := time.ParseDuration(s.Idle); err == nil && d > 0 {
			return d
		}
	}
	return r.defaultIdle
}
