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
	"github.com/egladman/magus/internal/ci/annotate"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/ci/volatility"
	"github.com/egladman/magus/internal/describe"
	"github.com/egladman/magus/internal/file/diff"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/handler/mcp/origin"
	"github.com/egladman/magus/internal/hostmem"
	"github.com/egladman/magus/internal/interactive"
	interp "github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/race"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/internal/secret"
	"github.com/egladman/magus/internal/service"
	"github.com/egladman/magus/internal/trail"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
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
	NoCache           bool     // force a fresh run even on a cache hit; still refreshes the entry (magus run --no-cache)
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
	return m.redactError(m.runResolved(ctx, targets, applyRunOpts(opts)))
}

// redactError masks any resolved secret in err's MESSAGE while leaving the error chain
// intact.
//
// The boundary where a run's error stops being something magus handles with a context
// and becomes text a caller prints: the CLI's final line is `slog.Error(err.Error())`
// with no context, so the handler's redaction cannot reach it, and a magusfile's
// `throw "auth failed: " + magus\secret.read(...)` propagates all the way there.
// Redacting once here covers every consumer instead of asking each to remember.
//
// The chain is preserved through Unwrap, so errors.Is/As keep working.
func (m *Magus) redactError(err error) error {
	if err == nil || m.resolver == nil {
		return err
	}
	msg := m.resolver.RedactString(err.Error())
	if msg == err.Error() {
		return err
	}
	return redactedError{msg: msg, err: err}
}

// redactedError carries a masked message over the original error.
type redactedError struct {
	msg string
	err error
}

func (e redactedError) Error() string { return e.msg }
func (e redactedError) Unwrap() error { return e.err }

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

// CharmsForCI returns charms with the write-granting ones removed, which is what a ci
// run actually executes under. Exported because the CLI has to print the same set it
// runs: a header reporting the RESOLVED charms would announce "charms: rw" for
// `magus run ci` and then run read-only, since RunCI strips them afterwards. Whoever
// reads that header is checking exactly this, so both sides read it from here.
func CharmsForCI(charms []string) []string {
	return slices.DeleteFunc(slices.Clone(charms), func(s string) bool {
		n := types.Normalize(s)
		return n == types.CharmReadWrite || n == types.CharmRelock
	})
}

// RunCI runs the ci target(s) with write mode forced off. "ci" is an ordinary
// magusfile-defined target; magus keeps it only as the affected-set anchor,
// not a hardcoded preflight...test chain. The magusfile composes the pipeline
// order via magus.needs.
func (m *Magus) RunCI(ctx context.Context, targets []types.Target, opts ...RunOption) error {
	o := applyRunOpts(opts)
	// Both write-granting charms come off: rw so check-only targets stay check-only,
	// and relock so a ci run verifies the committed dependency state rather than
	// re-resolving it against whatever the registry serves today.
	o.Charms = CharmsForCI(o.Charms)

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
			// The hint has to name the fix the scope can actually apply: a provided
			// project has no magusfile to declare ci in, so telling its user to edit
			// one sends them somewhere that does not exist.
			hint := "define a ci target in your magusfile to compose the gate, e.g.  " +
				"export fun ci(ctx: magus\\Context, args: [str]) > void { ctx.needs(build, test, lint); }  " +
				"(run 'magus describe targets' to see available stages)"
			if allProvided(projects) {
				hint = "these projects come from a workspace provider, so ci lives on the provider spell: " +
					"expose a \"ci\" op in its mgs_listTargets and the anchor is satisfied"
			}
			interactive.Emit(os.Stderr, hint)
			return types.DiagnosticErrorf(types.NoCITarget,
				"no %q target defined in the selected project(s); it is the anchor %q and %q key off, "+
					"so this run would do nothing", types.TargetCI, "magus affected ci", "magus affected --plan")
		}
	}
	return m.redactError(m.runResolved(ctx, targets, o))
}

// allProvided reports whether every project in the scope came from a workspace
// provider, which is the case where the no-ci hint must point at the provider
// spell rather than at a magusfile none of them have.
func allProvided(projects []*types.Project) bool {
	for _, p := range projects {
		if _, ok := p.Origin.Provider(); !ok {
			return false
		}
	}
	return true
}

// anyProjectDeclaresCI reports whether any project in scope declares a ci target.
// ci lives in the magusfile (composed via magus.needs), never in a spell - except
// for a provided project, which has no magusfile at all, so its bound spell's ci
// op is the only place ci can live. A magusfile project is checked by extracting
// its declared target nodes statically (the same AST extractor TargetGraph uses,
// never a raw text scan, so `ci` in a comment or string can't false-positive) and
// short-circuiting on the first ci found; a spell op named ci must NOT satisfy the
// anchor there, since the magusfile is the definition. The returned error is
// non-nil if a source couldn't be located, so a (false, err) result means "couldn't
// determine", not "definitely no ci" - the caller must not block on it.
func anyProjectDeclaresCI(projects []*types.Project) (bool, error) {
	var scanErr error
	for _, p := range projects {
		if _, ok := p.Origin.Provider(); ok {
			for _, s := range p.ResolvedSpells {
				if slices.Contains(s.Targets(), types.TargetCI) {
					return true, nil
				}
			}
			continue
		}
		srcs, err := interp.FindAll(p.Dir)
		if err != nil {
			scanErr = err
			continue
		}
		for _, src := range srcs {
			if src.Engine != "buzz" {
				continue
			}
			source := concatSource(src)
			for _, n := range describe.Extract(source) {
				// The extractor emits a node for every exported function (ctx-form
				// included) with names already normalized, so this matches the run
				// path's target-name resolution.
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
	targets, source, fellBack, err := m.ExpandAffected(ctx, target, o.BaseRef)
	if err != nil {
		return err
	}
	if fellBack {
		// Selecting every project is the SAFE direction - magus would rather over-build
		// than let a gate pass having checked nothing - but a run that quietly reverts to
		// a full build while looking incremental is worth saying out loud. The commonest
		// cause is a CI checkout too shallow to contain the base ref; source carries the
		// underlying reason.
		//
		// This is for the NON-TERMINAL callers. `magus affected` already reveals it on a
		// terminal, because the scope line renders source ("projects: . (affected: cannot
		// compute affected set ...)"). RunAffected's real caller is the MCP run_affected
		// tool, where there is no scope line and an agent would otherwise be told only
		// that the run passed - with no way to know it had just built the whole workspace.
		interactive.Emit(os.Stderr, fmt.Sprintf(
			"[%s] affected: could not compute a changed-file set, so EVERY project was selected. "+
				"This runs a full build, not an incremental one. Reason: %s (see %s)",
			types.AffectedSetUncomputable, source, types.CodeURL(types.AffectedSetUncomputable)))
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
	// An explicit target footprint is an ownership boundary, not an extra safety
	// margin.  Keeping the project-wide baseline here made a target that declared
	// its exact inputs indistinguishable from one that had not: unrelated spell
	// claims still invalidated it.  Retain the magusfiles (they define the target)
	// and the target-specific spell inputs, then let the body state the rest.
	if len(p.TargetInputs[target]) > 0 {
		step.Sources = append([]string(nil), magusfileGlobs(p.Path)...)
		if p.Path != "." {
			step.Sources = append(step.Sources, magusfileGlobs(".")...)
		}
	}
	// ctx.writesFiles completes the same ownership boundary for replay: once a target
	// names its own artifacts, do not inherit every project or spell output into
	// its snapshot. Otherwise an unrelated target can restore a stale tree merely
	// because it shares the project.
	if len(p.TargetOutputs[target]) > 0 {
		step.Outputs = nil
		step.RequiredOutputs = nil
		// The target named its own artifacts, so producing none of them is a real error and
		// snapshot should say so. Absent this, step.Outputs is the project/spell BASELINE,
		// which a check-only target never produces - see OutputsDeclared.
		step.OutputsDeclared = true
	}
	for _, s := range p.ResolvedSpells {
		step.Sources = append(step.Sources, s.TargetSources()[target]...)
	}
	// Which host facts key this step. Workspace-wide today: a per-target override is a
	// narrower claim, and there is no evidence yet that anyone needs one axis on for
	// one target and off for another within the same workspace.
	// Workspace answer, then the target's override when it made one. A target that
	// says nothing inherits, which is what every target did before overrides existed.
	step.IncludeOS = m.cfg.Cache.IncludeOS()
	step.IncludeArch = m.cfg.Cache.IncludeArch()
	if pol, ok := p.TargetPolicies[target]; ok {
		if pol.IncludeOS != nil {
			step.IncludeOS = *pol.IncludeOS
		}
		if pol.IncludeArch != nil {
			step.IncludeArch = *pol.IncludeArch
		}
	}
	// Per-target inputs declared via ctx.readsFiles define the cache footprint whenever
	// present. Each InputRef carries its owning project; joinGlob follows the same
	// ownership rule as outputs below.
	for _, ref := range p.TargetInputs[target] {
		if g := joinGlob(ref.Project, ref.Glob); !slices.Contains(step.Sources, g) {
			step.Sources = append(step.Sources, g)
		}
	}
	// Per-target ctx.modifiesExistingFiles refs fold into Sources exactly as inputs do, and into
	// Outputs not at all. That asymmetry IS the feature: staying out of the output set
	// keeps the file off the snapshot/replay path and out of magus clean's reach, while
	// landing in the source set means editing the authored prose around a generated
	// region invalidates the target that maintains that region. Declared as an output
	// instead, the file was excluded from the source hash, so an edit to it could not
	// invalidate anything.
	// ctx.withEnv / ctx.withCwd overrides fold into the KEY, not the sources: they change what the
	// tool does without naming a file, so two runs differing only by a derived env must
	// not share an entry.
	step.ExecOverrides = append(step.ExecOverrides, p.TargetExecOverrides[target]...)
	// Names, not values: hashStep reads each variable's process value at hash time.
	step.EnvAllow = append(step.EnvAllow, p.TargetEnvAllow[target]...)
	// ctx.observes: an external fact the answer depends on, keyed so the target stays
	// cacheable rather than having to opt out with skip_cache.
	step.Observations = append(step.Observations, p.TargetObservations[target]...)
	for _, ref := range p.TargetUpdates[target] {
		if g := joinGlob(ref.Project, ref.Glob); !slices.Contains(step.Sources, g) {
			step.Sources = append(step.Sources, g)
		}
	}
	for _, ref := range p.TargetOutputs[target] {
		owner := ref.Project
		if owner == "" {
			owner = p.Path
		}
		jg := joinGlob(owner, ref.Glob)
		if !slices.Contains(step.Outputs, jg) {
			step.Outputs = append(step.Outputs, jg)
		}
		// A cross-project output must actually be produced, so it is required rather
		// than best-effort. Another project's build order hangs off it: the owner runs
		// after this target specifically to see the finished bytes. If the write silently
		// produced nothing, the snapshot's "did ANY glob match" check would still pass on
		// this target's own outputs, the manifest would omit the file, and every later
		// cache hit would replay a partial output set into someone else's tree.
		if owner != p.Path && !slices.Contains(step.RequiredOutputs, jg) {
			step.RequiredOutputs = append(step.RequiredOutputs, jg)
		}
	}
	step.DependsOn = p.DependsOn
	pol := p.TargetPolicies[target]
	// A service op is a long-running process: it must never be cached, or a re-run
	// would replay a completed-target result instead of restarting the process. This
	// is inherent (not an author opt-in), so OR it into the explicit SkipCache policy.
	step.NoCache = pol.SkipCache || servesTarget(p.ResolvedSpells, target)
	step.Exclusive = pol.Exclusive
	// Resolve the two spellings of the same claim into the one number the limiter
	// understands. A target declaring memory_mb holds however many slots that memory
	// is worth on THIS host, so an 8GB suite throttles peers on a 16GB runner and
	// barely registers on a 64GB workstation, without the magusfile naming either.
	step.Slots = slotsForPolicy(pol.Slots, pol.MemoryMB, m.limiter().Capacity(), m.hostTotalBytes())
	return step
}

// ComputeTargetKey computes target's live cache key and the pre-hash key inputs behind
// it for the project at projectPath, without executing anything. The step is keyed
// exactly as a run with these charms would key it - spell claims, tool versions and
// the env allowlist all included - so the returned key equals the one a real run
// mints, and PortableRef of it equals the ref that run would print. Only args after
// `--` are absent (this is not a run, so there are none). It is the live half of the
// works-on-my-machine diff: `describe target --cache` compares these lines against
// the set stored behind a ref. Returns types.ErrNoCache on an Inspect workspace.
func (m *Magus) ComputeTargetKey(ctx context.Context, projectPath, target string, charms []string) (key string, lines []string, err error) {
	return m.computeTargetKey(ctx, projectPath, target, charms, nil)
}

// sweepReuse bundles the two sweep-scoped reuses computeTargetKey accepts: an
// optional cache.SourceMemo (passed to cache.StepKeyMemo) and an optional
// pre-resolved tool-version map keyed by project path. IdentifyRef is the only
// caller that supplies one, and it always supplies both together, since its sweep
// keys every candidate target under every charm variant against the SAME
// workspace tree - see cache.SourceMemo's doc for why that is safe here and
// nowhere a target actually runs. Bundling them into one type makes "always both
// or neither" a fact of the signature instead of a doc-comment promise.
type sweepReuse struct {
	memo         *cache.SourceMemo
	toolVersions map[string][]string
}

// computeTargetKey is ComputeTargetKey with an optional sweepReuse threaded in.
// ComputeTargetKey passes nil, so its public behavior is unchanged.
//
// The tool-version map matters more than it looks: toolVersionsByProject memoizes
// only WITHIN one call, and each probe SPAWNS A SUBPROCESS (`go version`, `pnpm
// --version`, ...). Resolving per target made the sweep re-probe every spell of
// every project once per target, which was the sweep's dominant cost - wall clock
// far exceeding CPU because the process was waiting on child processes, not
// computing.
func (m *Magus) computeTargetKey(ctx context.Context, projectPath, target string, charms []string, reuse *sweepReuse) (key string, lines []string, err error) {
	if m.cache == nil {
		return "", nil, types.ErrNoCache
	}
	p := m.Get(projectPath)
	if p == nil {
		return "", nil, fmt.Errorf("magus: compute target key: unknown project %q", projectPath)
	}
	var memo *cache.SourceMemo
	var toolVersions map[string][]string
	if reuse != nil {
		memo = reuse.memo
		toolVersions = reuse.toolVersions
	}
	if toolVersions == nil {
		toolVersions = m.toolVersionsByProject(ctx, []*types.Project{p})
	}
	step := m.buildStep(p, target)
	applyRunKeying(&step, toolVersions[p.Path], charms)
	return m.cache.StepKeyMemo(ctx, &step, memo)
}

// applyRunKeying stamps the key-relevant fields the RUN SCHEDULER adds on top of
// buildStep: resolved tool versions and the active charm set (sorted and deduped, so
// charm order never forks a key). Both the scheduler and ComputeTargetKey go through
// it, so `describe target --cache` cannot silently drift from the key a real run
// mints when a new key-relevant field is added here.
func applyRunKeying(step *cache.Step, toolVersions, charms []string) {
	step.ToolVersions = toolVersions
	ck := slices.Clone(charms)
	slices.Sort(ck)
	step.Charms = slices.Compact(ck)
}

// outputWatchDirs are the base directories the race detector watches for one target: the
// same declared outputs outputGlobsByRoot resolves, widened to the directories containing
// them.
//
// A projection of that function rather than a second resolution. Both used to walk
// p.TargetOutputs themselves, including the same owner-root branch, which is exactly the
// duplication that lets two views disagree about which root a cross-project glob belongs to.
//
// Wide is right HERE and only here: the race detector asks "what got written", so it must see
// paths no glob claimed. The replay asks "did the declared outputs change", so it filters -
// see diff.HashContent.
func outputWatchDirs(ws *types.Workspace, p *types.Project, target string) []string {
	sets := outputGlobsByRoot(ws, p, target)
	dirs := make([]string, 0, len(sets))
	for _, set := range sets {
		dirs = append(dirs, diff.GlobBaseDirs(set.Root, set.Globs)...)
	}
	slices.Sort(dirs)
	return slices.Compact(dirs)
}

// outputGlobsByRoot groups a target's declared outputs by the root each glob is relative to.
// Globs stay relative - see diff.OutputGlobs.
//
// It REPLACES rather than unions, mirroring buildStep: a target that named its own artifacts
// does not inherit the project or spell baseline, because an unrelated target can otherwise
// restore a stale tree merely because it shares the project. Unioning here made the replay
// compare a wider set than the cache snapshots, for exactly the targets that did the work of
// declaring ctx.writesFiles.
func outputGlobsByRoot(ws *types.Workspace, p *types.Project, target string) []diff.OutputGlobs {
	byRoot := map[string][]string{}
	if refs := p.TargetOutputs[target]; len(refs) > 0 {
		for _, ref := range refs {
			// A cross-project glob is relative to the tree it writes into.
			root := p.Dir
			if ref.Project != "" && ref.Project != p.Path {
				owner := ws.Get(ref.Project)
				if owner == nil {
					continue
				}
				root = owner.Dir
			}
			byRoot[root] = append(byRoot[root], ref.Glob)
		}
	} else if len(p.Outputs) > 0 {
		byRoot[p.Dir] = append(byRoot[p.Dir], p.Outputs...)
	}
	sets := make([]diff.OutputGlobs, 0, len(byRoot))
	for root, globs := range byRoot {
		slices.Sort(globs)
		sets = append(sets, diff.OutputGlobs{Root: root, Globs: slices.Compact(globs)})
	}
	slices.SortFunc(sets, func(a, b diff.OutputGlobs) int { return strings.Compare(a.Root, b.Root) })
	return sets
}

// formatOutputGlobs renders the declared globs for an error message. Root-qualified only
// when more than one root is involved, since the single-root case is the project the error
// already names.
func formatOutputGlobs(sets []diff.OutputGlobs) string {
	var parts []string
	for _, s := range sets {
		for _, g := range s.Globs {
			if len(sets) == 1 {
				parts = append(parts, g)
				continue
			}
			parts = append(parts, filepath.Join(s.Root, g))
		}
	}
	return strings.Join(parts, ", ")
}

// servesTarget reports whether target is backed by a service op in any of the
// project's resolved spells.
func servesTarget(spells []*spells.Spell, target string) bool {
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

// CurrentRevision resolves the workspace's active VCS revision (full hash) and dirty
// state, collapsing BOTH a resolution error and no VCS into ("", false).
//
// That differs from verifyReadOnly, which treats a vcs.Resolve error as a hard failure,
// and it is correct here because this is provenance metadata rather than a drift gate: a
// target that never declared FailOnDrift never asked to have its VCS state checked, so a
// missing revision is "unknown", never a reason to fail the caller.
//
// executeStages resolves it ONCE per invocation and copies it onto every step, as
// toolVersionsByProject does - probing per target would spawn a VCS subprocess per step.
// The revision and dirty returns are types.VCSMeta's ID and IsDirty. ("Hash" is git's word
// alone - hg reports a node, jj a commit id - which is why the field is named ID.)
func (m *Magus) CurrentRevision(ctx context.Context) (name, revision string, dirty bool) {
	res, err := vcs.Resolve(ctx, m.ws.Root, "", m.ws.VCSOptions)
	if err != nil || res.VCS == nil {
		return "", "", false
	}
	meta, err := res.VCS.Metadata(ctx, m.ws.Root)
	if err != nil {
		return "", "", false
	}
	// The provider name rides along because a hash does not identify its own kind,
	// and resolving it a second time would spawn another VCS subprocess.
	return res.Name, meta.ID, meta.IsDirty
}

// checkToolWindows fails the run when a probed tool falls outside the window its project
// declares, intersected with what the declaring spell requires.
//
// It reports EVERY violation rather than the first: a run stopped by a toolchain mismatch
// usually has more than one tool to fix, and finding them one rebuild at a time is the
// experience the version window exists to replace.
//
// A tool with no recorded version is skipped, never failed. That covers an absent binary,
// output carrying no version, and the case where tool-version probing is switched off
// entirely - magus cannot compare what it did not read, and "could not check" must never
// render as a violation.
func checkToolWindows(projects []*types.Project, versions map[string]string) error {
	var violations []string
	// One error carries one code, so a mixed batch has to choose: too-old always wins.
	// Recorded as violations are found, not recovered from the formatted message -
	// sniffing the sorted text for "older than" made the code turn on which PROJECT NAME
	// sorted first.
	sawOld, sawNew := false, false
	for _, p := range projects {
		for _, s := range p.ResolvedSpells {
			for _, tool := range s.ToolNames() {
				t, _ := s.Tool(tool) // ToolNames ranges the same map Tool reads
				window := t.Supported.Intersect(p.ToolBounds[tool])
				if window.IsZero() {
					continue
				}
				version, seen := versions[p.Path+"\x00"+s.Name()+"\x00"+tool]
				if !seen {
					continue
				}
				switch window.Check(version) {
				case spells.VerdictTooOld:
					sawOld = true
					violations = append(violations, fmt.Sprintf("%s: %s %s is older than the supported range (min %s)",
						types.ProjectLabel(p.Path, p.Dir), tool, version, window.Min))
				case spells.VerdictTooNew:
					sawNew = true
					violations = append(violations, fmt.Sprintf("%s: %s %s is newer than the supported range (below %s)",
						types.ProjectLabel(p.Path, p.Dir), tool, version, window.Below))
				case spells.VerdictInside, spells.VerdictUnknown:
					// Unknown belongs with Inside: an unparseable bound leaves nothing to
					// compare, and a comparison magus could not make must not fail a build.
				}
			}
		}
	}
	if len(violations) == 0 {
		return nil
	}
	slices.Sort(violations)
	code := types.ToolTooOld
	if sawNew && !sawOld {
		code = types.ToolTooNew
	}
	return types.DiagnosticErrorf(code, "toolchain outside the declared window:\n  %s",
		strings.Join(violations, "\n  "))
}

func (m *Magus) toolVersionsByProject(ctx context.Context, projects []*types.Project) map[string][]string {
	return m.probeTools(ctx, projects, nil)
}

// probeTools is toolVersionsByProject with an optional second output: when extracted is
// non-nil it also records each tool's full parsed version, keyed "project\x00spell\x00tool".
//
// The window gate needs the WHOLE version and the cache key needs a narrowed token, and
// both come from one probe. Threading the extra map out is what keeps the gate from
// forking a second time per tool - the run path already pays for these probes, so the
// check rides along free.
func (m *Magus) probeTools(ctx context.Context, projects []*types.Project, extracted map[string]string) map[string][]string {
	mode := toolVersionMode()
	if mode == "off" {
		return nil
	}
	// One probe, two consumers: the cache key wants a narrowed token, the window gate
	// wants the whole version. BOTH are memoized. The memo is keyed on (spell, dir, tool)
	// and the gate per PROJECT, so two projects sharing a dir take the hit path - a memo
	// holding only the token would leave every project after the first unchecked, and
	// silently, since the gate reads an absent version as "could not compare".
	type reading struct{ token, full string }
	memo := make(map[string]reading)
	out := make(map[string][]string, len(projects))
	for _, p := range projects {
		dir := p.Dir
		if mode == "workspace" {
			dir = m.ws.Root
		}
		var vers []string
		for _, s := range p.ResolvedSpells {
			// One uniform loop over every declared tool. There is no privileged
			// "primary" binary any more: `go` had one for historical cache-key reasons
			// and nothing principled separated it from golangci-lint, so both key as
			// spell:tool:version. Memoized on (spell, dir, tool) so N tools cost N
			// spawns per project per run rather than N per target.
			for _, tool := range s.ToolNames() {
				t, _ := s.Tool(tool)
				if !t.HasProbe() {
					continue
				}
				tk := s.Name() + "\x00" + dir + "\x00" + tool
				r, hit := memo[tk]
				if !hit {
					// A declared constant needs no process. It stays out of `full`:
					// an author typed it, so there is nothing for the gate to compare.
					if t.Probe.Bin == "" {
						r.token = t.Key.Const
					} else {
						probed, err := s.ProbeVersion(ctx, tool, dir)
						switch {
						case err != nil:
							slog.WarnContext(ctx, "magus: tool-version probe failed; cache key records UNPROBED",
								slog.String("spell", s.Name()), slog.String("tool", tool),
								slog.String("dir", dir), slog.String("err", err.Error()))
							r.token = "UNPROBED"
						default:
							token, note := spells.VersionToken(probed, t.Key)
							if note != "" {
								slog.WarnContext(ctx, "magus: tool-version key degraded; cache key is coarser than declared",
									slog.String("spell", s.Name()), slog.String("tool", tool),
									slog.String("dir", dir), slog.String("note", note))
							}
							slog.DebugContext(ctx, "magus: tool-version probe",
								slog.String("spell", s.Name()), slog.String("tool", tool),
								slog.String("output", probed), slog.String("token", token))
							r.token = token
							if full, ok := spells.ExtractVersion(probed); ok {
								r.full = full
							}
						}
					}
					memo[tk] = r
				}
				// Outside the miss branch: a project taking the hit path still needs its
				// own gate entry. See the reading type above.
				if extracted != nil && r.full != "" {
					extracted[p.Path+"\x00"+s.Name()+"\x00"+tool] = r.full
				}
				vers = append(vers, s.Name()+":"+tool+":"+r.token)
			}
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
	// Ahead of the dry-run branch, not after it: a dry run evaluates the same
	// target bodies under a tracing context, so without the forwarded args here
	// it printed the op's own command and silently omitted them - under-reporting
	// the very command it exists to show.
	if len(opts.ExtraArgs) > 0 {
		ctx = project.WithExtraArgs(ctx, opts.ExtraArgs)
	}

	// Every dispatch funnels through here, which is why the return sink is installed
	// here and not at the CLI: the cache snapshots a target's return value off this
	// sink, so an entry point without one persists Value: nil into a durable entry.
	// A caller that wants to READ the values back (the run and affected commands)
	// installs its own first, and this leaves it alone.
	ctx = types.EnsureReturnCapture(ctx)

	// Watch host memory for the life of the invocation. Every dispatch passes
	// through here and the machine is what is at risk, so this belongs per-run;
	// the peak-RSS collector in invokeSpell answers which target was expensive.
	// Silent unless headroom collapses.
	//
	// Not started for a dry run, which executes nothing and so cannot exhaust
	// anything. Joined rather than merely cancelled: an in-flight report would
	// otherwise land after the summary footer, or be cut off by process exit.
	if m.cache != nil && !opts.DryRun {
		// Rebase the heap peak and attribution: the daemon serves many invocations
		// from one process against a heap that never shrinks, and without this the
		// first run's peak is reported against every later one.
		vm.ResetHeapStats()
		watchCtx, stopWatch := context.WithCancel(ctx)
		var watchDone sync.WaitGroup
		watchDone.Add(1)
		go func() {
			defer watchDone.Done()
			hostmem.Watch(watchCtx, func(available, total int64) {
				heap := vm.ReadHeapStats()
				var hot string
				if sites, _ := vm.HeapHotSites(1); len(sites) > 0 {
					hot = sites[0].Site
				}
				m.cache.LogMemoryPressure(ctx, cache.MemoryPressure{
					AvailableBytes: available,
					TotalBytes:     total,
					BuzzObjects:    heap.Objects,
					BuzzPeak:       heap.Peak,
					BuzzHotSite:    hot,
				})
			})
		}()
		defer func() {
			stopWatch()
			watchDone.Wait()
		}()
	}

	if opts.DryRun {
		// Deep dry run: evaluate each target body under a tracing context, so
		// effectful host ops (exec, fs writes, network, env) record their intent and
		// skip instead of running. Sequential, so each project's commands stay grouped
		// under its [dry] line. Reads still work, so the plan reflects real conditionals.
		recCtx := types.WithTrace(ctx)
		dryStart := time.Now()
		if m.cache != nil {
			m.cache.LogDryBanner(ctx)
		} else {
			fmt.Println("dry run: commands shown, not executed")
		}
		planned := 0
		for _, st := range stages {
			for _, p := range st.projects {
				label := types.ProjectDisplayName(p.Path, p.Name, p.Dir)
				planned++
				if m.cache != nil {
					// Charms folded in, matching the executed line: a dry run whose repro
					// command omits them prints a command that reproduces something else,
					// and it is printed precisely when the reader is asking what will run.
					m.cache.LogDry(ctx, p.Path, label, charmedTarget(st.target, opts.Charms))
				} else {
					fmt.Printf("[dry] %s\n", label)
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
		// A dry run ends with a footer like every other run. Without it the output
		// simply stopped after the last plan line, so the one shape a reader looks
		// for at the bottom was missing precisely when they were reviewing a plan.
		if m.cache != nil {
			m.cache.LogDrySummary(ctx, planned, time.Since(dryStart))
		}
		return nil
	}

	start := time.Now()
	// Run-scoped remote-cache counters. Installed here rather than held on Cache
	// because the daemon reuses one Cache per workspace across runs and can serve two
	// adopted runs at once; LogRemoteSummary below reads them back off ctx.
	ctx = cache.WithRemoteStats(ctx)

	var uniqueProjects []*types.Project
	seenProj := make(map[string]struct{})
	addProj := func(p *types.Project) {
		if _, ok := seenProj[p.Path]; !ok {
			seenProj[p.Path] = struct{}{}
			uniqueProjects = append(uniqueProjects, p)
		}
	}
	for _, st := range stages {
		for _, p := range st.projects {
			addProj(p)
			// A target declaring ctx.writesFiles(<alias>.file(...)) mutates ANOTHER
			// project's tree, so that project belongs in the lock set too. Without it the
			// advisory lock's guarantee - no two magus invocations mutating one project at
			// once - is false by construction here: a concurrent `magus clean` locks the
			// owner, this run locks only the writer, and the two delete and write the same
			// file unserialized. It also serializes two writers into one tree, which
			// otherwise interleave with no contention and no ordering.
			for _, refs := range p.TargetOutputs {
				for _, ref := range refs {
					if ref.Project == "" || ref.Project == p.Path {
						continue
					}
					if owner := m.ws.Get(ref.Project); owner != nil {
						addProj(owner)
					}
				}
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

	// The probe pass doubles as the toolchain gate. Enforcement follows the
	// DECLARATION, stated per project, not the dispatch mechanism: hanging it off
	// spell-op dispatch missed every project whose targets shell out directly.
	//
	// Runs before any target, so a violation stops the invocation rather than
	// surfacing partway through, on probes this pass already paid for.
	//
	// KNOWN GAP: scope is uniqueProjects, so a project reached only through a target
	// dependency joins later in the dispatcher and is not gated.
	toolWindows := map[string]string{}
	toolVer := m.probeTools(ctx, uniqueProjects, toolWindows)
	if err := checkToolWindows(uniqueProjects, toolWindows); err != nil {
		return err
	}
	// Resolved ONCE for the whole invocation, not per target - see CurrentRevision.
	vcsName, revision, dirty := m.CurrentRevision(ctx)

	// Active charms participate in the cache key: a charm can change a target's
	// behaviour (pass/fail or output), so charm-variant runs must not collide.
	// A charm-less run hashes identically to before, keeping existing entries valid.
	// Normalized by applyRunKeying below, shared with ComputeTargetKey.
	charmKey := opts.Charms

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
			applyRunKeying(&step, toolVer[p.Path], charmKey)
			step.Revision = revision
			step.Dirty = dirty
			step.VCSName = vcsName
			// Args after `--` change what the target does, so they key the cache
			// exactly as charms do; without this a run with different args
			// replays the previous run's result.
			step.ExtraArgs = opts.ExtraArgs
			// The spell::op filter selects which definition runs (an explicit op
			// bypasses a shadowing magusfile export), so it keys the cache the
			// same way; without it a compile-only go::go-build recorded a pass
			// the real go-build target then replayed around a stale binary.
			step.Spell = opts.Spell
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
					declaredCharms[types.Normalize(c)] = struct{}{}
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
					slog.DebugContext(ctx, "magus: could not persist runtime diagnostics", slog.String("error", err.Error()))
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
	ctx = secret.ContextWithResolver(ctx, m.resolver)
	// So a magusfile binding can record a governance event (a credential granted, an
	// endpoint opened) without the trail path being threaded through the VM.
	ctx = trail.ContextWithBase(ctx, m.CacheDir())
	// Scopes a credential endpoint to THIS run. internal/secret cannot read the
	// invocation id itself - internal/journal imports it for redaction, so that would be
	// a cycle - so the id is threaded from here, where both are already in scope.
	ctx = secret.ContextWithInvocationID(ctx, journal.InvocationIDFromContext(ctx))
	ctx = types.WithWorkspace(ctx, m)
	// Seeded with the projects this run SELECTED, then marked further by the dispatcher
	// as cross-project dependencies run. Selection alone was not enough: `magus run
	// build .` selects only the root, so a nested project reached through a dependency
	// was still treated as foreign and its own writes blamed on the root.
	activeDispatch := &types.ActiveDispatch{}
	for path := range active {
		activeDispatch.Mark(path)
	}
	for path := range active {
		if pr := byPath[path]; pr != nil {
			activeDispatch.Mark(pr.Dir)
		}
	}
	ctx = types.WithActiveDispatch(ctx, activeDispatch)
	ctx = types.WithCharms(ctx, opts.Charms)
	if o, ok := origin.FromContext(ctx); ok {
		slog.InfoContext(
			ctx, "[AGENT] build triggered",
			slog.String("agent", o.Agent),
			slog.String("scope", scopeLabel),
		)
	}

	// Installed whenever history is enabled, NOT only when a target opted into
	// retries. The history this writes is the same store the shard forecaster
	// reads, so gating installation on RetryOnVolatile meant a workspace where no
	// target opts in - which is this one - recorded nothing at all, and the
	// forecaster predicted DefaultDurationMs for every project forever. Retrying
	// stays gated: the flag rides on the runtime and Decide honours it, so a
	// target that never asked for a retry still never gets one.
	var volatilityRT *volatility.Runtime
	if m.cfg.Volatility.Enabled {
		retry := trackVolatile && !opts.NoVolatilityRetry
		volatilityRT = m.buildVolatilityRuntime(ctx, retry)
		if volatilityRT != nil {
			ctx = volatility.WithRuntime(ctx, volatilityRT)
		}
	}

	checkOutputOverlap(dedupeByProject(steps), opts.Report)

	var raceRT *race.Runtime
	if opts.Race {
		raceRT = m.buildRaceRuntime()
		if err := raceRT.Start(ctx); err != nil {
			slog.WarnContext(ctx, "magus: race detector unavailable", "err", err)
			raceRT = nil
		} else {
			ctx = race.WithRuntime(ctx, raceRT)
		}
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
		slog.InfoContext(ctx, "magus: --step forces Concurrency=1")
		lim = cache.NewLimiter(1)
	}
	cacheOpts := []cache.RunOption{cache.WithLimiter(lim), cache.WithMaxFailures(m.cfg.MaxFailures)}
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
	// context.WithoutCancel: a cancelled run (Ctrl-C) still has to release the
	// services it acquired, or an in-process one leaks running and a daemon-hosted
	// one leaks its ref-count - passing the already-cancelled ctx through would make
	// Shutdown's bounded wait for e.ready return immediately and skip stopping
	// anything still starting. Bounded so a wedged service cannot hang teardown.
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), service.DefaultShutdownTimeout)
		defer cancel()
		svcSession.ReleaseAll(shutdownCtx)
	}()
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
			outDirs := outputWatchDirs(m.ws, p, s.Target)
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
			slog.WarnContext(ctx, "magus: failed to save volatility history", "err", err)
		}
	}

	if opts.RaceReplay && runErr == nil {
		// Every stage replays; a caller who asked for the check wants the whole list.
		for _, st := range stages {
			if err := runReplay(ctx, m.ws, st.projects, st.target, byPath, st.handler, opts.Report); err != nil && runErr == nil {
				runErr = err
			}
		}
	}

	if raceRT != nil {
		writtenByProject := raceRT.WrittenPaths()
		if err := raceRT.Flush(ctx, opts.Report); err != nil {
			slog.WarnContext(ctx, "magus: race detector flush failed", "err", err)
		}
		checkMissingDependencies(m.ws.All(), byPath, writtenByProject, scopeLabel, opts.Report)
	}

	// Footer summary for a fan-out: a single line tallying the per-project results.
	// Skipped for a single project, where the per-project status line already says it all.
	if s := m.cache.Stats(); s.Hit+s.Miss+s.Error > 1 {
		m.cache.LogSummary(ctx, time.Since(start))
	}
	// Beside the footer, not deferred by the caller: every path that runs targets
	// reaches here, including `magus x` and the MCP run tool, and a deferred summary
	// landed after the terminal band was released.
	m.cache.LogRemoteSummary(ctx)

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

func (o stageObserver) TargetEnd(ctx context.Context, name string, elapsed time.Duration, err error) {
	// ctx, not _: LogStage puts runErr.Error() in an attr, and a magusfile can throw an
	// interpolated credential. Without the context the record redacts against nothing.
	o.cache.LogStage(ctx, o.label, name, elapsed, err)
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
//
// It fails rather than warns: drift gating, cache replay, and regenerate-to-resolve
// merges are each unsound without byte-stability, so there is no useful "warned about it"
// state. Every offender is reported before it returns.
func runReplay(ctx context.Context, ws *types.Workspace, projects []*types.Project, target string,
	byPath map[string]*types.Project, handler TargetHandler,
	w *report.Writer,
) error {
	// One resolution per project, reused for admission and both snapshots, so the selection
	// loop and the comparison loops cannot disagree about what the outputs are.
	sets := make(map[string][]diff.OutputGlobs, len(projects))
	var replayable []*types.Project
	for _, p := range projects {
		s := outputGlobsByRoot(ws, p, target)
		if len(s) == 0 {
			continue
		}
		sets[p.Path] = s
		replayable = append(replayable, p)
	}
	if len(replayable) == 0 {
		return nil
	}

	var offenders []string
	snapsA := make(map[string]diff.ContentSnap, len(replayable))
	for _, p := range replayable {
		snap, err := diff.HashContent(ctx, sets[p.Path])
		if err != nil {
			// Report and keep going: returning here would skip byte-stability for every
			// remaining project, which is the "gate that checked nothing" this exists to stop.
			fmt.Fprintln(os.Stderr, types.FormatDiagnostic(types.NondeterministicOutput,
				fmt.Sprintf("cannot check byte-stability\n  project=%s target=%s err=%v", p.Path, target, err)))
			offenders = append(offenders, p.Path)
			continue
		}
		// A target that named its own outputs and produced none broke its promise, so an empty
		// snapshot means the comparison verified nothing. Gated on TargetOutputs for the reason
		// cache.OutputsDeclared documents: an inherited project or spell glob - the typescript
		// spell contributes dist/** to every target - routinely matches nothing, and a
		// check-only target like test would fail for a glob it never claimed.
		if len(snap) == 0 && len(p.TargetOutputs[target]) > 0 {
			fmt.Fprintln(os.Stderr, types.FormatDiagnostic(types.NondeterministicOutput,
				fmt.Sprintf("declared outputs matched nothing, so byte-stability was not checked\n  project=%s target=%s globs=%s",
					p.Path, target, formatOutputGlobs(sets[p.Path]))))
			_ = report.Record(w, report.DeterminismMismatch{Project: p.Path, Target: target})
			offenders = append(offenders, p.Path)
			continue
		}
		snapsA[p.Path] = snap
	}

	for _, p := range replayable {
		if err := handler(ctx, byPath[p.Path]); err != nil {
			slog.WarnContext(ctx, "magus: race-replay handler failed", "project", p.Path, "err", err)
		}
	}

	for _, p := range replayable {
		if _, ok := snapsA[p.Path]; !ok {
			continue // already reported above; its pre-snapshot is not comparable
		}
		postSnap, err := diff.HashContent(ctx, sets[p.Path])
		if err != nil {
			fmt.Fprintln(os.Stderr, types.FormatDiagnostic(types.NondeterministicOutput,
				fmt.Sprintf("cannot check byte-stability\n  project=%s target=%s err=%v", p.Path, target, err)))
			offenders = append(offenders, p.Path)
			continue
		}
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
		offenders = append(offenders, p.Path)
	}
	if len(offenders) == 0 {
		return nil
	}
	// The per-project diagnostics above already carry the differing paths.
	return types.DiagnosticErrorf(types.NondeterministicOutput,
		"non-deterministic output from %s in %s", target, strings.Join(offenders, ", "))
}

// checkMissingDependencies audits for undeclared dependencies (MGS4004).
// For each written path, finds workspace projects that consume it but weren't dispatched.
//
// scope is the invocation's display label (see TargetLabel), not a target name; the
// parameter is named scope rather than target because there genuinely is no single
// target to attribute a write to here: written comes from race.Runtime.WrittenPaths,
// which is keyed by project only (no per-target breakdown), and one invocation of
// executeStages can cover several target stages at once. report.MissingDependency's
// Target field still carries this label - the best identifier available for which
// run flagged it - so callers reading it should treat it as a run scope, not a target.
func checkMissingDependencies(allProjects []*types.Project, dispatched map[string]*types.Project,
	written map[string][]string, scope string, w *report.Writer,
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
							fmt.Sprintf("potential undeclared dependency\n  consumer=%s producer=%s path=%s scope=%s",
								consumer.Path, producer, path, scope)))
						_ = report.Record(w, report.MissingDependency{
							Consumer: consumer.Path,
							Producer: producer,
							Path:     path,
							Target:   scope,
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
//
// Each cache.Step already carries its own Target, so the overlap is reported against
// the two steps' real targets rather than the whole invocation's scope label - a
// single executeStages call can cover several target stages at once (runResolved
// groups multi-target requests into one call), so a blanket label would misattribute
// the overlap to a target that may not even be one of the two involved.
func checkOutputOverlap(steps []cache.Step, w *report.Writer) {
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
			tA, tB := steps[i].Target, steps[j].Target
			if pA > pB {
				pA, pB = pB, pA
				tA, tB = tB, tA
			}
			target := tA
			if tA != tB {
				target = tA + "," + tB
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
func (m *Magus) buildVolatilityRuntime(ctx context.Context, retry bool) *volatility.Runtime {
	var h forecast.History
	if err := h.Load(ctx, m.cfg.HistoryPath); err != nil {
		return nil
	}
	// Only when retrying. affected feeds shouldRetry and nothing else, so computing
	// it for a run that will never retry buys nothing and costs a full affected
	// pass - which every invocation now reaches, since recording is unconditional.
	var affected []string
	if retry {
		if res, err := m.Affected(ctx, ""); err == nil {
			affected = res.Affected
		}
	}
	return volatility.NewRuntime(&h, m.cfg.HistoryPath, m.volatilityConfig(), affected, retry)
}

// runTarget executes name on every spell in p and rejects writes into descendant projects.
func runTarget(ctx context.Context, p *types.Project, name string) error {
	a := audit.Begin(ctx, p, types.HasCharm(ctx, types.CharmReadWrite))
	err := forEachSpell(ctx, p, name, func(ctx context.Context, s *spells.Spell) error {
		return invokeSpell(ctx, p, name, s)
	})
	return errors.Join(err, a.Finish(ctx, name))
}

// invokeSpell executes one spell; when a volatility.Runtime is present, failures are eligible for auto-retry.
func invokeSpell(ctx context.Context, p *types.Project, name string, s *spells.Spell) error {
	req := spells.InvokeRequest{Target: name, Dir: p.Dir}
	rt := volatility.RuntimeFromContext(ctx)
	if rt == nil {
		resp, err := s.Invoke(ctx, req)
		if err == nil {
			types.RecordReturn(ctx, p.Path, name, resp.Data)
		}
		return err
	}

	volatileTarget := s.Name() + "/" + name
	affected := rt.IsAffected(p.Path)
	start := time.Now()
	// Collect the peak resident memory of every process this target runs, so the
	// outcome recorded below carries a memory figure alongside its duration. The
	// collector is installed here rather than higher up because the unit that
	// has to fit on one runner is the target, not the invocation.
	ctx = types.WithPeakRSS(ctx)
	resp, err := s.Invoke(ctx, req)
	// Only a SUCCESSFUL invocation's value is recorded. A failed attempt is not
	// snapshotted, so its value has no consumer, and recording it would survive
	// the retry below: a first attempt that failed after returning a value would
	// leave that value behind for a second attempt that succeeded returning none.
	if err == nil {
		types.RecordReturn(ctx, p.Path, name, resp.Data)
	}
	result := "pass"
	attempts := 1
	decision := volatility.Decision{}

	if err != nil {
		decision = rt.Decide(p.Path, volatileTarget, affected)
		if decision.Retry {
			resp2, err2 := s.Invoke(ctx, req)
			if err2 == nil {
				types.RecordReturn(ctx, p.Path, name, resp2.Data)
			}
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

	// Reported only when a process actually reported one: an unmeasured target
	// must stay zero in the record so a reader can tell it apart from a
	// measured-and-tiny one.
	peakRSS := types.PeakRSS(ctx)
	rt.Record(p.Path, volatileTarget, forecast.Outcome{
		Result:         result,
		AffectedByDiff: affected,
		DurationMs:     time.Since(start).Milliseconds(),
		At:             time.Now(),
		Attempts:       attempts,
		MaxRSSBytes:    peakRSS,
	})

	if decision.Retry {
		status := "retry_failed"
		if result == "volatile" {
			status = "retried_volatile"
		} else if rt.IsRegression(p.Path, volatileTarget) {
			status = "suspected_regression"
		}
		annotateVolatility(p.Path, volatileTarget, status, rt)

		if rw := report.WriterFromContext(ctx); rw != nil {
			_ = report.Record(rw, report.VolatilityCall{
				Project:         p.Path,
				Target:          volatileTarget,
				Status:          status,
				Attempts:        attempts,
				RetryReason:     string(decision.Reason),
				VolatilityScore: rt.Score(p.Path, volatileTarget),
			})
		}
	}

	return err
}

// annotateVolatility surfaces a retried target as a GitHub Actions warning
// annotation, so it appears on the pull request rather than only in a log
// nobody opens when the job comes back green.
//
// A retry that succeeded is the case worth annotating: the job passes, so
// nothing else tells the reviewer that a target needed two attempts, and
// an unnoticed volatile target is how a suite decays into one nobody
// trusts. Gated on the user's opt-in and on actually running under
// Actions, so no other host ever sees a workflow command.
func annotateVolatility(project, target, status string, rt *volatility.Runtime) {
	if !rt.Config().Annotate {
		return
	}
	_ = annotate.Detect(os.Stderr).Annotate(annotate.Annotation{
		Level:   annotate.LevelWarning,
		Title:   "magus: volatile target",
		File:    project,
		Message: fmt.Sprintf("%s %s: %s (volatility %.2f)", project, target, status, rt.Score(project, target)),
	})
}

// verifyReadOnly runs fn - a target expected to be read-only (preflight/generate
// without the rw charm) - then fails if it left uncommitted changes in dir, i.e. it
// wrote when it should only have checked (the error points the user at the rw charm).
// Skipped when dir has no VCS, so the guard never blocks a non-repo checkout.
func (m *Magus) verifyReadOnly(ctx context.Context, dir, target string, fn func() error) error {
	if err := fn(); err != nil {
		return err
	}
	// Resolve the active VCS (git/hg/sl/jj) rather than shelling out to git, so the
	// cleanliness gate works under any backend.
	//
	// Resolved from the WORKSPACE ROOT with the workspace's own options, matching every
	// other vcs.Resolve call site in the tree. Resolving from the project dir instead detects
	// a backend only when the marker sits in that exact directory, because claimsExist stats
	// the path it is given and does not walk up. A project nested below an .hg/.sl/.jj root
	// then matches nothing, falls through to the default git driver, and the gate fails with
	// "git could not report working-tree status" on a perfectly healthy Mercurial workspace.
	// Passing empty options compounds it by ignoring a configured vcs.name / vcs.enabled.
	//
	// The outcomes below are deliberately not collapsed, following the rule this file
	// already applies to a missing ci target: "definitely absent" and "could not tell" are
	// different answers, and only the first is safe to read as a pass. A target reaches
	// here only by declaring FailOnDrift, so it has explicitly asked to be checked;
	// reporting "clean" when the check never ran would silently retract that guarantee.
	res, err := vcs.Resolve(ctx, m.ws.Root, "", m.ws.VCSOptions)
	if err != nil {
		// Resolve fails only for an explicitly requested VCS that does not exist
		// (MAGUS_VCS_NAME naming an unknown backend). That is misconfiguration, not
		// an absent VCS, and silently skipping it would hide the typo forever.
		return fmt.Errorf("%s: %s declares FailOnDrift but the VCS could not be resolved: %w", dir, target, err)
	}
	// VCS disabled, or NO backend claimed the root. The second half is what actually keeps
	// magus usable outside a repository (a container build, an extracted tarball): Resolve
	// never hands back a nil driver for that case - it falls back to git and reports
	// VCSSourceDefault - so testing res.VCS alone promised a no-op that could not happen,
	// and an unversioned tree hard-failed here instead. An explicitly requested backend is
	// deliberately not covered: asking for one and not having it is worth failing over.
	if res.VCS == nil || res.Source == types.VCSSourceDefault {
		return nil
	}
	files, err := res.VCS.DirtyFiles(ctx, dir, []string{"."})
	if err != nil {
		return fmt.Errorf("%s: %s declares FailOnDrift but %s could not report working-tree status, so drift was not verified: %w",
			dir, target, res.VCS.Name(), err)
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
			ctx, cancel := m.withTargetDeadline(ctx)
			defer cancel()
			run := func() error { return runTarget(ctx, p, name) }
			pol := p.TargetPolicies[name]
			if pol.FailOnDrift && !types.HasCharm(ctx, types.CharmReadWrite) {
				return m.verifyReadOnly(ctx, p.Dir, name, run)
			}
			return run()
		}
	}
	return func(ctx context.Context, p *types.Project) error {
		ctx, cancel := m.withTargetDeadline(ctx)
		defer cancel()
		return runTarget(ctx, p, name)
	}
}

// withTargetDeadline bounds one target's execution when config.TargetTimeout
// is set, and is a pass-through otherwise.
//
// The runaway guard: a magusfile is code, so a non-terminating loop is writable by
// accident and nothing else reclaims a CI runner that hit one. The Buzz VM samples
// cancellation on loop back edges (vm.checkCancel), so a spinning target notices
// without a check per instruction.
//
// The deadline covers the whole target, subprocesses included, which is why it is off
// by default - a value near a legitimate target's runtime fails builds that were fine.
func (m *Magus) withTargetDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	if m.cfg.TargetTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, m.cfg.TargetTimeout)
}

// makeSpellFilteredHandler returns a handler that runs name on a single named spell.
func (*Magus) makeSpellFilteredHandler(name, spellName string) TargetHandler {
	return func(ctx context.Context, p *types.Project) error {
		return forSpellNamed(ctx, p, name, spellName, func(ctx context.Context, s *spells.Spell) error {
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

// charmedTarget renders "target:charm,..." the way cache.reproTarget does for an
// executed step, so a dry run and a real run print the same repro command.
func charmedTarget(target string, charms []string) string {
	if len(charms) == 0 {
		return target
	}
	return target + ":" + strings.Join(charms, ",")
}
