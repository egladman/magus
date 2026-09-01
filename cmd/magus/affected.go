package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/graph/url"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/project/impact"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// affected dispatches `magus affected <target>`; project set is determined by VCS diff.
func affected(ctx context.Context, root string, _ runConfig, args []string) error {
	// Kept before anything reshapes them: --detach re-submits this invocation verbatim.
	origArgs := args
	// Same grammar as `magus run`: the chain is split off the RAW args, before
	// anything partitions or reorders them. affected is the CI-facing twin, and CI
	// is exactly where "what did this produce" needs answering.
	args, chainArgs, chained := splitOnThen(args)

	// Parsed before the run, for the same reason `magus run` does: a typo'd verb must
	// not cost a full CI pipeline before it is rejected.
	var chain chainPlan
	if chained {
		var proceed bool
		var chainErr error
		if chain, proceed, chainErr = prepareChain(chainArgs); chainErr != nil || !proceed {
			return chainErr
		}
	}

	// Bare `magus affected` (no target) is a usage error, not a help request: a target
	// is required. Print a clear one-liner plus usage and exit non-zero, never silently.
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "magus affected: a target is required (e.g. `"+hint.Affected.With("ci")+"`)")
		fmt.Fprintln(os.Stderr, "")
		affectedUsage()
		return errSilent{exitCode: 2}
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
	// mirrors `magus run`. rest carries the hoisted flags for cmdParse below.
	rawTarget, rest, ok := splitTargetFromArgs(args, nil)
	if !ok {
		affectedUsage()
		return flag.ErrHelp
	}
	spellFilter, targetStr := parseTarget(rawTarget)
	parsed, perr := types.ParseTarget(targetStr)
	hintCanonicalSpelling(parsed)
	if perr != nil {
		return perr
	}
	target := canonicalTarget(parsed.Name) // expand short aliases at the CLI edge, mirroring `magus run`

	// Split on "--" before flag parsing so passthrough args aren't consumed by flag.
	flagArgs, extraArgs := splitOnDashDash(rest)

	var af *gen.AffectedFlags
	_, err := cmdParse("affected "+target, flagArgs, func(fs *flag.FlagSet) {
		af = gen.BindAffected(fs)
		fs.Usage = func() {
			fmt.Fprintf(os.Stderr, "Usage: magus affected %s [flags] [-- <extra args>]\n", target)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Run target "+target+" for every project affected by VCS changes.")
			fmt.Fprintln(os.Stderr, "Extra args after -- are forwarded to spells that honor them.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if af.Wait && !af.Detach {
		return usagef("magus affected: --wait applies to --detach; a plain run already blocks until it finishes")
	}
	if af.Detach {
		return detachToDaemon(ctx, root, append([]string{"affected"}, withoutDetachFlag(origArgs)...), af.Wait)
	}

	if af.Step && af.Stdin {
		return fmt.Errorf("magus affected: --step and --stdin are mutually exclusive")
	}
	if af.Step && !isInteractiveTTY() {
		fmt.Fprintln(os.Stderr, "magus: --step requires an interactive terminal")
		return errSilent{exitCode: 2}
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
		m, err := loadMagus(ctx, root)
		if err != nil {
			return err
		}
		streamCtx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		var streamOpts []magus.StreamOption
		if globalCfg.DryRun {
			streamOpts = append(streamOpts, magus.WithStreamDryRun())
		}
		if af.Null {
			streamOpts = append(streamOpts, magus.WithStreamNull())
		}
		if len(extraArgs) > 0 {
			streamOpts = append(streamOpts, magus.WithStreamExtraArgs(extraArgs))
		}
		return m.Stream(streamCtx, os.Stdin, target, func(err error) {
			slog.ErrorContext(streamCtx, "affected --stdin", slog.String("error", err.Error()))
		}, streamOpts...)
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
		return nil
	}

	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	targets, source, _, err := m.ExpandAffected(ctx, target, af.Base)
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
	// input that decides what runs - the same command against a different base is a
	// different build - and burying it in parentheses after a project list made it the
	// one header fact nobody read. source already names the VCS that produced it
	// ("git diff vs origin/main"), which is what distinguishes a git base from a jj one.
	m.LogScope(ctx, scopeLabel, "")
	m.LogBase(ctx, source, "")
	// Merge magus.yaml default_charms with any explicit charm on the target - the same
	// as `magus run` does. Previously `affected` used only the explicit charms, so
	// default_charms (e.g. rw) silently did NOT apply to `affected`, unlike `run`.
	charms := withDefaultCharms(parsed.Charms, globalCfg.DefaultCharms, af.NoDefaultCharms)
	// Same as `magus run`: an affected ci run goes through RunCI and loses the write
	// charms, so the header has to say so.
	if target == "ci" {
		charms = magus.CharmsForCI(charms)
	}
	m.LogCharms(ctx, strings.Join(charms, ","))
	m.LogCache(ctx)
	if len(targets) == 0 {
		slog.InfoContext(ctx, "affected: no projects affected", slog.String("target", target))
		return nil
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
	if af.NoCache {
		runOpts = append(runOpts, magus.WithNoCache())
	}
	if rw != nil {
		runOpts = append(runOpts, magus.WithReport(rw))
	}
	if len(extraArgs) > 0 {
		runOpts = append(runOpts, magus.WithExtraArgs(extraArgs))
	}
	if len(charms) > 0 {
		runOpts = append(runOpts, magus.WithCharms(charms...))
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
	// The client's cwd (carried on ctx for an adopted affected run), not the daemon's
	// process cwd, so the invocation's journal records where the user actually ran.
	cwd := clientCwd(ctx)
	liveBC, stopLive := beginLive(ctx, af.Open)
	defer stopLive()
	// An adopted affected run (dispatched by the daemon) also feeds the daemon's live-run
	// registry, carried on ctx; a plain CLI run has no sink, so this is empty there.
	captureHandlers := append(liveHandlers(liveBC), console.RunSinkHandlers(ctx)...)
	// Durable session facts ride the same fan-out here as on the run path: one fact per
	// target result, into the store every worktree of this repo shares. Without it
	// `magus session` would show a repository where only `magus run` ever happened,
	// and CI runs through this path.
	captureHandlers = withSessionJournal(ctx, captureHandlers, m.Root(), "affected", args)
	invCtx, endInvocation := m.BeginInvocation(ctx, journal.Command{
		Arguments: append([]string{"affected"}, args...), Cwd: cwd, Trigger: trigger,
	}, version, captureHandlers...)
	defer func() { endInvocation(err) }()

	invCtx, readReturns := types.WithReturnCapture(invCtx)
	if target == "ci" {
		err = m.RunCI(invCtx, targets, runOpts...)
	} else {
		err = m.Run(invCtx, targets, runOpts...)
	}
	if af.Timeout > 0 && errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("affected %s: timed out after %s", target, af.Timeout)
	}
	if reportedRunErr(err) {
		return errSilent{exitCode: 1}
	}
	if err != nil {
		return err
	}

	if chained {
		return runChain(ctx, m, opts, target, targets, chain, readReturns(target))
	}
	switch opts.Format {
	case outputJSON, outputYAML, outputTemplate:
		return emitRunResult(ctx, m, opts, target, charms, targets, readReturns(target))
	case outputName:
		return emitProjectNames(m, targets)
	}
	return nil
}

func affectedUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus affected <target> [flags]")
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

// planOutput is the provider-neutral JSON shape from `magus affected --plan`.
type planOutput struct {
	Count       int         `json:"count"`
	MaxParallel int         `json:"max_parallel"`
	Source      string      `json:"source"`
	Matrix      []planShard `json:"matrix"`
	// Detail is keyed by shard id and present only under --detail.
	//
	// A SIBLING of Matrix rather than fields on its entries, and that is a hard
	// constraint rather than a preference: the matrix is consumed as a GitHub Actions
	// job matrix (`fromJSON(needs.plan.outputs.matrix)`), where every key in an entry
	// becomes a job DIMENSION. Adding spells or write globs there would multiply the
	// job count or fail the workflow outright, so the detail hangs beside it and
	// the matrix keeps the exact two keys the workflow dereferences.
	Detail map[string]shardDetail `json:"detail,omitempty"`
}

type planShard struct {
	Shard    string `json:"shard"    yaml:"shard"`
	Projects string `json:"projects" yaml:"projects"`
}

// shardDetail is what each shard actually DOES: the invocation, what it runs, and what it
// writes. The plan has always known which projects may run concurrently - the hard half -
// while saying nothing about their content, leaving any reader to infer it from paths.
//
// Every field is JOINED from declarations magus already holds; none of it is new analysis.
// It is plain plan metadata and reads that way for a person, which is why only the one
// genuinely agent-shaped part is nested under Agents rather than spread through it.
type shardDetail struct {
	// Command is the invocation this shard is, spelled the way a person would run it.
	Command string `json:"command"`
	// Spells say what the shard will actually execute, so the environment it needs can
	// be checked before it starts rather than after it fails.
	Spells []string `json:"spells,omitempty"`
	// Writes is the collision surface: the declared output globs of every project in
	// the shard. This is the field that earns the briefing. Two shards are safe to run
	// together exactly when these do not overlap, and magus is the only party that knows
	// them: whoever splits the work up otherwise hands out units and hopes.
	Writes []string `json:"writes,omitempty"`
	// Exclusive marks a shard holding a project that refuses to run beside anything.
	Exclusive bool `json:"exclusive,omitempty"`
	// Agents is the only agent-specific part, kept in its own object so the rest reads as
	// what it is: ordinary plan metadata a person wants too.
	Agents *shardAgents `json:"agents,omitempty"`
}

// shardAgents is the one part of a shard record that is specific to an agent reader, kept
// in its own object for that reason. Everything beside it is ordinary plan metadata: this
// plan exists to fan CI jobs across runners, and it long predates anything agentic. That an
// agent can use the same record is a consequence of the record being correct, not a
// feature added for one.
type shardAgents struct {
	// Skills names the skills this shard's work routes to; Why states the derivation.
	// Derived rather than declared so it cannot drift from what the shard does - and
	// stated, because a routing decision an agent cannot audit is one it should not trust.
	Skills []string `json:"skills"`
	Why    []string `json:"why,omitempty"`
}

// affectedPlan emits a provider-neutral JSON shard plan for the affected set of a
// target (the --plan mode of `magus affected`). It does NOT execute the pipeline;
// CI wrappers (e.g. GitHub Actions) translate the matrix into their own parallel-job
// format with jq. The plan keys off the given target — exactly the set
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
	// `magus run <target> <projects>` already uses - so there is nothing new to learn, and
	// no flag invented for something the CLI already spells one way.
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
		return fmt.Errorf("magus affected --plan: a target is required (e.g. `%s`); run `%s` to list available targets",
			hint.Affected.With("ci", "--plan"), hint.DescribeTargets)
	}
	target = canonicalTarget(target) // expand short aliases at the CLI edge, mirroring `magus run`

	var pf *gen.AffectedPlanFlags
	if _, err := cmdParse("affected "+target+" --plan", flagArgs, func(fs *flag.FlagSet) {
		pf = gen.BindAffectedPlan(fs, gen.AffectedPlanDefaults{
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
			fs.PrintDefaults()
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
	}
	if pf.Stdin {
		planOpts.ChangedPaths, err = readAffectedPlanPaths(os.Stdin, pf.Null)
		if err != nil {
			return err
		}
	}
	plan, err := m.Plan(ctx, target, planOpts)
	if err != nil {
		return err
	}

	if len(only) > 0 {
		if plan.Shards, err = filterShards(ctx, m, plan.Shards, only); err != nil {
			return err
		}
		// The concurrency ceiling describes the plan being emitted, not the one it was
		// filtered from. Left alone it advertised room for six parallel jobs in a plan
		// carrying two, which a CI provider reads as a promise about this matrix.
		if plan.MaxParallel > len(plan.Shards) {
			plan.MaxParallel = len(plan.Shards)
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

	out := planOutput{
		Count:       len(plan.Shards),
		MaxParallel: plan.MaxParallel,
		Source:      plan.Source,
		Matrix:      make([]planShard, len(plan.Shards)),
	}
	for i, s := range plan.Shards {
		out.Matrix[i] = planShard{Shard: s.ID, Projects: strings.Join(s.ProjectPaths, " ")}
	}
	if pf.Detail {
		out.Detail, err = planDetail(ctx, m, target, plan.Shards)
		if err != nil {
			return err
		}
	}

	// --plan goes through the shared renderer like every other structured command, so -o
	// selects the encoding. Marshaling here directly would ignore it and print JSON for
	// `-o yaml`, in both flag positions.
	//
	// FormatText maps to JSON rather than to a prose rendering, because the plan has
	// no prose rendering to fall back on - the default output IS the machine-readable
	// document, and a workflow that pipes `--plan` without -o must keep getting it.
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputText, outputJSON:
		return emitFormatted(OutputOptions{Format: outputJSON}, out)
	case outputName:
		w, cleanup, err := outputDst()
		if err != nil {
			return err
		}
		defer func() { _ = cleanup() }()
		for _, s := range out.Matrix {
			if _, err := fmt.Fprintln(w, s.Shard); err != nil {
				return err
			}
		}
		return nil
	default:
		return emitFormatted(opts, out)
	}
}

func readAffectedPlanPaths(r io.Reader, null bool) ([]string, error) {
	var paths []string
	if null {
		body, err := io.ReadAll(r)
		if err != nil {
			return nil, fmt.Errorf("magus affected --plan: read stdin: %w", err)
		}
		for _, path := range bytes.Split(body, []byte{0}) {
			if len(path) > 0 {
				paths = append(paths, string(path))
			}
		}
	} else {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			if path := scanner.Text(); path != "" {
				paths = append(paths, path)
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("magus affected --plan: read stdin: %w", err)
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths), nil
}

// noteUndeclaredSeeds reports MGS1028: changed files that seeded a project through
// directory containment while that project declares none of them, so the targets they
// rerun were already correct.
//
// It lives at the CLI edge, not behind magus.Affected, because a library entry point
// must not write to a stream its caller never opted into - the condition rides
// types.AffectedResult.UndeclaredBySeed for every other consumer.
//
// The message names the SEED PROJECTS and nothing per-changeset, which is what makes
// it dedupe: interactive.Emit keys on the whole text, so a file list would differ on
// every request and churn a long-lived daemon's hint set instead of teaching once. The
// files are already on screen where this is emitted - --impact and --explain both mark
// each one - and `magus describe file` explains any of them in full.
func noteUndeclaredSeeds(undeclaredBySeed map[string][]string) {
	if len(undeclaredBySeed) == 0 {
		return
	}
	seeds := slices.Sorted(maps.Keys(undeclaredBySeed))
	if len(seeds) > undeclaredSeedHintCap {
		seeds = append(seeds[:undeclaredSeedHintCap:undeclaredSeedHintCap],
			fmt.Sprintf("and %d more", len(undeclaredBySeed)-undeclaredSeedHintCap))
	}
	interactive.Emit(os.Stderr, fmt.Sprintf(
		"[%s] projects seeded by changed files nothing declares: %s. Directory containment "+
			"selected them, so the targets they rerun were already correct. Declare the files "+
			"in the owning project's sources, or leave them undeclared deliberately (see %s)",
		types.UndeclaredSeedingFile, strings.Join(seeds, ", "),
		types.CodeURL(types.UndeclaredSeedingFile)))
}

// undeclaredSeedHintCap bounds how many seed projects MGS1028 names inline.
const undeclaredSeedHintCap = 5

// affectedImpact reports the blast radius of the current changeset (the --impact
// forensic mode of `magus affected`). It is strictly read-only: it maps changed files
// to seed projects and expands the dependency-graph reverse closure to name the
// affected projects and their targets, then surfaces `magus affected ci` as the
// follow-up. It NEVER executes a target and takes no positional target or project.
func affectedImpact(ctx context.Context, root string, args []string) error {
	// --impact routed us here (hasModeFlag); bind it so the flag parser accepts it,
	// then parse --base like the other forensic modes. No positional target is read.
	var imf *gen.AffectedImpactFlags
	if _, err := cmdParse("affected --impact", args, func(fs *flag.FlagSet) {
		imf = gen.BindAffectedImpact(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus affected --impact [--base <ref>]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Report the IMPACT of the current changeset: the changed files, the projects")
			fmt.Fprintln(os.Stderr, "that directly contain them (seeds), and the affected closure with each")
			fmt.Fprintln(os.Stderr, "project's targets. Read-only - it runs nothing.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags:")
			fs.PrintDefaults()
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
	noteUndeclaredSeeds(undeclared)

	// Enrich with the differentiated overlays (changed-symbol callers, coverage on
	// changed code). These read the heavier knowledge store - a prior symbol index and,
	// for coverage, a prior `magus run coverage` - not the lean workspace handle Compute
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
		for _, p := range out.AffectedProjects {
			fmt.Println(p.Path)
		}
		return nil
	}

	return printImpactText(out)
}

// impactFileCap bounds how many changed files are listed per seed project in text
// mode; a large changeset stays readable while the full set is still one -o json away.
const impactFileCap = 12

// printImpactText renders the impact report in the `magus graph explain` house style:
// counts before lists, verbs not arrows, full ids, plain ASCII. Changed files are
// grouped under the seed project that owns them (not dumped as one flat list) so a
// large changeset stays legible.
func printImpactText(out *types.ImpactResult) error {
	// Notes carry graceful degradation - a knowledge graph that failed to load, a
	// missing symbol index - so they must survive the early returns below. Without
	// this, an empty changeset rendered as a clean "nothing is affected" while the
	// reason the overlays were absent went unsaid, which is the worst shape a silent
	// failure can take in a gate command.
	defer func() {
		for _, n := range out.Notes {
			fmt.Printf("\nnote: %s\n", n)
		}
	}()

	if out.ChangedFileCount == 0 {
		fmt.Printf("No changed files against %s; nothing is affected.\n", out.Base)
		return nil
	}

	fmt.Printf("Changeset impact (base: %s)\n", out.Base)
	fmt.Printf("%s changed, seeding %s, affecting %s.\n",
		countLabel(out.ChangedFileCount, "file", "files"),
		countLabel(len(out.SeedProjects), "project", "projects"),
		countLabel(len(out.AffectedProjects), "project", "projects"))

	if len(out.AffectedProjects) == 0 {
		fmt.Printf("\nNo projects are affected (every changed file sits outside a project).\n")
		return nil
	}

	// Seeds first (they carry the changed files), then the projects reached only
	// through the dependency closure. Counting seeds here also yields the number of
	// changed files that landed inside a project, for an outside-any-project note.
	seeded := 0
	fmt.Printf("\nAffected projects (%d):\n", len(out.AffectedProjects))
	for _, p := range out.AffectedProjects {
		if !p.Seed {
			continue
		}
		seeded += len(p.Files)
		// "seeded by 12 changed files" reads as twelve files this project is built
		// from. Some of them may be seeding it by directory containment alone, which
		// is a rerun whose result was already correct - so the count says how many,
		// and the listing below marks which.
		label := countLabel(len(p.Files), "changed file", "changed files")
		if n := len(p.UndeclaredFiles); n > 0 {
			label += fmt.Sprintf(", %d declared by no project", n)
		}
		fmt.Printf("  %s (seeded by %s)\n", p.Path, label)
		if len(p.Targets) > 0 {
			fmt.Printf("    targets: %s\n", strings.Join(p.Targets, ", "))
		}
		shown := p.Files
		if len(shown) > impactFileCap {
			shown = shown[:impactFileCap]
		}
		for _, f := range shown {
			if slices.Contains(p.UndeclaredFiles, f) {
				fmt.Printf("    %s (undeclared)\n", f)
				continue
			}
			fmt.Printf("    %s\n", f)
		}
		if extra := len(p.Files) - len(shown); extra > 0 {
			fmt.Printf("    ... and %d more\n", extra)
		}
	}
	for _, p := range out.AffectedProjects {
		if p.Seed {
			continue
		}
		fmt.Printf("  %s (via dependencies)\n", p.Path)
		if len(p.Targets) > 0 {
			fmt.Printf("    targets: %s\n", strings.Join(p.Targets, ", "))
		}
	}

	if outside := out.ChangedFileCount - seeded; outside > 0 {
		fmt.Printf("\n%s changed outside any project (seeded nothing).\n", countLabel(outside, "file", "files"))
	}

	printImpactOverlays(out)

	// Complementary deep-link into the live Graph Explorer, focused on a single
	// representative seed with a blast view (what depends on it - the closure the
	// change ripples out to). The query grammar ANDs its terms with no OR, so the
	// full affected set cannot be selected in one query. Always printed; the daemon
	// may not be up when the browser opens it, hence the hint.
	if len(out.SeedProjects) > 0 {
		seed := out.SeedProjects[0]
		label := seed
		if len(out.SeedProjects) > 1 {
			label = fmt.Sprintf("%s (1 of %d seeds)", seed, len(out.SeedProjects))
		}
		link := liveExplorerLink(url.GraphLinkOpts{View: "blast", Node: types.KindProject + ":" + seed})
		fmt.Printf("\nView the blast radius of %s in the Graph Explorer: %s\n", label, link)
		fmt.Printf("%s\n", authHint)
		fmt.Printf("(start the magus daemon if the graph does not load)\n")
	}

	fmt.Printf("\nRun the full pipeline over this set with: %s\n", hint.Affected.With("ci"))
	return nil
}

// impactSymbolCap bounds how many changed symbols the caller overlay lists in text
// mode; the widest-reach symbols lead (the list is sorted by descending caller count),
// so a large changeset stays readable while the full set is one -o json away.
const impactSymbolCap = 20

// printImpactOverlays renders the differentiated overlay sections - changed-symbol
// callers and coverage on changed code - beneath the blast radius. Each is additive and
// self-suppressing: an overlay with no data prints nothing here (its honest output is
// the Note the enrichment appended). Same house style as the blast radius: counts before
// lists, verbs not arrows, plain ASCII.
func printImpactOverlays(out *types.ImpactResult) {
	if len(out.ChangedSymbols) > 0 {
		files := map[string]struct{}{}
		for _, s := range out.ChangedSymbols {
			files[s.File] = struct{}{}
		}
		fmt.Printf("\nChanged-symbol callers (%s across %s):\n",
			countLabel(len(out.ChangedSymbols), "symbol", "symbols"),
			countLabel(len(files), "changed file", "changed files"))
		shown := out.ChangedSymbols
		if len(shown) > impactSymbolCap {
			shown = shown[:impactSymbolCap]
		}
		for _, s := range shown {
			name := s.Label
			if name == "" {
				name = s.Symbol
			}
			line := fmt.Sprintf("  %s (%s): %s across %s", name, s.File,
				countLabel(s.RefCount, "caller", "callers"),
				countLabel(s.FileCount, "file", "files"))
			if s.Coverage.Total > 0 {
				line += fmt.Sprintf(" [coverage %s]", impactPct(s.Coverage.Ratio))
			}
			fmt.Println(line)
		}
		if extra := len(out.ChangedSymbols) - len(shown); extra > 0 {
			fmt.Printf("  ... and %d more\n", extra)
		}
	}

	if len(out.ChangedFileCoverage) > 0 {
		fmt.Printf("\nCoverage on changed files (%s):\n",
			countLabel(len(out.ChangedFileCoverage), "file", "files"))
		for _, c := range out.ChangedFileCoverage {
			fmt.Printf("  %s: %s (%d/%d stmts)\n", c.File,
				impactPct(c.Coverage.Ratio), c.Coverage.Covered, c.Coverage.Total)
		}
	}
}

// impactPct renders a 0..1 coverage ratio as a whole-percent string ("80%").
func impactPct(ratio float64) string {
	return fmt.Sprintf("%.0f%%", ratio*100)
}

// countLabel formats n with a singular/plural noun ("1 file", "3 files").
func countLabel(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
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
		case flagValueOf(a, "explain") != "":
			project = flagValueOf(a, "explain")
			ok = true
		case isFlagNamed(a, "base") && i+1 < len(args):
			base = args[i+1]
		case flagValueOf(a, "base") != "":
			base = flagValueOf(a, "base")
		}
	}
	return project, base, ok
}

// affectedExplainOutput is the structured result for --explain.
type affectedExplainOutput struct {
	Project  string                `json:"project"         yaml:"project"`
	Affected bool                  `json:"affected"        yaml:"affected"`
	Base     string                `json:"base"            yaml:"base"`
	Paths    []affectedExplainPath `json:"paths,omitempty" yaml:"paths,omitempty"`
}

type affectedExplainPath struct {
	Seed  string   `json:"seed"  yaml:"seed"`
	Chain []string `json:"chain" yaml:"chain"`
	Files []string `json:"files" yaml:"files"`
	// Undeclared is the subset of Files that no project declares - the ones whose
	// answer to "why did this run" is directory containment rather than a cache key.
	Undeclared []string `json:"undeclared,omitempty" yaml:"undeclared,omitempty"`
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
	noteUndeclaredSeeds(r.UndeclaredBySeed)

	g, err := ws.Graph()
	if err != nil {
		return err
	}

	out := affectedExplainOutput{Project: target, Base: r.Base}
	for _, a := range r.Affected {
		if a == target {
			out.Affected = true
			break
		}
	}

	if out.Affected {
		paths := g.PathsFromSeeds(r.Seed, target)
		for _, ap := range paths {
			out.Paths = append(out.Paths, affectedExplainPath{
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
		if out.Affected {
			fmt.Println(out.Project)
		}
		return nil
	}

	// text and wide
	if !printAffectedExplainText(out) {
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

// printAffectedExplainText renders the text and wide forms of `magus affected --explain`,
// reporting whether the project is affected at all - the caller appends the VCS diff
// hints only when it is. Split out for the same reason printImpactText is: the rendering
// is a pure function of the result, and the I/O around it is not.
func printAffectedExplainText(out affectedExplainOutput) bool {
	if !out.Affected {
		fmt.Printf("%s is not affected (base: %s)\n", out.Project, out.Base)
		return false
	}
	fmt.Printf("%s\n", out.Project)
	for _, ap := range out.Paths {
		if len(ap.Chain) == 1 {
			fmt.Printf("  changed files:\n")
		} else {
			fmt.Printf("  via %s:\n", strings.Join(ap.Chain, " -> "))
		}
		for _, f := range ap.Files {
			// The marker answers the question the file list raises but cannot settle:
			// whether this path is an input the seed is built from, or one that only
			// happens to sit inside it.
			if slices.Contains(ap.Undeclared, f) {
				fmt.Printf("    %s (undeclared: seeds by directory containment, keys nothing)\n", f)
				continue
			}
			fmt.Printf("    %s\n", f)
		}
	}
	return true
}

// planDetail joins the shard partition against what magus already declares about each
// project, producing one detail record per shard.
//
// This is assembly, not analysis. The plan knew which projects may run concurrently; the
// project list knows each one's spell, declared outputs, and exclusivity; the skills are
// derived from those two. Nothing here inspects a file or runs a target.
func planDetail(ctx context.Context, m *magus.Magus, target string, shards []types.Shard) (map[string]shardDetail, error) {
	projects, err := m.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	byPath := make(map[string]types.ProjectEntry, len(projects.Projects))
	for _, p := range projects.Projects {
		byPath[p.Path] = p
	}

	// Per-TARGET writes as well as project-wide ones. A project that declares its outputs
	// per target - ctx.writesFiles(...) - has an EMPTY project-level Outputs, so a
	// project-only join reported this workspace's root shard as writing nothing while it
	// rewrites MAGUS.md and gen/*.json. A collision surface that omits the busiest writer
	// is worse than none: it reads as a cleared shard.
	writesByProject := map[string][]string{}
	if graph, gerr := m.TargetGraph(ctx); gerr == nil {
		for _, proj := range graph.Projects {
			for _, node := range proj.Nodes {
				for _, ref := range node.WritesFiles {
					owner := ref.Project
					if owner == "" {
						owner = proj.Path
					}
					writesByProject[proj.Path] = appendUnique(writesByProject[proj.Path], joinProjectGlob(owner, ref.Glob))
				}
			}
		}
	}

	out := make(map[string]shardDetail, len(shards))
	for _, s := range shards {
		b := shardDetail{Command: hint.Run.With(target, strings.Join(s.ProjectPaths, " "))}
		for _, path := range s.ProjectPaths {
			p, ok := byPath[path]
			if !ok {
				continue // a shard naming a project the workspace no longer lists: nothing to say
			}
			b.Spells = appendUnique(b.Spells, p.Spells...)
			if p.Spell != "" {
				b.Spells = appendUnique(b.Spells, p.Spell)
			}
			// Project-relative as declared, rooted at the project, so two briefings can be
			// compared for overlap without the reader re-deriving where each one sits.
			for _, g := range p.Outputs {
				b.Writes = appendUnique(b.Writes, joinProjectGlob(path, g))
			}
			b.Writes = appendUnique(b.Writes, writesByProject[path]...)
			b.Exclusive = b.Exclusive || p.Exclusive
		}
		skills, why := shardSkills(b)
		b.Agents = &shardAgents{Skills: skills, Why: why}
		out[s.ID] = b
	}
	return out, nil
}

// shardSkills derives the agent skills a shard's work routes to, and says why.
//
// Derived from what the shard DOES rather than declared in a table, so it cannot drift
// from the shard it describes - a second copy of the routing table would rot the first
// time a project changed spells. Each reason is returned alongside, because a routing
// decision an agent cannot audit is one it should not act on.
func shardSkills(b shardDetail) (skills, why []string) {
	// The always-full twin, not the primary. The primary entry is the curated shorter
	// permutation, a bet that the reader who INSTALLED it can re-derive the steps it drops.
	// A record like this is read by someone who did not make that bet and would inherit it
	// with no say, so the twin is the name that survives being passed along.
	skills = append(skills, agent.FullTwinName("magus-run"))
	why = append(why, "magus-run: the shard is a target invocation, and the raw language tool would bypass the cache and the affected set")
	if len(b.Writes) > 0 {
		skills = append(skills, agent.FullTwinName("magus-vcs-hygiene"))
		why = append(why, "magus-vcs-hygiene: this shard declares outputs, so its run leaves generated files that must be classified before they are committed or reverted")
	}
	why = append(why, "variant: each skill is named as its always-full twin, because the reader of this record is not the session that chose the install")
	if b.Exclusive {
		why = append(why, "exclusive: a project here refuses to run beside anything, so this shard must not be handed out concurrently with another")
	}
	return skills, why
}

// joinProjectGlob roots a project-relative declared glob at the project, leaving an
// already-rooted or workspace-level glob alone.
func joinProjectGlob(project, glob string) string {
	if project == "" || project == "." || strings.HasPrefix(glob, project+"/") {
		return glob
	}
	return project + "/" + glob
}

// appendUnique appends each value not already present, preserving order.
func appendUnique(dst []string, values ...string) []string {
	for _, v := range values {
		if v != "" && !slices.Contains(dst, v) {
			dst = append(dst, v)
		}
	}
	return dst
}

// filterShards narrows a plan to the named projects, INTERSECTING them with the affected
// set rather than replacing it.
//
// The distinction is the whole point: `magus run ci docs` runs docs whether or not the diff
// touched it, while this answers "of the work this change actually implies, give me the
// docs part". Splitting a large affected set into units wants the second; the first would
// hand out work the change never justified.
//
// Shard IDs are preserved rather than renumbered, so a filtered plan can be read against
// the unfiltered one it came from; a shard left empty simply drops out.
//
// A name that matches no project in the WORKSPACE is an error, because it is a typo and
// silently planning nothing is how a typo turns into "the change affected nothing". A name
// that is a real project but outside the affected set is not an error - that is the honest
// empty answer, and it is the question being asked.
func filterShards(ctx context.Context, m *magus.Magus, shards []types.Shard, only []string) ([]types.Shard, error) {
	projects, err := m.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(projects.Projects))
	for _, p := range projects.Projects {
		known[p.Path] = true
	}
	want := make(map[string]bool, len(only))
	for _, name := range only {
		clean := strings.TrimSuffix(filepath.ToSlash(strings.TrimSpace(name)), "/")
		if !known[clean] {
			return nil, fmt.Errorf("magus affected --plan: no project %q in this workspace; run `%s` to list them", name, hint.Ls)
		}
		want[clean] = true
	}

	return filterShardPaths(shards, want), nil
}

// filterShardPaths is the pure half of filterShards: the intersection itself, with the
// workspace lookup and its typo check left to the caller.
func filterShardPaths(shards []types.Shard, want map[string]bool) []types.Shard {
	out := make([]types.Shard, 0, len(shards))
	for _, sh := range shards {
		kept := make([]string, 0, len(sh.ProjectPaths))
		for _, path := range sh.ProjectPaths {
			if want[path] {
				kept = append(kept, path)
			}
		}
		if len(kept) == 0 {
			continue
		}
		sh.ProjectPaths = kept
		out = append(out, sh)
	}
	return out
}
