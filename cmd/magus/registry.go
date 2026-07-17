package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	configgen "github.com/egladman/magus/internal/config/gen"
	"github.com/egladman/magus/internal/jobs"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

const defaultIdleTTL = 6 * time.Hour

type wsEntry struct {
	once       sync.Once
	m          *magus.Magus
	loadErr    error
	root       string
	loadedAt   time.Time
	lastAccess atomic.Int64 // unix nanoseconds; updated on every acquire
	inflight   int          // in-flight dispatches holding m; guarded by wsRegistry.mu
}

func (e *wsEntry) load(_ context.Context, lim *cache.Limiter, tel observability.Provider) {
	e.once.Do(func() {
		now := time.Now()
		e.lastAccess.Store(now.UnixNano())
		cfg, err := loadWorkspaceCfg(e.root)
		if err != nil {
			e.loadErr = fmt.Errorf("daemon: load config %s: %w", e.root, err)
			return
		}
		// Warm daemon workspaces record OTel metrics so the /dashboard can read live
		// cache/pool/target numbers as OTLP. Every workspace shares the daemon's single
		// provider (WithProvider), so counts survive eviction and the bridge Magus reads
		// them; only if none was supplied do we build a per-workspace collector.
		metricsOpt := magus.WithMetricsCollection()
		if tel != nil {
			metricsOpt = magus.WithProvider(tel)
		}
		// context.Background(): workspace goroutines must outlive individual RPC contexts.
		m, err := magus.Open(
			context.Background(), e.root,
			magus.WithLoadedConfig(cfg),
			workspace.WithLimiter(lim),
			metricsOpt,
		)
		if err != nil {
			e.loadErr = fmt.Errorf("daemon: open workspace %s: %w", e.root, err)
			return
		}
		e.m = m
		e.loadedAt = now
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
	configgen.ApplyEnv(&cfg, os.Getenv)
	return cfg, nil
}

// wsRegistry lazily loads and caches workspaces; when declared is non-empty only those roots are admissible.
type wsRegistry struct {
	mu       sync.Mutex
	entries  map[string]*wsEntry
	declared map[string]struct{} // nil/empty = legacy lazy mode (any workspace admissible)
	lim      *cache.Limiter
	tel      observability.Provider // shared with the bridge Magus; owned by the daemon, outlives evictions
	ttl      time.Duration
	now      func() time.Time // injectable for tests
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

func newWSRegistry(ctx context.Context, lim *cache.Limiter, ttl time.Duration, tel observability.Provider) *wsRegistry {
	if ttl <= 0 {
		ttl = defaultIdleTTL
	}
	r := &wsRegistry{
		entries: make(map[string]*wsEntry),
		lim:     lim,
		tel:     tel,
		ttl:     ttl,
		now:     time.Now,
		stopCh:  make(chan struct{}),
	}
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
// undeclared roots in declared mode.
func (r *wsRegistry) acquire(ctx context.Context, root string) (*wsEntry, error) {
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
		e = &wsEntry{root: root}
		r.entries[root] = e
	}
	r.mu.Unlock()

	e.load(ctx, r.lim, r.tel)
	if e.loadErr != nil {
		// Remove failed entry so it can be retried after TTL eviction.
		r.mu.Lock()
		if cur, ok := r.entries[root]; ok && cur == e {
			delete(r.entries, root)
		}
		r.mu.Unlock()
		return nil, e.loadErr
	}
	// Lease under the same lock evictIdle/close use, so it can't be torn down here.
	r.mu.Lock()
	e.lastAccess.Store(r.now().UnixNano())
	e.inflight++
	r.mu.Unlock()
	return e, nil
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
		e, err := r.acquire(ctx, root)
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
	e, err := r.acquire(ctx, root)
	if err != nil {
		return err
	}
	defer r.release(e) // hold the lease for the whole build
	ctx = withMagus(ctx, e.m)
	if proc.IsJob(ctx) {
		return dispatchJob(ctx, root, rc, args)
	}
	return dispatchAdopted(ctx, root, rc, args)
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
		Ts:        time.Now().Add(-dur).UnixMilli(),
		Kind:      trail.KindJob,
		Actor:     "daemon",
		Workspace: root,
		Action:    jobs.ActionString(args),
		Outcome:   trail.OutcomeOK,
		DurMs:     dur.Milliseconds(),
	}
	if err != nil {
		ev.Outcome = trail.OutcomeError
		ev.Error = err.Error()
	}
	trail.Append(base, ev)
}

// status returns a snapshot of loaded workspaces for the Status RPC.
func (r *wsRegistry) status() []proc.Workspace {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]proc.Workspace, 0, len(r.entries))
	for _, e := range r.entries {
		if e.m == nil {
			continue // still loading or failed
		}
		// This workspace's cache is long-lived in the daemon, so its counters accumulate
		// across every adopted run - the live cache activity the /dashboard shows.
		st := e.m.CacheStats()
		out = append(out, proc.Workspace{
			Root:       e.root,
			LoadedAt:   e.loadedAt,
			LastAccess: time.Unix(0, e.lastAccess.Load()),
			CacheHit:   st.Hit,
			CacheMiss:  st.Miss,
			CacheError: st.Error,
			CacheBytes: e.m.CacheDiskBytes(),
		})
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
	for root, e := range r.entries {
		if e.m != nil {
			_ = e.m.Close()
		}
		delete(r.entries, root)
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

func (r *wsRegistry) evictIdle() {
	cutoff := r.now().Add(-r.ttl).UnixNano()
	r.mu.Lock()
	defer r.mu.Unlock()
	for root, e := range r.entries {
		if e.inflight > 0 {
			continue // never evict a workspace with an in-flight dispatch, even past its TTL
		}
		if e.lastAccess.Load() < cutoff {
			if e.m != nil {
				_ = e.m.Close()
			}
			delete(r.entries, root)
		}
	}
}
