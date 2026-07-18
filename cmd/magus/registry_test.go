package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/egladman/magus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRegistry builds a wsRegistry with no janitor goroutine, suitable for exercising
// the entry bookkeeping (adoptBridge, status, evictIdle) directly.
func newTestRegistry() *wsRegistry {
	return &wsRegistry{
		entries: make(map[string]*wsEntry),
		ttl:     defaultIdleTTL,
		now:     time.Now,
		stopCh:  make(chan struct{}),
	}
}

// TestAdoptBridgeReportsWorkspace pins the fix for /readyz reporting "no workspaces loaded"
// even after a live MCP query: the MCP bridge workspace and the adopted-run registry were two
// separate pools, and the WorkspaceLister only saw the latter. adoptBridge registers the
// bridge Magus into the registry the lister reads, so a workspace loaded by the MCP dispatch
// path is reported immediately, before any adopted run populates the pool.
func TestAdoptBridgeReportsWorkspace(t *testing.T) {
	r := newTestRegistry()
	root := t.TempDir()

	require.Empty(t, r.status(), "no workspaces before the bridge loads")

	r.adoptBridge(root, &magus.Magus{})

	got := r.status()
	require.Len(t, got, 1, "the bridge workspace must show in the lister the daemon exposes")
	assert.Equal(t, root, got[0].Root)
}

// TestAdoptBridgeIsPinned verifies the bridge entry is leased so the idle janitor never
// evicts the daemon's own long-lived MCP workspace, which would make /readyz flap back to
// "no workspaces loaded" after the TTL.
func TestAdoptBridgeIsPinned(t *testing.T) {
	r := newTestRegistry()
	root := t.TempDir()
	r.adoptBridge(root, &magus.Magus{})

	// Force every entry past its TTL and evict: a pinned (inflight) entry must survive.
	r.now = func() time.Time { return time.Now().Add(2 * defaultIdleTTL) }
	r.evictIdle()

	assert.Len(t, r.status(), 1, "pinned bridge workspace must not be evicted when idle")
}

// TestAdoptBridgeReusedByAcquire checks the unification claim: an adopted run for the same
// root reuses the bridge instance rather than opening a second workspace.
func TestAdoptBridgeReusedByAcquire(t *testing.T) {
	r := newTestRegistry()
	root := t.TempDir()
	bridge := &magus.Magus{}
	r.adoptBridge(root, bridge)

	e, err := r.acquire(context.Background(), root)
	require.NoError(t, err)
	defer r.release(e)
	assert.Same(t, bridge, e.m, "acquire must hand back the already-adopted bridge Magus")
}

// TestAdoptBridgeDoesNotClobberExisting ensures a workspace an adopted run already loaded is
// left in place (adoptBridge is a best-effort seed, not a replacement).
func TestAdoptBridgeDoesNotClobberExisting(t *testing.T) {
	r := newTestRegistry()
	root := t.TempDir()

	existing := &magus.Magus{}
	e := &wsEntry{root: root, m: existing}
	e.once = sync.Once{}
	e.once.Do(func() {})
	e.lastAccess.Store(time.Now().UnixNano())
	r.entries[root] = e

	r.adoptBridge(root, &magus.Magus{})
	assert.Same(t, existing, r.entries[root].m, "an existing entry must not be replaced")
}
