package magus

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/log/attr"
	procrun "github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/internal/symbols"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// Background symbol auto-indexing keeps each symbol-capable project's SCIP index fresh
// without a manual `magus run ::scip`. It lives ONLY in the server (a one-shot CLI has
// no long-lived loop to schedule it) and is deliberately unobtrusive: it never runs on
// the query path, coalesces a burst of edits into one run (the quiet window), caps how
// often a project re-indexes (the min interval), and runs one project at a time. Each run
// goes through the normal m.Run path, so it is cached and shows up as an ordinary
// journaled job: transparent, not hidden background magic.
//
// Backpressure is the LIMITER's, not this scheduler's. An index run is one queued caller
// against a FIFO-fair semaphore, so a user run behind it waits one scip op. Two attempts
// to be cleverer than that both failed: an idle gate (dispatch only when nothing runs at
// all) is an off switch in a session where something always runs, and a contention gate
// that cancelled on a non-empty queue could not tell a waiting user from its own wait for
// a slot, so on a busy pool it cancelled itself every tick and never finished an index.

const (
	// Tuned for an agent session, where a stale index is read within seconds of the edit
	// that staled it. A minute of quiet plus a five-minute ceiling meant the index was
	// almost never current while anyone was working. The quiet window costs nothing: the
	// min interval bounds how often a project re-indexes whatever it says. The min interval
	// is the expensive knob. Measured 2026-10-09 on this repository's root project, a real
	// reindex took 16s on 3-4 cores at 1.7 GiB, plus up to 11s merging the guard index,
	// so 45s keeps it near a 60% duty cycle while someone edits.
	defaultSymbolQuiet       = 3 * time.Second  // sources must be this quiet before a re-index
	defaultSymbolMinInterval = 45 * time.Second // ceiling on how often one project re-indexes
	symbolIndexTick          = 5 * time.Second  // how often the scheduler re-evaluates
	symbolIndexBackoffBase   = 2 * time.Minute  // first backoff after a failed run (doubles, capped)
	symbolIndexBackoffMax    = 30 * time.Minute
)

// indexRef names one symbol index: a project and the indexer op that writes it. A project
// bound to go and buzz holds two, scheduled, run and backed off apart, so a missing
// scip-buzz never holds back the Go index.
//
// It is the one identity an index has: the scheduler, the freshness verdicts and the
// declarations ingestion records all key on it.
type indexRef struct {
	project string // workspace-relative project path
	op      string // the indexing spell's spells.Spell.SymbolIndexOp
}

// indexState is one index's scheduling state, guarded by symbolIndexer.mu.
type indexState struct {
	lastChange  time.Time // most recent source change under the project
	lastRun     time.Time // start of the most recent index run
	dirty       bool      // has changes not yet reflected in a completed run
	failures    int       // consecutive failures, for backoff
	backoffTill time.Time // do not retry before this instant
}

// symbolIndexer is the server's background auto-indexer. Its collaborators are injected
// as closures so the scheduling logic is testable without a live workspace, watcher, or
// run pipeline.
type symbolIndexer struct {
	log         *slog.Logger
	quiet       time.Duration
	minInterval time.Duration
	now         func() time.Time

	indexesForPath func(absPath string) []indexRef                     // changed file -> the indexes of its owning symbol-capable project
	runIndex       func(ctx context.Context, index indexRef) error     // execute one index's op
	status         func(ctx context.Context) []types.SymbolIndexStatus // every index's freshness, probed now
	onChange       func()                                              // fired when a capable project's sources change or an index run completes (invalidates the freshness memo); nil = no-op

	busy  atomic.Bool // an auto-index run is in flight (only one at a time)
	mu    sync.Mutex
	state map[indexRef]*indexState
}

// loop runs the scheduler: it folds change batches into per-project state and, on each
// tick, dispatches at most one due project. It returns when ctx is cancelled or the
// watcher stops, and owns watcher and closes it.
func (si *symbolIndexer) loop(ctx context.Context, watcher *watch.Watcher) {
	defer watcher.Close()
	ticker := time.NewTicker(symbolIndexTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case b, ok := <-watcher.Events():
			if !ok {
				return
			}
			si.mark(b.Paths)
		case <-ticker.C:
			si.dispatchDue(ctx)
		}
	}
}

// mark folds a change batch into per-index state: each changed path under a
// symbol-capable project marks every index of that project dirty and resets its quiet
// window.
func (si *symbolIndexer) mark(paths []string) {
	now := si.now()
	si.mu.Lock()
	changed := false
	for _, p := range paths {
		for _, ref := range si.indexesForPath(p) {
			st := si.state[ref]
			if st == nil {
				st = &indexState{}
				si.state[ref] = st
			}
			st.lastChange = now
			st.dirty = true
			changed = true
		}
	}
	si.mu.Unlock()
	// A capable project's sources changed, so its index freshness may have too; drop
	// the memo so the dashboard reflects it. Outside the lock (onChange takes its own).
	if changed {
		si.fireChange()
	}
}

// seed marks every index si.status finds stale dirty without opening a quiet window, so
// each one is due on the first tick that the min interval and backoff allow. State is in
// memory only, so this is how an index that went stale while no server watched it gets
// reindexed before its project is next edited.
func (si *symbolIndexer) seed(ctx context.Context) {
	refs := staleIndexRefs(si.status(ctx))
	si.mu.Lock()
	defer si.mu.Unlock()
	for _, ref := range refs {
		st := si.state[ref]
		if st == nil {
			st = &indexState{}
			si.state[ref] = st
		}
		st.dirty = true
	}
}

// staleIndexRefs is the indexes a run would bring current: out of date, or never built
// with its indexer installed. A missing indexer only fails into backoff, and an
// unvouched index stays unvouched however often it runs.
func staleIndexRefs(statuses []types.SymbolIndexStatus) []indexRef {
	var out []indexRef
	for _, s := range statuses {
		if s.Freshness == types.SymbolIndexStale || (s.Freshness == types.SymbolIndexNotBuilt && s.Detail == "") {
			out = append(out, indexRef{project: s.Project.Path, op: s.Op})
		}
	}
	return out
}

// fireChange invokes onChange if set. Freshness can flip on a source edit (fresh ->
// out-of-date) or when an index run completes (out-of-date -> up-to-date), so both call it.
func (si *symbolIndexer) fireChange() {
	if si.onChange != nil {
		si.onChange()
	}
}

// dispatchDue starts an index run for one due index, unless an auto-index is in flight.
// One at a time keeps the auto-indexer from ever being the reason the machine is busy.
func (si *symbolIndexer) dispatchDue(ctx context.Context) {
	if si.busy.Load() {
		return
	}
	ref, ok := si.pickDue()
	if !ok {
		return
	}
	si.busy.Store(true)
	go si.execute(ctx, ref)
}

// pickDue returns the first index (in project, then op order) whose changes are due to be
// indexed, or ok=false when none is.
func (si *symbolIndexer) pickDue() (indexRef, bool) {
	now := si.now()
	si.mu.Lock()
	defer si.mu.Unlock()
	byPosition := func(a, b indexRef) int { return cmp.Or(cmp.Compare(a.project, b.project), cmp.Compare(a.op, b.op)) }
	for _, ref := range slices.SortedFunc(maps.Keys(si.state), byPosition) {
		if dueToIndex(si.state[ref], now, si.quiet, si.minInterval) {
			return ref, true
		}
	}
	return indexRef{}, false
}

// execute runs one index's op. lastRun and dirty are updated up front so a change landing
// during the run re-marks the index; a failed run re-marks it dirty to retry.
//
// It does NOT watch for contention and cancel itself. The limiter is already a FIFO-fair
// semaphore bounding concurrent work, so an index run is one queued caller among many and
// a user run behind it waits one scip op: under a second replayed, seconds for a real
// reindex. A second layer of backpressure on top of that read the pool's queue depth to
// decide whether to yield, and could not tell a waiting user from its OWN wait for a slot: on a saturated pool it
// dispatched, queued for itself, read that as contention, cancelled, and repeated every
// tick without ever finishing an index.
func (si *symbolIndexer) execute(ctx context.Context, ref indexRef) {
	defer si.busy.Store(false)
	// Freshness may change once the run finishes (index rebuilt, or a failed attempt);
	// deferred first so it fires after the state-update lock below is released.
	defer si.fireChange()

	si.mu.Lock()
	if st := si.state[ref]; st != nil {
		// Optimistically clear dirty; a change landing during the run re-marks it. lastRun
		// is stamped only after the run below, NOT here: a run cancelled at shutdown would
		// otherwise throttle its own retry by minInterval.
		st.dirty = false
	}
	si.mu.Unlock()

	si.log.DebugContext(ctx, "background symbol index starting", slog.String("project", ref.project), slog.String("op", ref.op))
	err := si.runIndex(ctx, ref)

	si.mu.Lock()
	defer si.mu.Unlock()
	st := si.state[ref]
	if st == nil {
		return
	}
	if err != nil && ctx.Err() != nil {
		// The server is shutting down, not a failure: leave lastRun alone and re-mark
		// dirty so the next start picks it up. No backoff.
		st.dirty = true
		return
	}
	// A run that actually executed (completed or failed) stamps lastRun to throttle re-runs.
	st.lastRun = si.now()
	if errors.Is(err, types.UndeclaredSourceModified) {
		// An edit landed while the indexer read the sources. The indexer writes none of
		// them, so this is a lost race, not a broken indexer, and backing off would keep
		// the index stale for minutes whenever someone edits during a run.
		st.dirty = true
		return
	}
	if err != nil {
		st.failures++
		st.dirty = true
		st.backoffTill = si.now().Add(backoffDuration(st.failures))
		// A missing indexer (scip-go not installed) lands here; the growing backoff keeps
		// it from re-failing every window instead of spamming.
		si.log.WarnContext(ctx, "background symbol index failed, backing off",
			slog.String("project", ref.project), slog.String("op", ref.op), slog.Int("failures", st.failures), slog.String("error", err.Error()))
		return
	}
	st.failures = 0
}

// dueToIndex is the pure scheduling decision: a project is due when it has unindexed
// changes, its quiet window has elapsed since the last change, the minimum interval has
// elapsed since its last run, and it is not in a failure backoff.
func dueToIndex(st *indexState, now time.Time, quiet, minInterval time.Duration) bool {
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

// projectIndex is one symbol index a project declares: the indexer op that writes it, the
// language it indexes and the binary the op forks, for matching a changed file back to its
// project and naming the indexer in a failure hint. A project bound to two indexing spells
// holds one per index.
type projectIndex struct {
	project  *types.Project
	op       string // the indexer op that writes this index
	language string // canonical language of the indexing spell
	bin      string // the indexer binary the op forks
}

func (idx projectIndex) ref() indexRef { return indexRef{project: idx.project.Path, op: idx.op} }

// projectRef is the project idx belongs to, as errors and refusals name it.
func (idx projectIndex) projectRef() types.ProjectRef {
	return types.NewProjectRef(idx.project.Path, idx.project.Dir)
}

// matchProject returns the symbol-capable project that owns absPath (the one whose
// directory is the longest path-prefix of the file), or ok=false when none does. The
// trailing-separator guard stops a project dir from claiming a sibling with a shared
// name prefix.
func matchProject(absPath string, idxs []projectIndex) (string, bool) {
	best, bestLen, ok := "", -1, false
	for _, idx := range idxs {
		dir := idx.project.Dir
		if absPath == dir || strings.HasPrefix(absPath, dir+string(filepath.Separator)) {
			if len(dir) > bestLen {
				best, bestLen, ok = idx.project.Path, len(dir), true
			}
		}
	}
	return best, ok
}

// WatchSymbolIndexing starts the server's background symbol auto-indexer: a file watcher
// that re-runs each symbol-capable project's indexer ops when its sources change, throttled
// and contention-gated (see symbolIndexer). It returns a stop function; the server
// calls it once at startup, alongside WatchKnowledgeGraph. A no-op (never an error) when
// disabled by config or when no project is symbol-capable, so nothing is spun up need-
// lessly. A one-shot CLI never calls it and so never auto-indexes.
func (m *Magus) WatchSymbolIndexing(ctx context.Context) (func(), error) {
	scfg := m.cfg.Knowledge.SymbolIndexing
	if scfg.Disabled {
		return func() {}, nil
	}
	capable := m.workspaceIndexes()
	if len(capable) == 0 {
		return func() {}, nil
	}
	byRef := make(map[indexRef]projectIndex, len(capable))
	refsByPath := map[string][]indexRef{}
	for _, idx := range capable {
		byRef[idx.ref()] = idx
		refsByPath[idx.project.Path] = append(refsByPath[idx.project.Path], idx.ref())
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
		log:         slog.With(attr.Component("magus")),
		quiet:       quiet,
		minInterval: minInterval,
		now:         time.Now,
		state:       map[indexRef]*indexState{},
		indexesForPath: func(abs string) []indexRef {
			project, ok := matchProject(abs, capable)
			if !ok {
				return nil
			}
			return refsByPath[project]
		},
		runIndex: func(ctx context.Context, ref indexRef) error {
			err := m.Run(ctx, []types.Target{{Path: ref.project, Name: ref.op}})
			if err == nil {
				if gerr := m.WriteGuardIndex(ctx); gerr != nil {
					slog.With(attr.Component("magus")).DebugContext(ctx, "guard index not written", slog.String("error", gerr.Error()))
				}
			}
			if err == nil || ctx.Err() != nil {
				return err // a clean run, or a yield-cancel that carries no useful hint
			}
			idx := byRef[ref]
			return symbolRunError(idx.projectRef(), idx.language, err)
		},
		status: m.SymbolIndexStatusByStamp,
		// This watcher is what makes the freshness memo trustworthy: it drops the memo
		// whenever a capable project's sources change or an index run finishes.
		onChange: m.symbolStatus.invalidate,
	}

	wctx, cancel := context.WithCancel(ctx)
	// BuiltinIgnore skips the cache dir (so the indexer's own output never triggers a
	// re-index loop), VCS metadata, and editor temporaries: the same filter the warm
	// graph watcher uses.
	watcher, err := watch.New(wctx, watch.WithRoot(m.Root()), watch.WithIgnore(watch.BuiltinIgnore))
	if err != nil {
		cancel()
		return func() {}, err // always return a safe-to-defer stop func, even on error
	}
	// The freshness memo is trusted only while this watcher runs (like the warm graph).
	m.symbolStatus.setWatched(true)
	go si.loop(wctx, watcher)
	go si.seed(wctx)
	slog.With(attr.Component("magus")).DebugContext(ctx, "background symbol auto-indexing enabled", slog.Int("projects", len(capable)))
	return func() {
		m.symbolStatus.setWatched(false)
		cancel()
	}, nil
}

// projectIndexes is the symbol indexes p declares, one per indexer op among the spells it
// binds (a spell that declares a symbol indexer), in binding order. The single source of
// truth for "which indexes get built", so the auto-indexer, ReindexSymbols, and status
// reporting cannot disagree. Two spells running under one op (go and python both declare
// none, so both run under the default) share that op's index, and the first one's
// language names it.
func projectIndexes(p *types.Project) []projectIndex {
	var out []projectIndex
	for _, sp := range p.ResolvedSpells {
		op := sp.SymbolIndexOp()
		if op == "" || slices.ContainsFunc(out, func(idx projectIndex) bool { return idx.op == op }) {
			continue
		}
		out = append(out, projectIndex{project: p, op: op, language: sp.Language(), bin: sp.SymbolIndexer().Command.Bin})
	}
	return out
}

// indexesSymbols reports whether target is one of p's indexer ops.
func indexesSymbols(p *types.Project, target string) bool {
	return target != "" && slices.ContainsFunc(p.ResolvedSpells, func(s *spells.Spell) bool { return s.SymbolIndexOp() == target })
}

// workspaceIndexes returns every symbol index the workspace's projects declare, so a
// changed file can be matched back to its project and a failed index can name the missing
// indexer.
func (m *Magus) workspaceIndexes() []projectIndex {
	ps := m.All()
	out := make([]projectIndex, 0, len(ps))
	for _, p := range ps {
		out = append(out, projectIndexes(p)...)
	}
	return out
}

// symbolStatusCache memoizes SymbolIndexStatus so a dashboard status push does not
// re-stat every project's sources on each tick. Like the warm graph, the cache is
// trusted only while a watcher invalidates it (watched); without one (a one-shot CLI,
// or the server with auto-indexing disabled) every call recomputes, so it can never go
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
// index reflects current sources: fresh, out-of-date, or not-indexed. In the server it
// answers from a watcher-invalidated memo (a status push does not re-stat source trees);
// elsewhere it recomputes each call. Powers `magus status` and the dashboard.
func (m *Magus) SymbolIndexStatus(ctx context.Context) []types.SymbolIndexStatus {
	if v, ok := m.symbolStatus.get(); ok {
		return v
	}
	v := m.SymbolIndexStatusByStamp(ctx)
	m.symbolStatus.store(v)
	return v
}

// symbolIndexStep is the cache step a run of op on p mints, so a freshness probe against
// it finds the manifest that run wrote.
//
// applyRunKeying is the whole point. buildStep alone omits the tool versions and probed
// observations the run scheduler stamps, so a probe that skipped it hashed a step no run
// had ever minted: the lookup missed every time and every built index read as
// out-of-date. Charmless because ReindexSymbols runs the op with no RunOptions.
func (m *Magus) symbolIndexStep(p *types.Project, op string, toolVersions []string, observations map[string]string) cache.Step {
	step := m.buildStep(p, op)
	applyRunKeying(&step, p, toolVersions, observationsForTarget(p, op, observations), nil)
	return step
}

// symbolIndexSources is the source globs of each of p's spells whose indexer runs as op,
// rooted at p: what buildStep keys that op on.
func symbolIndexSources(p *types.Project, op string) []types.Glob {
	var out []types.Glob
	for _, s := range p.ResolvedSpells {
		if s.SymbolIndexOp() != op {
			continue
		}
		// A spell whose globs do not parse contributed none to the project either.
		sources, _, err := types.SpellGlobs(s)
		if err != nil {
			continue
		}
		for _, g := range sources {
			out = append(out, g.Root(p.Path))
		}
	}
	return out
}

// indexerUses is the tools s's symbol indexer runs besides its own binary when target is
// s's indexer op, and nil for any other target: their versions key the index and nothing
// else.
func indexerUses(s *spells.Spell, target string) []string {
	if target == "" || target != s.SymbolIndexOp() || s.SymbolIndexer() == nil {
		return nil
	}
	return s.SymbolIndexer().Uses
}

// indexerUseKey is how targetDrivenBins marks a tool s's indexer uses: apart from the
// "spell:bin" a build op that runs the same binary is driven under, so keying a build
// never probes the indexer's view of it.
func indexerUseKey(s *spells.Spell, tool string) string {
	return s.Name() + ":" + tool + "@" + s.SymbolIndexOp()
}

// indexesDriven is, per project, the binaries the ops of idxs drive: the set
// ComputeTargetKey hands probeObservations for the same ops. An observation outside it
// never reaches a step's key, so probing it would fork for nothing (govulncheck's database
// probe was seven of a query's forks), and a probe of the Go index never forks scip-buzz,
// whose absence would warn on every graph read.
func indexesDriven(idxs []projectIndex) map[string]map[string]bool {
	driven := map[string]map[string]bool{}
	for _, idx := range idxs {
		path := idx.project.Path
		if driven[path] == nil {
			driven[path] = map[string]bool{}
		}
		maps.Copy(driven[path], targetDrivenBins(idx.project, idx.op))
	}
	return driven
}

// freshnessCache is the handle a read-only freshness probe hashes against: the
// workspace's own cache when it was opened, and a bare local one otherwise.
//
// An Inspect-constructed workspace has no cache on purpose, because Open also wires
// telemetry, the remote backend and machine admission. A manifest lookup needs none of
// those, and without this the verbs that only INSPECT could not ask the cache's question
// at all: they compared mtimes instead, which `format` advances on a rewrite that changes
// no bytes, so they reported a staleness `magus graph build` could not clear.
//
// cache.Open does touch the cache DIRECTORY (it creates it and writes a short-lived
// mtime-resolution probe), which is why the inspect-only verbs avoided it. Nothing in the
// working tree is written, and a lookup that cannot say whether its answer is complete was
// the worse trade.
func (m *Magus) freshnessCache(ctx context.Context) *cache.Cache {
	if m.cache != nil {
		return m.cache
	}
	m.probeCacheOnce.Do(func() {
		c, err := cache.Open(ctx, resolveCacheDir(m.Root(), m.cfg))
		if err != nil {
			slog.With(attr.Component("magus")).WarnContext(ctx, "cannot open the cache to probe symbol index freshness", slog.String("error", err.Error()))
			return
		}
		m.probeCache = c
	})
	return m.probeCache
}

// ReindexSymbols runs every indexer op of every symbol-capable project, refreshing each
// cached SCIP index, one run per index. An index whose indexer is missing or fails is
// reported with an actionable install hint but does not stop the rest, the project's other
// indexes included. It returns how many indexes were rebuilt and the joined errors. This
// is the manual counterpart to the server's background auto-indexer, invoked by `magus
// graph build`.
func (m *Magus) ReindexSymbols(ctx context.Context) (int, error) {
	var errs []error
	done := 0
	for _, idx := range m.workspaceIndexes() {
		if err := m.Run(ctx, []types.Target{{Path: idx.project.Path, Name: idx.op}}); err != nil {
			errs = append(errs, symbolRunError(idx.projectRef(), idx.language, err))
			continue
		}
		done++
	}
	return done, errors.Join(errs...)
}

// symbolCapableIn is the symbol-capable projects among paths, by path.
func (m *Magus) symbolCapableIn(paths []string) []string {
	var out []string
	for _, idx := range m.workspaceIndexes() {
		if path := idx.project.Path; slices.Contains(paths, path) && !slices.Contains(out, path) {
			out = append(out, path)
		}
	}
	return out
}

// FreshenSymbolIndexes brings every symbol index the workspace declares up to date before a
// reader mines it. Inside a target the rebuild runs through that target's run, under the lock
// it holds; outside a run it is a run of its own. The error is freshenSymbolIndexes's MGS7003,
// naming each index that could not be made current; the others are current regardless.
func (m *Magus) FreshenSymbolIndexes(ctx context.Context) error {
	var paths []string
	for _, idx := range m.workspaceIndexes() {
		if !slices.Contains(paths, idx.project.Path) {
			paths = append(paths, idx.project.Path)
		}
	}
	return m.freshenSymbolIndexes(ctx, paths)
}

// freshenSymbolIndexes brings every symbol index of the symbol-capable projects among paths up
// to date before a review reads it, by running each index's op: through the run scheduler and
// the cache, so a current index replays and a stale one rebuilds only itself.
//
// It returns an MGS7003 error naming each index that could not be made current: the indexer
// is missing or failed, or the run left the cache nothing to vouch for the result by. A review
// never reads a stale index as if it were current. One exception keeps a second index from
// costing the first its review: an index whose indexer is not installed is left out while
// another index of the same project has its indexer (see installedIndexes).
func (m *Magus) freshenSymbolIndexes(ctx context.Context, paths []string) error {
	var touched []projectIndex
	for _, idx := range m.workspaceIndexes() {
		if slices.Contains(paths, idx.project.Path) {
			touched = append(touched, idx)
		}
	}
	touched = installedIndexes(ctx, touched)
	if len(touched) == 0 {
		return nil
	}
	c := m.freshnessCache(ctx)
	if c == nil {
		var projects []string
		for _, idx := range touched {
			if name := idx.projectRef().Display(); !slices.Contains(projects, name) {
				projects = append(projects, name)
			}
		}
		return types.DiagnosticErrorf(types.SymbolIndexNotCurrent,
			"symbol index not current for %s: the cache would not open; make the cache directory writable, then rerun",
			strings.Join(projects, ", ")).
			WithWhy(conformanceSkippedWhy + " The cache records whether an index is current, so without it nothing can vouch for the index.")
	}
	// The step each probe keyed, so a rebuild inside a run is keyed exactly as the probe
	// after it reads it back.
	keyed := map[indexRef]cache.Step{}
	unkeyable := map[indexRef]error{}
	probe := func(idxs []projectIndex) map[indexRef]bool {
		var ps []*types.Project
		for _, idx := range idxs {
			if !slices.Contains(ps, idx.project) {
				ps = append(ps, idx.project)
			}
		}
		// An unprobeable project reads as not current, and its rebuild is refused with the
		// MGS3035 that names the tool, as m.Run would refuse it.
		toolVersions, unprobeable := m.toolVersionsEach(ctx, ps)
		observations := m.probeObservations(ctx, ps, indexesDriven(idxs))
		out := map[indexRef]bool{}
		for _, idx := range idxs {
			path := idx.project.Path
			if unprobeable[path] != nil {
				unkeyable[idx.ref()] = unprobeable[path]
				out[idx.ref()] = false
				continue
			}
			step := m.symbolIndexStep(idx.project, idx.op, toolVersions[path], observations[path])
			keyed[idx.ref()] = step
			ok, err := c.IsCached(ctx, step)
			out[idx.ref()] = err == nil && ok
		}
		return out
	}
	return freshenIndexes(touched, probe, func(idx projectIndex) error {
		if err := unkeyable[idx.ref()]; err != nil {
			return err
		}
		return m.buildSymbolIndex(ctx, idx, keyed[idx.ref()])
	}, m.cfg.Cache.WriteEnabled())
}

// buildSymbolIndex rebuilds one stale index. A reader inside a target body (the run that
// owns ctx holds the project's lock for its whole invocation) rebuilds through that run's
// scheduler, as step: a nested m.Run would be refused MGS3007 for the very project the
// body runs in. Anywhere else it runs the index op as its own invocation.
func (m *Magus) buildSymbolIndex(ctx context.Context, idx projectIndex, step cache.Step) error {
	if ran, err := interp.CrossDispatchFromContext(ctx).DispatchStep(ctx, idx.project, step); ran {
		return err
	}
	return m.Run(ctx, []types.Target{{Path: idx.project.Path, Name: idx.op}})
}

// installedIndexes drops each index whose indexer is not installed, as long as its project
// keeps another index whose indexer is. A project with none installed keeps them all, so its
// review still fails naming the indexer to install rather than reading as checked. Installed
// means the binary resolves on the PATH a run started from ctx hands its children, which is
// where the run that builds the index looks for it.
func installedIndexes(ctx context.Context, idxs []projectIndex) []projectIndex {
	installed := make([]bool, len(idxs))
	present := map[string]bool{}
	for i, idx := range idxs {
		_, err := procrun.LookPath(ctx, idx.bin)
		installed[i] = err == nil
		if installed[i] {
			present[idx.project.Path] = true
		}
	}
	out := make([]projectIndex, 0, len(idxs))
	for i, idx := range idxs {
		if present[idx.project.Path] && !installed[i] {
			continue
		}
		out = append(out, idx)
	}
	return out
}

// freshenIndexes is freshenSymbolIndexes' policy: probe, build what is stale, and probe again,
// because a build the cache could not record is one nothing can vouch for.
func freshenIndexes(touched []projectIndex, probe func([]projectIndex) map[indexRef]bool,
	build func(projectIndex) error, writable bool,
) error {
	fresh := probe(touched)
	var stale []projectIndex
	for _, idx := range touched {
		if !fresh[idx.ref()] {
			stale = append(stale, idx)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	var problems []string
	var built []projectIndex
	writesOff := false
	for _, idx := range stale {
		if err := build(idx); err != nil {
			problems = append(problems, types.InlineDiagnostic(symbolRunError(idx.projectRef(), idx.language, err)))
			continue
		}
		built = append(built, idx)
	}
	if len(built) > 0 {
		after := probe(built)
		for _, idx := range built {
			if after[idx.ref()] {
				continue
			}
			why := "the cache recorded no run for the index it wrote"
			if !writable {
				why = "cache writes are off (cache.write.enabled: false); enable them for this run"
				writesOff = true
			}
			problems = append(problems, idx.projectRef().Display()+": "+why)
		}
	}
	if len(problems) == 0 {
		return nil
	}
	fix := "fix it"
	if len(problems) > 1 {
		fix = "fix each"
	}
	why := conformanceSkippedWhy
	if writesOff {
		why += " With cache writes off the cache records no run to vouch for the index it wrote;" +
			" enabling them for this run publishes nothing without a signing key."
	}
	return types.DiagnosticErrorf(types.SymbolIndexNotCurrent,
		"symbol index not current for %s; %s, then `magus graph build`", strings.Join(problems, "; "), fix).
		WithWhy(why)
}

// conformanceSkippedWhy is the rationale every MGS7003 carries.
const conformanceSkippedWhy = "The conformance checks read the symbol index, so they did not run."

// uncoveredProjects names each project with a changed file, other than a declared output, that
// has no symbol indexer: the conformance checks cannot see it, so their silence says nothing
// about it.
func uncoveredProjects(files []types.DiffFile, capable []string) []types.DiffUncovered {
	var out []types.DiffUncovered
	for _, f := range files {
		if f.Project == "" || f.Generated() || slices.Contains(capable, f.Project) ||
			slices.ContainsFunc(out, func(u types.DiffUncovered) bool { return u.Project == f.Project }) {
			continue
		}
		out = append(out, types.DiffUncovered{Project: f.Project, Reason: types.DiffUncoveredNoIndexer})
	}
	return out
}

// toDiagnostic is err as a Diagnostic: its MGS code, message and docs link when err carries a
// code, and the bare message otherwise. Its Why is the chain's rationale either way.
func toDiagnostic(err error) types.Diagnostic {
	var d *types.DiagnosticError
	if errors.As(err, &d) {
		f := d.BuzzError()
		return types.Diagnostic{Code: f["code"], Message: f["message"], URL: f["url"], Why: types.DiagnosticRationale(err)}
	}
	return types.Diagnostic{Message: err.Error(), Why: types.DiagnosticRationale(err)}
}

// symbolRunError wraps a failed scip run with the project (by its display name, so the
// workspace root reads as its repo name, not ".") and, when known, an actionable hint
// naming the language's indexer and where to install it.
//
// A run refused because another magus holds the lock or the machine budget never reached the
// indexer, so it gets no install hint: the fix is to rerun once the holder the error names
// finishes. A refusal by the run this one is nested inside (MGS3007), or over a tool that
// reports no version (MGS3035), never reached it either, and rerunning does not help, so it
// gets no hint of either kind: the diagnostic says what to do.
func symbolRunError(project types.ProjectRef, language string, err error) error {
	if errors.Is(err, types.ProjectLockHeldByAncestor) || errors.Is(err, types.ToolUnprobeable) {
		return fmt.Errorf("%s: %w", project.Display(), err)
	}
	var busy interface{ ExitCode() int }
	if errors.As(err, &busy) && busy.ExitCode() == lockContendedExit {
		return fmt.Errorf("%s: the indexer never ran, so rerun once that finishes: %w", project.Display(), err)
	}
	if hint := symbols.InstallHint(language); hint != "" {
		return fmt.Errorf("%s: %s: %w", project.Display(), hint, err)
	}
	return fmt.Errorf("%s: %w", project.Display(), err)
}
