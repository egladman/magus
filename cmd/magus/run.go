package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/interactive/clihint"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/types"
)

// runTarget dispatches `magus run <target> [projects...]`.
func runTarget(ctx context.Context, root string, _ runConfig, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		return targetUsage()
	}
	// Kept before anything reshapes them: --detach re-submits this invocation verbatim,
	// so it needs the args as the user wrote them, chain and all.
	origArgs := args

	// The chain is split off the RAW args, before anything else touches them.
	// splitTargetFromArgs partitions flags from positionals and reorders them
	// (flags first), which would hoist a verb's own --path across the separator and
	// leave the chain holding a bare flag.
	args, chainArgs, chained := splitOnThen(args)

	// Parsed before the run, not after it: a typo'd verb used to build the whole
	// project and only then exit 2 on something checkable up front.
	var chain chainPlan
	if chained {
		var proceed bool
		var chainErr error
		if chain, proceed, chainErr = prepareChain(chainArgs); chainErr != nil || !proceed {
			return chainErr
		}
	}

	// Find the target even if global flags precede it (`magus run --dry-run build`);
	// stdlib flag would otherwise treat the flag as the target. rest carries the hoisted
	// flags + any project args for cmdParse below.
	rawTarget, rest, ok := splitTargetFromArgs(args)
	if !ok {
		return targetUsage()
	}
	spellFilter, targetStr := parseTarget(rawTarget)
	parsedTarget, parseErr := types.ParseTarget(targetStr)
	hintCanonicalSpelling(parsedTarget)
	if parseErr != nil {
		return parseErr
	}
	targetName := canonicalTarget(parsedTarget.Name) // expand short aliases at the CLI edge
	parsedTarget.Name = targetName

	startedAt := time.Now()

	flagArgs, extraArgs := splitOnDashDash(rest)

	// Bound from the command registry, not declared here: the man page's copy of
	// this list and this one used to be written separately and reconciled by a test.
	// The two env-var defaults below are applied after binding, because they are a
	// property of THIS process rather than of the documented flag.
	var rf *gen.RunFlags
	projectArgs, err := cmdParse("run "+targetName, flagArgs, func(fs *flag.FlagSet) {
		rf = gen.BindRun(fs)
		// The shard pair defaults from the environment CI sets, so a matrix job
		// need not repeat itself on every magus call. Applied by seeding the bound
		// value (an explicit flag still wins, since parsing runs after this) and
		// mirroring it into DefValue so -h reports what the flag will actually do.
		envDefault(fs, gen.FlagRunShard, os.Getenv("MAGUS_SHARD"))
		envDefault(fs, gen.FlagRunNShards, os.Getenv("MAGUS_N_SHARDS"))
		fs.Usage = func() {
			fmt.Fprintf(os.Stderr, "Usage: magus run %s [flags] [project...] [-- <extra args>]\n", rawTarget)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Run target "+targetName+" for the selected projects.")
			if spellFilter != "" {
				fmt.Fprintf(os.Stderr, "Spell filter: only the %q spell runs.\n", spellFilter)
			}
			fmt.Fprintln(os.Stderr, "With no project args: all projects (or cwd-scoped if inside one).")
			fmt.Fprintln(os.Stderr, "Extra args after -- are forwarded to spells that honor them.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if rf.Wait && !rf.Detach {
		return usagef("magus run: --wait applies to --detach; a plain run already blocks until it finishes")
	}
	if rf.Detach {
		return detachToDaemon(ctx, root, append([]string{"run"}, withoutDetachFlag(origArgs)...), rf.Wait)
	}
	// -s deliberately suppresses the usual target progress. That is useful to an
	// agent, but a person otherwise has no positive signal that a slow invocation
	// is alive. Say it once, before any potentially long workspace load, and point
	// at the existing observer rather than inventing another progress surface.
	if global.silent {
		// Says what arrives and when, rather than sending the reader to poll a
		// dashboard in another terminal. This run BLOCKS: waiting for it is the
		// normal thing to do, and every target that runs prints an output ref
		// on the way past, so there is already an exact handle for anything
		// worth reading afterwards.
		interactive.Emit(os.Stderr, fmt.Sprintf(
			"running quietly; output refs print as targets finish, and `%s` reads any of them",
			clihint.QueryOutput.With("<ref>")))
	}

	if rf.Step && !isInteractiveTTY() {
		fmt.Fprintln(os.Stderr, "magus: --step requires an interactive terminal")
		return errSilent{exitCode: 2}
	}

	if rf.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = withTimeout(ctx, rf.Timeout, "run:"+targetName)
		defer cancel()
	}

	if rf.Step && !isInteractiveTTY() {
		fmt.Fprintln(os.Stderr, "magus: --step requires an interactive terminal")
		return errSilent{exitCode: 2}
	}
	if rf.Step {
		ctx = withStepGate(ctx)
	}

	if spellFilter != "" && targetName == "ci" {
		return fmt.Errorf("spell-qualified syntax (e.g. %q) is not supported for the ci target", rawTarget)
	}

	if rf.Graph {
		ws, err := inspectWorkspace(ctx, root)
		if err != nil {
			return err
		}
		return renderWorkspaceGraph(ctx, ws, graphRenderOptions{
			Upstream: rf.Upstream,
			Depth:    rf.Depth,
			Spell:    spellFilter,
			Roots:    projectArgs,
			Target:   targetName,
		})
	}

	if targetName == "ls" {
		ws, err := inspectWorkspace(ctx, root)
		if err != nil {
			return err
		}
		listTarget := types.Target{Path: parsedTarget.Path, Name: "ls"}
		targets, source, err := resolveTargets(ctx, ws, listTarget, projectArgs, clientCwd(ctx))
		if err != nil {
			return err
		}
		listTargets("run:ls", targets, source)
		return nil
	}

	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	// cwd is the caller's directory: for an adopted run it is the client's, carried on
	// ctx, not the daemon's process cwd. It scopes target resolution below and is recorded
	// on the invocation's journal, so both agree with where the user actually ran.
	cwd := clientCwd(ctx)
	targets, source, err := resolveTargets(ctx, m, parsedTarget, projectArgs, cwd)
	if err != nil {
		return err
	}
	// Fault tolerance by design: a target only some projects serve should skip - not
	// error - the projects that lack it when the scope is the workspace or several
	// projects, but a single project that does not serve it (or a name no project serves
	// at all) is an error. This is the run counterpart to affected's tolerance.
	if len(targets) > 0 {
		targets, err = filterServedTargets(ctx, m, targets, targetName)
		if err != nil {
			return err
		}
	}
	var scopeLabel string
	if len(targets) == 1 {
		scopeLabel = targets[0].Path
	} else {
		scopeLabel = fmt.Sprintf("%d projects", len(targets))
	}
	m.LogScope(ctx, scopeLabel, source)
	// Surface the active charms up front, next to the projects header, so the run's
	// state ("here's what's in effect") is visible before any work - and so a missing
	// default charm (e.g. rw not applied) is obvious rather than silent.
	charms := withDefaultCharms(parsedTarget.Charms, globalCfg.DefaultCharms, rf.NoDefaultCharms)
	// ci dispatches through RunCI, which drops the write-granting charms. Report what
	// runs, not what was asked for, or the header contradicts the run it introduces.
	if targetName == "ci" {
		charms = magus.CharmsForCI(charms)
	}
	m.LogCharms(ctx, strings.Join(charms, ","))
	m.LogCache(ctx)
	if len(targets) == 0 {
		// Zero targets here means the fan-out found no projects at all in the resolved
		// workspace - a degenerate or wrong-workspace resolution, not "nothing to do".
		// (A named target no project serves already errored in filterServedTargets above.)
		// An adopted run that mis-resolved the client's workspace lands here; fail loudly,
		// naming the workspace and target, so it can never pass with nothing executed.
		// m.Root() is the resolved root (the root arg is "" for a local run that walked up).
		return fmt.Errorf("run: workspace %s has no projects to run target %q", m.Root(), targetName)
	}

	opts, optsErr := outputOptionsOrDefault()
	if optsErr != nil {
		return optsErr
	}

	var rw *magus.ReportWriter
	if opts.Format == outputJSONL {
		w, cleanup, openErr := outputDst()
		if openErr != nil {
			return openErr
		}
		defer func() { _ = cleanup() }()
		var rwErr error
		rw, rwErr = magus.NewReportWriter(w, globalCfg.Report.Filter)
		if rwErr != nil {
			return rwErr
		}
		m.SetGraphObserver(rw.GraphObserver())
		defer func() { _ = rw.Close() }()
	}

	var runOpts []magus.RunOption
	if globalCfg.DryRun {
		runOpts = append(runOpts, magus.WithDryRun())
	}
	if len(charms) > 0 {
		runOpts = append(runOpts, magus.WithCharms(charms...))
	}
	if rf.NoVolatilityRetry {
		runOpts = append(runOpts, magus.WithNoVolatilityRetry())
	}
	race, err := resolveRace(rf.Race)
	if err != nil {
		return err
	}
	switch {
	case race.Replay:
		runOpts = append(runOpts, magus.WithRaceReplay())
	case race.Enabled:
		runOpts = append(runOpts, magus.WithRace())
	}
	if rf.Step {
		runOpts = append(runOpts, magus.WithStep())
	}
	if rf.NoCache {
		runOpts = append(runOpts, magus.WithNoCache())
	}
	if rw != nil {
		runOpts = append(runOpts, magus.WithReport(rw))
	}
	if spellFilter != "" {
		runOpts = append(runOpts, magus.WithSpellFilter(spellFilter))
	}
	if len(extraArgs) > 0 {
		runOpts = append(runOpts, magus.WithExtraArgs(extraArgs))
	}
	// Capture this run as an invocation: mint an id + open the union record log, and
	// record the command's lineage (run vs ci) so the viewer can trace it back.
	trigger := journal.TriggerRun
	if targetName == "ci" {
		trigger = journal.TriggerCI
	}
	liveBC, stopLive := beginLive(ctx, rf.Open)
	defer stopLive()
	// An adopted run (dispatched by the daemon) also feeds the daemon's live-run registry,
	// carried on ctx; a plain CLI run has no sink, so this is empty there.
	captureHandlers := append(liveHandlers(liveBC), console.RunSinkHandlers(ctx)...)
	invCtx, endInvocation := m.BeginInvocation(ctx, journal.Command{
		Arguments: append([]string{"run"}, args...), Cwd: cwd, Trigger: trigger,
	}, version, captureHandlers...)
	defer func() { endInvocation(err) }()

	// The sink rides the context down through the fan-out, so collecting return
	// values needs no signature change anywhere between here and invokeSpell.
	invCtx, readReturns := types.WithReturnCapture(invCtx)
	if targetName == "ci" {
		err = m.RunCI(invCtx, targets, runOpts...)
	} else {
		err = m.Run(invCtx, targets, runOpts...)
	}
	if rf.Timeout > 0 && errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("run %s: timed out after %s", targetName, rf.Timeout)
	}
	if rw != nil && rf.Shard != "" && rf.NShards > 0 {
		_ = rw.RecordShardTotal(rf.Shard, rf.NShards, time.Since(startedAt))
	}
	if reportedRunErr(err) {
		return errSilent{exitCode: 1}
	}
	if err != nil {
		return err
	}

	if chained {
		return runChain(ctx, m, opts, targetName, targets, chain, readReturns(targetName))
	}
	switch opts.Format {
	case outputJSON, outputYAML, outputTemplate:
		return emitRunResult(ctx, m, opts, targetName, charms, targets, readReturns(targetName))
	}
	return nil
}

// resolveTargets resolves targets from the workspace: by path, explicit args, cwd-scope, or all.
// cwd is the caller's working directory (the client's, for an adopted run - see clientCwd);
// it anchors relative project args and the cwd-scope lookup. Resolving cwd-scope against the
// daemon's own os.Getwd() is exactly the transposition that let a daemon adopt a run for an
// unrelated workspace and pass it vacuously, so the cwd is threaded in rather than read here.
func resolveTargets(ctx context.Context, ws types.WorkspaceRepository, t types.Target, projectArgs []string, cwd string) ([]types.Target, string, error) {
	anchor := cwdAnchor(ws.Root(), cwd)
	if t.Path != "" {
		resolved, err := file.ResolveProject(ctx, t.Path, anchor)
		if err != nil {
			return nil, "", err
		}
		t.Path = resolved
		targets, err := ws.ExpandPath(t)
		return targets, "", err
	}
	if len(projectArgs) > 0 {
		var all []types.Target
		for _, arg := range projectArgs {
			resolved, err := file.ResolveProject(ctx, arg, anchor)
			if err != nil {
				return nil, "", err
			}
			expanded, err := ws.ExpandPath(types.Target{Path: resolved, Name: t.Name})
			if err != nil {
				return nil, "", err
			}
			all = append(all, expanded...)
		}
		return all, "", nil
	}
	// cwd-scope: run only the project containing the caller's cwd, when it is inside one.
	// Keyed on the explicit cwd (not ws.ExpandCwd, which reads os.Getwd internally).
	if cwd != "" {
		if p, ok := ws.Where(cwd); ok {
			return []types.Target{{Path: p.Path, Name: t.Name}}, "cwd", nil
		}
	}
	targets, err := ws.ExpandPath(t)
	return targets, "", err
}

// filterServedTargets keeps only the (project, target) pairs whose project actually
// defines the target, so a fan-out never runs - or misreports "[pass]" for - a project
// that lacks it. It errors when nothing serves the target: a single project in scope
// names that project (you asked for it explicitly), otherwise it reads as an unknown
// target across the selection. When some projects serve it and others don't, the ones
// that don't are dropped with a warning - the tolerant multi-project behavior.
func filterServedTargets(ctx context.Context, m *magus.Magus, targets []types.Target, targetName string) ([]types.Target, error) {
	return applyTargetFilter(targets, targetName, buildDefinesTarget(ctx, m), func(path string) string { return projectLabelFor(m, path) })
}

// applyTargetFilter is the pure policy behind filterServedTargets: partition targets by
// whether their project defines the target, error when none do (single project named vs
// unknown-across-selection), otherwise drop the undefined ones with a warning. Split out
// so the fault-tolerance matrix is testable without a live workspace.
func applyTargetFilter(targets []types.Target, targetName string, defines func(path, target string) bool, label func(path string) string) ([]types.Target, error) {
	var served, skipped []types.Target
	for _, t := range targets {
		if defines(t.Path, t.Name) {
			served = append(served, t)
		} else {
			skipped = append(skipped, t)
		}
	}
	if len(served) == 0 {
		if len(targets) == 1 {
			return nil, fmt.Errorf("run: target %q is not defined in project %s", targetName, label(targets[0].Path))
		}
		return nil, fmt.Errorf("run: target %q is not defined in any of the %d selected projects", targetName, len(targets))
	}
	if len(skipped) > 0 {
		names := make([]string, 0, len(skipped))
		for _, t := range skipped {
			names = append(names, label(t.Path))
		}
		slog.Warn("run: target not defined in some selected projects; skipping them",
			slog.String("target", targetName), slog.String("skipped", strings.Join(names, ", ")))
	}
	return served, nil
}

// buildDefinesTarget returns a predicate reporting whether a project defines a target,
// covering BOTH sources of runnable targets: magusfile-declared targets (the target
// graph's nodes) and spell ops (each bound spell's Targets). Neither set alone is
// complete - the magusfile spell exposes no ops, and spell ops are not graph nodes.
func buildDefinesTarget(ctx context.Context, m *magus.Magus) func(path, target string) bool {
	byProject := map[string]map[string]bool{}
	add := func(path, name string) {
		set := byProject[path]
		if set == nil {
			set = map[string]bool{}
			byProject[path] = set
		}
		set[name] = true
	}
	// buildDefinesTarget returns a bare predicate closure with no error path of its
	// own; a cancelled ctx here just drops the graph-derived entries from the
	// predicate (the spell-derived ones added below still make it usable), rather
	// than failing a caller that never signed up to handle this.
	if graph, err := m.TargetGraph(ctx); err == nil {
		for _, p := range graph.Projects {
			for _, n := range p.Nodes {
				add(p.Path, n.Name)
			}
		}
	}
	for _, p := range m.All() {
		for _, sp := range p.ResolvedSpells {
			for _, t := range sp.Targets() {
				add(p.Path, t)
			}
		}
	}
	return func(path, target string) bool { return byProject[path][target] }
}

// projectLabelFor renders a project's human name (never a bare ".") from its path.
func projectLabelFor(m *magus.Magus, path string) string {
	if p := m.Get(path); p != nil {
		return types.ProjectLabel(p.Path, p.Dir)
	}
	return types.ProjectLabel(path, "")
}

// cwdAnchor returns cwd as a slash path relative to root, the anchor for
// resolving relative project args. It falls back to "." - the workspace root -
// when cwd is empty, cannot be made relative to root, or lies OUTSIDE it.
//
// That last case is what `--root <path>` selects: the caller is standing
// somewhere else, and filepath.Rel happily hands back a "../.."-prefixed path,
// which every relative project ref then inherits and fails the escape check on.
// `magus --root <ws> run build .` reported `project path "." escapes workspace
// root from "../<dir>"` for exactly that reason. A relative ref is measured from
// the workspace the caller NAMED, so an outside cwd contributes nothing to it.
// A cwd inside the workspace is untouched, which is every invocation without --root.
func cwdAnchor(root, cwd string) string {
	if cwd == "" {
		return "."
	}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	rel, err := filepath.Rel(root, cwd)
	if err != nil {
		return "."
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "."
	}
	return rel
}

// clientCwd returns the working directory that scopes this invocation. An adopted
// run is dispatched inside the long-lived daemon, whose own os.Getwd() is unrelated
// to (and in a stale daemon may be a deleted) directory; the daemon carries the
// client's cwd on ctx (proc.CwdFromContext), so prefer it. A plain local run has no
// ctx cwd and falls back to os.Getwd(). Empty only when neither is available.
func clientCwd(ctx context.Context) string {
	if c := proc.CwdFromContext(ctx); c != "" {
		return c
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}

func targetUsage() error {
	fmt.Fprintln(os.Stderr, "Usage: magus run <target> [flags] [project...]")
	fmt.Fprintln(os.Stderr, "       magus run <spell>::<target> [flags] [project...]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Targets:")
	fmt.Fprintln(os.Stderr, "  <name>   any target exported by a project's magusfile")
	fmt.Fprintln(os.Stderr, "  ls       print selected projects (no execution)")
	fmt.Fprintln(os.Stderr, "  ci       the magusfile's ci target, run read-only (affected/pipeline anchor)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "To see what a project can run: `magus ls targets [project]`")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Conventional lifecycle names (you compose these in your magusfile from a")
	fmt.Fprintln(os.Stderr, "spell's tool-native ops, e.g. global function build(_a) go.build() end):")
	fmt.Fprintln(os.Stderr, "  build / test / lint / format / clean / generate / ci")
	fmt.Fprintln(os.Stderr, "  (fmt -> format and gen -> generate are accepted as aliases)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Spell-qualified targets run only the named spell's op:")
	fmt.Fprintln(os.Stderr, "  magus run typescript::eslint api   # eslint op of the typescript spell on api")
	fmt.Fprintln(os.Stderr, "  magus run go::go-vet /             # go-vet op of the go spell across all projects")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Without project args, the cwd project (or all) is selected.")
	fmt.Fprintln(os.Stderr, "With project args, exactly those projects are targeted:")
	fmt.Fprintln(os.Stderr, "  magus run build api web/studio extensions/drape")
	fmt.Fprintln(os.Stderr, "Use \"/\" to target all projects regardless of cwd:")
	fmt.Fprintln(os.Stderr, "  magus run build /")
	return flag.ErrHelp
}

// parseTarget splits on "::" into (spell, target); e.g. "ts::lint" → ("ts", "lint").
func parseTarget(s string) (spell, target string) {
	if before, after, ok := strings.Cut(s, "::"); ok {
		return before, after
	}
	return "", s
}

type runConfig struct {
	watchIgnores []types.IgnorePattern
}

// runArtifact is one file the run produced, with the classification `magus describe
// file` would give it. Role rides along because "where did the build put it" and "is
// this thing generated" are the two questions an agent asks in sequence, and
// answering only the first leaves it to guess the second.
type runArtifact struct {
	Path string `json:"path"           yaml:"path"`
	Glob string `json:"glob"           yaml:"glob"`
	Role string `json:"role,omitempty" yaml:"role,omitempty"`
}

// runProject is one project's slice of the run. Artifacts and Value are per
// project because a run fans out: a single flat list could not say which project
// produced which file, and a single value would be meaningless for a multi-project
// run.
type runProject struct {
	Path string `json:"path" yaml:"path"`
	// Value is what the target returned (str or [str]), absent for a `> void`
	// target. Replayed from the cache entry on a hit, so it is present whether or
	// not this invocation actually executed the target.
	Value     any           `json:"value,omitempty"     yaml:"value,omitempty"`
	Artifacts []runArtifact `json:"artifacts,omitempty" yaml:"artifacts,omitempty"`
}

// runOutput is the structured result of `magus run <target>`.
type runOutput struct {
	Target   string       `json:"target"           yaml:"target"`
	Charms   []string     `json:"charms,omitempty" yaml:"charms,omitempty"`
	DryRun   bool         `json:"dry_run"          yaml:"dry_run"`
	Count    int          `json:"count"            yaml:"count"`
	Projects []runProject `json:"projects"         yaml:"projects"`
}

// emitRunResult renders what a run produced. It is deliberately NOT a second
// progress stream: -o jsonl already streams events, and this answers the different
// question of what exists on disk now that the run is done.
//
// A dry run reports no artifacts: nothing executed, so any file matching an output
// glob is left over from a previous run and reporting it would claim this invocation
// produced it. It DOES report the return value, because a dry run evaluates the
// target body under a tracing context - the value is real, and omitting it made
// `-o json` claim "no return value" for a target that had just produced one, while
// `--then value` printed it.
func emitRunResult(ctx context.Context, m *magus.Magus, opts OutputOptions, target string, charms []string, selection []types.Target, returns types.Returns) error {
	out := runOutput{Target: target, Charms: charms, DryRun: globalCfg.DryRun}
	projects := m.ResolveProjects(selection)
	out.Count = len(projects)

	// Artifacts are resolved once for the whole set, then bucketed by owning
	// project, so the expansion walks each project's globs exactly once.
	byProject := map[string][]runArtifact{}
	if !out.DryRun {
		artifacts, err := m.ResolveTargetOutputs(ctx, projects, target)
		if err != nil {
			return err
		}
		paths := make([]string, len(artifacts))
		for i, a := range artifacts {
			paths[i] = a.Path
		}
		roles, err := m.ClassifyFiles(ctx, paths)
		if err != nil {
			return err
		}
		roleOf := make(map[string]string, len(roles))
		for _, f := range roles {
			roleOf[f.Path] = f.Role
		}
		// Bucket by the project that DECLARED the glob, in one pass. This used to ask
		// FindOutputProducer inside the per-project loop, which was O(projects x
		// artifacts) with a workspace walk per pair and - worse - got the nil case
		// backwards: an artifact no project's tree claimed failed the
		// `owner.Path != p.Path` guard for every project, so it was reported N times
		// in an N-project result. The declaring project is already on the artifact and
		// cannot come back nil.
		for _, a := range artifacts {
			byProject[a.ProjectPath] = append(byProject[a.ProjectPath], runArtifact{Path: a.Path, Glob: a.Glob, Role: roleOf[a.Path]})
		}
	}

	for _, p := range projects {
		out.Projects = append(out.Projects, runProject{
			Path:      p.Path,
			Artifacts: byProject[p.Path],
			Value:     returns[p.Path],
		})
	}
	return emitFormatted(opts, out)
}

// envDefault seeds a bound flag from an environment variable, leaving it alone when
// the variable is unset. DefValue is updated too, so `-h` shows the value the flag
// would take rather than the registry's zero.
func envDefault(fs *flag.FlagSet, name, value string) {
	if value == "" {
		return
	}
	f := fs.Lookup(name)
	if f == nil {
		return
	}
	if err := f.Value.Set(value); err != nil {
		return
	}
	f.DefValue = value
}

// localOnlyFlags never travel to the daemon. --detach would make it detach
// again, handing the work to itself forever; --wait describes what THIS process
// does after submitting and means nothing to the run itself.
//
// Both names come from the command registry rather than from a literal. The
// second one used to be spelled "wait" right here, beside a constant for the
// first - and a flag whose name is a literal in one place and a constant in
// another is a rename waiting to go half-applied.
var localOnlyFlags = []string{gen.FlagRunDetach, gen.FlagRunWait}

// withoutDetachFlag drops the local-only flags from an argv, in every spelling
// the flag package accepts: -name, --name, and either with an inline =value.
//
// The argv is re-submitted verbatim to the daemon, so leaving one in would be
// acted on there.
func withoutDetachFlag(args []string) []string {
	out := make([]string, 0, len(args))
	for i, a := range args {
		if a == "--" {
			// Past the separator the tokens belong to the forwarded tool, not to
			// magus; the argv is re-submitted verbatim, so copy the rest through
			// unchanged instead of scanning it for --detach.
			out = append(out, args[i:]...)
			break
		}
		trimmed := strings.TrimLeft(a, "-")
		if key, _, _ := strings.Cut(trimmed, "="); strings.HasPrefix(a, "-") && slices.Contains(localOnlyFlags, key) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// detachToDaemon hands this invocation to the daemon and returns as soon as it is queued,
// so a caller can watch it instead of blocking or sleeping.
//
// It requires a PERSISTENT daemon (`magus server start`), and says so rather than falling
// back. A per-process proc server - which magus starts for ordinary commands - dies when
// this invocation exits, so submitting there would queue work that is silently dropped:
// the caller would be told it detached, and nothing would ever run. Refusing is the only
// honest answer, and the remedy is one command.
func detachToDaemon(ctx context.Context, root string, argv []string, wait bool) error {
	addr, err := resolveDaemonAddr(ctx, "")
	if err != nil || addr == "" {
		return types.WrapDiagnostic(types.DaemonRequired, nil,
			"--detach hands the work to the daemon, and none is running; start one with `%s`", clihint.ServerStart)
	}
	st, serr := proc.QueryStatus(ctx, addr)
	if serr != nil || st == nil || st.Mode != "daemon" {
		return types.WrapDiagnostic(types.DaemonRequired, nil,
			"--detach needs the persistent daemon, and %s is not one: a per-process server exits with this command, so the work would be queued and silently dropped. Start it with `%s`",
			addr, clihint.ServerStart)
	}
	inv, err := proc.SubmitJob(ctx, addr, argv, version)
	if err != nil {
		return fmt.Errorf("--detach: %w", err)
	}
	if inv == "" {
		fmt.Fprintln(os.Stderr, "magus: the daemon is already running this exact command; not queued twice")
		return nil
	}
	// Hand back the HANDLE and the command that resolves it, never a dashboard
	// to poll. A "watch it with status --watch" hint gives an agent nothing to
	// parse and no completion signal, and gives a human another window to
	// babysit; the invocation id is addressable, and `query invocation` reads
	// its journal - outcome, timings, the output refs of every target it ran -
	// whenever the reader actually wants it.
	if !wait {
		fmt.Fprintf(os.Stderr, "magus: detached as %s\n  read it with: %s\n",
			inv, clihint.QueryInvocation.With(inv))
		return nil
	}
	fmt.Fprintf(os.Stderr, "magus: running as %s on the daemon\n", inv)
	return awaitInvocation(ctx, root, inv)
}

// awaitInvocation blocks until the daemon's run records a finished event, then
// reports its outcome and exits with it.
//
// This is what --detach --wait is FOR, and the combination is not a
// contradiction: the daemon owns the run, so it coalesces with an identical one
// already in flight and shares the pool, while the caller still gets a
// synchronous answer and an exit status to branch on. Detach alone is for
// firing and forgetting; this is for wanting the daemon's scheduling without
// giving up the shell's.
//
// Waiting on Status, never on FinishedMs: an interrupted run has no finished
// event, and InvocationFromEvents backfills its finish time from the last event
// it saw - so a timestamp says "something happened last", while a status is the
// only thing that says "this is over".
func awaitInvocation(ctx context.Context, root, inv string) error {
	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	for delay := 100 * time.Millisecond; ; {
		select {
		case <-ctx.Done():
			// Ctrl-C detaches the WATCHER, not the run: the daemon owns it and
			// keeps going, so say how to pick it up again rather than implying
			// it was cancelled.
			fmt.Fprintf(os.Stderr, "\nmagus: stopped waiting; %s is still running on the daemon\n  read it with: %s\n",
				inv, clihint.QueryInvocation.With(inv))
			return ctx.Err()
		case <-time.After(delay):
		}
		// A log that does not exist yet is a run the daemon has not started, not
		// an error - it is the ordinary first tick.
		header, err := m.InvocationByID(inv)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("--wait: read run log for %s: %w", inv, err)
		}
		if err == nil && header.Status != "" {
			return reportInvocation(header, inv)
		}
		if delay < 2*time.Second {
			delay *= 2
		}
	}
}

// reportInvocation prints a finished run's outcome and exits with it.
func reportInvocation(header magus.Invocation, inv string) error {
	took := time.Duration(header.FinishedMs-header.StartedMs) * time.Millisecond
	if header.Status != "pass" {
		fmt.Fprintf(os.Stderr, "magus: %s failed (%s)\n  read it with: %s\n",
			inv, formatDur(took), clihint.QueryInvocation.With(inv))
		return errSilent{exitCode: 1}
	}
	fmt.Fprintf(os.Stderr, "magus: %s passed (%s)\n  read it with: %s\n",
		inv, formatDur(took), clihint.QueryInvocation.With(inv))
	return nil
}
