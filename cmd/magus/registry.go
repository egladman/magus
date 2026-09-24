package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	configgen "github.com/egladman/magus/internal/config/gen"
	"github.com/egladman/magus/internal/file/watch"
	activityhandler "github.com/egladman/magus/internal/handler/activity"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

const defaultIdleTTL = 6 * time.Hour

// wsEntry is one workspace the registry holds. It is LOADING until load publishes, then
// ACTIVE (m set) or FAILED (loadErr set). A failed entry stays, so status reports why, and
// is replaced by a fresh entry when a source changes (D9 in the error-model plan).
type wsEntry struct {
	once sync.Once
	root string
	// m, loadErr, failure and loadedAt are published by load under wsRegistry.mu.
	m          *magus.Magus
	loadErr    error
	failure    *types.WorkspaceFailure
	loadedAt   time.Time
	lastAccess atomic.Int64 // unix nanoseconds; updated on every acquire
	inflight   int          // in-flight dispatches holding m; guarded by wsRegistry.mu
	// gone is closed when the entry leaves the registry, stopping its source watcher.
	gone chan struct{}
}

func newEntry(root string, now time.Time) *wsEntry {
	e := &wsEntry{root: root, gone: make(chan struct{})}
	e.lastAccess.Store(now.UnixNano())
	return e
}

// openWorkspace opens root the way every registry workspace is opened.
func openWorkspace(root string, lim *cache.Limiter, budget *cache.MachineBudget, tel observability.Provider) (*magus.Magus, error) {
	cfg, err := loadWorkspaceCfg(root)
	if err != nil {
		return nil, fmt.Errorf("daemon: load config %s: %w", root, err)
	}
	// Warm daemon workspaces record OTel metrics so the /dashboard can read live
	// cache/pool/target numbers as OTLP. Every workspace shares the daemon's single
	// provider (WithProvider), so counts survive eviction and the bridge Magus reads
	// them; only if none was supplied do we build a per-workspace collector.
	metricsOpt := magus.WithMetricsCollection()
	if tel != nil {
		metricsOpt = magus.WithProvider(tel)
	}
	opts := []magus.Option{
		magus.WithLoadedConfig(cfg),
		// The version lets a load failure that looks like a stale daemon say so.
		magus.WithVersion(version),
		workspace.WithLimiter(lim),
		metricsOpt,
	}
	// The budget is held HERE, so hand it over directly: a workspace inside the
	// daemon that dialled the daemon's socket would be waiting on itself. Only when
	// there IS one: a registry built without a budget (every test that does) must
	// not hand the cache an admitter that arbitrates nothing.
	if budget != nil {
		opts = append(opts, workspace.WithMachineAdmitter(cache.LocalAdmitter{Budget: budget}))
	}
	// context.Background(): workspace goroutines must outlive individual RPC contexts.
	m, err := magus.Open(context.Background(), root, opts...)
	if err != nil {
		return nil, fmt.Errorf("daemon: open workspace %s: %w", root, err)
	}
	return m, nil
}

// load opens e once and publishes the outcome. A failure starts the watcher that retries it.
func (r *wsRegistry) load(e *wsEntry) {
	e.once.Do(func() {
		m, err := r.open(e.root, r.lim, r.budget, r.tel)
		r.mu.Lock()
		defer r.mu.Unlock()
		defer r.bump()
		if err != nil {
			e.loadErr = err
			e.failure = magus.WorkspaceLoadFailure(e.root, err)
			r.watchFailed(e)
			return
		}
		e.m = m
		e.loadedAt = r.now()
	})
}

func loadWorkspaceCfg(root string) (config.Config, error) {
	path := filepath.Join(root, "magus.yaml")
	cfg, err := config.LoadFile(path, false)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			// A malformed or unreadable magus.yaml is a real error, not a
			// silent fallback to defaults (the callers wrap and surface it).
			return config.Config{}, err
		}
		// Missing file is fine; use env-var defaults.
		cfg = config.Defaults()
	}
	if err := configgen.ApplyEnv(&cfg, os.Getenv); err != nil {
		return config.Config{}, fmt.Errorf("invalid configuration from the environment: %w", err)
	}
	return cfg, nil
}

// wsRegistry lazily loads and caches workspaces; when declared is non-empty only those roots are admissible.
type wsRegistry struct {
	mu       sync.Mutex
	entries  map[string]*wsEntry
	declared map[string]struct{} // nil/empty = legacy lazy mode (any workspace admissible)
	lim      *cache.Limiter
	budget   *cache.MachineBudget   // the machine's admission budget; shared with every workspace this daemon serves
	tel      observability.Provider // shared with the bridge Magus; owned by the daemon, outlives evictions
	ttl      time.Duration
	now      func() time.Time // injectable for tests
	// open and awaitChange are seams for tests; production opens with openWorkspace and
	// waits on a filesystem watcher.
	open        func(root string, lim *cache.Limiter, budget *cache.MachineBudget, tel observability.Provider) (*magus.Magus, error)
	awaitChange func(e *wsEntry) bool
	// changed is closed and replaced whenever an entry's state moves; guarded by mu.
	changed chan struct{}
	ctx     context.Context
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

func newWSRegistry(ctx context.Context, lim *cache.Limiter, budget *cache.MachineBudget, ttl time.Duration, tel observability.Provider) *wsRegistry {
	if ttl <= 0 {
		ttl = defaultIdleTTL
	}
	r := &wsRegistry{
		entries: make(map[string]*wsEntry),
		lim:     lim,
		budget:  budget,
		tel:     tel,
		ttl:     ttl,
		now:     time.Now,
		open:    openWorkspace,
		changed: make(chan struct{}),
		ctx:     ctx,
		stopCh:  make(chan struct{}),
	}
	r.awaitChange = r.awaitSourceChange
	r.wg.Add(1)
	go r.janitor(ctx)
	return r
}

// resolveDeclaredWorkspaces merges cfg.Daemon.Workspaces and MAGUS_DAEMON_WORKSPACES into absolute paths.
func resolveDeclaredWorkspaces(cfgList []string, envVal string) []string {
	var raw []string
	raw = append(raw, cfgList...)
	for _, p := range filepath.SplitList(envVal) {
		if p != "" {
			raw = append(raw, p)
		}
	}
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		abs, err := filepath.Abs(p)
		if err != nil {
			slog.Warn("daemon: skipping declared workspace (cannot resolve absolute path)",
				"path", p, "err", err)
			continue
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		if st, err := os.Stat(abs); err != nil || !st.IsDir() {
			slog.Warn("daemon: skipping declared workspace (not a directory)",
				"path", abs)
			continue
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
	}
	return out
}

// setDeclared records the explicit workspace allowlist; empty keeps legacy lazy mode.
func (r *wsRegistry) setDeclared(roots []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(roots) == 0 {
		r.declared = nil
		return
	}
	r.declared = make(map[string]struct{}, len(roots))
	for _, root := range roots {
		r.declared[root] = struct{}{}
	}
}

// preloadAndApplySandbox unions policies for all declared workspaces and applies landlock once.
// The policy assembly/application lives behind the public library seam so the CLI
// does not reach into internal/sandbox directly (CRIT-6).
func (*wsRegistry) preloadAndApplySandbox(ctx context.Context, roots []string) error {
	return magus.ApplyUnionSandbox(ctx, roots)
}

// acquire loads the workspace for root and takes an in-flight lease so evictIdle/close
// won't Close it underneath the caller. Caller must release(e) when done. Rejects
// undeclared roots in declared mode. Takes no context: load's own r.open seam has no ctx
// parameter to bound, so a caller cancelling mid-load could not shorten this call anyway.
func (r *wsRegistry) acquire(root string) (*wsEntry, error) {
	r.mu.Lock()
	if r.declared != nil {
		if _, ok := r.declared[root]; !ok {
			r.mu.Unlock()
			return nil, fmt.Errorf("%w: workspace %q is not in this daemon's declared list; add it to daemon.workspaces (magus.yaml) or MAGUS_DAEMON_WORKSPACES and restart the daemon",
				types.DiagnosticErrorf(types.SandboxPolicyMismatch, "workspace not declared"),
				root)
		}
	}
	e, ok := r.entries[root]
	if !ok {
		e = newEntry(root, r.now())
		r.entries[root] = e
	}
	r.mu.Unlock()

	r.load(e)
	// Lease under the same lock evictIdle/close use, so it can't be torn down here.
	r.mu.Lock()
	defer r.mu.Unlock()
	if e.loadErr != nil {
		// The entry stays FAILED: reopening the same bytes per request cannot succeed, and
		// its watcher retries once a source changes. The error is the load's own, so a
		// delegated run reads exactly as a local one.
		return nil, e.loadErr
	}
	e.lastAccess.Store(r.now().UnixNano())
	e.inflight++
	return e, nil
}

// bump wakes everyone waiting on a state change. Callers hold mu.
func (r *wsRegistry) bump() {
	if r.changed != nil {
		close(r.changed)
	}
	r.changed = make(chan struct{})
}

// drop removes e from the registry and stops its watcher. Callers hold mu.
func (r *wsRegistry) drop(e *wsEntry) {
	if cur, ok := r.entries[e.root]; ok && cur == e {
		delete(r.entries, e.root)
	}
	if e.gone != nil {
		select {
		case <-e.gone:
		default:
			close(e.gone)
		}
	}
}

// watchFailed retries e's load once a workspace source changes. Callers hold mu.
func (r *wsRegistry) watchFailed(e *wsEntry) {
	select {
	case <-r.stopCh:
		return
	default:
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		if r.awaitChange(e) {
			r.retry(e)
		}
	}()
}

// retry replaces the failed entry e with a fresh one, keeping its pin, and loads it. A
// failure again leaves the fresh entry FAILED with a watcher of its own.
func (r *wsRegistry) retry(e *wsEntry) {
	r.mu.Lock()
	if cur, ok := r.entries[e.root]; !ok || cur != e {
		r.mu.Unlock()
		return
	}
	fresh := newEntry(e.root, r.now())
	fresh.inflight = e.inflight
	r.drop(e)
	r.entries[e.root] = fresh
	r.bump()
	r.mu.Unlock()
	r.load(fresh)
}

// awaitSourceChange blocks until a file a workspace load reads changes under e.root, and
// reports false when e left the registry or the daemon is stopping first.
func (r *wsRegistry) awaitSourceChange(e *wsEntry) bool {
	ctx := r.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	w, err := watch.New(ctx, watch.WithRoot(e.root),
		watch.WithIgnore(watch.RelativeIgnore(e.root, watch.BuiltinIgnore)))
	if err != nil {
		slog.WarnContext(ctx, "daemon: cannot watch a failed workspace; it stays failed until `magus server reload`",
			slog.String("root", e.root), slog.String("error", err.Error()))
		return false
	}
	defer func() { _ = w.Close() }()
	for {
		select {
		case <-r.stopCh:
			return false
		case <-ctx.Done():
			return false
		case <-e.gone:
			return false
		case b, ok := <-w.Events():
			if !ok {
				return false
			}
			if slices.ContainsFunc(b.Paths, isWorkspaceSource) {
				return true
			}
		}
	}
}

// isWorkspaceSource reports whether a load reads path: a Buzz file (magusfile, spell or
// import) or the workspace config.
func isWorkspaceSource(path string) bool {
	return filepath.Ext(path) == ".buzz" || filepath.Base(path) == "magus.yaml"
}

// failBridge records the daemon's own workspace as FAILED with err, pinned like an adopted
// bridge, so status reports it and its watcher retries it.
func (r *wsRegistry) failBridge(root string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[root]; ok {
		e.inflight++ // pin whatever is there; awaitActive picks it up
		return
	}
	e := newEntry(root, r.now())
	e.once.Do(func() {})
	e.loadErr = err
	e.failure = magus.WorkspaceLoadFailure(root, err)
	e.inflight = 1
	r.entries[root] = e
	r.watchFailed(e)
	r.bump()
}

// awaitActive blocks until root is ACTIVE and returns its workspace, or nil once ctx ends.
func (r *wsRegistry) awaitActive(ctx context.Context, root string) *magus.Magus {
	for {
		r.mu.Lock()
		if e, ok := r.entries[root]; ok && e.m != nil {
			r.mu.Unlock()
			return e.m
		}
		ch := r.changed
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil
		case <-ch:
		}
	}
}

// unavailable is what a call needing root answers while root is not ACTIVE: FAILED with
// the recorded failure, else LOADING.
func (r *wsRegistry) unavailable(root string) rpcerr.Error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[root]; ok && e.failure != nil {
		return rpcerr.WorkspaceFailed(root, e.failure)
	}
	return rpcerr.WorkspaceLoading(root)
}

// release drops one in-flight lease taken by acquire.
func (r *wsRegistry) release(e *wsEntry) {
	r.mu.Lock()
	e.inflight--
	r.mu.Unlock()
}

// warmInBackground launches warm in a goroutine tracked by the WaitGroup so close() blocks until done.
func (r *wsRegistry) warmInBackground(ctx context.Context, roots []string) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.warm(ctx, roots)
	}()
}

// warm eagerly acquires all declared workspaces so readiness probes are meaningful before client traffic.
func (r *wsRegistry) warm(ctx context.Context, roots []string) {
	for _, root := range roots {
		select {
		case <-r.stopCh:
			return
		case <-ctx.Done():
			return
		default:
		}
		e, err := r.acquire(root)
		if err != nil {
			slog.WarnContext(ctx, "daemon: warm workspace failed (readiness probe may be delayed)",
				"root", root, "err", err)
			continue
		}
		r.release(e) // warm only triggers the load; it does not hold the workspace
	}
}

// dispatch acquires the workspace for root, injects it into ctx, and forwards the work. An
// adopted run (run/affected) goes to dispatchAdopted; a background job (proc.SubmitJob, marked
// on ctx) goes to dispatchJob, which admits the wider maintenance command set. Both reuse the
// warm workspace via withMagus.
func (r *wsRegistry) dispatch(ctx context.Context, root string, rc runConfig, args []string) error {
	e, err := r.acquire(root)
	if err != nil {
		return err
	}
	defer r.release(e) // hold the lease for the whole build
	ctx = withMagus(ctx, e.m)
	if proc.IsJob(ctx) {
		return dispatchJob(trail.ContextWithEntryPoint(ctx, types.EntryPointDaemon), root, rc, args)
	}
	// An adopted run is a CLI invocation the daemon executes on the client's behalf.
	return dispatchAdopted(trail.ContextWithEntryPoint(ctx, types.EntryPointCLI), root, rc, args)
}

// recordJobActivity appends a KIND_JOB event to the daemon-wide activity trail after a background
// job (reindex, graph build, VCS refresh) completes. It is the proc OnJobDone callback. The event
// carries the job's workspace root (resolved from the context the same way the run handler does,
// since the context holds the caller's cwd, not necessarily the root) so the single trail stays
// disambiguated. Best-effort: an unresolvable root or an unset trail base (bridge not up) drops
// the record. Ts is the job's start, completion minus its measured duration.
func recordJobActivity(ctx context.Context, args []string, dur time.Duration, err error) {
	base := daemonTrailBase
	if base == "" {
		return
	}
	root := proc.RootFromContext(ctx)
	if root == "" {
		resolved, ferr := magus.FindRoot(proc.CwdFromContext(ctx))
		if ferr != nil {
			return
		}
		root = resolved
	}
	ev := trail.Event{
		Ts:         time.Now().Add(-dur).UnixMilli(),
		Kind:       trail.KindJob,
		Origin:     types.Origin{EntryPoint: types.EntryPointDaemon},
		Workspace:  root,
		Action:     job.ActionString(args),
		Outcome:    trail.OutcomeOK,
		DurationMs: dur.Milliseconds(),
	}
	if err != nil {
		ev.Outcome = trail.OutcomeError
		ev.Error = err.Error()
	}
	trail.Append(ctx, base, ev)
	completeJobRow(ctx, args, dur, err)
}

// daemonJobStore is the daemon's ONE job store, published beside daemonTrailBase and for
// the same reason: this callback needs it, and a second Store over one file would hold its
// own mutex and serialize against nothing.
var daemonJobStore *job.Store

// completeJobRow finishes a catalog job's row: where it now stands, what the run cost, and
// whether it worked. The invocation id is not here to record (this callback is handed argv,
// duration and error only), so it merges into the row the submit left.
//
// Best-effort, like the trail append above it. The store refuses a write from a checkout
// bound to a lease, and background maintenance must not fail because a worker holds this one.
func completeJobRow(ctx context.Context, args []string, dur time.Duration, jobErr error) {
	if daemonJobStore == nil {
		return
	}
	i := slices.IndexFunc(job.All(), func(j job.CatalogEntry) bool { return slices.Equal(j.Argv, args) })
	if i < 0 {
		return // an adopted run rather than one of the daemon's own, so there is no row
	}
	catalog := job.All()[i]
	if _, err := daemonJobStore.Update(ctx, catalog.Name, func(row *types.Job) {
		row.Holder = types.HolderDaemon
		row.Criteria = catalog.Desc
		row.State = types.StatePass
		if jobErr != nil {
			row.State = types.StateFail
		}
		if row.LastRun == nil {
			row.LastRun = &types.JobRun{}
		}
		row.LastRun.Ended = time.Now().UnixMilli()
		row.LastRun.DurationMs = dur.Milliseconds()
		row.LastRun.OK = jobErr == nil
		row.LastRun.Error = ""
		if jobErr != nil {
			row.LastRun.Error = jobErr.Error()
		}
	}); err != nil {
		slog.DebugContext(ctx, "completing the job's row failed",
			slog.String("job", catalog.Name), slog.String("error", err.Error()))
	}
}

// adoptBridge registers an already-open Magus (the daemon's bridge workspace, loaded by
// startMCPWithDaemon for MCP, health, and the warm knowledge graph) as a pinned registry
// entry for root. Without this the daemon keeps two workspace pools: the bridge that MCP
// tool calls actually use, and this registry that only adopted run/affected dispatches
// populate. The WorkspaceLister reads this registry, so /readyz reported "no workspaces
// loaded" even after a live MCP query. Adopting the bridge here unifies them: there is one
// instance per root, the lister reports the daemon's own workspace immediately, and a later
// adopted run of the same root reuses this instance instead of opening a second. The entry
// is pinned (inflight held, never released) so the idle janitor never evicts the daemon's
// long-lived MCP workspace.
func (r *wsRegistry) adoptBridge(root string, m *magus.Magus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.entries[root]; ok {
		if cur.failure == nil {
			return // an adopted run already loaded (or is loading) this root; leave it in place
		}
		r.drop(cur) // the bridge loaded where an earlier attempt failed; it supersedes that
	}
	e := newEntry(root, r.now())
	e.m, e.loadedAt = m, r.now()
	// Consume the once so a later acquire()'s load() is a no-op and returns this m,
	// rather than re-opening the workspace.
	e.once.Do(func() {})
	e.inflight = 1 // pin: the daemon owns this workspace for its whole lifetime
	r.entries[root] = e
	r.bump()
}

// status returns a snapshot of every workspace the registry holds, in whatever state, for
// the Status RPC.
func (r *wsRegistry) status() []proc.Workspace {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]proc.Workspace, 0, len(r.entries))
	for _, e := range r.entries {
		switch {
		case e.failure != nil:
			out = append(out, proc.Workspace{
				Root: e.root, State: types.WorkspaceFailed, Error: e.failure,
				LastAccess: time.Unix(0, e.lastAccess.Load()),
			})
			continue
		case e.m == nil:
			out = append(out, proc.Workspace{
				Root: e.root, State: types.WorkspaceLoading,
				LastAccess: time.Unix(0, e.lastAccess.Load()),
			})
			continue
		}
		// This workspace's cache is long-lived in the daemon, so its counters accumulate
		// across every adopted run: the live cache activity the /dashboard shows.
		st := e.m.CacheStats()
		out = append(out, proc.Workspace{
			Root:         e.root,
			State:        types.WorkspaceActive,
			LoadedAt:     e.loadedAt,
			LastAccess:   time.Unix(0, e.lastAccess.Load()),
			CacheHit:     st.Hit,
			CacheMiss:    st.Miss,
			CacheError:   st.Error,
			CacheBytes:   e.m.CacheDiskBytes(),
			CacheSavedMs: st.SavedMs,
			// Which provider is loaded, not what it can reach.
			SecretProvider: e.m.SecretProvider(),
		})
	}
	return out
}

// activityWorkspaces returns every loaded workspace paired with its cache dir: the trails the
// daemon-wide ActivityService merges. It walks the SAME entries map as status(), so the activity
// view and the status view can never disagree about which workspaces exist, and it takes the cache
// dir off the already-open Magus rather than resolving root -> cache dir a second way.
func (r *wsRegistry) activityWorkspaces() []activityhandler.Workspace {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]activityhandler.Workspace, 0, len(r.entries))
	for _, e := range r.entries {
		if e.m == nil {
			continue // still loading or failed
		}
		out = append(out, activityhandler.Workspace{Root: e.root, CacheDir: e.m.CacheDir()})
	}
	return out
}

// close stops the janitor and closes all loaded workspaces. Caller must first drain
// in-flight dispatches (proc.Server.Close waits on connWg) so no handler is using a
// workspace when it's closed here.
func (r *wsRegistry) close() {
	close(r.stopCh)
	r.wg.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.entries {
		if e.m != nil {
			_ = e.m.Close()
		}
		r.drop(e)
	}
}

// janitor periodically evicts workspaces that have been idle longer than ttl.
func (r *wsRegistry) janitor(ctx context.Context) {
	defer r.wg.Done()
	tick := time.NewTicker(r.ttl / 2)
	defer tick.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-ctx.Done():
			return
		case <-tick.C:
			r.evictIdle()
		}
	}
}

// evictAll drops every workspace not currently serving a dispatch, so the next command
// against each reopens it and re-reads its config. It returns how many were dropped and
// how many were left because a run was in flight.
//
// This is `magus server reload`. It is eviction rather than a config PATCH on purpose:
// the daemon holds open workspaces that each captured a config when they loaded, not a
// config object to overwrite, so dropping them makes the next load read magus.yaml
// through exactly the path a cold start uses, and there is no second code path that could
// disagree with it about what the file means.
//
// A busy workspace is skipped, not waited for. A run that is already underway keeps the
// config it started with, which is the correct answer rather than a limitation: swapping
// a running build's config halfway is not a reload, it is a race.
//
// A FAILED workspace, pinned or not, is loaded again at once rather than dropped: nothing
// holds it, and reload is the way out when its source watcher could not start.
func (r *wsRegistry) evictAll() (dropped, busy int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.entries {
		if e.failure != nil {
			r.wg.Add(1)
			go func() {
				defer r.wg.Done()
				r.retry(e)
			}()
			dropped++
			continue
		}
		if e.inflight > 0 {
			busy++
			continue
		}
		if e.m != nil {
			_ = e.m.Close()
		}
		r.drop(e)
		dropped++
	}
	return dropped, busy
}

func (r *wsRegistry) evictIdle() {
	cutoff := r.now().Add(-r.ttl).UnixNano()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.entries {
		if e.inflight > 0 {
			continue // never evict a workspace with an in-flight dispatch, even past its TTL
		}
		if e.lastAccess.Load() < cutoff {
			if e.m != nil {
				_ = e.m.Close()
			}
			r.drop(e)
		}
	}
}
