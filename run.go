package magus

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/magus/internal/audit"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/ci/flake"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/describe"
	"github.com/egladman/magus/internal/file/diff"
	"github.com/egladman/magus/internal/interactive"
	interp "github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/mcp/origin"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/race"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// RunOption configures a [Run], [CI], or [RunAffected] invocation.
type RunOption func(*run)

// run is the accumulated state of a Run/CI/RunAffected call.
type run struct {
	DryRun       bool
	Charms       []string       // execution charms propagated via context; "rw" enables mutating targets
	Report       *report.Writer // caller-owned; caller closes; mutually exclusive with ReportWriter
	ReportWriter io.Writer      // run engine wraps this in its own Writer
	NoFlakeRetry bool
	BaseRef      string
	Race         bool     // MGS4001/4002/4004 race diagnostics; near-zero overhead
	RaceReplay   bool     // MGS4003 determinism replay; orthogonal to Race
	Spell        string   // when set, restricts execution to this spell; unmatched projects are skipped
	Step         bool     // forces Concurrency=1; StepGate comes from ctx
	ExtraArgs    []string // forwarded to spells via project.WithExtraArgs
	Normalizer   types.TargetNameNormalizer
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

// WithNoFlakeRetry disables the flake auto-retry logic.
func WithNoFlakeRetry() RunOption { return func(o *run) { o.NoFlakeRetry = true } }

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

	var stages []stage
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
// not a hardcoded preflight→…→test chain. The magusfile composes the pipeline
// order via magus.needs.
func (m *Magus) RunCI(ctx context.Context, targets []types.Target, opts ...RunOption) error {
	o := applyRunOpts(opts)
	o.Charms = slices.DeleteFunc(slices.Clone(o.Charms), func(s string) bool {
		return types.NormalizeCharmName(s) == types.CharmReadWrite
	})

	// ci is the one target that must not silently no-op when undefined. Ordinary
	// targets fan out and skip projects that lack them, but ci is the anchor that
	// `magus affected ci` and `magus affected --plan` key off — a missing ci would
	// otherwise exit 0 having run nothing, a green gate that gated nothing. When
	// the run scope has projects but none declare ci, fail with an actionable
	// message and a hint. (An empty scope — e.g. affected with no changes — is a
	// legitimate no-op and is left alone.)
	// Only block when we definitely scanned the scope and found no ci — a scan
	// error (unreadable magusfile) must not masquerade as "no ci" and abort the
	// gate; let runResolved surface the real read failure instead.
	if projects := m.targetProjects(targets); len(projects) > 0 {
		if has, scanErr := anyProjectDeclaresCI(projects); !has && scanErr == nil {
			interactive.Emit(os.Stderr, "define a ci target in your magusfile to compose the gate, e.g.  "+
				"export fun ci(_a: [str]) > void { magus.needs(magus.target.literal(\"build\"), magus.target.literal(\"test\"), magus.target.literal(\"lint\")) }  "+
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
// DescribeGraph uses — never a raw text scan, so `ci` in a comment or string can't
// false-positive) and short-circuits on the first ci found. The returned error is
// non-nil if a source couldn't be located, so a (false, err) result means "couldn't
// determine" rather than "definitely no ci" — the caller must not block on it.
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
// excluding magus's reserved built-in charms (write, cd) — candidates for a
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

// stage is one target to run across a project set. afterTarget orders this stage
// after the named target for the same project (CI step ordering).
type stage struct {
	target      string
	afterTarget string
	handler     func(context.Context, *types.Project) error
	projects    []*types.Project
}

// buildStep assembles the cache.Step for running target on p.
func (m *Magus) buildStep(p *types.Project, target string) cache.Step {
	step := m.baseStep(p)
	step.Target = target
	for _, s := range p.ResolvedSpells {
		step.Sources = append(step.Sources, s.TargetSources()[target]...)
	}
	step.DependsOn = p.DependsOn
	pol := p.TargetPolicies[target]
	step.NoCache = pol.SkipCache
	step.Isolated = pol.Exclusive
	return step
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

// toolVersionsByProject returns ProjectPath → "spell:version" entries for cache keys.
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
func (m *Magus) executeOnProjects(ctx context.Context, projects []*types.Project, target string, scopeLabel string, opts run, handler func(context.Context, *types.Project) error) error {
	return m.executeStages(ctx, []stage{{target: target, handler: handler, projects: projects}}, scopeLabel, opts)
}

// executeStages schedules every (project,target) pair via dependency-ordered RunAll;
// afterTarget edges keep each project's CI steps sequential.
func (m *Magus) executeStages(ctx context.Context, stages []stage, scopeLabel string, opts run) error {
	if opts.DryRun {
		for _, st := range stages {
			for _, p := range st.projects {
				fmt.Printf("[dry-run] would run %s for %s\n", st.target, p.Path)
			}
		}
		return nil
	}

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
	toolVer := m.toolVersionsByProject(ctx, uniqueProjects)

	// Active charms participate in the cache key: a charm can change a target's
	// behaviour (pass/fail or output), so charm-variant runs must not collide.
	// A charm-less run hashes identically to before, keeping existing entries valid.
	charmKey := slices.Clone(opts.Charms)
	slices.Sort(charmKey)
	charmKey = slices.Compact(charmKey)

	var steps []cache.Step
	byPath := make(map[string]*types.Project)
	handlerOf := make(map[string]func(context.Context, *types.Project) error, len(stages))
	active := make(map[string]struct{})
	declaredCharms := map[string]struct{}{}
	trackFlake := false
	for _, st := range stages {
		handlerOf[st.target] = st.handler
		if firstTargetPolicy(st.projects, st.target).RetryOnFlake {
			trackFlake = true
		}
		for _, p := range st.projects {
			step := m.buildStep(p, st.target)
			step.ToolVersions = toolVer[p.Path]
			step.Charms = charmKey
			if st.afterTarget != "" {
				step.After = []string{cache.DepKey(p.Path, st.afterTarget)}
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

	if opts.Report == nil && opts.ReportWriter != nil {
		rw := report.NewWriter(opts.ReportWriter)
		defer func() { _ = rw.Close() }()
		opts.Report = rw
	}

	if opts.Report != nil {
		ctx = report.WithWriter(ctx, opts.Report)
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

	var flakeRT *flake.Runtime
	if trackFlake && m.cfg.Flake.Enabled && !opts.NoFlakeRetry {
		flakeRT = m.buildFlakeRuntime(ctx)
		if flakeRT != nil {
			ctx = flake.WithRuntime(ctx, flakeRT)
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
	if m.cache == nil {
		return fmt.Errorf("magus: workspace was constructed with Inspect; use Open to enable Run")
	}
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
		var err error
		if raceRT != nil {
			outDirs := diff.GlobBaseDirs(p.Dir, p.Outputs)
			err = raceRT.TrackProject(s.ProjectPath, s.Target, outDirs, func() error {
				return handler(spanCtx, p)
			})
		} else {
			err = handler(spanCtx, p)
		}
		endSpan(err)
		return err
	}, cacheOpts...)

	if flakeRT != nil {
		if err := flakeRT.Save(ctx); err != nil {
			slog.Warn("magus: failed to save flake history", "err", err)
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

	return runErr
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
	byPath map[string]*types.Project, handler func(context.Context, *types.Project) error,
	w *report.Writer,
) {
	var replayable []*types.Project
	for _, p := range projects {
		if len(p.Outputs) > 0 {
			replayable = append(replayable, p)
		}
	}
	if len(replayable) == 0 {
		return
	}

	snapsA := make(map[string]diff.ContentSnap, len(replayable))
	for _, p := range replayable {
		snapsA[p.Path] = diff.HashContent(diff.GlobBaseDirs(p.Dir, p.Outputs))
	}

	for _, p := range replayable {
		if err := handler(ctx, byPath[p.Path]); err != nil {
			slog.Warn("magus: race-replay handler failed", "project", p.Path, "err", err)
		}
	}

	for _, p := range replayable {
		postSnap := diff.HashContent(diff.GlobBaseDirs(p.Dir, p.Outputs))
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

// buildFlakeRuntime returns a flake.Runtime for the current run, or nil when history cannot be loaded.
func (m *Magus) buildFlakeRuntime(ctx context.Context) *flake.Runtime {
	var h forecast.History
	if err := h.Load(ctx, m.cfg.HistoryPath); err != nil {
		return nil
	}
	var affected []string
	if res, err := m.Affected(ctx, ""); err == nil {
		affected = res.Affected
	}
	return flake.NewRuntime(&h, m.cfg.HistoryPath, m.flakeConfig(), affected)
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

// invokeSpell executes one spell; when a flake.Runtime is present, failures are eligible for auto-retry.
func invokeSpell(ctx context.Context, p *types.Project, name string, s *types.Spell) error {
	req := types.InvokeRequest{Target: name, Dir: p.Dir}
	rt := flake.RuntimeFromContext(ctx)
	if rt == nil {
		_, err := s.Invoke(ctx, req)
		return err
	}

	flakeTarget := s.Name() + "/" + name
	affected := rt.IsAffected(p.Path)
	start := time.Now()
	_, err := s.Invoke(ctx, req)
	result := "pass"
	attempts := 1
	decision := flake.Decision{}

	if err != nil {
		decision = rt.Decide(p.Path, flakeTarget, affected)
		if decision.Retry {
			_, err2 := s.Invoke(ctx, req)
			attempts = 2
			if err2 == nil {
				result = "flake"
				err = nil
			} else {
				result = "fail"
				err = err2
			}
		} else {
			result = "fail"
		}
	}

	rt.Record(p.Path, flakeTarget, forecast.Outcome{
		Result:         result,
		AffectedByDiff: affected,
		DurationMs:     time.Since(start).Milliseconds(),
		At:             time.Now(),
		Attempts:       attempts,
	})

	if rw := report.WriterFromContext(ctx); rw != nil && decision.Retry {
		status := "retry_failed"
		if result == "flake" {
			status = "retried_flake"
		} else if rt.IsRegression(p.Path, flakeTarget) {
			status = "suspected_regression"
		}
		_ = report.Record(rw, report.FlakeCall{
			Project:     p.Path,
			Target:      flakeTarget,
			Status:      status,
			Attempts:    attempts,
			RetryReason: string(decision.Reason),
			FlakeScore:  rt.Score(p.Path, flakeTarget),
		})
	}

	return err
}

// checkCleanAfter runs fn then fails if the working tree is dirty. Skipped when git is unavailable.
func checkCleanAfter(ctx context.Context, dir, target string, fn func() error) error {
	if err := fn(); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain", "--", ".")
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil //nolint:nilerr // git unavailable: skip the post-write cleanliness check
	}
	if dirty := strings.TrimSpace(out.String()); dirty != "" {
		return fmt.Errorf("%s: %s produced uncommitted changes; re-run with the rw charm (%s:rw) to apply:\n%s", dir, target, target, dirty)
	}
	return nil
}

func (m *Magus) makeHandler(name string) func(context.Context, *types.Project) error {
	if name == "preflight" || name == "generate" {
		return func(ctx context.Context, p *types.Project) error {
			run := func() error { return runTarget(ctx, p, name) }
			pol := p.TargetPolicies[name]
			if pol.FailOnDrift && !types.HasCharm(ctx, types.CharmReadWrite) {
				return checkCleanAfter(ctx, p.Dir, name, run)
			}
			return run()
		}
	}
	return func(ctx context.Context, p *types.Project) error {
		return runTarget(ctx, p, name)
	}
}

// makeSpellFilteredHandler returns a handler that runs name on a single named spell.
func (*Magus) makeSpellFilteredHandler(name, spellName string) func(context.Context, *types.Project) error {
	return func(ctx context.Context, p *types.Project) error {
		return forSpellNamed(ctx, p, name, spellName, func(ctx context.Context, s *types.Spell) error {
			return invokeSpell(ctx, p, name, s)
		})
	}
}
