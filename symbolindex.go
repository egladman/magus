package magus

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/internal/symbols"
	"github.com/egladman/magus/types"
)

// Background symbol auto-indexing keeps each symbol-capable project's SCIP index fresh
// without a manual `magus run ::scip`. It lives ONLY in the daemon (a one-shot CLI has
// no long-lived loop to schedule it) and is deliberately unobtrusive: it never runs on
// the query path, coalesces a burst of edits into one run (the quiet window), caps how
// often a project re-indexes (the min interval), dispatches only when no other work is
// running, and cancels itself the moment a user run is starved for a slot. Each run
// goes through the normal m.Run path, so it is cached and shows up as an ordinary
// journaled job - transparent, not hidden background magic.

const (
	defaultSymbolQuiet       = 60 * time.Second // sources must be this quiet before a re-index
	defaultSymbolMinInterval = 5 * time.Minute  // ceiling on how often one project re-indexes
	symbolIndexTick          = 5 * time.Second  // how often the scheduler re-evaluates
	symbolIndexBackoffBase   = 2 * time.Minute  // first backoff after a failed run (doubles, capped)
	symbolIndexBackoffMax    = 30 * time.Minute
)

// projIndexState is one project's scheduling state, guarded by symbolIndexer.mu.
type projIndexState struct {
	lastChange  time.Time // most recent source change under the project
	lastRun     time.Time // start of the most recent index run
	dirty       bool      // has changes not yet reflected in a completed run
	failures    int       // consecutive failures, for backoff
	backoffTill time.Time // do not retry before this instant
}

// symbolIndexer is the daemon's background auto-indexer. Its collaborators are injected
// as closures so the scheduling logic is testable without a live workspace, watcher, or
// run pipeline.
type symbolIndexer struct {
	log         *slog.Logger
	quiet       time.Duration
	minInterval time.Duration
	now         func() time.Time

	projectForPath func(absPath string) (string, bool)             // changed file -> owning symbol-capable project, ok
	runIndex       func(ctx context.Context, project string) error // execute the scip op for a project
	idle           func() bool                                     // true when no work runs (safe to dispatch)
	contended      func() bool                                     // true when a user run is starved (cancel in flight)
	onChange       func()                                          // fired when a capable project's sources change or an index run completes (invalidates the freshness memo); nil = no-op

	busy  atomic.Bool // an auto-index run is in flight (only one at a time)
	mu    sync.Mutex
	state map[string]*projIndexState
}

// loop runs the scheduler: it folds change batches into per-project state and, on each
// tick, dispatches at most one due project when the workspace is idle. It returns when
// ctx is cancelled or the batch channel closes (the watcher stopped).
func (si *symbolIndexer) loop(ctx context.Context, batches <-chan watch.Batch) {
	ticker := time.NewTicker(symbolIndexTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case b, ok := <-batches:
			if !ok {
				return
			}
			si.mark(b.Paths)
		case <-ticker.C:
			si.dispatchDue(ctx)
		}
	}
}

// mark folds a change batch into per-project state: each changed path under a
// symbol-capable project marks that project dirty and resets its quiet window.
func (si *symbolIndexer) mark(paths []string) {
	now := si.now()
	si.mu.Lock()
	changed := false
	for _, p := range paths {
		proj, ok := si.projectForPath(p)
		if !ok {
			continue
		}
		st := si.state[proj]
		if st == nil {
			st = &projIndexState{}
			si.state[proj] = st
		}
		st.lastChange = now
		st.dirty = true
		changed = true
	}
	si.mu.Unlock()
	// A capable project's sources changed, so its index freshness may have too - drop
	// the memo so the dashboard reflects it. Outside the lock (onChange takes its own).
	if changed {
		si.fireChange()
	}
}

// fireChange invokes onChange if set. Freshness can flip on a source edit (fresh ->
// out-of-date) or when an index run completes (out-of-date -> up-to-date), so both call it.
func (si *symbolIndexer) fireChange() {
	if si.onChange != nil {
		si.onChange()
	}
}

// dispatchDue starts an index run for one due project, but only when nothing else is
// running (idle gate) and no auto-index is already in flight. One at a time keeps the
// auto-indexer from ever being the reason the machine is busy.
func (si *symbolIndexer) dispatchDue(ctx context.Context) {
	if si.busy.Load() || !si.idle() {
		return
	}
	proj, ok := si.pickDue()
	if !ok {
		return
	}
	si.busy.Store(true)
	go si.execute(ctx, proj)
}

// pickDue returns the first project (in deterministic path order) whose changes are due
// to be indexed, or ok=false when none is.
func (si *symbolIndexer) pickDue() (string, bool) {
	now := si.now()
	si.mu.Lock()
	defer si.mu.Unlock()
	for _, proj := range slices.Sorted(maps.Keys(si.state)) {
		if dueToIndex(si.state[proj], now, si.quiet, si.minInterval) {
			return proj, true
		}
	}
	return "", false
}

// execute runs one project's scip op, cancelling if a user run becomes starved for a
// slot while it runs. lastRun and dirty are updated up front so a change landing during
// the run re-marks the project; a cancelled or failed run re-marks it dirty to retry.
func (si *symbolIndexer) execute(parent context.Context, proj string) {
	defer si.busy.Store(false)
	// Freshness may change once the run finishes (index rebuilt, or a failed attempt);
	// deferred first so it fires after the state-update lock below is released.
	defer si.fireChange()

	si.mu.Lock()
	if st := si.state[proj]; st != nil {
		// Optimistically clear dirty; a change landing during the run re-marks it. lastRun
		// is stamped only after a run that actually executes (below), NOT here - stamping
		// up front would let a yield-cancelled run throttle its own retry by minInterval.
		st.dirty = false
	}
	si.mu.Unlock()

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stop := make(chan struct{})
	defer close(stop)
	go si.yieldWatch(ctx, cancel, stop, proj)

	si.log.Debug("magus: background symbol index starting", slog.String("project", proj))
	err := si.runIndex(ctx, proj)

	si.mu.Lock()
	defer si.mu.Unlock()
	st := si.state[proj]
	if st == nil {
		return
	}
	if err != nil && ctx.Err() != nil {
		// Cancelled to yield to user work: the run never completed, so leave lastRun
		// alone (minInterval measures from the last real run) and re-mark dirty to retry
		// the next idle window. Not a failure, no backoff.
		st.dirty = true
		si.log.Debug("magus: background symbol index yielded to user work", slog.String("project", proj))
		return
	}
	// A run that actually executed (completed or failed) stamps lastRun to throttle re-runs.
	st.lastRun = si.now()
	if err != nil {
		st.failures++
		st.dirty = true
		st.backoffTill = si.now().Add(backoffDuration(st.failures))
		// A missing indexer (scip-go not installed) lands here; the growing backoff keeps
		// it from re-failing every window instead of spamming.
		si.log.Warn("magus: background symbol index failed, backing off",
			slog.String("project", proj), slog.Int("failures", st.failures), slog.String("error", err.Error()))
		return
	}
	st.failures = 0
}

// yieldWatch cancels an in-flight run as soon as a user run is starved for a slot, so
// the auto-index never delays the user's own work. It exits when the run finishes
// (stop) or the context is already done.
func (si *symbolIndexer) yieldWatch(ctx context.Context, cancel context.CancelFunc, stop <-chan struct{}, proj string) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			if si.contended() {
				si.log.Debug("magus: yielding background symbol index to user work", slog.String("project", proj))
				cancel()
				return
			}
		}
	}
}

// dueToIndex is the pure scheduling decision: a project is due when it has unindexed
// changes, its quiet window has elapsed since the last change, the minimum interval has
// elapsed since its last run, and it is not in a failure backoff.
func dueToIndex(st *projIndexState, now time.Time, quiet, minInterval time.Duration) bool {
	if st == nil || !st.dirty {
		return false
	}
	if now.Before(st.backoffTill) {
		return false
	}
	if now.Sub(st.lastChange) < quiet {
		return false
	}
	if !st.lastRun.IsZero() && now.Sub(st.lastRun) < minInterval {
		return false
	}
	return true
}

// backoffDuration grows the retry delay exponentially with consecutive failures, capped,
// so a persistently failing project (e.g. its indexer is not installed) stops churning.
func backoffDuration(failures int) time.Duration {
	if failures < 1 {
		return 0
	}
	// Saturate before shifting: base<<4 already exceeds the cap, and a large shift would
	// otherwise wrap to a small positive value and briefly retry faster than the cap.
	if failures >= 5 {
		return symbolIndexBackoffMax
	}
	d := symbolIndexBackoffBase << (failures - 1)
	if d > symbolIndexBackoffMax {
		return symbolIndexBackoffMax
	}
	return d
}

// capableProject is a symbol-capable project paired with its absolute directory and the
// language it indexes, for matching a changed file back to its project and naming the
// indexer in a failure hint.
type capableProject struct {
	path     string // workspace-relative project path
	dir      string // absolute project directory
	language string // canonical language of the project's symbol-capable spell
}

// matchProject returns the symbol-capable project that owns absPath - the one whose
// directory is the longest path-prefix of the file - or ok=false when none does. The
// trailing-separator guard stops a project dir from claiming a sibling with a shared
// name prefix.
func matchProject(absPath string, projects []capableProject) (string, bool) {
	best, bestLen, ok := "", -1, false
	for _, c := range projects {
		if absPath == c.dir || strings.HasPrefix(absPath, c.dir+string(filepath.Separator)) {
			if len(c.dir) > bestLen {
				best, bestLen, ok = c.path, len(c.dir), true
			}
		}
	}
	return best, ok
}

// WatchSymbolIndexing starts the daemon's background symbol auto-indexer: a file watcher
// that re-runs each symbol-capable project's scip op when its sources change, throttled
// and idle-gated (see symbolIndexer). It returns a stop function; the long-lived daemon
// calls it once at startup, alongside WatchKnowledgeGraph. A no-op (never an error) when
// disabled by config or when no project is symbol-capable, so nothing is spun up need-
// lessly. A one-shot CLI never calls it and so never auto-indexes.
func (m *Magus) WatchSymbolIndexing(ctx context.Context) (func(), error) {
	scfg := m.cfg.Knowledge.SymbolIndexing
	if scfg.Disabled {
		return func() {}, nil
	}
	capable := m.symbolCapableProjects()
	if len(capable) == 0 {
		return func() {}, nil
	}
	byPath := make(map[string]capableProject, len(capable))
	for _, c := range capable {
		byPath[c.path] = c
	}

	quiet := defaultSymbolQuiet
	if scfg.QuietSeconds > 0 {
		quiet = time.Duration(scfg.QuietSeconds) * time.Second
	}
	minInterval := defaultSymbolMinInterval
	if scfg.MinIntervalSeconds > 0 {
		minInterval = time.Duration(scfg.MinIntervalSeconds) * time.Second
	}

	si := &symbolIndexer{
		log:            slog.Default(),
		quiet:          quiet,
		minInterval:    minInterval,
		now:            time.Now,
		state:          map[string]*projIndexState{},
		projectForPath: func(abs string) (string, bool) { return matchProject(abs, capable) },
		runIndex: func(ctx context.Context, project string) error {
			err := m.Run(ctx, []types.Target{{Path: project, Name: symbols.IndexOp}})
			if err == nil || ctx.Err() != nil {
				return err // a clean run, or a yield-cancel that carries no useful hint
			}
			c := byPath[project]
			return symbolRunError(types.NewProjectRef(c.path, c.dir), c.language, err)
		},
		idle:      func() bool { s := m.limiter().Snapshot(); return s.Running == 0 && s.Queued == 0 },
		contended: func() bool { return m.limiter().Snapshot().Queued > 0 },
		// This watcher is what makes the freshness memo trustworthy: it drops the memo
		// whenever a capable project's sources change or an index run finishes.
		onChange: m.symbolStatus.invalidate,
	}

	wctx, cancel := context.WithCancel(ctx)
	// BuiltinIgnore skips the cache dir (so the indexer's own output never triggers a
	// re-index loop), VCS metadata, and editor temporaries - the same filter the warm
	// graph watcher uses.
	watcher, err := watch.New(wctx, watch.WithRoot(m.Root()), watch.WithIgnore(watch.BuiltinIgnore))
	if err != nil {
		cancel()
		return func() {}, err // always return a safe-to-defer stop func, even on error
	}
	// The freshness memo is trusted only while this watcher runs (like the warm graph).
	m.symbolStatus.setWatched(true)
	go func() {
		defer watcher.Close()
		si.loop(wctx, watcher.Events())
	}()
	slog.Default().Debug("magus: background symbol auto-indexing enabled", slog.Int("projects", len(capable)))
	return func() {
		m.symbolStatus.setWatched(false)
		cancel()
	}, nil
}

// symbolCapableLanguage reports whether a project is symbol-capable - bound to a spell
// that exposes the reserved scip op - and the language of that spell. The single source
// of truth for "which projects get indexed", so the auto-indexer, ReindexSymbols, and
// status reporting cannot disagree.
func symbolCapableLanguage(p *types.Project) (string, bool) {
	for _, sp := range p.ResolvedSpells {
		if slices.Contains(sp.Targets(), symbols.IndexOp) {
			return sp.Language(), true
		}
	}
	return "", false
}

// symbolCapableProjects returns the symbol-capable projects, each with its absolute
// directory and indexed language, so a changed file can be matched back to its project
// and a failed index can name the missing indexer.
func (m *Magus) symbolCapableProjects() []capableProject {
	var out []capableProject
	for _, p := range m.All() {
		if lang, ok := symbolCapableLanguage(p); ok {
			out = append(out, capableProject{path: p.Path, dir: p.Dir, language: lang})
		}
	}
	return out
}

// symbolStatusCache memoizes SymbolIndexStatus so a dashboard status push does not
// re-stat every project's sources on each tick. Like the warm graph, the cache is
// trusted only while a watcher invalidates it (watched); without one - a one-shot CLI,
// or the daemon with auto-indexing disabled - every call recomputes, so it can never go
// stale.
type symbolStatusCache struct {
	mu      sync.Mutex
	cached  []types.SymbolIndexStatus
	valid   bool
	watched bool
}

func (c *symbolStatusCache) get() ([]types.SymbolIndexStatus, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.watched && c.valid {
		return c.cached, true
	}
	return nil, false
}

func (c *symbolStatusCache) store(v []types.SymbolIndexStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.watched { // only cache while a watcher can invalidate; otherwise stay always-fresh
		c.cached, c.valid = v, true
	}
}

// invalidate drops the memo so the next SymbolIndexStatus recomputes; called by the
// symbol-index watcher on a source change or after an index run.
func (c *symbolStatusCache) invalidate() {
	c.mu.Lock()
	c.valid, c.cached = false, nil
	c.mu.Unlock()
}

func (c *symbolStatusCache) setWatched(on bool) {
	c.mu.Lock()
	c.watched = on
	if !on {
		c.valid, c.cached = false, nil
	}
	c.mu.Unlock()
}

// SymbolIndexStatus reports, for each symbol-capable project, whether its cached SCIP
// index reflects current sources: fresh, out-of-date, or not-indexed. In the daemon it
// answers from a watcher-invalidated memo (a status push does not re-stat source trees);
// elsewhere it recomputes each call. Powers `magus status` and the dashboard.
func (m *Magus) SymbolIndexStatus(ctx context.Context) []types.SymbolIndexStatus {
	if v, ok := m.symbolStatus.get(); ok {
		return v
	}
	v := m.computeSymbolIndexStatus(ctx)
	m.symbolStatus.store(v)
	return v
}

// computeSymbolIndexStatus does the actual read-only work: an index-file existence check
// plus a Cache.Fresh probe per symbol-capable project. Sorted by project.
func (m *Magus) computeSymbolIndexStatus(ctx context.Context) []types.SymbolIndexStatus {
	cacheDir := resolveCacheDir(m.Root(), m.cfg)
	var out []types.SymbolIndexStatus
	for _, p := range m.All() {
		lang, ok := symbolCapableLanguage(p)
		if !ok {
			continue
		}
		s := types.SymbolIndexStatus{Project: types.NewProjectRef(p.Path, p.Dir), Language: lang, Freshness: types.SymbolIndexNotBuilt}
		if _, err := os.Stat(symbols.IndexPath(cacheDir, p.Dir)); err == nil {
			// The index exists; it is fresh only if the scip step would replay for the
			// current sources (a cache hit means the op would not re-run, so the index
			// is current).
			s.Freshness = types.SymbolIndexStale
			if m.cache != nil {
				if fresh, ferr := m.cache.Fresh(ctx, m.buildStep(p, symbols.IndexOp)); ferr == nil && fresh {
					s.Freshness = types.SymbolIndexFresh
				}
			}
		}
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b types.SymbolIndexStatus) int { return cmp.Compare(a.Project.Path, b.Project.Path) })
	return out
}

// ReindexSymbols runs the scip op for every symbol-capable project, refreshing each
// project's cached SCIP index. A project whose indexer is missing or fails is reported
// with an actionable install hint but does not stop the rest. It returns how many
// projects were reindexed and the joined errors. This is the manual counterpart to the
// daemon's background auto-indexer, invoked by `magus graph build`.
func (m *Magus) ReindexSymbols(ctx context.Context) (int, error) {
	capable := m.symbolCapableProjects()
	var errs []error
	done := 0
	for _, c := range capable {
		if err := m.Run(ctx, []types.Target{{Path: c.path, Name: symbols.IndexOp}}); err != nil {
			errs = append(errs, symbolRunError(types.NewProjectRef(c.path, c.dir), c.language, err))
			continue
		}
		done++
	}
	return done, errors.Join(errs...)
}

// symbolRunError wraps a failed scip run with the project (by its display name, so the
// workspace root reads as its repo name, not ".") and, when known, an actionable hint
// naming the language's indexer and where to install it.
func symbolRunError(project types.ProjectRef, language string, err error) error {
	if hint := symbols.InstallHint(language); hint != "" {
		return fmt.Errorf("%s: %w; %s", project.Display(), err, hint)
	}
	return fmt.Errorf("%s: %w", project.Display(), err)
}
