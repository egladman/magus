package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRegistry builds a wsRegistry with no janitor goroutine, suitable for exercising
// the entry bookkeeping (adoptBridge, status, evictIdle) directly.
func newTestRegistry() *wsRegistry {
	r := &wsRegistry{
		entries: make(map[string]*wsEntry),
		ttl:     defaultIdleTTL,
		now:     time.Now,
		open:    openWorkspace,
		changed: make(chan struct{}),
		stopCh:  make(chan struct{}),
	}
	// No filesystem watcher: a failed entry waits until it leaves or the registry stops.
	r.awaitChange = func(e *wsEntry) bool {
		select {
		case <-e.gone:
		case <-r.stopCh:
		}
		return false
	}
	return r
}

// scriptedOpens makes r.open fail until ok is closed, counting every attempt.
func scriptedOpens(r *wsRegistry, ok <-chan struct{}) *atomic.Int32 {
	var n atomic.Int32
	r.open = func(root string, _ *cache.Limiter, _ *cache.MachineBudget, _ observability.Provider) (*magus.Magus, error) {
		n.Add(1)
		select {
		case <-ok:
			return &magus.Magus{}, nil
		default:
			return nil, errors.New("magusfile: exec magusfile.buzz: buzz: line 3:3: expected expression")
		}
	}
	return &n
}

// D9: a failed workspace stays FAILED and is not reopened per request. Reopening the same
// bytes cannot succeed, and the status has to say why rather than list nothing.
func TestAcquireKeepsAFailedWorkspaceWithoutReopening(t *testing.T) {
	r := newTestRegistry()
	defer close(r.stopCh)
	opens := scriptedOpens(r, make(chan struct{}))
	root := t.TempDir()

	_, err1 := r.acquire(context.Background(), root)
	_, err2 := r.acquire(context.Background(), root)

	require.Error(t, err1)
	assert.Same(t, err1, err2, "the recorded load error, not a second attempt")
	assert.Equal(t, int32(1), opens.Load())
	got := r.status()
	require.Len(t, got, 1)
	assert.Equal(t, types.WorkspaceFailed, got[0].State)
	require.NotNil(t, got[0].Error)
	assert.Equal(t, []types.SourceDiagnostic{{Line: 3, Column: 3, Message: "expected expression"}}, got[0].Error.Diagnostics)
	assert.Equal(t, rpcerr.WorkspaceFailed(root, got[0].Error).Code, r.unavailable(root).Code)
}

// End to end through a real load: the status names the file, position and BZZ code the
// magusfile stopped on, which is what the console and the Connect error carry.
func TestAcquireReportsWhereAMagusfileFailed(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"),
		[]byte("import \"fs\";\nexport fun ci(ctx: magus\\Context, args: [str]) > void {\n  final x: int = \"s\";\n}\n"), 0o644))
	r := newTestRegistry()
	defer close(r.stopCh)

	_, err = r.acquire(context.Background(), root)
	require.Error(t, err)

	got := r.status()
	require.Len(t, got, 1)
	require.NotNil(t, got[0].Error)
	assert.Equal(t, []types.SourceDiagnostic{{
		Code: "BZZ1005", URL: "https://github.com/egladman/magus/blob/main/libs/gopherbuzz/docs/codes/BZZ1005.md",
		File: "magusfile.buzz", Line: 3, Column: 3, Message: `cannot assign str to int variable "x"`,
	}}, got[0].Error.Diagnostics)
}

// A source change is what retries a failed workspace; the retried entry keeps the pin.
func TestSourceChangeRetriesAFailedBridge(t *testing.T) {
	r := newTestRegistry()
	defer close(r.stopCh)
	ok := make(chan struct{})
	opens := scriptedOpens(r, ok)
	changed := make(chan struct{})
	r.awaitChange = func(e *wsEntry) bool {
		select {
		case <-changed:
			return true
		case <-e.gone:
			return false
		}
	}
	root := t.TempDir()
	r.failBridge(root, errors.New("[BZZ1005] cannot assign"))
	assert.Equal(t, types.WorkspaceFailed, r.status()[0].State)

	close(ok)
	close(changed)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m := r.awaitActive(ctx, root)

	require.NotNil(t, m, "the retried load became the bridge")
	assert.Equal(t, int32(1), opens.Load())
	r.mu.Lock()
	assert.Equal(t, 1, r.entries[root].inflight, "the bridge stays pinned across the retry")
	r.mu.Unlock()
	assert.Equal(t, types.WorkspaceActive, r.status()[0].State)
}

// `magus server reload` is the escape hatch when a watcher cannot start: it retries a failed
// workspace, pinned or not, instead of reporting it busy.
func TestEvictAllRetriesAFailedWorkspace(t *testing.T) {
	r := newTestRegistry()
	ok := make(chan struct{})
	opens := scriptedOpens(r, ok)
	root := t.TempDir()
	r.failBridge(root, errors.New("boom"))

	close(ok)
	dropped, busy := r.evictAll()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	assert.Equal(t, 1, dropped)
	assert.Zero(t, busy)
	assert.NotNil(t, r.awaitActive(ctx, root))
	assert.Equal(t, int32(1), opens.Load())
	close(r.stopCh)
	r.wg.Wait()
}

func TestUnavailableIsLoadingWhileALoadRuns(t *testing.T) {
	r := newTestRegistry()
	root := t.TempDir()
	r.entries[root] = newEntry(root, time.Now())

	assert.Equal(t, rpcerr.WorkspaceLoading(root).Code, r.unavailable(root).Code)
	assert.Equal(t, types.WorkspaceLoading, r.status()[0].State)
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

// Benchmarks lock the current behaviour of the daemon-side workspace
// registry's acquire path. The agent's roadmap explicitly called this
// out as "verify with a benchmark; if no contention, no code change".
// These benchmarks are the verification — if they ever show measurable
// contention or per-call cost above a sync.Map lookup, swap acquire's
// mutex-guarded map for an atomic pointer or sync.Map.

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

// TestEvictAllDropsIdleKeepsBusy pins `magus server reload`. The daemon keeps a workspace
// warm across invocations and each one captured its config when it loaded, so editing
// magus.yaml had no effect until something evicted the entry: a TTL away, and invisible.
// Reload drops them so the next command reopens and re-reads.
//
// A workspace with a run in flight is deliberately kept: swapping a running build's config
// halfway is a race, not a reload. It is reported instead, so the caller knows to re-run.
func TestEvictAllDropsIdleKeepsBusy(t *testing.T) {
	r := newTestRegistry()
	idleA, idleB, busy := t.TempDir(), t.TempDir(), t.TempDir()
	r.entries[idleA] = &wsEntry{root: idleA, m: &magus.Magus{}}
	r.entries[idleB] = &wsEntry{root: idleB, m: &magus.Magus{}}
	r.entries[busy] = &wsEntry{root: busy, m: &magus.Magus{}, inflight: 1}

	dropped, stillBusy := r.evictAll()

	assert.Equal(t, 2, dropped, "both idle workspaces are dropped so they reload")
	assert.Equal(t, 1, stillBusy, "the in-flight one is kept and reported")
	assert.NotContains(t, r.entries, idleA)
	assert.NotContains(t, r.entries, idleB)
	assert.Contains(t, r.entries, busy, "a running workspace keeps the config it started with")
}

// TestEvictAllOnEmptyRegistry proves reload is a clean no-op when the daemon holds nothing,
// which is what `magus server reload` reports rather than treating as a failure.
func TestEvictAllOnEmptyRegistry(t *testing.T) {
	dropped, busy := newTestRegistry().evictAll()

	assert.Zero(t, dropped)
	assert.Zero(t, busy)
}

// TestCompleteJobRowKeepsWhatOnlySubmitCouldRecord pins the two-writer split the row depends
// on. OnJobDone is handed argv, duration and error and no invocation, so completion has to
// MERGE into the row submit left; a completion that replaced it would drop the one field
// naming the log the run produced.
func TestCompleteJobRowKeepsWhatOnlySubmitCouldRecord(t *testing.T) {
	dir := t.TempDir()
	daemonJobStore = job.NewStore(job.Location{CacheDir: dir, Root: dir})
	t.Cleanup(func() { daemonJobStore = nil })

	_, err := daemonJobStore.Update(t.Context(), "sync-graph", func(row *types.Job) {
		row.Holder = types.HolderDaemon
		row.State = types.StateRunning
		row.LastRun = &types.JobRun{Invocation: "inv-1"}
	})
	require.NoError(t, err)

	completeJobRow(t.Context(), []string{"graph", "build"}, 250*time.Millisecond, nil)

	rows, err := daemonJobStore.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	got := rows[0]
	assert.Equal(t, "sync-graph", got.ID)
	assert.Equal(t, types.HolderDaemon, got.Holder)
	assert.Equal(t, types.StatePass, got.State)
	require.NotNil(t, got.LastRun)
	assert.Equal(t, "inv-1", got.LastRun.Invocation)
	assert.Equal(t, int64(250), got.LastRun.DurationMs)
	assert.True(t, got.LastRun.OK)
	assert.Positive(t, got.LastRun.Ended)

	completeJobRow(t.Context(), []string{"graph", "build"}, time.Second, errors.New("graph build failed"))

	rows, err = daemonJobStore.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, types.StateFail, rows[0].State)
	assert.False(t, rows[0].LastRun.OK)
	assert.Equal(t, "graph build failed", rows[0].LastRun.Error)
	assert.Equal(t, "inv-1", rows[0].LastRun.Invocation)
}

// TestCompleteJobRowIgnoresAnAdoptedRun keeps the callback narrow: it fires for every
// background job the daemon finishes, and an adopted run is somebody's `magus run`, which
// has no row of its own to complete.
func TestCompleteJobRowIgnoresAnAdoptedRun(t *testing.T) {
	dir := t.TempDir()
	daemonJobStore = job.NewStore(job.Location{CacheDir: dir, Root: dir})
	t.Cleanup(func() { daemonJobStore = nil })

	completeJobRow(t.Context(), []string{"run", "test", "."}, time.Second, nil)

	rows, err := daemonJobStore.List()
	require.NoError(t, err)
	assert.Empty(t, rows)
}
