package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	internalci "github.com/egladman/magus/internal/ci"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/project/impact"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/check"
	"github.com/egladman/magus/vcs"
)

// The plan types and helpers live in internal/ci; run.go and session_load_next.go still
// call them by these names.
type (
	planOutput = internalci.PlanOutput
	planShard  = internalci.PlanShard
)

// failedResults wraps internalci.FailedResults so run.go keeps its unexported accessor.
type failedResults struct{ internalci.FailedResults }

func (c *failedResults) list() []journal.Event { return c.List() }

func planSummaryMarkdown(out planOutput) string { return internalci.PlanSummaryMarkdown(out) }

// affected dispatches `magus affected <target>`; project set is determined by VCS diff.
func affected(ctx context.Context, root string, _ runConfig, args []string) error {
	// Bare `magus affected` (no target) is a usage error, not a help request: a target
	// is required. Print a clear one-liner plus usage and exit non-zero, never silently.
	if len(args) == 0 {
		affectedMissingTargetUsage()
		return exit.Reported(exit.Usage)
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		affectedUsage()
		return flag.ErrHelp
	}

	// --explain <project> is a separate mode: it shows why a project is in the
	// affected set rather than running a target.
	if explainProject, base, ok := parseExplainArgs(args); ok {
		return affectedExplain(ctx, root, explainProject, base)
	}

	// --plan and --bisect are forensic modes (siblings of --explain) that reason
	// about the affected set rather than running a target. --plan emits a CI shard
	// plan for the affected set; --bisect drives VCS bisect to find a regression's
	// culprit commit. Both are detected before the positional target is parsed.
	if hasModeFlag(args, "plan") {
		return affectedPlan(ctx, root, args)
	}
	if hasModeFlag(args, "bisect") {
		return affectedBisect(ctx, root, args)
	}
	// --impact is a read-only forensic mode: it reports the blast radius of the
	// current changeset (changed files, seed projects, and the affected closure with
	// each project's targets) and never executes a target. It takes no positional
	// target, so it is detected before the target split.
	if hasModeFlag(args, "impact") {
		return affectedImpact(ctx, root, args)
	}

	// Find the target even if global flags precede it (`magus affected --dry-run ci`);
	// mirrors `magus run`. rest carries the hoisted flags for commandParse below.
	rawTarget, rest, ok := splitTargetFromArgs(args, func(fs *flag.FlagSet) { flaggen.BindAffected(fs) })
	if !ok {
		affectedUsage()
		return flag.ErrHelp
	}
	spellFilter, targetStr := parseTarget(rawTarget)
	parsed, perr := types.ParseTarget(targetStr)
	hintCanonicalSpelling(ctx, parsed)
	if perr != nil {
		return perr
	}
	target := canonicalTarget(parsed.Name) // expand short aliases at the CLI edge, mirroring `magus run`

	// Split on "--" before flag parsing so passthrough args aren't consumed by flag.
	flagArgs, extraArgs := splitOnDashDash(rest)

	var af *flaggen.AffectedFlags
	_, err := commandParse("affected "+target, flagArgs, func(fs *flag.FlagSet) {
		af = flaggen.BindAffected(fs)
		fs.Usage = func() {
			fmt.Fprintf(os.Stderr, "Usage: magus affected %s [flags] [-- <extra args>]\n", target)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintf(os.Stderr, "Run target %s for every project affected by VCS changes.\n", target)
			fmt.Fprintln(os.Stderr, "Extra args after -- are forwarded to spells that honor them.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if af.Detach {
		return detachRun(ctx)
	}

	if af.Step && af.Stdin {
		return fmt.Errorf("magus affected: --step and --stdin are mutually exclusive")
	}
	if af.Step && !isInteractiveTTY() {
		slog.ErrorContext(ctx, "--step requires an interactive terminal", attr.Notice(""), attr.Component("magus"))
		return exit.Reported(exit.Usage)
	}
	if af.Step {
		ctx = withStepGate(ctx)
	}

	if af.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = withTimeout(ctx, af.Timeout, "affected:"+target)
		defer cancel()
	}

	if af.Stdin && af.Base != "" {
		return fmt.Errorf("magus affected: --stdin and --base are mutually exclusive")
	}

	if af.Graph {
		if af.Stdin {
			return fmt.Errorf("magus affected: --graph and --stdin are mutually exclusive")
		}
		ws, err := inspectWorkspace(ctx, root)
		if err != nil {
			return err
		}
		targets, _, _, err := ws.ExpandAffected(ctx, "list", af.Base)
		if err != nil {
			return err
		}
		roots := make([]string, len(targets))
		for i, t := range targets {
			roots[i] = t.Path
		}
		return renderWorkspaceGraph(ctx, ws, graphRenderOptions{
			Upstream: af.Upstream,
			Depth:    af.Depth,
			Roots:    roots,
		})
	}

	if af.Stdin {
		if target == "ls" {
			return fmt.Errorf("magus affected: --stdin is not supported with the ls target")
		}
		opts, err := outputOptionsOrDefault()
		if err != nil {
			return err
		}
		m, err := loadMagus(ctx, root)
		if err != nil {
			return err
		}
		sink, closeSink, err := openSink(ctx, opts)
		if err != nil {
			return err
		}
		defer func() { _ = closeSink() }()
		// The graph traversal is recorded on the same sink: the run's, and every read before it.
		ctx = types.ContextWithGraphObserver(ctx, sink.GraphObserver())
		streamCtx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		runOpts := []magus.RunOption{magus.WithSink(sink)}
		if globalCfg.DryRun {
			runOpts = append(runOpts, magus.WithDryRun())
		}
		if len(extraArgs) > 0 {
			runOpts = append(runOpts, magus.WithExtraArgs(extraArgs))
		}
		streamAffected(streamCtx, m, os.Stdin, target, af.Null, func(err error) {
			slog.ErrorContext(streamCtx, "affected --stdin", attr.Error(err))
		}, runOpts...)
		return nil
	}

	if target == "ls" {
		ws, err := inspectWorkspace(ctx, root)
		if err != nil {
			return err
		}
		targets, source, _, err := ws.ExpandAffected(ctx, "list", af.Base)
		if err != nil {
			return err
		}
		listTargets("affected:ls", targets, source)
		// The listing is one path per line whatever -o says, so the breadcrumbs print
		// only for the text reader; a caller parsing names must not find a next: block.
		// The plan breadcrumb names ci, not ls: ls is the listing verb and has no shard
		// plan of its own, while ci is the target the set is listed in order to run.
		if opts, err := outputOptionsOrDefault(); err == nil && opts.Format == outputText {
			paths := make([]string, len(targets))
			for i, t := range targets {
				paths[i] = t.Path
			}
			nx := newNextGate(root)
			printNext(os.Stdout, nx, nx.served(hint.NextForAffected(types.TargetCI, paths)))
		}
		return nil
	}

	// See the matching comment in runTarget: finalizeConfig already pointed
	// globalCfg.Log.Format at "jsonl" for this invocation.
	opts, optsErr := outputOptionsOrDefault()
	if optsErr != nil {
		return optsErr
	}

	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}

	sink, closeSink, err := openSink(ctx, opts)
	if err != nil {
		return err
	}
	defer func() { _ = closeSink() }()
	// The graph traversal is recorded on the same sink: the run's, and every read before it.
	ctx = types.ContextWithGraphObserver(ctx, sink.GraphObserver())

	targets, source, _, affectedSet, err := m.ExpandAffectedSet(ctx, target, af.Base)
	if err != nil {
		return err
	}
	// Name the projects, with the count after them. "3 projects" told a reader how many
	// but not which, so the first thing anyone did was re-run with --plan to find out.
	// The count stays because it is the number a shard matrix is sized from.
	var scopeLabel string
	switch {
	case len(targets) == 1:
		scopeLabel = targets[0].Path
	case len(targets) > 1:
		paths := make([]string, 0, len(targets))
		for _, t := range targets {
			paths = append(paths, t.Path)
		}
		scopeLabel = fmt.Sprintf("%s (%d)", strings.Join(paths, " "), len(paths))
	default:
		scopeLabel = "0 projects"
	}
	// The base goes on its own line rather than in the projects suffix. It is the third
	// input that decides what runs (the same command against a different base is a
	// different build), and burying it in parentheses after a project list made it the
	// one header fact nobody read. source already names the VCS that produced it
	// ("git diff vs origin/main"), which is what distinguishes a git base from a jj one.
	sink.EmitScope(ctx, scopeLabel, "", scopePaths(targets))
	sink.EmitBase(ctx, source)
	// Merge magus.yaml default_charms with any explicit charm on the target, the same
	// as `magus run` does. Previously `affected` used only the explicit charms, so
	// default_charms (e.g. rw) silently did NOT apply to `affected`, unlike `run`.
	charms := withDefaultCharms(parsed.Charms, globalCfg.DefaultCharms, !af.DefaultCharms)
	// Same as `magus run`: RunCI and a sized gate get the carried set and
	// verify without the write charms, which the header names.
	carried := charms
	if target == "ci" {
		charms = magus.CharmsForCI(charms)
	}
	sink.EmitCharms(ctx, strings.Join(charms, ","))
	tier, mode, dir := m.CacheDescription()
	sink.EmitCache(ctx, tier, mode, dir)
	if len(targets) == 0 {
		slog.With(attr.Component("affected")).InfoContext(ctx, "no projects affected", slog.String("target", target))
		return nil
	}

	// MGS1028 where it is paid for: --impact and --explain report the same condition
	// forensically, and this is the invocation that buys the rerun.
	undeclaredOnly := internalci.TrackedUndeclaredSeeds(ctx, m.Root(), m.VCSOptions(), internalci.UndeclaredOnlySeeds(affectedSet))

	// Same check as `magus run ci`: a redundant gate refuses under load and
	// advises when the machine is idle. See prepareGateRedundancy.
	gate := prepareGateRedundancy(ctx, m, target, targets, charms, false)
	gate.noteUndeclaredSeeds(slices.Sorted(maps.Keys(undeclaredOnly)))
	if gateErr := gate.evaluate(ctx, !af.RedundancyCheck); gateErr != nil {
		return gateErr
	}
	// Sized once the gate is going to run. A trivial change is proven to need no gate,
	// which is a verdict on the change rather than a deferral, so it exits 0.
	sized := gate.size(ctx, af.Base, !af.RedundancyCheck)
	if sized != nil {
		slog.InfoContext(ctx, renderSizing(target, *sized), attr.Notice(""), attr.Component("magus"))
		if sized.Tier == check.RiskTrivial {
			return nil
		}
	}
	// After the gate agreed to run, because a refused gate pays for nothing.
	noteUndeclaredSeedCost(ctx, sink, undeclaredOnly)
	reportUndeclaredSeeds(ctx, sink, undeclaredOnly)

	var runOpts []magus.RunOption
	if isGateInvocation(target, false) {
		runOpts = append(runOpts, magus.WithGate())
	}
	race, err := resolveRace(af.Race)
	if err != nil {
		return err
	}
	switch {
	case race.Replay:
		runOpts = append(runOpts, magus.WithRaceReplay())
	case race.Enabled:
		runOpts = append(runOpts, magus.WithRace())
	}
	if globalCfg.DryRun {
		runOpts = append(runOpts, magus.WithDryRun())
	}
	if af.Step {
		runOpts = append(runOpts, magus.WithStep())
	}
	if !af.Cache {
		runOpts = append(runOpts, magus.WithCache(false))
	}
	runOpts = append(runOpts, magus.WithSink(sink))
	ctx = processStdioContext(ctx)
	if len(extraArgs) > 0 {
		runOpts = append(runOpts, magus.WithExtraArgs(extraArgs))
	}
	if spellFilter != "" {
		if target == "ci" {
			return fmt.Errorf("affected: spell-qualified syntax (e.g. %q) is not supported for the ci target", rawTarget)
		}
		runOpts = append(runOpts, magus.WithSpellFilter(spellFilter))
	}
	// Capture as an invocation (lineage: affected, or affected ci) with a union log.
	trigger := journal.TriggerAffected
	if target == "ci" {
		trigger = journal.TriggerCI
	}
	// The client's cwd (carried on ctx for an adopted affected run), not the server's
	// process cwd, so the invocation's journal records where the user actually ran.
	cwd := clientCurrentWorkingDirectory(ctx)
	liveBC, stopLive := beginLive(ctx, af.Open)
	defer stopLive()
	// An adopted affected run (dispatched by the server) also feeds the server's live-run
	// registry, carried on ctx; a plain CLI run has no sink, so this is empty there.
	captureHandlers := append(liveHandlers(liveBC), console.RunSinkHandlers(ctx)...)
	// Durable session facts ride the same fan-out here as on the run path: one fact per
	// target result, into the store every worktree of this repo shares. Without it
	// `magus session` would show a repository where only `magus run` ever happened,
	// and CI runs through this path.
	captureHandlers = withInvocationJournal(ctx, captureHandlers, m.Root(), "affected", args)
	failed := &failedResults{}
	statuses := newResultStatuses(target)
	captureHandlers = append(captureHandlers, failed, statuses)
	invCtx, endInvocation := m.BeginInvocation(ctx, magus.Command{
		Arguments: append([]string{"affected"}, args...), Cwd: cwd, Trigger: trigger,
	}, version, captureHandlers...)
	defer func() { endInvocation(err) }()

	invCtx, readReturns := types.WithReturnCapture(invCtx)
	switch {
	case sized != nil:
		err = m.RunGate(invCtx, *sized, withCharms(runOpts, carried)...)
	case target == "ci":
		err = m.RunCI(invCtx, targets, withCharms(runOpts, carried)...)
	default:
		err = m.Run(invCtx, targets, withCharms(runOpts, charms)...)
	}
	// Read the instant the run returns: after this line a cancellation means the signal
	// arrived once the verdict was already decided, and dropping it would throw away a
	// gate that genuinely ran.
	gate.record(invCtx, err, invCtx.Err() != nil, failed.list())
	if err != nil && opts.Format == outputText {
		internalci.PrintFailedResults(os.Stdout, failed.list())
	}
	if opts.Format == outputText {
		internalci.PrintRunRef(os.Stdout, journal.InvocationIDFromContext(invCtx))
	}
	if af.Timeout > 0 && errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("affected %s: timed out after %s", target, af.Timeout)
	}
	if reportedRunError(err) {
		return exit.Reported(exit.Failure)
	}
	if err != nil {
		return err
	}
	emitConcurrencyNudge(ctx, sink, m, os.Args[1:])

	sink.RecordValues(target, readReturns(target))
	switch opts.Format {
	case outputJSON, outputYAML, outputTemplate:
		return emitRunResult(ctx, m, opts, target, charms, targets, readReturns(target), statuses,
			slices.Sorted(maps.Keys(undeclaredOnly)))
	case outputName:
		return emitProjectNames(m, targets)
	}
	return nil
}

// withCharms is opts running under charms, when there are any.
func withCharms(opts []magus.RunOption, charms []string) []magus.RunOption {
	if len(charms) == 0 {
		return opts
	}
	return append(slices.Clip(opts), magus.WithCharms(charms...))
}

func affectedMissingTargetUsage() {
	fmt.Fprintf(os.Stderr, "magus affected: a target is required (e.g. `%s`)\n", hint.Affected.With("ci"))
	fmt.Fprintln(os.Stderr, "")
	affectedUsage()
}

func affectedUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus affected <target> [--base <ref>] [flags]")
	fmt.Fprintln(os.Stderr, "       magus affected --explain <project> [--base <ref>]")
	fmt.Fprintln(os.Stderr, "       magus affected --impact [--base <ref>]")
	fmt.Fprintln(os.Stderr, "       magus affected <target> --plan [--max-shards N]")
	fmt.Fprintln(os.Stderr, "       magus affected --bisect <project> [--target <target>] [--good <sha>]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Targets (same as 'run' but project set comes from VCS diff):")
	fmt.Fprintln(os.Stderr, "  list      print affected projects (no execution)")
	fmt.Fprintln(os.Stderr, "  ci        full pipeline for affected projects")
	fmt.Fprintln(os.Stderr, "  <target>    any target supported by the project's tool (build, test, lint, ...)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Forensic modes (reason about the affected set instead of executing):")
	fmt.Fprintln(os.Stderr, "  --explain <project>  show why a project is in the affected set")
	fmt.Fprintln(os.Stderr, "  --impact             report the blast radius of the changeset (changed files, seeds, affected)")
	fmt.Fprintln(os.Stderr, "  <target> --plan      emit a provider-neutral JSON CI shard plan for <target> (e.g. ci)")
	fmt.Fprintln(os.Stderr, "  --bisect <project>   drive VCS bisect to find a regression's culprit commit")
	fmt.Fprintln(os.Stderr, "  --base <ref>         override VCS base ref (default: MAGUS_VCS_BASE_REF or origin/main)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Use MAGUS_VCS_BASE_REF or --base to set the comparison ref.")
	fmt.Fprintf(os.Stderr, "Use --stdin to read changed paths from a pipe (e.g. `%s | %s`).\n", hint.Watch, hint.Affected.With("--stdin", "build"))
}

// hasModeFlag reports whether --name (or -name, with an optional =value) appears
// in args. It routes affected's forensic modes (--plan, --bisect) before the
// positional target is parsed, mirroring how --explain is detected.
func hasModeFlag(args []string, name string) bool {
	long, short := "--"+name, "-"+name
	for _, a := range args {
		if a == long || a == short ||
			strings.HasPrefix(a, long+"=") || strings.HasPrefix(a, short+"=") {
			return true
		}
	}
	return false
}

// affectedPlan emits a provider-neutral JSON shard plan for the affected set of a
// target (the --plan mode of `magus affected`). It does NOT execute the pipeline;
// CI wrappers (e.g. GitHub Actions) translate the matrix into their own parallel-job
// format, and `magus run --stdin` runs its shards. The plan keys off the given target — exactly the set
// `magus affected <target>` would run — which is required (no default). Adaptive
// sharding is applied when runtime history is available.
func affectedPlan(ctx context.Context, root string, args []string) error {
	// --plan can sit anywhere (hasModeFlag routed us here); drop it so what's left
	// follows the normal `affected <target> [flags]` shape.
	var planless []string
	for _, a := range args {
		if a == "--plan" || a == "-plan" || strings.HasPrefix(a, "--plan=") || strings.HasPrefix(a, "-plan=") {
			continue
		}
		planless = append(planless, a)
	}

	// The anchor is the leading positional, exactly like a normal affected run, so
	// the plan reflects what `magus affected <target>` would run rather than a
	// hardcoded "ci". A target is required — magus favors explicitness, and a silent
	// default is the footgun this mode used to have (it ignored the target entirely).
	// Leading positionals after the target are project filters, the same grammar
	// `magus run <target> <projects>` already uses, so there is nothing new to learn, and
	// no flag invented for something the CLI already spells one way.
	var anchorArgs []string
	if i := slices.Index(planless, "--"); i >= 0 {
		planless, anchorArgs = planless[:i], planless[i+1:]
	}
	var target string
	var only []string
	flagArgs := planless
	if len(planless) > 0 && !strings.HasPrefix(planless[0], "-") {
		target = planless[0]
		flagArgs = planless[1:]
		for len(flagArgs) > 0 && !strings.HasPrefix(flagArgs[0], "-") {
			only = append(only, flagArgs[0])
			flagArgs = flagArgs[1:]
		}
	}
	if target == "" {
		return fmt.Errorf("magus affected --plan: a target is required (e.g. `%s`), run `%s` to list available targets",
			hint.Affected.With("ci", "--plan"), hint.DescribeTargets)
	}
	parsed, err := types.ParseTarget(canonicalTarget(target)) // expand short aliases at the CLI edge, mirroring `magus run`
	if err != nil {
		return exit.Usagef("magus affected --plan: %v", err)
	}
	target = parsed.Name

	var pf *flaggen.AffectedPlanFlags
	if _, err := commandParse(fmt.Sprintf("affected %s --plan", target), flagArgs, func(fs *flag.FlagSet) {
		pf = flaggen.BindAffectedPlan(fs, flaggen.AffectedPlanDefaults{
			MaxShards:         globalCfg.CI.MaxShards,
			MaxParallelBudget: globalCfg.CI.RunnerPoolBudget,
		})
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus affected <target> --plan [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Emit a provider-neutral JSON shard plan for the affected project set of")
			fmt.Fprintln(os.Stderr, "<target> (required, e.g. ci). Does NOT execute the pipeline; CI wrappers")
			fmt.Fprintln(os.Stderr, "(e.g. GitHub Actions) translate the matrix into their own format.")
			fmt.Fprintln(os.Stderr, "Adaptive sharding is always enabled; set MAGUS_HISTORY_PATH or history_path")
			fmt.Fprintln(os.Stderr, "in magus.yaml to override the history file location.")
			fmt.Fprintln(os.Stderr, "Use --stdin for a one-shot plan of proposed repo-relative paths before editing.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags:")
			printOwnDefaults(fs)
		}
	}); err != nil {
		return err
	}
	if pf.Stdin && pf.Base != "" {
		return fmt.Errorf("magus affected --plan: --stdin and --base are mutually exclusive")
	}
	if pf.Null && !pf.Stdin {
		return fmt.Errorf("magus affected --plan: --null requires --stdin")
	}
	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}

	planOpts := magus.PlanOptions{
		MaxShards:        pf.MaxShards,
		RunnerPoolBudget: pf.MaxParallelBudget,
		BaseRef:          pf.Base,
		Charms:           parsed.Charms,
		Args:             anchorArgs,
	}
	if pf.Stdin {
		planOpts.ChangedPaths, err = internalci.ReadAffectedPlanPaths(os.Stdin, pf.Null)
		if err != nil {
			return err
		}
	}
	// Before the plan reads the tree, which a stage upstream may still be writing.
	if err := awaitPipeline(ctx, root); err != nil {
		return err
	}
	plan, err := m.Plan(ctx, target, planOpts)
	if err != nil {
		return err
	}

	if len(only) > 0 {
		// Stage zero stays whole: the shards kept may still share its
		// prerequisites, and a stage-zero shard nobody needs only costs a runner.
		if plan.Shards, err = internalci.FilterShards(ctx, m, plan.Shards, only); err != nil {
			return err
		}
		// The concurrency ceiling describes the plan being emitted, not the one it was
		// filtered from. Left alone it advertised room for six parallel jobs in a plan
		// carrying two, which a CI provider reads as a promise about this matrix.
		if plan.MaxParallel > len(plan.Shards) {
			plan.MaxParallel = len(plan.Shards)
		}
	}

	// Verdict inheritance, only for a full ci plan: a filtered or hypothetical
	// (--stdin) plan describes a subset no green run vouches for. On a hit the
	// shards are emptied, so the workflow's count>0 guard skips the fan-out, and
	// the finding rides the output for the report job to publish.
	var inherit *internalci.InheritFinding
	if target == types.TargetCI && !pf.Stdin && len(only) == 0 {
		if inherit = planInheritance(ctx, m); inherit != nil {
			plan.StageZero, plan.Shards = nil, nil
			plan.MaxParallel = 0
			slog.InfoContext(ctx, "ci plan inherits a green run's verdict",
				slog.String("run", inherit.Run), slog.String("commit", inherit.Commit))
		}
	}
	// A change that tiers trivial against the plan's base needs no shard. Any other tier
	// keeps the whole matrix: a shard runs `magus run ci <projects>`, and narrowing one
	// waits on a multi-target run.
	var trivial *internalci.PlanRisk
	if target == types.TargetCI && !pf.Stdin && len(only) == 0 && inherit == nil {
		if rep, err := m.AssessChange(ctx, target, magus.AssessOptions{Base: pf.Base}); err == nil && rep.Tier == check.RiskTrivial {
			trivial = internalci.NewPlanRisk(rep)
			plan.StageZero, plan.Shards = nil, nil
			plan.MaxParallel = 0
			slog.InfoContext(ctx, "ci plan sized trivial", slog.String("base", rep.Base))
		}
	}

	totalProjects := 0
	for _, s := range plan.Shards {
		totalProjects += len(s.ProjectPaths)
	}
	slog.InfoContext(ctx, "affected plan computed",
		slog.String("target", target),
		slog.Int("projects", totalProjects),
		slog.Int("shards", len(plan.Shards)),
		slog.String("source", plan.Source),
		slog.String("forecast", globalCfg.HistoryPath))
	// Advice, once, where a person reads it. Both facts are about runner spend and
	// runner death, neither of which the shard table shows.
	if n := plan.Sufficient; n > 0 && n < len(plan.Shards) {
		slog.With(attr.Component("magus")).WarnContext(ctx, fmt.Sprintf(
			"%d shard(s) planned, %d finish just as fast; the longest single project bounds the makespan, and the rest each pay a runner's setup for nothing (cap with `--max-shards=%d`)",
			len(plan.Shards), n, n))
	}
	if len(plan.OverBudget) > 0 {
		slog.With(attr.Component("magus")).WarnContext(ctx, fmt.Sprintf(
			"shard(s) %s are predicted to exceed one runner's memory; a runner that runs out vanishes and reports \"cancelled\" with no diagnostics (a higher `--max-shards` splits them, unless one project exceeds the budget alone)",
			strings.Join(plan.OverBudget, ", ")))
	}
	if len(plan.Unmeasured) > 0 && len(plan.Shards) > 0 {
		slog.With(attr.Component("magus")).InfoContext(ctx, fmt.Sprintf(
			"%d of the plan's targets have no recorded duration, so the plan weighed them with the workspace fallback: %s",
			len(plan.Unmeasured), internalci.CappedUnits(plan.Unmeasured)), attr.Notice("note"))
	}
	if len(plan.Unreached) > 0 {
		slog.With(attr.Component("magus")).InfoContext(ctx, fmt.Sprintf(
			"%d of the affected projects' targets read no changed path, so the plan leaves them to the cache: %s",
			len(plan.Unreached), internalci.CappedUnits(plan.Unreached)), attr.Notice("note"))
	}

	if len(plan.Fallbacks) > 0 {
		lines := make([]string, len(plan.Fallbacks))
		for i, f := range plan.Fallbacks {
			lines[i] = f.String()
		}
		slog.With(attr.Component("magus")).WarnContext(ctx, "the plan fell back: "+strings.Join(lines, " "))
	}
	out := planOutput{
		Target:      target,
		Charms:      plan.Charms,
		Args:        plan.Args,
		Machine:     plan.Machine,
		Fallbacks:   plan.Fallbacks,
		Count:       len(plan.Shards),
		MaxParallel: plan.MaxParallel,
		Source:      plan.Source,
		Matrix:      make([]planShard, len(plan.Shards)),
		Affected:    plan.Affected,
		UnboundedBy: plan.UnboundedBy,
		Unmeasured:  plan.Unmeasured,
		Unreached:   plan.Unreached,
	}
	for i, s := range plan.Shards {
		out.Matrix[i] = internalci.NewPlanShard(s)
	}
	for _, s := range plan.StageZero {
		out.StageZero = append(out.StageZero, internalci.NewPlanShard(s))
	}
	if inherit != nil {
		out.Inherit = internalci.NewPlanInherit(inherit)
	}
	out.Risk = trivial
	if pf.Detail {
		out.Detail, err = internalci.PlanDetail(ctx, m, target, slices.Concat(plan.StageZero, plan.Shards))
		if err != nil {
			return err
		}
	}
	if out.Outputs, err = internalci.PlanOutputs(out); err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	return emitPlan(opts, out)
}

// emitPlan renders a --plan through the shared renderer like every other structured
// command, so -o selects the encoding. Marshaling here directly would ignore it and
// print JSON for `-o yaml`, in both flag positions.
//
// outputText maps to JSON rather than to a prose rendering, because the plan has
// no prose rendering to fall back on: the default output IS the machine-readable
// document, and a workflow that pipes `--plan` without -o must keep getting it.
func emitPlan(opts output.Options, out planOutput) error {
	switch opts.Format {
	case outputText, outputJSON:
		return emitFormatted(output.Options{Format: outputJSON}, out)
	case outputName:
		return emitItemNames(os.Stdout, out.Matrix, func(s planShard) string { return s.Shard })
	default:
		if opts.Format == outputTemplate {
			out.Summary = planSummaryMarkdown(out)
		}
		return emitFormatted(opts, out)
	}
}

// noteUndeclaredSeeds reports MGS1028: changed files that seeded a project through
// directory containment while that project declares none of them, so the targets they
// rerun were already correct.
//
// It lives at the CLI edge, not behind magus.Affected, because a library entry point
// must not write to a stream its caller never opted into; the condition rides
// types.AffectedResult.UndeclaredBySeed for every other consumer.
//
// The message names the SEED PROJECTS and nothing per-changeset, which is what makes
// it dedupe: interactive.Hint keys on the whole text, so a file list would differ on
// every request and churn a long-lived server's hint set instead of teaching once. The
// files are already on screen where this is emitted (--impact and --explain both mark
// each one), and `magus describe file` explains any of them in full.
func noteUndeclaredSeeds(ctx context.Context, undeclaredBySeed map[string][]string) {
	if len(undeclaredBySeed) == 0 {
		return
	}
	interactive.Hint(ctx, fmt.Sprintf("[%s] %s", string(types.UndeclaredSeedingFile), internalci.UndeclaredSeedNotice(undeclaredBySeed, false)))
}

// noteUndeclaredSeedCost reports MGS1028 on the run that PAYS for it: `magus affected
// <target>` selected these projects through undeclared files alone, and is about to
// rerun their targets for an answer that was already correct. It names the files
// because this reader has none on screen: unlike --impact and --explain, the run
// prints a project list and never the changeset.
//
// It rides interactive.Emit for the same reason the unchanged-failure line does: it is
// the one channel a -s run still bubbles up, and the dedupe there holds it to once per
// invocation. Nothing is skipped; the run proceeds exactly as it would have.
//
// The file list makes the text vary per changeset, so an adopted run in a long-lived
// server dedupes fewer of these than the project-only twin above. That is the trade for
// naming files the reader cannot see anywhere else, and interactive.maxEmittedDedupe
// bounds what it can cost.
func noteUndeclaredSeedCost(ctx context.Context, sink *magus.Sink, undeclaredOnly map[string][]string) {
	if len(undeclaredOnly) == 0 {
		return
	}
	sink.EmitNotice(ctx, slog.LevelWarn, types.UndeclaredSeedingFile, internalci.UndeclaredSeedNotice(undeclaredOnly, true))
}

// reportUndeclaredSeeds carries MGS1028 as one coded event per project, the shape the
// engine's own diagnostic sink emits, so a -o jsonl consumer counts the code and the
// unit instead of matching the notice's wording.
func reportUndeclaredSeeds(ctx context.Context, sink *magus.Sink, undeclaredOnly map[string][]string) {
	for _, seed := range slices.Sorted(maps.Keys(undeclaredOnly)) {
		sink.EmitDiagnostic(ctx, seed, types.UndeclaredSeedingFile,
			"seeded only by changed files no project declares: "+
				strings.Join(internalci.CappedList(undeclaredOnly[seed]), ", "))
	}
}

// affectedImpact reports the blast radius of the current changeset (the --impact
// forensic mode of `magus affected`). It is strictly read-only: it maps changed files
// to seed projects and expands the dependency-graph reverse closure to name the
// affected projects and their targets, then surfaces `magus affected ci` as the
// follow-up. It NEVER executes a target and takes no positional target or project.
func affectedImpact(ctx context.Context, root string, args []string) error {
	// --impact routed us here (hasModeFlag); bind it so the flag parser accepts it,
	// then parse --base like the other forensic modes. No positional target is read.
	var imf *flaggen.AffectedImpactFlags
	if _, err := commandParse("affected --impact", args, func(fs *flag.FlagSet) {
		imf = flaggen.BindAffectedImpact(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus affected --impact [--base <ref>]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Report the IMPACT of the current changeset: the changed files, the projects")
			fmt.Fprintln(os.Stderr, "that directly contain them (seeds), and the affected closure with each")
			fmt.Fprintln(os.Stderr, "project's targets. Read-only - it runs nothing.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags:")
			printOwnDefaults(fs)
		}
	}); err != nil {
		return err
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}

	out, err := impact.Compute(ctx, ws, imf.Base)
	if err != nil {
		return err
	}
	undeclared := map[string][]string{}
	for _, p := range out.AffectedProjects {
		if len(p.UndeclaredFiles) > 0 {
			undeclared[p.Path] = p.UndeclaredFiles
		}
	}
	noteUndeclaredSeeds(ctx, undeclared)

	// Enrich with the differentiated overlays (changed-symbol callers, coverage on
	// changed code). These read the heavier knowledge store (a prior symbol index and,
	// for coverage, a prior `magus run coverage`), not the lean workspace handle Compute
	// runs on, so load the graph (with the lazily-merged @symbols/@coverage shards) here
	// and hand it to the enrichment step. Best-effort: a graph that fails to load leaves
	// the blast radius intact and records a Note rather than failing the whole report.
	if g, gerr := loadKnowledgeGraph(ctx, root, false /*refresh*/, false /*global*/, true /*includeSymbols*/); gerr == nil {
		impact.Enrich(out, impact.GraphStore(g))
	} else {
		out.Notes = append(out.Notes, "changed-symbol and coverage overlays skipped: "+gerr.Error())
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		names := make([]string, 0, len(out.AffectedProjects))
		for _, p := range out.AffectedProjects {
			names = append(names, p.Path)
		}
		return emitNames(os.Stdout, names)
	}

	return internalci.PrintImpactText(os.Stdout, out, func(seed string) (string, string) {
		link := liveExplorerLink(graphLinkOpts{View: "blast", Node: fmt.Sprintf("%s:%s", types.KindProject, seed)})
		return link, authHint(link)
	})
}

// parseExplainArgs scans args for --explain[=<project>] and optionally --base.
// Returns (project, base, true) when --explain is present; otherwise ("", "", false).
func parseExplainArgs(args []string) (project, base string, ok bool) {
	for i, a := range args {
		switch {
		case isFlagNamed(a, "explain"):
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				project = args[i+1]
			}
			ok = true
		case parseFlagValue(a, "explain") != "":
			project = parseFlagValue(a, "explain")
			ok = true
		case isFlagNamed(a, "base") && i+1 < len(args):
			base = args[i+1]
		case parseFlagValue(a, "base") != "":
			base = parseFlagValue(a, "base")
		}
	}
	return project, base, ok
}

func affectedExplain(ctx context.Context, root, target, base string) error {
	if target == "" {
		return fmt.Errorf("magus affected --explain: project path required")
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}

	r, err := ws.Affected(ctx, base)
	if err != nil {
		return err
	}
	noteUndeclaredSeeds(ctx, r.UndeclaredBySeed)

	g, err := ws.Graph()
	if err != nil {
		return err
	}

	out := internalci.AffectedExplainOutput{Project: target, Base: r.Base}
	for _, a := range r.Affected {
		if a == target {
			out.Affected = true
			break
		}
	}

	if out.Affected {
		paths := g.PathsFromSeeds(r.Seed, target)
		for _, ap := range paths {
			out.Paths = append(out.Paths, internalci.AffectedExplainPath{
				Seed:       ap.Seed,
				Chain:      ap.Chain,
				Files:      r.FilesBySeed[ap.Seed],
				Undeclared: r.UndeclaredBySeed[ap.Seed],
			})
		}
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		// One name or none, which emitNames renders as a line or an empty file. The
		// empty file is the point: `-o name` piped into xargs should see nothing rather
		// than a blank line.
		if !out.Affected {
			return emitNames(os.Stdout, nil)
		}
		return emitNames(os.Stdout, []string{out.Project})
	}

	// text and wide
	if !internalci.PrintAffectedExplainText(os.Stdout, out) {
		return nil
	}

	if res, err := vcs.Resolve(ctx, ws.Root(), "", ws.VCSOptions()); err == nil && res.VCS != nil {
		if hints, err := res.VCS.DiffCommands(ctx, ws.Root(), out.Base); err == nil {
			fmt.Printf("\nTo inspect these changes:\n")
			fmt.Printf("  %s\n", hints.CLI)
			if hints.GUI != "" {
				fmt.Printf("  %s\n", hints.GUI)
			}
		}
	}
	return nil
}
