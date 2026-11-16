package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	internalci "github.com/egladman/magus/internal/ci"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/types"
)

// runTarget dispatches `magus run <target> [projects...]`.
func runTarget(ctx context.Context, root string, _ runConfig, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		return targetUsage()
	}

	// Find the target even if global flags precede it (`magus run --dry-run build`);
	// stdlib flag would otherwise treat the flag as the target. rest carries the hoisted
	// flags + any project args for commandParse below. The prescan binds run's own flags too,
	// throwing the values away: it needs to know which of them consume the next token.
	var skips skipFlag
	prescanRun := func(fs *flag.FlagSet) { bindRunFlags(fs, nil) }
	// Read first: a plan run may name no target, leaving the plan to supply it.
	var saved *internalci.PlanOutput
	if runReadsPlan(args) {
		// A read from a terminal would wait for input nobody knows to type.
		if termkit.StdinIsTerminal() {
			return internalci.RefusePlan("--stdin reads a saved plan, and stdin is a terminal; pipe one in (`%s | magus run --stdin`) or redirect a file (< plan.json)",
				hint.Affected.With("ci", "--plan"))
		}
		p, err := internalci.ReadSavedPlan(os.Stdin)
		// A stage upstream that failed printed no plan, so its failure is the one to report.
		if werr := awaitPipeline(ctx, root); werr != nil {
			return werr
		}
		if err != nil {
			return err
		}
		saved = &p
	}
	rawTarget, rest, ok := splitTargetFromArgs(args, prescanRun)
	if !ok && saved != nil {
		args = append([]string{internalci.SavedTargetRef(*saved)}, args...)
		rawTarget, rest, ok = splitTargetFromArgs(args, prescanRun)
	}
	if !ok {
		return targetUsage()
	}
	spellFilter, targetStr := parseTarget(rawTarget)
	parsedTarget, parseErr := types.ParseTarget(targetStr)
	hintCanonicalSpelling(ctx, parsedTarget)
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
	var rf *flaggen.RunFlags
	var shardEnvErr error
	projectArgs, err := commandParse("run "+targetName, flagArgs, func(fs *flag.FlagSet) {
		rf = bindRunFlags(fs, &skips)
		// The shard pair defaults from the environment CI sets, so a matrix job
		// need not repeat itself on every magus call. Applied by seeding the bound
		// value (an explicit flag still wins, since parsing runs after this) and
		// mirroring it into DefValue so -h reports what the flag will actually do.
		for _, seed := range []struct{ flag, env string }{
			{flaggen.FlagRunShard, "MAGUS_SHARD"},
			{flaggen.FlagRunNShards, "MAGUS_N_SHARDS"},
		} {
			if err := envDefault(fs, seed.flag, os.Getenv(seed.env)); err != nil {
				shardEnvErr = fmt.Errorf("%s: %w", seed.env, err)
				break
			}
		}
		fs.Usage = func() {
			fmt.Fprintf(os.Stderr, "Usage: magus run %s [flags] [project...] [-- <extra args>]\n", rawTarget)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintf(os.Stderr, "Run target %s for the selected projects.\n", targetName)
			if spellFilter != "" {
				fmt.Fprintf(os.Stderr, "Spell filter: only the %q spell runs.\n", spellFilter)
			}
			fmt.Fprintln(os.Stderr, "With no project args: all projects (or cwd-scoped if inside one).")
			fmt.Fprintln(os.Stderr, "Extra args after -- are forwarded to spells that honor them.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	// Refused, never ignored: the shard pair decides how much of the selection runs, so
	// a value magus could not read means this job would gate less than the caller asked
	// for while still exiting 0.
	if shardEnvErr != nil {
		return exit.Usagef("magus run: %v", shardEnvErr)
	}
	var planShards []internalci.PlanShard
	if saved != nil {
		switch {
		case rf.Detach:
			return exit.Usagef("magus run: a plan on --stdin runs here; it does not apply to --detach")
		case rf.Graph:
			return exit.Usagef("magus run: a plan on --stdin runs its shards; it does not apply to --graph")
		case len(projectArgs) > 0 || parsedTarget.Path != "":
			return exit.Usagef("magus run: the plan on --stdin names the projects; drop the project arguments")
		case len(skips.refs) > 0:
			return exit.Usagef("magus run: the plan on --stdin names the projects; --skip does not apply")
		case !slices.Equal(extraArgs, saved.Args):
			return internalci.RefusePlan("the plan was evaluated with args %q, not %q; plan again with the args the shard runs",
				strings.Join(saved.Args, " "), strings.Join(extraArgs, " "))
		case !internalci.SameCharms(parsedTarget.Charms, saved.Charms):
			return internalci.RefusePlan("the plan was evaluated under charms %q, not %q; plan again under the charms the shard runs",
				strings.Join(saved.Charms, ","), strings.Join(parsedTarget.Charms, ","))
		}
		shards, err := internalci.SelectPlanShards(*saved, targetName, rf.Shard, rf.NShards)
		if err != nil {
			return err
		}
		// The args chose the anchor's steps; ctx.needs hands a step none.
		extraArgs = nil
		// Both halves of the shard label now come from the plan, and a run of every
		// shard is not partial.
		rf.NShards = 0
		if rf.Shard == internalci.AllPlanShards {
			rf.Shard = ""
		}
		if rf.Shard != "" {
			rf.NShards = len(internalci.PlanStage(*saved, rf.Shard))
		}
		if globalCfg.DryRun {
			return emitSavedPlanDryRun(ctx, *saved, rawTarget, shards)
		}
		planShards = shards
		projectArgs = internalci.PlanProjects(shards)
		if len(projectArgs) == 0 {
			slog.InfoContext(ctx, "magus run --stdin: the plan has no shards, so nothing runs", slog.String("target", targetName))
			return nil
		}
	}
	if len(skips.refs) > 0 && rf.Graph {
		// Refused rather than ignored: --graph renders from the raw roots, so honoring
		// the flag there would take a second subtraction, and accepting it silently is
		// the un-gated skip this flag exists to prevent.
		return exit.Usagef("magus run: --skip applies to the run selection, not --graph")
	}
	// After the usage checks, so a mistyped flag fails here rather than in a log nobody
	// is reading; before the timeout, which the detached run applies to itself.
	if rf.Detach {
		return detachRun(ctx)
	}
	if rf.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = withTimeout(ctx, rf.Timeout, "run:"+targetName)
		defer cancel()
	}
	// -s deliberately suppresses the usual target progress. That is useful to an
	// agent, but a person otherwise has no positive signal that a slow invocation
	// is alive. Say it once, before any potentially long workspace load, and point
	// at the existing observer rather than inventing another progress display.
	// Only when a person is watching. The liveness signal is the whole point, and a
	// caller reading a captured transcript gets the finished result instead: Emit dedupes
	// within a process, so one invocation per tool call repeats it forever, and the line
	// measured 48KB across four agent sessions that could not act on any of it.
	// A structured invocation never gets this: -o jsonl on a TTY is rare, and the
	// one free-text line it would add is not worth a special-cased typed event.
	if global.silent && isInteractiveTTY() && global.output != string(outputJSONL) {
		// Says what arrives and when, rather than sending the reader to poll a
		// dashboard in another terminal. This run BLOCKS: waiting for it is the
		// normal thing to do, and every target that runs prints an output ref
		// on the way past, so there is already an exact handle for anything
		// worth reading afterwards.
		interactive.Hint(ctx, fmt.Sprintf(
			"running quietly; targets print an output ref as they finish, and `%s` reads one",
			hint.QueryOutput.With("<ref>")))
	}

	if rf.Step && !isInteractiveTTY() {
		slog.ErrorContext(ctx, "--step requires an interactive terminal", attr.Notice(""), attr.Component("magus"))
		return exit.Reported(exit.Usage)
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
		targets, source, err := interactive.ResolveTargets(ctx, ws, listTarget, interactive.RunSelection{Projects: projectArgs, Skips: skips.refs, Cwd: clientCurrentWorkingDirectory(ctx)})
		if err != nil {
			return err
		}
		listTargets("run:ls", targets, source)
		return nil
	}

	// finalizeConfig already pointed globalCfg.Log.Format at "jsonl" for this
	// invocation (before the workspace preload could build the cache logger from
	// the old format); resolved again here to choose the invocation's one sink.
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

	// cwd is the caller's directory: for an adopted run it is the client's, carried on
	// ctx, not the server's process cwd. It scopes target resolution below and is recorded
	// on the invocation's journal, so both agree with where the user actually ran.
	cwd := clientCurrentWorkingDirectory(ctx)
	sel := interactive.RunSelection{Projects: projectArgs, Skips: skips.refs, Cwd: cwd}
	if saved != nil {
		// A plan's paths are workspace-relative; anchored at the caller's cwd, "." in a
		// subdirectory would name the wrong project.
		sel.Cwd = m.Root()
	}
	inherited := false
	// Projects flow forward: named projects win, and a run naming none takes what the
	// record stage upstream of it ran on, the way xargs takes its arguments.
	if len(projectArgs) == 0 && parsedTarget.Path == "" {
		projects, upstream, ok, err := inheritedProjects(ctx)
		if err != nil {
			return err
		}
		if ok && len(projects) == 0 {
			sink.EmitNotice(ctx, slog.LevelInfo, "", fmt.Sprintf(
				"pid %d, upstream of this run in a pipe, reported no projects, so this run ran nothing", upstream))
			return nil
		}
		if ok {
			// Workspace paths, so anchored at the root rather than the caller's cwd.
			sel.Projects, sel.Cwd, inherited = projects, m.Root(), true
		}
	}
	targets, source, err := interactive.ResolveTargets(ctx, m, parsedTarget, sel)
	if err != nil {
		return err
	}
	if inherited {
		source = "pipe"
	}
	if saved != nil {
		targets, source = internalci.PlanTargets(planShards), "plan"
	}
	// Fault tolerance by design: a target only some projects serve should skip (not
	// error) the projects that lack it when the scope is the workspace or several
	// projects, but a single project that does not serve it (or a name no project serves
	// at all) is an error. This is the run counterpart to affected's tolerance.
	if len(targets) > 0 && saved == nil {
		targets, err = interactive.FilterServedTargets(ctx, m, targets, targetName)
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
	sink.EmitScope(ctx, scopeLabel, source, scopePaths(targets))
	// Surface the active charms up front, next to the projects header, so the run's
	// state ("here's what's in effect") is visible before any work, and so a missing
	// default charm (e.g. rw not applied) is obvious rather than silent.
	charms := withDefaultCharms(parsedTarget.Charms, globalCfg.DefaultCharms, !rf.DefaultCharms)
	// RunCI verifies without the write-granting charms and repairs with rw, so
	// it gets the carried set while the header names what each pass verifies
	// under.
	carried := charms
	if targetName == "ci" {
		charms = magus.CharmsForCI(charms)
	}
	sink.EmitCharms(ctx, strings.Join(charms, ","))
	tier, mode, dir := m.CacheDescription()
	sink.EmitCache(ctx, tier, mode, dir)
	if len(targets) == 0 {
		// Zero targets here means the fan-out found no projects at all in the resolved
		// workspace: a degenerate or wrong-workspace resolution, not "nothing to do".
		// (A named target no project serves already errored in filterServedTargets above.)
		// An adopted run that mis-resolved the client's workspace lands here; fail loudly,
		// naming the workspace and target, so it can never pass with nothing executed.
		// m.Root() is the resolved root (the root arg is "" for a local run that walked up).
		return fmt.Errorf("run: workspace %s has no projects to run target %q", m.Root(), targetName)
	}

	// The redundancy check: a ci gate this branch already passed with equivalent
	// inputs refuses under load (MGS3010, exit 75) and advises when the machine
	// is idle. Inert off the ci target and when nothing is on record.
	partial := rf.Shard != "" || rf.NShards > 0
	gate := prepareGateRedundancy(ctx, m, targetName, targets, charms, partial)
	if gateErr := gate.evaluate(ctx, !rf.RedundancyCheck); gateErr != nil {
		return gateErr
	}

	var runOpts []magus.RunOption
	if globalCfg.DryRun {
		runOpts = append(runOpts, magus.WithDryRun())
	}
	if isGateInvocation(targetName, partial) {
		runOpts = append(runOpts, magus.WithGate())
	}
	if len(carried) > 0 {
		runOpts = append(runOpts, magus.WithCharms(carried...))
	}
	runOpts = append(runOpts, magus.WithVolatilityRetry(rf.VolatilityRetry))
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
	runOpts = append(runOpts, magus.WithCache(rf.Cache))
	runOpts = append(runOpts, magus.WithSink(sink))
	ctx = processStdioContext(ctx)
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
	// An adopted run (dispatched by the server) also feeds the server's live-run registry,
	// carried on ctx; a plain CLI run has no sink, so this is empty there.
	captureHandlers := append(liveHandlers(liveBC), console.RunSinkHandlers(ctx)...)
	// Durable invocation facts ride the same fan-out: one fact per target result, into a
	// store every worktree of this repo shares (`magus session` reads it back).
	captureHandlers = withInvocationJournal(ctx, captureHandlers, m.Root(), "run", args)
	refs := &report.ResultRefs{}
	statuses := report.NewResultStatuses(targetName)
	failed := &failedResults{}
	captureHandlers = append(captureHandlers, refs, statuses, failed)
	invCtx, endInvocation := m.BeginInvocation(ctx, magus.Command{
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
	// See affected.go: the cut-short question is read here, not inside record.
	gate.record(invCtx, err, invCtx.Err() != nil, failed.list())
	if rf.Timeout > 0 && errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("run %s: timed out after %s", targetName, rf.Timeout)
	}
	if rf.Shard != "" && rf.NShards > 0 {
		sink.EmitShardTotal(ctx, rf.Shard, rf.NShards, time.Since(startedAt))
	}
	if reportedRunError(err) {
		return exit.Reported(exit.Failure)
	}
	if err != nil {
		return err
	}
	if slices.Contains(charms, types.CharmReadWrite) {
		loc, placeErr := jobPlace(m.Root())
		lease, outside, readErr := "", []string(nil), placeErr
		if placeErr == nil {
			lease, outside, readErr = job.LeaseRewrites(ctx, loc, targetName, m.ResolveProjects(targets))
		}
		switch {
		case readErr != nil:
			slog.InfoContext(ctx, "what this run rewrote outside its lease was not read", attr.Error(readErr), attr.Notice("note"))
		case len(outside) > 0:
			slog.InfoContext(ctx, fmt.Sprintf("this run rewrote declared outputs outside lease %s's write paths, and a commit carrying one is refused: unstage it:\n  %s",
				lease, strings.Join(outside, "\n  ")), attr.Notice("note"))
		}
	}
	emitConcurrencyNudge(ctx, sink, m, os.Args[1:])
	if global.silent && opts.Format == outputText {
		if line := report.RefLine(refs.List()); line != "" {
			slog.InfoContext(ctx, line, attr.Notice(""), attr.Result())
		}
	}

	sink.RecordValues(targetName, readReturns(targetName))
	switch opts.Format {
	case outputJSON, outputYAML, outputTemplate:
		// No undeclared seeds: `magus run` names its own selection, so no changed file seeded it.
		return emitRunResult(ctx, m, opts, targetName, charms, targets, readReturns(targetName), statuses, nil)
	case outputName:
		return emitProjectNames(m, targets)
	}
	return nil
}

// runSelection is interactive.RunSelection under the lowercase field names clean and list
// still spell.
type runSelection struct {
	projects []string
	skips    []string
	cwd      string
}

func resolveTargets(ctx context.Context, ws types.WorkspaceRepository, t types.Target, sel runSelection) ([]types.Target, string, error) {
	return interactive.ResolveTargets(ctx, ws, t, interactive.RunSelection{Projects: sel.projects, Skips: sel.skips, Cwd: sel.cwd})
}

func buildDefinesTarget(ctx context.Context, m *magus.Magus) func(path, target string) bool {
	return interactive.DefinesTarget(ctx, m)
}

type resultStatuses = report.ResultStatuses

func newResultStatuses(target string) *resultStatuses { return report.NewResultStatuses(target) }

// emitProjectNames is `-o name` for a run: the projects the run covered, one path per
// line: the identity of what executed, and the form the next command takes as arguments.
// Shared by run and affected so the two cannot disagree about what a run is named by.
func emitProjectNames(m *magus.Magus, selection []types.Target) error {
	projects := m.ResolveProjects(selection)
	names := make([]string, len(projects))
	for i, p := range projects {
		names[i] = p.Path
	}
	return emitNames(os.Stdout, names)
}

// bindRunFlags registers `magus run`'s flags: the generated set from the command
// registry, plus --skip by hand. --skip is repeatable, a shape the registry declares
// (FlagCustom) for the man page and binds nothing, exactly like watch's --ignore. The
// usage string is the registry's Doc verbatim, so `-h` and the man page cannot drift.
//
// A nil skips binds the flag to a throwaway accumulator, which is what the prescan wants:
// it has to know that --skip CONSUMES its next token, and has no use for the values.
func bindRunFlags(fs *flag.FlagSet, skips *skipFlag) *flaggen.RunFlags {
	rf := flaggen.BindRun(fs)
	if skips == nil {
		skips = &skipFlag{}
	}
	fs.Var(skips, flaggen.FlagRunSkip, "Exclude projects from the selection; repeatable or comma-separated. Takes project references like positionals, or a doublestar glob over project paths (libs/*); a value matching nothing is an error")
	return rf
}

// runReadsPlan reports whether run's raw args (after the verb) ask for a saved plan on
// stdin. It reads the args the way run's parser will, so a value flag's value, a
// forwarded arg after --, or a project named stdin is not the flag. A malformed --stdin
// value reads as false here; the parser reports it.
func runReadsPlan(args []string) bool {
	fs := flag.NewFlagSet("prescan", flag.ContinueOnError)
	flaggen.BindFlags(fs, &globalCfg)
	bindDisplayFlags(fs)
	bindRunFlags(fs, nil)
	flags, _ := partitionFlags(fs, args)
	stdin := false
	for i := 0; i < len(flags); i++ {
		name, value, hasValue := strings.Cut(strings.TrimLeft(flags[i], "-"), "=")
		f := fs.Lookup(name)
		switch {
		case f == nil:
		case name == flaggen.FlagRunStdin:
			stdin = !hasValue
			if hasValue {
				stdin, _ = strconv.ParseBool(value)
			}
		case !flagIsBool(f) && !hasValue:
			i++ // partitionFlags kept the flag's value beside it
		}
	}
	return stdin
}

// emitSavedPlanDryRun answers `run --stdin --dry-run`, which runs nothing. Structured -o
// renders the plan document as read, which is how a saved plan renders again without
// being computed again; text names each shard's command.
func emitSavedPlanDryRun(ctx context.Context, p internalci.PlanOutput, target string, shards []internalci.PlanShard) error {
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputText:
		if len(shards) == 0 {
			slog.InfoContext(ctx, "[dry] the plan has no shards; nothing would run", attr.Notice(""))
		}
		for _, s := range shards {
			slog.InfoContext(ctx, fmt.Sprintf("[dry] shard %s: %s runs %s", s.Shard, target, internalci.PlanUnitList(s.Units)), attr.Notice(""))
		}
		return nil
	case outputName:
		return emitNames(os.Stdout, internalci.PlanProjects(shards))
	default:
		if opts.Format == outputTemplate {
			p.Summary = internalci.PlanSummaryMarkdown(p)
		}
		return emitFormatted(opts, p)
	}
}

// skipFlag accumulates repeated --skip values; satisfies flag.Value.
type skipFlag struct {
	refs []string
}

func (f *skipFlag) String() string { return strings.Join(f.refs, ",") }

func (f *skipFlag) Set(value string) error {
	// Both spellings: repeated --skip flags and one comma-separated value. An empty
	// segment (a bare or trailing comma) is refused rather than dropped, so a typo
	// cannot shrink the skip set silently.
	for _, ref := range strings.Split(value, ",") {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return errors.New("empty project reference")
		}
		f.refs = append(f.refs, ref)
	}
	return nil
}

// clientCurrentWorkingDirectory returns the working directory that scopes this invocation. An adopted
// run is dispatched inside the long-lived server, whose own os.Getwd() is unrelated
// to (and in a stale server may be a deleted) directory; the server carries the
// client's cwd on ctx (proc.CurrentWorkingDirectoryFromContext), so prefer it. A plain local run has no
// ctx cwd and falls back to os.Getwd(). Empty only when neither is available.
func clientCurrentWorkingDirectory(ctx context.Context) string {
	if c := proc.CurrentWorkingDirectoryFromContext(ctx); c != "" {
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
	fmt.Fprintf(os.Stderr, "To see what a project can run: `%s`\n", hint.LsTargets.With("[project]"))
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Conventional lifecycle names (each is an exported magusfile function, e.g.")
	fmt.Fprintln(os.Stderr, hint.TargetExample+"):")
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

// emitRunResult renders what a run produced. It is deliberately NOT a second
// progress stream: -o jsonl already streams events, and this answers the different
// question of what exists on disk now that the run is done.
//
// A dry run reports no artifacts: nothing executed, so any file matching an output
// glob is left over from a previous run and reporting it would claim this invocation
// produced it. It DOES report the return value, because a dry run evaluates the
// target body under a tracing context: the value is real, and omitting it made
// `-o json` claim "no return value" for a target that had just produced one.
func emitRunResult(ctx context.Context, m *magus.Magus, opts output.Options, target string, charms []string, selection []types.Target, returns types.Returns, statuses *report.ResultStatuses, undeclaredSeeds []string) error {
	out := report.RunOutput{Target: target, Charms: charms, DryRun: globalCfg.DryRun, Permitted: m.CacheStats().Permitted, UndeclaredSeeds: undeclaredSeeds}
	projects := m.ResolveProjects(selection)
	out.Count = len(projects)

	// Artifacts are resolved once for the whole set, then bucketed by owning
	// project, so the expansion walks each project's globs exactly once.
	byProject := map[string][]report.RunArtifact{}
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
		// artifacts) with a workspace walk per pair and (worse) got the nil case
		// backwards: an artifact no project's tree claimed failed the
		// `owner.Path != p.Path` guard for every project, so it was reported N times
		// in an N-project result. The declaring project is already on the artifact and
		// cannot come back nil.
		for _, a := range artifacts {
			byProject[a.ProjectPath] = append(byProject[a.ProjectPath], report.RunArtifact{Path: a.Path, Glob: a.Glob, Role: roleOf[a.Path]})
		}
	}

	for _, p := range projects {
		out.Projects = append(out.Projects, report.RunProject{
			Path:      p.Path,
			Status:    statuses.Of(p.Path),
			Artifacts: byProject[p.Path],
			Value:     returns[p.Path],
		})
	}
	return emitFormatted(opts, out)
}

// envDefault seeds a bound flag from an environment variable, leaving it alone when
// the variable is unset. DefValue is updated too, so `-h` shows the value the flag
// would take rather than the registry's zero.
//
// A value the flag cannot parse is REPORTED rather than applied. Dropping it left
// the flag at its default with nothing said, and for the shard pair that default is
// dangerous: NShards 0 means "do not shard", so a matrix job with a malformed
// MAGUS_N_SHARDS ran the entire selection in every job and reported success.
// Returning it keeps the decision with the caller: an unknown flag is still a
// silent no-op, because that is a wiring mistake and not something a user set.
func envDefault(fs *flag.FlagSet, name, value string) error {
	if value == "" {
		return nil
	}
	f := fs.Lookup(name)
	if f == nil {
		return nil
	}
	if err := f.Value.Set(value); err != nil {
		return fmt.Errorf("%q is not a valid --%s: %w", value, name, err)
	}
	f.DefValue = value
	return nil
}
