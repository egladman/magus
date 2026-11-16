package magus

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"github.com/egladman/magus/types/diff"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/mod/modfile"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/maintenance"
	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/internal/retry"
	"github.com/egladman/magus/internal/symbols"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
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

// errMachineBuildHeld stops a background index when another build on this
// machine holds the build lock. It never queues behind one.
var errMachineBuildHeld = errors.New("another graph build holds this machine's build lock")

// takeMachineBuildLock takes the machine's build lock for a background index
// of root, or answers errMachineBuildHeld naming the holder. It records no
// store, so a graph build queued behind it still builds.
func takeMachineBuildLock(root string) (*maintenance.GraphBuildLock, error) {
	me := maintenance.GraphBuildHolder{PID: os.Getpid(), Started: time.Now(), By: "the background symbol index of " + root}
	notices := slog.NewLogLogger(slog.Default().Handler(), slog.LevelInfo).Writer()
	lock, holder, err := maintenance.TryMachineGraphBuild(me, notices)
	if err != nil {
		return nil, err
	}
	if holder != nil {
		return nil, fmt.Errorf("%w: %s", errMachineBuildHeld, holder)
	}
	return lock, nil
}

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
	si.settle(ctx, ref, si.runIndex(ctx, ref))
}

// settle records how one run of ref's op ended, deciding when it may run again.
func (si *symbolIndexer) settle(ctx context.Context, ref indexRef, err error) {
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
	if errors.Is(err, errMachineBuildHeld) {
		// Nothing ran, so no throttle: the next tick asks the lock again.
		si.log.DebugContext(ctx, "background symbol index stopped", slog.String("project", ref.project), slog.String("op", ref.op), attr.Error(err))
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
	if errors.Is(err, types.GateSuperseded) || lostTheLock(err) {
		// A run wanted this project: no verdict on the indexer.
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
			slog.String("project", ref.project), slog.String("op", ref.op), slog.Int("failures", st.failures), attr.Error(err))
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
	return retry.Backoff(symbolIndexBackoffBase, symbolIndexBackoffMax, failures)
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
	si := m.newSymbolIndexer(scfg, capable)

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

// newSymbolIndexer is the auto-indexer over capable, its windows from scfg.
func (m *Magus) newSymbolIndexer(scfg config.SymbolIndexingConfig, capable []projectIndex) *symbolIndexer {
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

	return &symbolIndexer{
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
			return m.runBackgroundIndex(ctx, byRef[ref])
		},
		status: m.SymbolIndexStatusByStamp,
		// The watcher makes the freshness memo trustworthy: it drops the
		// memo when a capable project's sources change or an index run ends.
		onChange: m.symbolStatus.invalidate,
	}
}

// runBackgroundIndex runs idx's op under the machine's build lock, then writes
// the indexes a guard hook reads.
func (m *Magus) runBackgroundIndex(ctx context.Context, idx projectIndex) error {
	lock, err := takeMachineBuildLock(m.Root())
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()
	if err := m.runTargets(ctx, []types.Target{{Path: idx.project.Path, Name: idx.op}}, run{Background: true}); err != nil {
		if ctx.Err() != nil {
			return err // a yield-cancel carries no useful hint
		}
		return symbolRunError(idx.projectRef(), idx.language, err)
	}
	// Claimed after the run: a step behind its own process's claim
	// may never seat.
	release, err := m.admitSymbolRefresh(ctx)
	if err != nil {
		return err
	}
	defer release()
	if gerr := m.WriteGuardIndex(ctx); gerr != nil {
		slog.With(attr.Component("magus")).DebugContext(ctx, "guard index not written", attr.Error(gerr))
	}
	if serr := m.WriteScriptsIndex(ctx); serr != nil {
		slog.With(attr.Component("magus")).DebugContext(ctx, "scripts index not written", attr.Error(serr))
	}
	return nil
}

// projectIndexes is the symbol indexes p declares, one per indexer op among the spells it
// binds (a spell that declares a symbol indexer), in binding order. The single source of
// truth for "which indexes get built", so the auto-indexer, ReindexSymbols, and status
// reporting cannot disagree. Two spells running under one op (go and python both declare
// none, so both run under the default) share that op's index, and the first one's
// language names it.
func projectIndexes(p *types.Project, cfg config.SpellsConfig) []projectIndex {
	var out []projectIndex
	for _, sp := range p.ResolvedSpells {
		op := runningIndexOp(sp, cfg)
		if op == "" || slices.ContainsFunc(out, func(idx projectIndex) bool { return idx.op == op }) {
			continue
		}
		out = append(out, projectIndex{project: p, op: op, language: sp.Language(), bin: sp.SymbolIndexer().Command.Bin})
	}
	return out
}

// indexesSymbols reports whether target is one of p's indexer ops that runs here.
func indexesSymbols(p *types.Project, target string, cfg config.SpellsConfig) bool {
	return target != "" && slices.ContainsFunc(p.ResolvedSpells, func(s *spells.Spell) bool { return runningIndexOp(s, cfg) == target })
}

// runningIndexOp is the op sp's symbol indexer runs under, or "" when sp declares none or
// its indexer is an external SCIP command spells.symbol_indexers does not list. Every
// question of which indexes exist goes through it, so an indexer the workspace has not
// opted in is never built, keyed or reported as stale.
func runningIndexOp(sp *spells.Spell, cfg config.SpellsConfig) string {
	if !cfg.RunsSymbolIndexer(sp.Name(), sp.SymbolIndexer()) {
		return ""
	}
	return sp.SymbolIndexOp()
}

// workspaceIndexes returns every symbol index the workspace's projects declare, so a
// changed file can be matched back to its project and a failed index can name the missing
// indexer.
func (m *Magus) workspaceIndexes() []projectIndex {
	ps := m.All()
	out := make([]projectIndex, 0, len(ps))
	for _, p := range ps {
		out = append(out, projectIndexes(p, m.cfg.Spells)...)
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
// elsewhere it recomputes each call. Status and the dashboard read it.
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
	applyRunKeying(&step, p, m.cfg.Spells, toolVersions, observationsForTarget(p, op, observations), nil)
	return step
}

// symbolIndexSources is the source globs of each of p's spells whose indexer runs as op,
// rooted at p: what buildStep keys that op on.
func symbolIndexSources(p *types.Project, op string, cfg config.SpellsConfig) []types.Glob {
	var out []types.Glob
	for _, s := range p.ResolvedSpells {
		if runningIndexOp(s, cfg) != op {
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
	return fmt.Sprintf("%s:%s@%s", s.Name(), tool, s.SymbolIndexOp())
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
		storeDir, err := config.StoreDir(m.Root(), m.cfg)
		if err != nil {
			slog.With(attr.Component("magus")).WarnContext(ctx, "cannot open the cache to probe symbol index freshness", attr.Error(err))
			return
		}
		c, err := cache.Open(ctx, resolveCacheDir(m.Root(), m.cfg), cache.WithStoreDir(storeDir))
		if err != nil {
			slog.With(attr.Component("magus")).WarnContext(ctx, "cannot open the cache to probe symbol index freshness", attr.Error(err))
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
		if err := m.runTargets(ctx, []types.Target{{Path: idx.project.Path, Name: idx.op}}, run{}); err != nil {
			errs = append(errs, symbolRunError(idx.projectRef(), idx.language, err))
			continue
		}
		done++
	}
	return done, errors.Join(errs...)
}

const (
	// SymbolRefreshBudget covers a cold index of a large repository. A plain
	// read never waits on it: it answers now and refreshes in the background.
	SymbolRefreshBudget = 3 * time.Minute
	// How long past the budget `--refresh` waits for a run to honor
	// cancellation before it answers anyway.
	symbolRefreshAbandon = 2 * time.Second
	// Beside an index, the record that its last refresh failed. A CLI
	// has no loop to hold a backoff in, and every read would pay again.
	symbolRefreshFailedSuffix = ".refresh-failed"
	// The machine claim of a refresh's own process. Measured 2026-10-31
	// on this repository: 1.74 GiB footprint. An indexer claims for itself.
	symbolRefreshMemoryMB = 2048
)

// SymbolRefresh is what [Magus.RefreshSymbolIndexes] did.
type SymbolRefresh struct {
	Refreshed []string      // indexes brought current: "libs/api (go index)"
	Problems  []string      // indexes that stayed behind, and why
	Took      time.Duration // wall time
	Swept     time.Duration // of Took, finding what is behind
	Indexed   time.Duration // of Took, running the indexers
}

// RefreshSymbolIndexes brings the stale and never-built indexes up to the
// tree, through the cache, so a current one replays. It returns within
// SymbolRefreshBudget plus symbolRefreshAbandon and never fails a read:
// what stayed behind is in Problems. A failed index waits
// symbolIndexBackoffBase; a lost lock or a full machine waits for nothing.
func (m *Magus) RefreshSymbolIndexes(ctx context.Context) SymbolRefresh {
	cacheDir := resolveCacheDir(m.Root(), m.cfg)
	r := symbolRefresher{
		budget:  SymbolRefreshBudget,
		abandon: symbolRefreshAbandon,
		now:     time.Now,
		admit:   m.admitSymbolRefresh,
		status:  m.SymbolIndexStatusByStamp,
		indexPath: func(s types.SymbolIndexStatus) string {
			return symbols.IndexPath(cacheDir, cmp.Or(s.Project.Path, "."), s.Op)
		},
		run: func(ctx context.Context, s types.SymbolIndexStatus) error {
			err := m.runTargets(ctx, []types.Target{{Path: cmp.Or(s.Project.Path, "."), Name: s.Op}}, run{Background: true})
			if err == nil {
				return nil
			}
			return symbolRunError(s.Project, s.Language, err)
		},
		after: func(ctx context.Context) {
			log := slog.With(attr.Component("magus"))
			if err := m.WriteGuardIndex(ctx); err != nil {
				log.DebugContext(ctx, "guard index not written", attr.Error(err))
			}
			if err := m.WriteScriptsIndex(ctx); err != nil {
				log.DebugContext(ctx, "scripts index not written", attr.Error(err))
			}
		},
	}
	return r.refresh(ctx)
}

// admitSymbolRefresh claims the machine budget for the refresh's own work.
// A nil broker is `broker: off`, which arbitrates nothing.
func (m *Magus) admitSymbolRefresh(ctx context.Context) (func(), error) {
	if m.broker == nil {
		return func() {}, nil
	}
	policy := cmp.Or(m.brokerPolicy, m.cfg.Broker)
	return cache.AdmitMachine(ctx, m.broker, types.MachineClaim{
		Project:  ".",
		Target:   "symbol-refresh",
		MemoryMB: symbolRefreshMemoryMB,
		Dir:      m.Root(),
	}, policy.Resolved() == types.BrokerRequired, m.cfg.BudgetWait)
}

// symbolRefresher is RefreshSymbolIndexes' policy, its collaborators
// injected so a run that hangs or fails is testable without a workspace.
type symbolRefresher struct {
	budget, abandon time.Duration
	now             func() time.Time
	admit           func(context.Context) (func(), error)
	status          func(context.Context) []types.SymbolIndexStatus
	indexPath       func(types.SymbolIndexStatus) string
	run             func(context.Context, types.SymbolIndexStatus) error
	after           func(context.Context) // once anything was refreshed
}

func (r symbolRefresher) refresh(ctx context.Context) SymbolRefresh {
	started := r.now()
	ctx, cancel := context.WithTimeout(ctx, r.budget)
	defer cancel()

	var t refreshTally
	indexed, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		refreshed := r.work(ctx, t.report)
		close(indexed)
		if refreshed && r.after != nil {
			// Past the budget this is left to finish or die with the process.
			// Cancelling it would record its tool probes as failed.
			afterCtx := context.WithoutCancel(ctx)
			release, err := r.admit(afterCtx)
			if err != nil {
				t.report("", "guard and scripts indexes not written: "+types.InlineDiagnostic(err), 0, 0)
				return
			}
			defer release()
			r.after(afterCtx)
		}
	}()

	finished := true
	select {
	case <-done:
	case <-ctx.Done():
		select {
		case <-indexed:
		case <-time.After(r.abandon):
			finished = false
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !finished {
		t.out.Problems = append(t.out.Problems, fmt.Sprintf("a run outlived the %s budget by %s, so this read stopped waiting for it", r.budget, r.abandon))
	}
	return SymbolRefresh{Refreshed: slices.Clone(t.out.Refreshed), Problems: slices.Clone(t.out.Problems), Took: r.now().Sub(started), Swept: t.out.Swept, Indexed: t.out.Indexed}
}

// refreshTally gathers a refresh's outcomes. A run past the budget keeps
// reporting while the read that abandoned it returns.
type refreshTally struct {
	mu  sync.Mutex
	out SymbolRefresh
}

func (t *refreshTally) report(refreshed, problem string, swept, indexed time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if refreshed != "" {
		t.out.Refreshed = append(t.out.Refreshed, refreshed)
	}
	if problem != "" {
		t.out.Problems = append(t.out.Problems, problem)
	}
	t.out.Swept += swept
	t.out.Indexed += indexed
}

// work refreshes each wanted index behind the tree, reporting each outcome,
// and returns whether any landed.
func (r symbolRefresher) work(ctx context.Context, report func(refreshed, problem string, swept, indexed time.Duration)) bool {
	refreshed := false
	started := r.now()
	release, err := r.admit(ctx)
	if err != nil {
		report("", types.InlineDiagnostic(err), 0, 0)
		return false
	}
	statuses := r.status(ctx)
	// Handed back first: a step behind its own process's claim may never seat.
	release()
	report("", "", r.now().Sub(started), 0)
	for _, s := range statuses {
		if len(staleIndexRefs([]types.SymbolIndexStatus{s})) == 0 {
			continue
		}
		name := types.KnowledgeStaleIndex{Project: cmp.Or(s.Project.Path, "."), Language: s.Language}.Describe()
		index := r.indexPath(s)
		if why := r.backedOff(index); why != "" {
			report("", fmt.Sprintf("%s: %s", name, why), 0, 0)
			continue
		}
		if ctx.Err() != nil {
			report("", fmt.Sprintf("%s: not reached within the %s budget", name, r.budget), 0, 0)
			continue
		}
		ran := r.now()
		err := r.run(ctx, s)
		took := r.now().Sub(ran)
		switch {
		case err == nil:
			_ = os.Remove(index + symbolRefreshFailedSuffix)
			refreshed = true
			report(name, "", 0, took)
		case errors.Is(err, types.GateSuperseded):
			// The next index may share the lock the run is polling for.
			report("", types.InlineDiagnostic(err), 0, took)
			return refreshed
		case lostTheLock(err):
			report("", types.InlineDiagnostic(err), 0, took)
		default:
			why := types.InlineDiagnostic(err)
			if ctx.Err() != nil {
				why = fmt.Sprintf("%s: did not finish within the %s budget", name, r.budget)
			}
			recordRefreshFailure(index, why)
			report("", why, 0, took)
		}
	}
	return refreshed
}

// lostTheLock reports a run refused for another magus holding the project
// lock or the machine budget, which says nothing about the indexer.
func lostTheLock(err error) bool {
	var busy interface{ ExitCode() int }
	return errors.As(err, &busy) && busy.ExitCode() == lockContendedExit
}

// backedOff is why index is not tried again yet, "" when it may be.
func (r symbolRefresher) backedOff(index string) string {
	path := index + symbolRefreshFailedSuffix
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	age := r.now().Sub(info.ModTime())
	if age >= symbolIndexBackoffBase {
		return ""
	}
	why, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("its last refresh failed %s ago, and reads retry in %s: %s",
		age.Round(time.Second), (symbolIndexBackoffBase - age).Round(time.Second), why)
}

// recordRefreshFailure starts index's backoff. A record that cannot be
// written costs the next read another attempt and nothing else.
func recordRefreshFailure(index, why string) {
	path := index + symbolRefreshFailedSuffix
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = file.WriteFileAtomic(path, []byte(why), 0o644)
}

// RecentSymbolRefreshFailures is why each index under cacheDir last failed
// to refresh, while its backoff holds. A read names them rather than
// starting a refresh that would only back off.
func RecentSymbolRefreshFailures(cacheDir string, now time.Time) []string {
	records, err := filepath.Glob(filepath.Join(cacheDir, "symbols", "*", "*"+symbolRefreshFailedSuffix))
	if err != nil {
		return nil
	}
	var out []string
	for _, record := range records {
		info, err := os.Stat(record)
		if err != nil || now.Sub(info.ModTime()) >= symbolIndexBackoffBase {
			continue
		}
		if why, err := os.ReadFile(record); err == nil {
			out = append(out, string(why))
		}
	}
	return out
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
// Naming languages leaves the other languages' indexes alone.
func (m *Magus) FreshenSymbolIndexes(ctx context.Context, languages []string) error {
	var paths []string
	for _, idx := range m.workspaceIndexes() {
		if !slices.Contains(paths, idx.project.Path) {
			paths = append(paths, idx.project.Path)
		}
	}
	return m.freshenSymbolIndexes(ctx, paths, languages)
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
func (m *Magus) freshenSymbolIndexes(ctx context.Context, paths, languages []string) error {
	var touched []projectIndex
	for _, idx := range m.workspaceIndexes() {
		if slices.Contains(paths, idx.project.Path) && (len(languages) == 0 || slices.Contains(languages, idx.language)) {
			touched = append(touched, idx)
		}
	}
	touched = installedIndexes(ctx, touched)
	if len(touched) == 0 {
		return nil
	}
	c := m.freshnessCache(ctx)
	if c == nil {
		return cacheWouldNotOpen(touched)
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
			ok, err := indexReplayed(ctx, c, step)
			out[idx.ref()] = err == nil && ok
		}
		return out
	}
	return freshenIndexes(touched, probe, func(idx projectIndex) error {
		if err := unkeyable[idx.ref()]; err != nil {
			return err
		}
		return m.buildSymbolIndex(ctx, idx, keyed[idx.ref()])
	}, m.cfg.Cache.WriteEnabled(), func() string { return m.buildSkew(ctx) })
}

// cacheWouldNotOpen is the MGS7003 when no cache can vouch for touched.
func cacheWouldNotOpen(touched []projectIndex) error {
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

// buildSkew is binaryTreeSkew for this workspace and the running binary.
func (m *Magus) buildSkew(ctx context.Context) string {
	goMod, _ := os.ReadFile(filepath.Join(m.ws.Root, "go.mod"))
	_, head, _ := m.CurrentRevision(ctx)
	built := types.MagusBuildFromContext(ctx).Commit
	return binaryTreeSkew(goMod, built, head, func() ([]string, error) {
		res, err := vcs.Resolve(ctx, m.ws.Root, "", m.ws.VCSOptions)
		if err != nil {
			return nil, err
		}
		if res.VCS == nil {
			return nil, fmt.Errorf("no version control answered in %s", m.ws.Root)
		}
		return job.ChangedBuildInputs(ctx, res.VCS, m.ws.Root, m.All(), built, head)
	})
}

// buildSymbolIndex rebuilds one stale index. A reader inside a target body (the run that
// owns ctx holds the project's lock for its whole invocation) rebuilds through that run's
// scheduler, as step: a nested m.Run would be refused MGS3007 for the very project the
// body runs in. Anywhere else it runs the index op as its own invocation.
//
// A magus that body started has no scheduler to ask.
// Its run takes no lock its ancestor holds.
func (m *Magus) buildSymbolIndex(ctx context.Context, idx projectIndex, step cache.Step) error {
	if ran, err := interp.CrossDispatchFromContext(ctx).DispatchStep(ctx, idx.project, step); ran {
		return err
	}
	return m.runTargets(ctx, []types.Target{{Path: idx.project.Path, Name: idx.op}}, run{UnderAncestor: true})
}

// installedIndexes drops each index whose indexer is not installed, as long as its project
// keeps another index whose indexer is. A project with none installed keeps them all, so its
// review still fails naming the indexer to install rather than reading as checked. Installed
// means the binary resolves on the PATH a run started from ctx hands its children, which is
// where the run that builds the index looks for it.
// An in-process indexer names no binary, so it is always kept.
func installedIndexes(ctx context.Context, idxs []projectIndex) []projectIndex {
	installed := make([]bool, len(idxs))
	present := map[string]bool{}
	for i, idx := range idxs {
		if idx.bin == "" {
			installed[i] = true
			continue
		}
		_, err := spawn.LookPath(ctx, idx.bin)
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
	build func(projectIndex) error, writable bool, skew func() string,
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
	writesOff, skewed := false, false
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
			} else if s := skew(); s != "" {
				why, skewed = fmt.Sprintf("%s: %s", why, s), true
			}
			problems = append(problems, fmt.Sprintf("%s: %s", idx.projectRef().Display(), why))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return indexesNotCurrent(problems, writesOff, skewed)
}

// indexesNotCurrent is the MGS7003 naming each problem and its fix.
func indexesNotCurrent(problems []string, writesOff, skewed bool) error {
	fix := "fix it"
	if len(problems) > 1 {
		fix = "fix each"
	}
	why := conformanceSkippedWhy
	if writesOff {
		why += " With cache writes off the cache records no run to vouch for the index it wrote;" +
			" enabling them for this run publishes nothing without a signing key."
	}
	if skewed {
		fix = "build ./magus at the checkout's HEAD, stop the pool parents of other builds with `magus server stop --pools`"
		why += " A magus built from other build inputs than the tree's records the index's run under a key the probe does not read back."
	}
	return types.DiagnosticErrorf(types.SymbolIndexNotCurrent,
		"symbol index not current for %s; %s, then `magus graph build`", strings.Join(problems, "; "), fix).
		WithWhy(why)
}

// binaryTreeSkew names a binary built at built running in magus's own checkout (goMod is its
// go.mod) at head when changed, the build inputs that differ between the two commits, is not
// empty. It is "" in any other workspace, when either commit is unknown or abbreviates the
// other, and when no build input changed: that binary is the one head builds. Inputs that
// cannot be listed are named as the skew, never read as unchanged.
func binaryTreeSkew(goMod []byte, built, head string, changed func() ([]string, error)) string {
	if modfile.ModulePath(goMod) != ownModule || built == "" || built == "unknown" || head == "" ||
		strings.HasPrefix(built, head) || strings.HasPrefix(head, built) {
		return ""
	}
	inputs, err := changed()
	switch {
	case err != nil:
		return fmt.Sprintf("this magus was built at %.12s and the checkout is at %.12s, whose build inputs could not be compared: %v", built, head, err)
	case len(inputs) == 0:
		return ""
	}
	what := inputs[0]
	if len(inputs) > 1 {
		what = fmt.Sprintf("%s and %d more build inputs", what, len(inputs)-1)
	}
	return fmt.Sprintf("this magus was built at %.12s and the checkout is at %.12s, and %s changed between them", built, head, what)
}

// conformanceSkippedWhy is the rationale every MGS7003 carries.
const conformanceSkippedWhy = "The conformance checks read the symbol index, so they did not run."

// uncoveredProjects names each project with a changed file, other than a declared output, that
// has no symbol indexer: the conformance checks cannot see it, so their silence says nothing
// about it.
func uncoveredProjects(files []diff.File, capable []string) []diff.Uncovered {
	var out []diff.Uncovered
	for _, f := range files {
		if f.Project == "" || f.Generated() || slices.Contains(capable, f.Project) ||
			slices.ContainsFunc(out, func(u diff.Uncovered) bool { return u.Project == f.Project }) {
			continue
		}
		out = append(out, diff.Uncovered{Project: f.Project, Reason: diff.UncoveredNoIndexer})
	}
	return out
}

// toDiagnostic is err as a Diagnostic: its MGS code, message, docs link and fix when err
// carries a code, and the bare message otherwise. Its Why is the chain's rationale either way.
func toDiagnostic(err error) types.Diagnostic {
	var d *types.DiagnosticError
	if errors.As(err, &d) {
		f := d.BuzzError()
		return types.Diagnostic{Code: f["code"], Message: f["message"], URL: f["url"], Fix: f["fix"], Why: types.DiagnosticRationale(err)}
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
	if errors.Is(err, types.ProjectLockHeldByAncestor) || errors.Is(err, types.ToolUnprobeable) || errors.Is(err, types.GateSuperseded) {
		return fmt.Errorf("%s: %w", project.Display(), err)
	}
	var busy interface{ ExitCode() int }
	if errors.As(err, &busy) && busy.ExitCode() == lockContendedExit {
		return fmt.Errorf("%s: the indexer never ran, so rerun once that finishes: %w", project.Display(), err)
	}
	if symbols.MissingFile(err) {
		return fmt.Errorf("%s: the indexer ran and a path it was given does not exist: %w", project.Display(), err)
	}
	if hint := symbols.InstallHint(language); hint != "" {
		return fmt.Errorf("%s: %s: %w", project.Display(), hint, err)
	}
	return fmt.Errorf("%s: %w", project.Display(), err)
}
