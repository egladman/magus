package main

// Benchmarks lock the current behaviour of the daemon-side workspace
// registry's acquire path. The agent's roadmap explicitly called this
// out as "verify with a benchmark; if no contention, no code change".
// These benchmarks are the verification — if they ever show measurable
// contention or per-call cost above a sync.Map lookup, swap acquire's
// mutex-guarded map for an atomic pointer or sync.Map.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/egladman/magus"
)

// preloadedRegistry returns a wsRegistry with a single pre-loaded entry
// for root, bypassing the real magus.Open (which we are not measuring).
func preloadedRegistry(b *testing.B, root string) *wsRegistry {
	b.Helper()
	r := &wsRegistry{
		entries: make(map[string]*wsEntry),
		ttl:     defaultIdleTTL,
		now:     time.Now,
		stopCh:  make(chan struct{}),
	}
	e := &wsEntry{root: root, m: &magus.Magus{}}
	e.once = sync.Once{}
	// Trip the once so load() is a no-op on subsequent calls.
	e.once.Do(func() {})
	e.lastAccess.Store(time.Now().UnixNano())
	r.entries[root] = e
	return r
}

// BenchmarkRegistryAcquireHot measures the cost of acquire() when the
// workspace is already loaded — the steady-state path inside the multi-
// workspace daemon. Today this is a mutex-guarded map lookup; sub-µs
// expected.
func BenchmarkRegistryAcquireHot(b *testing.B) {
	root := b.TempDir()
	r := preloadedRegistry(b, root)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = r.acquire(ctx, root)
	}
}

// BenchmarkRegistryAcquireParallel exercises acquire under concurrent
// callers. Detects mutex contention that would justify swapping the
// map+mutex for sync.Map or sharded entries.
func BenchmarkRegistryAcquireParallel(b *testing.B) {
	root := b.TempDir()
	r := preloadedRegistry(b, root)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = r.acquire(ctx, root)
		}
	})
}
