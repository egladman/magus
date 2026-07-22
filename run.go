package magus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus/internal/audit"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/ci/volatility"
	"github.com/egladman/magus/internal/describe"
	"github.com/egladman/magus/internal/file/diff"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/handler/mcp/origin"
	"github.com/egladman/magus/internal/interactive"
	interp "github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/race"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/internal/service"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// RunOption configures a [Magus.Run], [Magus.RunCI], or [Magus.RunAffected] invocation.
type RunOption func(*run)

// run is the accumulated state of a Run/CI/RunAffected call.
type run struct {
	DryRun            bool
	Charms            []string       // execution charms propagated via context; "rw" enables mutating targets
	Report            *report.Writer // caller-owned; caller closes; mutually exclusive with ReportWriter
	ReportWriter      io.Writer      // run engine wraps this in its own Writer
	NoVolatilityRetry bool
	BaseRef           string
	Race              bool     // MGS4001/4002/4004 race diagnostics; near-zero overhead
	RaceReplay        bool     // MGS4003 determinism replay; orthogonal to Race
	Spell             string   // when set, restricts execution to this spell; unmatched projects are skipped
	Step              bool     // forces Concurrency=1; StepGate comes from ctx
	ExtraArgs         []string // forwarded to spells via project.WithExtraArgs
	Normalizer        types.TargetNameNormalizer
	NoCache           bool // force a fresh run even on a cache hit; still refreshes the entry (magus run --no-cache)
}

// WithDryRun prints what would run without invoking any handler.
func WithDryRun() RunOption { return func(o *run) { o.DryRun = true } }

// WithReportWriter streams one JSONL event per target to w; the run engine
// constructs and closes the report.Writer around it.
func WithReportWriter(w io.Writer) RunOption { return func(o *run) { o.ReportWriter = w } }

// WithWrite enables mutating mode for format/generate targets; sugar for the "rw" charm.
func WithWrite() RunOption { return WithCharms(types.CharmReadWrite) }

// WithCharms sets execution charms propagated to spells via context.
func WithCharms(charms ...string) RunOption {
	return func(o *run) { o.Charms = append(o.Charms, charms...) }
}

// WithNoVolatilityRetry disables the volatility auto-retry logic.
func WithNoVolatilityRetry() RunOption { return func(o *run) { o.NoVolatilityRetry = true } }

// WithBaseRef overrides MAGUS_VCS_BASE_REF for RunAffected invocations.
func WithBaseRef(ref string) RunOption { return func(o *run) { o.BaseRef = ref } }

// WithSpellFilter restricts Run to projects that have the named spell.
func WithSpellFilter(name string) RunOption { return func(o *run) { o.Spell = name } }

// WithTargetNameNormalizer overrides how exported-function identifiers are
// converted to target names. Defaults to kebab-case via lo.KebabCase.
func WithTargetNameNormalizer(n types.TargetNameNormalizer) RunOption {
	return func(o *run) { o.Normalizer = n }
}

// WithStep enables per-subprocess stepping mode; forces Concurrency=1.
func WithStep() RunOption { return func(o *run) { o.Step = true } }

// WithExtraArgs forwards args to spells via project.WithExtraArgs.
func WithExtraArgs(args []string) RunOption { return func(o *run) { o.ExtraArgs = args } }

// WithRace enables race-condition diagnostics (MGS4001/4002/4004). Diagnostic only.
func WithRace() RunOption { return func(o *run) { o.Race = true } }

// WithRaceReplay enables determinism replay (MGS4003). Compose with WithRace for MGS4001/4002/4004.
func WithRaceReplay() RunOption { return func(o *run) { o.RaceReplay = true } }

// WithNoCache forces every selected target to run fresh even on a cache hit.
// Unlike a skip_cache target policy (which never snapshots), a --no-cache run
// still refreshes the cache entry on success, so a subsequent ordinary run
// replays the rebuilt result instead of the stale one.
func WithNoCache() RunOption { return func(o *run) { o.NoCache = true } }

func applyRunOpts(opts []RunOption) run {
	var o run
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// Run executes targets against their projects. Independent pairs run concurrently
// up to the limiter budget. "ci" is an ordinary magusfile target (compose its
// pipeline with magus.needs); magus no longer hardcodes a CI chain.
func (m *Magus) Run(ctx context.Context, targets []types.Target, opts ...RunOption) error {
	if len(targets) == 0 {
		return nil
	}
	return m.runResolved(ctx, targets, applyRunOpts(opts))
}

// runResolved groups targets by name and executes them with already-applied
// options. Shared by Run and the read-only RunCI entry point.
func (m *Magus) runResolved(ctx context.Context, targets []types.Target, o run) error {
	type targetGroup struct {
		name    string
		targets []types.Target
	}
	var groups []targetGroup
	targetIdx := make(map[string]int, 4)
	for _, t := range targets {
		if i, ok := targetIdx[t.Name]; ok {
			groups[i].targets = append(groups[i].targets, t)
		} else {
			targetIdx[t.Name] = len(groups)
			groups = append(groups, targetGroup{name: t.Name, targets: []types.Target{t}})
		}
	}

	stages := make([]stage, 0, len(groups))
	for _, g := range groups {
		projects := m.targetProjects(g.targets)
		handler := m.makeHandler(g.name)
		if o.Spell != "" {
			handler = m.makeSpellFilteredHandler(g.name, o.Spell)
		}
		stages = append(stages, stage{target: g.name, handler: handler, projects: projects})
	}
	return m.executeStages(ctx, stages, TargetLabel(targets, ""), o)
}

// RunCI runs the ci target(s) with write mode forced off. "ci" is an ordinary
// magusfile-defined target; magus keeps it only as the affected-set anchor,
// not a hardcoded preflight...test chain. The magusfile composes the pipeline
// order via magus.needs.
func (m *Magus) RunCI(ctx context.Context, targets []types.Target, opts ...RunOption) error {
	o := applyRunOpts(opts)
	o.Charms = slices.DeleteFunc(slices.Clone(o.Charms), func(s string) bool {
		return types.NormalizeCharmName(s) == types.CharmReadWrite
	})

	// ci is the one target that must not silently no-op when undefined. Ordinary
	// targets fan out and skip projects that lack them, but ci is the anchor that
	// `magus affected ci` and `magus affected --plan` key off: a missing ci would
	// otherwise exit 0 having run nothing, a green gate that gated nothing. So when
	// the run scope has projects but none declare ci, fail with an actionable
	// message and hint. (An empty scope, e.g. affected with no changes, is a
	// legitimate no-op and is left alone.) Only block when we definitely scanned the
	// scope and found no ci: a scan error (unreadable magusfile) must not masquerade
	// as "no ci" and abort the gate; let runResolved surface the real read failure.
	if projects := m.targetProjects(targets); len(projects) > 0 {
		if has, scanErr := anyProjectDeclaresCI(projects); !has && scanErr == nil {
			interactive.Emit(os.Stderr, "define a ci target in your magusfile to compose the gate, e.g.  "+
				"export fun ci(_a: [str]) > void { magus.needs(build, test, lint); }  "+
				"(run 'magus describe targets' to see available stages)")
			return types.DiagnosticErrorf(types.NoCITarget,
				"no %q target defined in the selected project(s); it is the anchor %q and %q key off, "+
					"so this run would do nothing", types.TargetCI, "magus affected ci", "magus affected --plan")
		}
	}
	return m.runResolved(ctx, targets, o)
}

// anyProjectDeclaresCI reports whether any project in scope declares a ci target.
// ci lives in the magusfile (composed via magus.needs), never in a spell, so it
// extracts each project's declared target nodes statically (the same AST extractor
// DescribeGraph uses, never a raw text scan, so `ci` in a comment or string can't
// false-positive) and short-circuits on the first ci found. The returned error is
// non-nil if a source couldn't be located, so a (false, err) result means "couldn't
// determine", not "definitely no ci" - the caller must not block on it.
func anyProjectDeclaresCI(projects []*types.Project) (bool, error) {
	var scanErr error
	for _, p := range projects {
		srcs, err := interp.FindAll(p.Dir)
		if err != nil {
			scanErr = err
			continue
		}
		for _, src := range srcs {
			if src.Engine != "buzz" {
				continue
			}
			for _, n := range describe.Extract(concatSource(src)) {
				// Node names are already normalized by the extractor, so this
				// matches the run path's target-name resolution.
				if n.Name == types.TargetCI {
					return true, nil
				}
			}
		}
	}
	return false, scanErr
}

// RunAffected computes the VCS-diff target set and runs target on it.
func (m *Magus) RunAffected(ctx context.Context, target string, opts ...RunOption) error {
	o := applyRunOpts(opts)
	targets, _, _, err := m.ExpandAffected(ctx, target, o.BaseRef)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}
	return m.Run(ctx, targets, opts...)
}

// undeclaredCharms returns the active charms that no selected target declares,
// excluding magus's reserved built-in charms (write, cd); candidates for a
// soft typo warning.
func undeclaredCharms(active []string, declared map[string]struct{}) []string {
	var out []string
	for _, c := range active {
		if types.IsReservedCharm(c) {
			continue
		}
		if _, ok := declared[c]; !ok {
			out = append(out, c)
		}
	}
	return out
}

// targetProjects resolves targets to projects via workspace lookup.
func (m *Magus) targetProjects(targets []types.Target) []*types.Project {
	out := make([]*types.Project, 0, len(targets))
	for _, t := range targets {
		if p := m.ws.Get(t.Path); p != nil {
			out = append(out, p)
		}
	}
	return out
}

// TargetHandler runs one target on one resolved project. It is the single executor
// seam the run pipeline schedules: the same handler serves both a real run and a dry
// run - types.WithTrace(ctx) switches it, so under a tracing context the effect
// boundary (proc/run.Exec, fs, net) records each op's intent and skips it instead of
// executing. One path, two modes: no separate dry-run executor, just a tracing
// context over this one contract. (The in-browser evaluator in internal/dry is a
// different thing - it takes raw source, never a resolved *Project, so it sits before
// this seam and cannot implement it; see that package's doc.)
type TargetHandler func(context.Context, *types.Project) error

// stage is one target to run across a project set.
type stage struct {
	target   string
	handler  TargetHandler
	projects []*types.Project
}

// raceForcesNoCache reports whether o requires bypassing the cache so race
// diagnostics always observe a genuine execution. Race diagnostics (watch:
// MGS4001/4002/4004 via raceRT.TrackProject; replay: MGS4003 via runReplay)
// both need one: a cache hit skips the body entirely, so raceRT never wraps
// it, and replay's "before" snapshot would come from a stale artifact instead
// of this run. NoCache (not just skip-replay) also keeps a --race run from
// ever snapshotting: its steps carry no race-specific cache key of their own,
// so a snapshot here would otherwise sit in the ordinary entry and satisfy a
// later, non-race run.
func raceForcesNoCache(o run) bool {
	return o.Race || o.RaceReplay
}

// buildStep assembles the cache.Step for running target on p.
func (m *Magus) buildStep(p *types.Project, target string) cache.Step {
	step := m.baseStep(p)
	step.Target = target
	for _, s := range p.ResolvedSpells {
		step.Sources = append(step.Sources, s.TargetSources()[target]...)
	}
	// Per-target inputs declared via magus.inputs (one InputRef shape for same-project
	// globs and cross-project files; each carries its owning project). This is the one
	// place the declared "inputs" vocabulary folds into the cache Step's aggregate
	// "sources" footprint: joinGlob(Project, Glob) - the SAME join the outputs fold
	// below uses, so inputs and outputs treat an identical literal identically. Inputs
	// ADD to this target's key (unioned onto the project-wide globs baseStep seeded,
	// deduped so a glob declared both places isn't hashed twice); keyVersion is unchanged.
	for _, ref := range p.TargetInputs[target] {
		if g := joinGlob(ref.Project, ref.Glob); !slices.Contains(step.Sources, g) {
			step.Sources = append(step.Sources, g)
		}
	}
	for _, g := range p.TargetOutputs[target] {
		if jg := joinGlob(p.Path, g); !slices.Contains(step.Outputs, jg) {
			step.Outputs = append(step.Outputs, jg)
		}
	}
	step.DependsOn = p.DependsOn
	pol := p.TargetPolicies[target]
	// A service op is a long-running process: it must never be cached, or a re-run
	// would replay a completed-target result instead of restarting the process. This
	// is inherent (not an author opt-in), so OR it into the explicit SkipCache policy.
	step.NoCache = pol.SkipCache || servesTarget(p.ResolvedSpells, target)
	step.Exclusive = pol.Exclusive
	step.Slots = pol.Slots
	return step
}

// effectiveOutputs is a target's full output-glob set: the project-wide Outputs
// unioned with the per-target globs it declared via magus.outputs. It keeps the race
// detector and race-replay diagnostics consistent with the cache, which sees the same
// deduped union via buildStep's step.Outputs. Globs are project-relative (as p.Outputs
// and the per-target values are stored pre-join); callers join to p.Dir themselves.
//
// There is no effectiveSources twin: the sources union has a single consumer (buildStep,
// which folds it inline into the cache key), whereas outputs need the union in three
// places (the race detector plus the pre/post race-replay snapshots), so only outputs
// earn a named helper.
func effectiveOutputs(p *types.Project, target string) []string {
	extra := p.TargetOutputs[target]
	if len(extra) == 0 {
		return p.Outputs
	}
	out := make([]string, 0, len(p.Outputs)+len(extra))
	out = append(out, p.Outputs...)
	for _, g := range extra {
		if !slices.Contains(out, g) {
			out = append(out, g)
		}
	}
	return out
}

// servesTarget reports whether target is backed by a service op in any of the
// project's resolved spells.
func servesTarget(spells []*types.Spell, target string) bool {
	for _, s := range spells {
		if s.IsServiceTarget(target) {
			return true
		}
	}
	return false
}

// firstTargetPolicy returns the policy for target from the first project that declares one.
func firstTargetPolicy(projects []*types.Project, target string) types.Target {
	for _, p := range projects {
		if pol, ok := p.TargetPolicies[target]; ok {
			return pol
		}
	}
	return types.Target{}
}

// toolVersionMode resolves the cache tool-version policy from MAGUS_CACHE_TOOL_VERSION.
func toolVersionMode() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MAGUS_CACHE_TOOL_VERSION"))) {
	case "off":
		return "off"
	case "workspace":
		return "workspace"
	default:
		return "project"
	}
}

// toolVersionsByProject returns ProjectPath to "spell:version" entries for cache keys.
// Probes are memoized by (spell, dir); failures record "spell:UNPROBED".
func (m *Magus) toolVersionsByProject(ctx context.Context, projects []*types.Project) map[string][]string {
	mode := toolVersionMode()
	if mode == "off" {
		return nil
	}
	memo := make(map[string]string)
	out := make(map[string][]string, len(projects))
	for _, p := range projects {
		dir := p.Dir
		if mode == "workspace" {
			dir = m.ws.Root
		}
		var vers []string
		for _, s := range p.ResolvedSpells {
			if !s.HasVersionProbe() {
				continue
			}
			key := s.Name() + "\x00" + dir
			v, ok := memo[key]
			if !ok {
				probed, err := s.ProbeVersion(ctx, dir)
				if err != nil {
					slog.WarnContext(ctx, "magus: tool-version probe failed; cache key records UNPROBED",
						slog.String("spell", s.Name()), slog.String("dir", dir), slog.String("err", err.Error()))
					probed = "UNPROBED"
				} else {
					slog.DebugContext(ctx, "magus: tool-version probe",
						slog.String("spell", s.Name()), slog.String("dir", dir), slog.String("version", probed))
				}
				v = probed
				memo[key] = v
			}
			vers = append(vers, s.Name()+":"+v)
		}
		if len(vers) > 0 {
			out[p.Path] = vers
		}
	}
	return out
}

// executeOnProjects runs handler for every project for a single target.
func (m *Magus) executeOnProjects(ctx context.Context, projects []*types.Project, target string, scopeLabel string, opts run, handler TargetHandler) error {
	return m.executeStages(ctx, []stage{{target: target, handler: handler, projects: projects}}, scopeLabel, opts)
}

// executeStages schedules every (project,target) pair via dependency-ordered RunAll.
func (m *Magus) executeStages(ctx context.Context, stages []stage, scopeLabel string, opts run) error {
	if opts.DryRun {
		// Deep dry run: evaluate each target body under a tracing context, so
		// effectful host ops (exec, fs writes, network, env) record their intent and
		// skip instead of running. Sequential, so each project's commands stay grouped
		// under its [dry] line. Reads still work, so the plan reflects real conditionals.
		recCtx := types.WithTrace(ctx)
		if m.cache != nil {
			m.cache.LogDryBanner()
		} else {
			fmt.Println("dry run - commands shown, not executed")
		}
		for _, st := range stages {
			for _, p := range st.projects {
				label := types.ProjectLabel(p.Path, p.Dir)
				if m.cache != nil {
					m.cache.LogDry(label, st.target)
				} else {
					fmt.Printf("[dry] %s %s\n", label, st.target)
				}
				// Fresh memo per target so a shared dependency (e.g. format -> generate)
				// records once, matching the real run's pool dedup.
				stepCtx := buzz.WithTargetMemo(recCtx, buzz.NewTargetMemo())
				if err := st.handler(stepCtx, p); err != nil {
					slog.WarnContext(ctx, "dry-run: target evaluation stopped early",
						slog.String("project", label), slog.String("target", st.target), slog.String("error", err.Error()))
				}
			}
		}
		return nil
	}

	start := time.Now()

	var uniqueProjects []*types.Project
	seenProj := make(map[string]struct{})
	for _, st := range stages {
		for _, p := range st.projects {
			if _, ok := seenProj[p.Path]; !ok {
				seenProj[p.Path] = struct{}{}
				uniqueProjects = append(uniqueProjects, p)
			}
		}
	}
	// Per-project workspace lock: this is a mutating invocation (it writes outputs
	// and the cache), so take every reachable project's EXCLUSIVE advisory lock up
	// front, in sorted order, and hold it for the whole invocation. It serializes
	// against a SEPARATE concurrent magus process; the intra-process scheduler fans
	// out beneath it untouched. Acquired here (after the dry-run early return) so a
	// dry run, which mutates nothing, takes no lock.
	releaseLocks, err := m.acquireProjectLocks(ctx, uniqueProjects)
	if err != nil {
		return err
	}
	defer releaseLocks()

	toolVer := m.toolVersionsByProject(ctx, uniqueProjects)

	// Active charms participate in the cache key: a charm can change a target's
	// behaviour (pass/fail or output), so charm-variant runs must not collide.
	// A charm-less run hashes identically to before, keeping existing entries valid.
	charmKey := slices.Clone(opts.Charms)
	slices.Sort(charmKey)
	charmKey = slices.Compact(charmKey)

	var steps []cache.Step
	byPath := make(map[string]*types.Project)
	handlerOf := make(map[string]TargetHandler, len(stages))
	active := make(map[string]struct{})
	declaredCharms := map[string]struct{}{}
	trackVolatile := false
	for _, st := range stages {
		handlerOf[st.target] = st.handler
		if firstTargetPolicy(st.projects, st.target).RetryOnVolatile {
			trackVolatile = true
		}
		for _, p := range st.projects {
			step := m.buildStep(p, st.target)
			step.ToolVersions = toolVer[p.Path]
			step.Charms = charmKey
			if raceForcesNoCache(opts) {
				step.NoCache = true
			}
			if opts.NoCache {
				step.SkipReplay = true
			}
			steps = append(steps, step)
			byPath[p.Path] = p
			active[p.Path] = struct{}{}
			for _, s := range p.ResolvedSpells {
				for _, c := range s.Charms(st.target) {
					declaredCharms[types.NormalizeCharmName(c)] = struct{}{}
				}
			}
		}
	}
	if len(steps) == 0 {
		return nil
	}

	// Soft typo guard: warn for an active charm no selected target declares. A
	// function target may read an undeclared charm, hence a warning, not an error.
	for _, c := range undeclaredCharms(charmKey, declaredCharms) {
		slog.WarnContext(ctx, "magus: charm not declared by any selected target (typo? a function target may still read it)", "charm", c)
	}

	// MGS5001: warn when this run brings up services that look like near-duplicate
	// copies of one shared service (same image and container port, subtly different).
	// Scoped to the run's reachable projects so it fires at the moment of cost.
	m.warnNearDuplicateServices(uniqueProjects, charmKey)

	if opts.Report == nil && opts.ReportWriter != nil {
		rw := report.NewWriter(opts.ReportWriter)
		defer func() { _ = rw.Close() }()
		opts.Report = rw
	}

	if opts.Report != nil {
		ctx = report.WithWriter(ctx, opts.Report)
	}
	// Capture diagnostics fired during this run into one sink: it forwards each to
	// the report stream and, at run end, persists the set to the runtime records
	// that enrich the knowledge graph's @runtime shard (one capture, two consumers).
	diag := &diagCollector{report: opts.Report}
	ctx = types.WithDiagnosticSink(ctx, diag)
	if !cacheImmutable(m.cfg) {
		defer func() {
			if evs := diag.snapshot(); len(evs) > 0 {
				if err := knowledge.RecordRuntimeEvents(resolveCacheDir(m.Root(), m.cfg), evs); err != nil {
					slog.Debug("magus: could not persist runtime diagnostics", slog.String("error", err.Error()))
				}
			}
		}()
	}
	if m.tel != nil {
		ctx = observability.WithProvider(ctx, m.tel)
		// Let cache.Run open phase spans (hash/replay/snapshot) without the cache
		// package importing observability; CacheTracer is nil (no-op) when disabled.
		ctx = cache.ContextWithTracer(ctx, observability.CacheTracer(m.tel))
	}
	if m.cfg.Sandbox.Enabled {
		var err error
		ctx, err = m.applySandbox(ctx)
		if err != nil {
			return err
		}
	}
	ctx = installWorkspaceRegistry(ctx, m.wsReg)
	ctx = types.WithWorkspace(ctx, m)
	ctx = types.WithActiveDispatch(ctx, active)
	ctx = types.WithCharms(ctx, opts.Charms)
	norm := opts.Normalizer
	if norm == nil {
		norm = types.DefaultTargetNameNormalizer
	}
	ctx = interp.WithTargetNameNormalizer(ctx, norm)
	if o, ok := origin.FromContext(ctx); ok {
		slog.InfoContext(
			ctx, "[AGENT] build triggered",
			slog.String("agent", o.Agent),
			slog.String("scope", scopeLabel),
		)
	}

	var volatilityRT *volatility.Runtime
	if trackVolatile && m.cfg.Volatility.Enabled && !opts.NoVolatilityRetry {
		volatilityRT = m.buildVolatilityRuntime(ctx)
		if volatilityRT != nil {
			ctx = volatility.WithRuntime(ctx, volatilityRT)
		}
	}

	checkOutputOverlap(dedupeByProject(steps), scopeLabel, opts.Report)

	var raceRT *race.Runtime
	if opts.Race {
		raceRT = m.buildRaceRuntime()
		if err := raceRT.Start(ctx); err != nil {
			slog.Warn("magus: race detector unavailable", "err", err)
			raceRT = nil
		} else {
			ctx = race.WithRuntime(ctx, raceRT)
		}
	}

	if len(opts.ExtraArgs) > 0 {
		ctx = project.WithExtraArgs(ctx, opts.ExtraArgs)
	}

	ctx = buzz.WithPoolRegistry(ctx, m.buzzPoolRegistry())
	// Feed Buzz session-pool lifecycle (reuse, warm, eviction, idle) to the spine.
	// nil when telemetry is disabled, so the pool runs unobserved on one-shot runs.
	if po := interp.NewPoolObserver(ctx); po != nil {
		ctx = buzz.WithPoolObserver(ctx, po)
	}
	// One coordinator per run so target-level cross-project deps (project imports)
	// run their remote target at most once and detect cross-project cycles.
	ctx = interp.WithCrossDispatch(ctx, interp.NewCrossDispatch())
	lim := m.limiter()
	if opts.Step {
		slog.Info("magus: --step forces Concurrency=1")
		lim = cache.NewLimiter(1)
	}
	cacheOpts := []cache.RunOption{cache.WithLimiter(lim)}
	cacheOpts = append(cacheOpts, observability.CacheRunOptions(ctx, m.tel)...)
	spellsOf := func(projectPath string) []string {
		p, ok := byPath[projectPath]
		if !ok {
			return nil
		}
		names := make([]string, len(p.ResolvedSpells))
		for i, s := range p.ResolvedSpells {
			names[i] = s.Name()
		}
		return names
	}
	cacheOpts = append(cacheOpts, observability.TargetRunOptions(ctx, m.tel, spellsOf)...)
	if opts.Report != nil {
		cacheOpts = append(cacheOpts, report.RunOptions(opts.Report)...)
	}
	cacheOpts = append(cacheOpts, diagnosticCaptureOption(ctx))
	if m.cache == nil {
		return fmt.Errorf("magus: workspace was constructed with Inspect; use Open to enable Run")
	}
	// One service supervisor per run: a service op reached as a dependency is started
	// and readiness-gated, deduped by fingerprint so N dependents share one instance,
	// and released when the run ends (warm on the daemon, or stopped in-process).
	svcSession := m.newServiceSession(ctx)
	defer svcSession.ReleaseAll()
	ctx = service.WithSession(ctx, svcSession)
	_, runErr := m.cache.RunAll(ctx, steps, func(ctx context.Context, s cache.Step) error {
		// Each step invocation gets a fresh TargetMemo so depends_on diamonds
		// within one target's inline dispatch run shared deps exactly once.
		ctx = buzz.WithTargetMemo(ctx, buzz.NewTargetMemo())

		p := byPath[s.ProjectPath]
		handler := handlerOf[s.Target]
		spanCtx, endSpan := m.tel.StartSpan(
			ctx,
			"magus.target.run",
			observability.Attr{Key: "magus.project", Value: s.ProjectPath},
			observability.Attr{Key: "magus.target", Value: s.Target},
		)
		// In collapse mode the project's subprocess output is withheld, so attach a
		// stage observer: it prints a progress line as each magus.needs sub-target
		// completes, giving the reader a checklist of what ran in place of the wall.
		if m.cache.Collapsing() {
			spanCtx = buzz.WithObserver(spanCtx, stageObserver{cache: m.cache, label: s.Label})
		}
		var err error
		if raceRT != nil {
			outDirs := diff.GlobBaseDirs(p.Dir, effectiveOutputs(p, s.Target))
			err = raceRT.TrackProject(s.ProjectPath, s.Target, outDirs, func() error {
				return handler(spanCtx, p)
			})
		} else {
			err = handler(spanCtx, p)
		}
		endSpan(err)
		return err
	}, cacheOpts...)

	if volatilityRT != nil {
		if err := volatilityRT.Save(ctx); err != nil {
			slog.Warn("magus: failed to save volatility history", "err", err)
		}
	}

	if opts.RaceReplay && runErr == nil {
		for _, st := range stages {
			runReplay(ctx, st.projects, st.target, byPath, st.handler, opts.Report)
		}
	}

	if raceRT != nil {
		writtenByProject := raceRT.WrittenPaths()
		if err := raceRT.Flush(ctx, opts.Report); err != nil {
			slog.Warn("magus: race detector flush failed", "err", err)
		}
		checkMissingDependencies(m.ws.All(), byPath, writtenByProject, scopeLabel, opts.Report)
	}

	// Footer summary for a fan-out: a single line tallying the per-project results.
	// Skipped for a single project, where the per-project status line already says it all.
	if s := m.cache.Stats(); s.Hit+s.Miss+s.Error > 1 {
		m.cache.LogSummary(time.Since(start))
	}

	return runErr
}

// stageObserver bridges the Buzz pool's per-target notifications to the cache logger:
// as each magus.needs sub-target finishes, it emits a stage progress line for the
// owning project. Attached only in collapse mode (see executeStages), where the
// project's own subprocess output is withheld. It implements buzz.TargetObserver.
type stageObserver struct {
	cache *cache.Cache
	label string // normalized project display name (never "" or "."); see types.ProjectLabel
}

func (o stageObserver) TargetEnd(_ context.Context, name string, elapsed time.Duration, err error) {
	o.cache.LogStage(o.label, name, elapsed, err)
}

// dedupeByProject returns one step per ProjectPath (first seen).
func dedupeByProject(steps []cache.Step) []cache.Step {
	seen := make(map[string]struct{}, len(steps))
	out := make([]cache.Step, 0, len(steps))
	for _, s := range steps {
		if _, ok := seen[s.ProjectPath]; ok {
			continue
		}
		seen[s.ProjectPath] = struct{}{}
		out = append(out, s)
	}
	return out
}

func (m *Magus) buildRaceRuntime() *race.Runtime {
	return race.NewRuntime(m.ws.Root)
}

// runReplay re-executes projects and compares output content hashes to detect
// non-determinism (MGS4003). Bypasses cache so spells actually re-execute.
func runReplay(ctx context.Context, projects []*types.Project, target string,
	byPath map[string]*types.Project, handler TargetHandler,
	w *report.Writer,
) {
	var replayable []*types.Project
	for _, p := range projects {
		if len(effectiveOutputs(p, target)) > 0 {
			replayable = append(replayable, p)
		}
	}
	if len(replayable) == 0 {
		return
	}

	snapsA := make(map[string]diff.ContentSnap, len(replayable))
	for _, p := range replayable {
		snapsA[p.Path] = diff.HashContent(diff.GlobBaseDirs(p.Dir, effectiveOutputs(p, target)))
	}

	for _, p := range replayable {
		if err := handler(ctx, byPath[p.Path]); err != nil {
			slog.Warn("magus: race-replay handler failed", "project", p.Path, "err", err)
		}
	}

	for _, p := range replayable {
		postSnap := diff.HashContent(diff.GlobBaseDirs(p.Dir, effectiveOutputs(p, target)))
		changed := diff.DiffContent(snapsA[p.Path], postSnap)
		if len(changed) == 0 {
			continue
		}
		fmt.Fprintln(os.Stderr, types.FormatDiagnostic(types.NondeterministicOutput,
			fmt.Sprintf("non-deterministic output\n  project=%s target=%s differing_paths=%v",
				p.Path, target, changed)))
		_ = report.Record(w, report.DeterminismMismatch{
			Project:        p.Path,
			Target:         target,
			DifferingPaths: changed,
		})
	}
}

// checkMissingDependencies audits for undeclared dependencies (MGS4004).
// For each written path, finds workspace projects that consume it but weren't dispatched.
func checkMissingDependencies(allProjects []*types.Project, dispatched map[string]*types.Project,
	written map[string][]string, target string, w *report.Writer,
) {
	if len(written) == 0 {
		return
	}
	for _, consumer := range allProjects {
		if _, isDispatched := dispatched[consumer.Path]; isDispatched {
			continue
		}
		if len(consumer.Sources) == 0 {
			continue
		}
		consumerGlobs := make([]string, len(consumer.Sources))
		for i, g := range consumer.Sources {
			consumerGlobs[i] = filepath.Join(consumer.Dir, g)
		}
		for producer, paths := range written {
			if producer == consumer.Path {
				continue
			}
			for _, path := range paths {
				for _, glob := range consumerGlobs {
					if ok, _ := doublestar.PathMatch(glob, path); ok {
						fmt.Fprintln(os.Stderr, types.FormatDiagnostic(types.MissingDependencyDetected,
							fmt.Sprintf("potential undeclared dependency\n  consumer=%s producer=%s path=%s target=%s",
								consumer.Path, producer, path, target)))
						_ = report.Record(w, report.MissingDependency{
							Consumer: consumer.Path,
							Producer: producer,
							Path:     path,
							Target:   target,
						})
						break
					}
				}
			}
		}
	}
}

// checkOutputOverlap detects projects in the same dispatch that declare the same
// output glob (MGS4002). Runs at graph construction time.
func checkOutputOverlap(steps []cache.Step, target string, w *report.Writer) {
	for i := 0; i < len(steps); i++ {
		if len(steps[i].Outputs) == 0 {
			continue
		}
		outSet := make(map[string]struct{}, len(steps[i].Outputs))
		for _, o := range steps[i].Outputs {
			outSet[o] = struct{}{}
		}
		for j := i + 1; j < len(steps); j++ {
			if len(steps[j].Outputs) == 0 {
				continue
			}
			var overlap []string
			for _, o := range steps[j].Outputs {
				if _, ok := outSet[o]; ok {
					overlap = append(overlap, o)
				}
			}
			if len(overlap) == 0 {
				continue
			}
			pA, pB := steps[i].ProjectPath, steps[j].ProjectPath
			if pA > pB {
				pA, pB = pB, pA
			}
			fmt.Fprintln(os.Stderr, types.FormatDiagnostic(types.OutputOverlapDetected,
				fmt.Sprintf("declared output overlap\n  projects=[%s,%s] target=%s overlapping=%v",
					pA, pB, target, overlap)))
			_ = report.Record(w, report.OutputOverlapDetected{
				ProjectA:    pA,
				ProjectB:    pB,
				Target:      target,
				Overlapping: overlap,
			})
		}
	}
}

// buildVolatilityRuntime returns a volatility.Runtime for the current run, or nil when history cannot be loaded.
func (m *Magus) buildVolatilityRuntime(ctx context.Context) *volatility.Runtime {
	var h forecast.History
	if err := h.Load(ctx, m.cfg.HistoryPath); err != nil {
		return nil
	}
	var affected []string
	if res, err := m.Affected(ctx, ""); err == nil {
		affected = res.Affected
	}
	return volatility.NewRuntime(&h, m.cfg.HistoryPath, m.volatilityConfig(), affected)
}

// runTarget executes name on every spell in p under an audit that warns on out-of-dispatch writes.
func runTarget(ctx context.Context, p *types.Project, name string) error {
	a := audit.Begin(ctx, p, types.HasCharm(ctx, types.CharmReadWrite))
	err := forEachSpell(ctx, p, name, func(ctx context.Context, s *types.Spell) error {
		return invokeSpell(ctx, p, name, s)
	})
	a.Finish(ctx, name)
	return err
}

// invokeSpell executes one spell; when a volatility.Runtime is present, failures are eligible for auto-retry.
func invokeSpell(ctx context.Context, p *types.Project, name string, s *types.Spell) error {
	req := types.InvokeRequest{Target: name, Dir: p.Dir}
	rt := volatility.RuntimeFromContext(ctx)
	if rt == nil {
		_, err := s.Invoke(ctx, req)
		return err
	}

	volatileTarget := s.Name() + "/" + name
	affected := rt.IsAffected(p.Path)
	start := time.Now()
	_, err := s.Invoke(ctx, req)
	result := "pass"
	attempts := 1
	decision := volatility.Decision{}

	if err != nil {
		decision = rt.Decide(p.Path, volatileTarget, affected)
		if decision.Retry {
			_, err2 := s.Invoke(ctx, req)
			attempts = 2
			if err2 == nil {
				result = "volatile"
				err = nil
			} else {
				result = "fail"
				err = err2
			}
		} else {
			result = "fail"
		}
	}

	rt.Record(p.Path, volatileTarget, forecast.Outcome{
		Result:         result,
		AffectedByDiff: affected,
		DurationMs:     time.Since(start).Milliseconds(),
		At:             time.Now(),
		Attempts:       attempts,
	})

	if rw := report.WriterFromContext(ctx); rw != nil && decision.Retry {
		status := "retry_failed"
		if result == "volatile" {
			status = "retried_volatile"
		} else if rt.IsRegression(p.Path, volatileTarget) {
			status = "suspected_regression"
		}
		_ = report.Record(rw, report.VolatilityCall{
			Project:         p.Path,
			Target:          volatileTarget,
			Status:          status,
			Attempts:        attempts,
			RetryReason:     string(decision.Reason),
			VolatilityScore: rt.Score(p.Path, volatileTarget),
		})
	}

	return err
}

// verifyReadOnly runs fn - a target expected to be read-only (preflight/generate
// without the rw charm) - then fails if it left uncommitted changes in dir, i.e. it
// wrote when it should only have checked (the error points the user at the rw charm).
// Skipped when dir has no VCS, so the guard never blocks a non-repo checkout.
func verifyReadOnly(ctx context.Context, dir, target string, fn func() error) error {
	if err := fn(); err != nil {
		return err
	}
	// Resolve the active VCS (git/hg/jj) rather than shelling out to git, so the
	// cleanliness gate works under any backend. No VCS or a failed probe skips the
	// check, matching the prior "skip when git is unavailable" behavior.
	res, err := vcs.Resolve(ctx, dir, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return nil //nolint:nilerr // VCS unavailable or unresolved: skip the post-write cleanliness check
	}
	files, err := res.VCS.DirtyFiles(ctx, dir, []string{"."})
	if err != nil {
		return nil //nolint:nilerr // VCS status unavailable: skip the post-write cleanliness check
	}
	if len(files) > 0 {
		return fmt.Errorf("%s: %s produced uncommitted changes; re-run with the rw charm (%s:rw) to apply:\n%s",
			dir, target, target, strings.Join(files, "\n"))
	}
	return nil
}

func (m *Magus) makeHandler(name string) TargetHandler {
	if name == "preflight" || name == "generate" {
		return func(ctx context.Context, p *types.Project) error {
			run := func() error { return runTarget(ctx, p, name) }
			pol := p.TargetPolicies[name]
			if pol.FailOnDrift && !types.HasCharm(ctx, types.CharmReadWrite) {
				return verifyReadOnly(ctx, p.Dir, name, run)
			}
			return run()
		}
	}
	return func(ctx context.Context, p *types.Project) error {
		return runTarget(ctx, p, name)
	}
}

// makeSpellFilteredHandler returns a handler that runs name on a single named spell.
func (*Magus) makeSpellFilteredHandler(name, spellName string) TargetHandler {
	return func(ctx context.Context, p *types.Project) error {
		return forSpellNamed(ctx, p, name, spellName, func(ctx context.Context, s *types.Spell) error {
			return invokeSpell(ctx, p, name, s)
		})
	}
}

// diagCollector is the run-scoped diagnostic sink: it forwards each captured
// diagnostic to the report stream and retains the set for the run to persist to the
// knowledge graph's @runtime shard. One capture, two consumers. Concurrency-safe.
type diagCollector struct {
	mu     sync.Mutex
	events []types.DiagnosticEvent
	report *report.Writer // forward target; nil when no report is configured (Record is a no-op)
}

func (d *diagCollector) Record(ev types.DiagnosticEvent) {
	d.mu.Lock()
	d.events = append(d.events, ev)
	d.mu.Unlock()
	_ = report.Record(d.report, report.DiagnosticEmitted{Unit: ev.Unit, Code: string(ev.Code), Message: ev.Message})
}

// snapshot returns a copy of the collected events for persistence at run end.
func (d *diagCollector) snapshot() []types.DiagnosticEvent {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]types.DiagnosticEvent(nil), d.events...)
}

// diagnosticCaptureOption records a failed target's DiagnosticError to the run's
// sink, tagged with the target's identity, via the same EmitDiagnostic path a deep
// emission site uses. This is the primary capture point: a diagnostic that fails a
// target surfaces here as the run error, with s.ProjectPath/s.Target in hand.
func diagnosticCaptureOption(ctx context.Context) cache.RunOption {
	return cache.OnResult(func(s *cache.Step, _ *cache.Result, err error) {
		if ev, ok := diagEventFromError(s.ProjectPath, s.Target, err); ok {
			types.EmitDiagnostic(ctx, ev)
		}
	})
}

// diagEventFromError extracts a DiagnosticEvent from a target's run error when it
// is a coded DiagnosticError, tagging it with the target's identity. Returns
// ok=false for a nil or non-diagnostic error (a plain build failure is not an MGS
// event).
func diagEventFromError(projectPath, target string, err error) (types.DiagnosticEvent, bool) {
	var de *types.DiagnosticError
	if err == nil || !errors.As(err, &de) {
		return types.DiagnosticEvent{}, false
	}
	unit := projectPath
	if target != "" {
		unit += ":" + target
	}
	return types.DiagnosticEvent{Code: de.Code, Message: de.Msg, Unit: unit}, true
}
