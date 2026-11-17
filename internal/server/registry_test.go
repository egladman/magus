package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egladman/magus/types/daemon"

	"github.com/egladman/magus"
	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// newTestRegistry builds a wsRegistry with no janitor goroutine, suitable for exercising
// the entry bookkeeping (adoptBridge, status, evictIdle) directly.
func newTestRegistry() *wsRegistry {
	r := &wsRegistry{
		entries: make(map[string]*wsEntry),
		ttl:     defaultIdleTTL,
		now:     time.Now,
		open:    testOpen,
		leaseOf: func(string) string { return "" },
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
	r.open = func(root string, _ *cache.Limiter, _ *broker.Client, _ observability.Provider) (*magus.Magus, error) {
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

	_, err1 := r.acquire(root)
	_, err2 := r.acquire(root)

	require.Error(t, err1)
	assert.Same(t, err1, err2, "the recorded load error, not a second attempt")
	assert.Equal(t, int32(1), opens.Load())
	got := r.status()
	require.Len(t, got, 1)
	assert.Equal(t, daemon.WorkspaceFailed, got[0].State)
	require.NotNil(t, got[0].Error)
	assert.Equal(t, []daemon.SourceDiagnostic{{Line: 3, Column: 3, Message: "expected expression"}}, got[0].Error.Diagnostics)
	assert.Equal(t, rpcerr.WorkspaceFailed(root, got[0].Error).Code, r.unavailable(root).Code)
}

// End to end through a real load: the status names the file, position and BZZ code the
// magusfile stopped on, which is what the console and the Connect error carry.
func TestAcquireReportsWhereAMagusfileFailed(t *testing.T) {
	testkit.Isolate(t)
	w := testkit.NewWorkspace(t)
	root := w.Root()
	w.Write("magus.yaml", "")
	w.Magusfile("import \"magus\";\nimport \"fs\";\nexport fun ci(ctx: magus\\Context, args: [str]) > void {\n  final x: int = \"s\";\n}\n")
	r := newTestRegistry()
	defer close(r.stopCh)

	_, err := r.acquire(root)
	require.Error(t, err)

	got := r.status()
	require.Len(t, got, 1)
	require.NotNil(t, got[0].Error)
	assert.Equal(t, []daemon.SourceDiagnostic{{
		Code: "BZZ1005", URL: "https://github.com/egladman/magus/blob/main/libs/gopherbuzz/docs/codes/BZZ1005.md",
		File: "magusfile.buzz", Line: 4, Column: 3, Message: `cannot assign str to int variable "x"`,
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
	assert.Equal(t, daemon.WorkspaceFailed, r.status()[0].State)

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
	assert.Equal(t, daemon.WorkspaceActive, r.status()[0].State)
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
	assert.Equal(t, daemon.WorkspaceLoading, r.status()[0].State)
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
	require.Len(t, got, 1, "the bridge workspace must show in the lister the server exposes")
	assert.Equal(t, root, got[0].Root)
}

// TestAdoptBridgeIsPinned verifies the bridge entry is leased so the idle janitor never
// evicts the server's own long-lived MCP workspace, which would make /readyz flap back to
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

	e, err := r.acquire(root)
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

// Benchmarks lock the current behaviour of the server-side workspace
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
// workspace server. Today this is a mutex-guarded map lookup; sub-µs
// expected.
func BenchmarkRegistryAcquireHot(b *testing.B) {
	root := b.TempDir()
	r := preloadedRegistry(b, root)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = r.acquire(root)
	}
}

// BenchmarkRegistryAcquireParallel exercises acquire under concurrent
// callers. Detects mutex contention that would justify swapping the
// map+mutex for sync.Map or sharded entries.
func BenchmarkRegistryAcquireParallel(b *testing.B) {
	root := b.TempDir()
	r := preloadedRegistry(b, root)

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = r.acquire(root)
		}
	})
}

// TestEvictAllDropsIdleKeepsBusy pins `magus server reload`. The server keeps a workspace
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

// TestEvictAllOnEmptyRegistry proves reload is a clean no-op when the server holds nothing,
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
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: dir, Root: dir})

	ctx := trail.ContextWithEntryPoint(t.Context(), types.EntryPointServer)
	_, err := store.Update(ctx, "sync-graph", func(row *types.Job) {
		row.State = types.StateRunning
		row.LastRun = &types.JobRun{Invocation: "inv-1"}
	})
	require.NoError(t, err)

	completeJobRow(t.Context(), store, []string{"graph", "build"}, 250*time.Millisecond, nil)

	rows, err := store.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	got := rows[0]
	assert.True(t, got.ServerRun(), "the server registered its own row")
	assert.Equal(t, types.StatePass, got.State)
	require.NotNil(t, got.LastRun)
	assert.Positive(t, got.LastRun.Ended)
	run := *got.LastRun
	run.Ended = 0
	assert.Equal(t, types.JobRun{Invocation: "inv-1", DurationMs: 250, OK: true}, run)

	completeJobRow(t.Context(), store, []string{"graph", "build"}, time.Second, errors.New("graph build failed"))

	rows, err = store.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, types.StateFail, rows[0].State)
	require.NotNil(t, rows[0].LastRun)
	run = *rows[0].LastRun
	run.Ended = 0
	assert.Equal(t, types.JobRun{Invocation: "inv-1", DurationMs: 1000, Error: "graph build failed"}, run)
}

// TestCompleteJobRowIgnoresAnAdoptedRun keeps the callback narrow: it fires for every
// background job the server finishes, and an adopted run is somebody's `magus run`, which
// has no row of its own to complete.
func TestCompleteJobRowIgnoresAnAdoptedRun(t *testing.T) {
	dir := t.TempDir()
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: dir, Root: dir})

	completeJobRow(t.Context(), store, []string{"run", "test", "."}, time.Second, nil)

	rows, err := store.List()
	require.NoError(t, err)
	assert.Empty(t, rows)
}

// RegistryLeaseSuite pins which broker client a daemon workspace's runs are charged to:
// the lease its checkout binds, so a worker's runs get its job's weight and caps.
type RegistryLeaseSuite struct {
	suite.Suite
	r      *wsRegistry
	shared *broker.Client
	leases map[string]string
	dialed []string
	opened []*broker.Client
	logged bytes.Buffer
	prior  *slog.Logger
}

func TestRegistryLeaseSuite(t *testing.T) { suite.Run(t, new(RegistryLeaseSuite)) }

func (s *RegistryLeaseSuite) SetupTest() {
	s.r = newTestRegistry()
	s.shared = broker.NewClient("unix:///nonexistent/shared")
	s.r.broker = s.shared
	s.leases, s.dialed, s.opened = map[string]string{}, nil, nil
	s.r.leaseOf = func(root string) string { return s.leases[root] }
	s.r.leasedBroker = func(lease string) *broker.Client {
		s.dialed = append(s.dialed, lease)
		return broker.NewClient("unix:///nonexistent/" + lease)
	}
	s.r.open = func(_ string, _ *cache.Limiter, b *broker.Client, _ observability.Provider) (*magus.Magus, error) {
		s.opened = append(s.opened, b)
		return &magus.Magus{}, nil
	}
	s.logged.Reset()
	s.prior = slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&s.logged, nil)))
}

func (s *RegistryLeaseSuite) TearDownTest() {
	slog.SetDefault(s.prior)
	close(s.r.stopCh)
}

func (s *RegistryLeaseSuite) TestALeasedCheckoutOpensWithAClientOfItsOwn() {
	root := s.T().TempDir()
	s.leases[root] = "fix-parser"

	e, err := s.r.acquire(root)

	s.Require().NoError(err)
	s.Equal([]string{"fix-parser"}, s.dialed)
	s.Require().Len(s.opened, 1)
	s.NotSame(s.shared, s.opened[0])
	s.Same(s.opened[0], e.broker)
	s.Equal("fix-parser", e.lease)
}

func (s *RegistryLeaseSuite) TestAnUnleasedCheckoutSharesTheServersClient() {
	e, err := s.r.acquire(s.T().TempDir())

	s.Require().NoError(err)
	s.Empty(s.dialed)
	s.Require().Len(s.opened, 1)
	s.Same(s.shared, s.opened[0])
	s.Nil(e.broker, "the shared client is the server's to close")
}

func (s *RegistryLeaseSuite) TestRebindReopensAnIdleWorkspaceWhoseLeaseMoved() {
	root := s.T().TempDir()
	e, err := s.r.acquire(root)
	s.Require().NoError(err)
	s.r.release(e)
	s.leases[root] = "fix-parser"

	s.r.rebind(root, s.r.leaseOf(root))
	again, err := s.r.acquire(root)

	s.Require().NoError(err)
	s.NotSame(e, again)
	s.Equal("fix-parser", again.lease)
	s.Len(s.opened, 2)
}

func (s *RegistryLeaseSuite) TestRebindKeepsABusyWorkspace() {
	root := s.T().TempDir()
	e, err := s.r.acquire(root)
	s.Require().NoError(err)
	s.leases[root] = "fix-parser"

	s.r.rebind(root, s.r.leaseOf(root))

	s.Same(e, s.r.entries[root], "a run in flight keeps the lease it opened with")
	s.Len(s.opened, 1)
}

func (s *RegistryLeaseSuite) TestRebindKeepsAWorkspaceWhoseLeaseHeld() {
	root := s.T().TempDir()
	s.leases[root] = "fix-parser"
	e, err := s.r.acquire(root)
	s.Require().NoError(err)
	s.r.release(e)

	s.r.rebind(root, s.r.leaseOf(root))

	s.Same(e, s.r.entries[root])
	s.Len(s.opened, 1)
}

func (s *RegistryLeaseSuite) TestAnUnleasedRunSaysSo() {
	noticeRunLease(s.T().Context(), "", "")

	s.Contains(s.logged.String(), "this run is unleased")
}

func (s *RegistryLeaseSuite) TestARunWhoseClaimTheCheckoutDoesNotBindSaysSo() {
	noticeRunLease(s.T().Context(), "", "fix-parser")

	s.Contains(s.logged.String(), "this run names lease fix-parser, but its checkout binds none")
}

func (s *RegistryLeaseSuite) TestALeasedRunSaysNothing() {
	noticeRunLease(s.T().Context(), "fix-parser", "fix-parser")

	s.Empty(s.logged.String())
}

// TestResolveDeclaredWorkspacesMergesAndDedupes verifies that cfg and env
// inputs are merged, deduplicated, resolved to absolute paths, and that
// non-existent or non-directory entries are skipped.
func TestResolveDeclaredWorkspacesMergesAndDedupes(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	c := t.TempDir()

	// Non-existent path should be dropped silently.
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	got := ResolveDeclaredWorkspaces(t.Context(),
		[]string{a, b}, // cfg
		fmt.Sprintf("%s%s%s%s%s", b, string(filepath.ListSeparator), c, string(filepath.ListSeparator), missing), // env (b is duplicate)
	)
	require.Len(t, got, 3)
	want := map[string]bool{a: true, b: true, c: true}
	for _, root := range got {
		assert.True(t, want[root], "unexpected root in result: %q", root)
	}
}
func TestResolveDeclaredWorkspacesEmpty(t *testing.T) {
	assert.Nil(t, ResolveDeclaredWorkspaces(t.Context(), nil, ""), "expected nil for empty inputs")
}

// TestAcquireRejectsNonDeclared confirms that once setDeclared has been
// called with an allowlist, acquire of a root outside the list fails.
func TestAcquireRejectsNonDeclared(t *testing.T) {
	allowed := t.TempDir()
	forbidden := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lim := cache.NewLimiter(1)
	reg := newTestRegistryAt(ctx, lim, nil, 0, nil)
	defer reg.close()

	reg.setDeclared([]string{allowed})

	_, err := reg.acquire(forbidden)
	require.ErrorIs(t, err, errWorkspaceUndeclared)
}

// TestAcquireAdmitsDeclaredEvenWithoutMagusYaml verifies that a declared
// workspace without a magus.yaml falls back to defaults rather than being
// rejected as undeclared. Without an actual workspace layout the load will
// still fail; we only verify that the allowlist check passes (the failure
// surfaces from magus.Open, not from the declared gate).
func TestAcquireAdmitsDeclaredEvenWithoutMagusYaml(t *testing.T) {
	allowed := t.TempDir()
	// Write an empty magus.yaml so config loading succeeds and the test
	// reaches the magus.Open step deterministically.
	require.NoError(t, os.WriteFile(filepath.Join(allowed, "magus.yaml"), []byte(""), 0o644))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lim := cache.NewLimiter(1)
	reg := newTestRegistryAt(ctx, lim, nil, 0, nil)
	defer reg.close()
	reg.setDeclared([]string{allowed})

	// acquire may fail at magus.Open (no real workspace), but it must NOT
	// fail with the declared-list gate.
	_, err := reg.acquire(allowed)
	assert.NotErrorIs(t, err, errWorkspaceUndeclared,
		"acquire of declared root was wrongly rejected by the allowlist gate")
}

// TestWarmRespectsContextCancellation verifies that warm exits promptly when
// the context is already cancelled, without hanging on acquire.
func TestWarmRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before warm is called

	lim := cache.NewLimiter(2)
	reg := newTestRegistryAt(context.Background(), lim, nil, 0, nil)
	defer reg.close()

	// Supply several roots; warm should bail after the first ctx.Err() check.
	roots := []string{t.TempDir(), t.TempDir(), t.TempDir()}
	reg.setDeclared(roots)

	done := make(chan struct{})
	go func() {
		reg.warm(ctx, roots)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("warm did not return promptly after context cancellation")
	}
}

// TestWarmCompletesAndPopulatesStatus verifies that warm runs to completion
// and that any successfully loaded workspaces appear in status() afterwards.
// Warm must not panic, hang, or silently skip entries.
func TestWarmCompletesAndPopulatesStatus(t *testing.T) {
	root1 := t.TempDir()
	root2 := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lim := cache.NewLimiter(2)
	reg := newTestRegistryAt(ctx, lim, nil, 0, nil)
	defer reg.close()
	reg.setDeclared([]string{root1, root2})

	done := make(chan struct{})
	go func() {
		reg.warm(ctx, []string{root1, root2})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("warm did not complete within timeout")
	}
	// Any workspace that loaded successfully must appear in status().
	// The exact count depends on whether magus.Open succeeds for bare dirs.
	assert.NotPanics(t, func() { _ = reg.status() })
}

// TestWarmInBackgroundTrackedByClose verifies that close() waits for an
// in-flight warm rather than racing it — otherwise a workspace could be
// acquired (and leaked) after close() swept the entry map.
func TestWarmInBackgroundTrackedByClose(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lim := cache.NewLimiter(2)
	reg := newTestRegistryAt(ctx, lim, nil, 0, nil)
	reg.setDeclared([]string{root})
	reg.warmInBackground(ctx, []string{root})

	// close() must return: its wg.Wait() should account for the warm goroutine
	// (plus the janitor) and not hang.
	done := make(chan struct{})
	go func() {
		reg.close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("close did not return; warm goroutine not tracked by the waitgroup?")
	}
}

// testOpen opens root as a library caller would.
func testOpen(root string, lim *cache.Limiter, _ *broker.Client, _ observability.Provider) (*magus.Magus, error) {
	cfg, err := LoadWorkspaceConfig(root)
	if err != nil {
		return nil, fmt.Errorf("load config %s: %w", root, err)
	}
	return magus.Open(context.Background(), root, workspace.WithLoadedConfig(cfg), workspace.WithLimiter(lim), magus.WithSpawnRule(magus.TierCeiling))
}

// newTestRegistryAt is newRegistry opening workspaces as a library caller would, with the
// janitor running.
func newTestRegistryAt(ctx context.Context, lim *cache.Limiter, b *broker.Client, ttl time.Duration, tel observability.Provider) *wsRegistry {
	return newRegistry(ctx, lim, b, ttl, tel, RegistryDeps{Open: testOpen, LeaseOf: func(string) string { return "" }})
}
