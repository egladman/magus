package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/internal/interactive/clihint"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// affected dispatches `magus affected <target>`; project set is determined by VCS diff.
func affected(ctx context.Context, root string, _ runConfig, args []string) error {
	// Bare `magus affected` (no target) is a usage error, not a help request: a target
	// is required. Print a clear one-liner plus usage and exit non-zero, never silently.
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "magus affected: a target is required (e.g. `magus affected ci`)")
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

	// Find the target even if global flags precede it (`magus affected --dry-run ci`);
	// mirrors `magus run`. rest carries the hoisted flags for cmdParse below.
	rawTarget, rest, ok := splitTargetFromArgs(args)
	if !ok {
		affectedUsage()
		return flag.ErrHelp
	}
	spellFilter, targetStr := parseTarget(rawTarget)
	parsed, perr := types.ParseTarget(targetStr)
	if perr != nil {
		return perr
	}
	target := canonicalTarget(parsed.Name) // expand short aliases at the CLI edge, mirroring `magus run`

	// Split on "--" before flag parsing so passthrough args aren't consumed by flag.
	flagArgs, extraArgs := splitOnDashDash(rest)

	var (
		baseStr         string
		base            *string
		stdin           *bool
		null            *bool
		timeout         *time.Duration
		graphView       *bool
		upstream        *bool
		graphDepth      *int
		step            *bool
		raceFlag        *string
		noDefaultCharms *bool
		live            *bool
		noCache         *bool
	)
	_, err := cmdParse("affected "+target, flagArgs, func(fs *flag.FlagSet) {
		// affected-only: VCS diff base ref; `magus run` has no diff. See run_affected_parity_test.go.
		fs.StringVar(&baseStr, "base", "", "Override base ref for the VCS diff (default: MAGUS_VCS_BASE_REF or origin/main)")
		fs.StringVar(&baseStr, "b", "", "Short for --base")
		base = &baseStr
		// affected-only: reads changed paths from a pipe (watch loop); `magus run` takes explicit project paths. See run_affected_parity_test.go.
		stdin = fs.Bool("stdin", false, "Read changed file paths from stdin instead of git diff;\n\t\tpairs with `magus watch` (mutually exclusive with --base)")
		// affected-only: pairs with --stdin. See run_affected_parity_test.go.
		null = fs.Bool("null", false, "With --stdin: expect NUL-separated paths and double-NUL between batches\n\t\t(pairs with `magus watch --null`)")
		timeout = fs.Duration("timeout", 0, "Abort if not finished within this duration (e.g. 5m, 1h30m); 0 = no limit")
		graphView = fs.Bool("graph", false, "Render the dependency graph for the affected scope instead of executing")
		upstream = fs.Bool("upstream", false, "With --graph: show dependents instead of dependencies")
		graphDepth = fs.Int("depth", 0, "With --graph: cap displayed depth (0 = unlimited)")
		step = fs.Bool("step", false, "Pause before each subprocess for interactive stepping (requires TTY; implies --concurrency=1; not compatible with --stdin)")
		raceFlag = fs.String("race", "", raceFormatHelp)
		noDefaultCharms = fs.Bool("no-default-charms", false, "Ignore magus.yaml default_charms for this run")
		live = fs.Bool("live", false, "Print a local log-viewer link and stream this run's output to it live over an ephemeral loopback server (127.0.0.1); the link and data never leave your machine")
		noCache = fs.Bool("no-cache", false, "Force a fresh run even on a cache hit; still refreshes the entry (unlike a skip_cache target, which never snapshots)")
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

	if *step && *stdin {
		return fmt.Errorf("magus affected: --step and --stdin are mutually exclusive")
	}
	if *step && !isInteractiveTTY() {
		fmt.Fprintln(os.Stderr, "magus: --step requires an interactive terminal")
		return errSilent{exitCode: 2}
	}
	if *step {
		ctx = withStepGate(ctx)
	}

	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = withTimeout(ctx, *timeout, "affected:"+target)
		defer cancel()
	}

	if *stdin && *base != "" {
		return fmt.Errorf("magus affected: --stdin and --base are mutually exclusive")
	}

	if *graphView {
		if *stdin {
			return fmt.Errorf("magus affected: --graph and --stdin are mutually exclusive")
		}
		ws, err := inspectWorkspace(ctx, root)
		if err != nil {
			return err
		}
		targets, _, _, err := ws.ExpandAffected(ctx, "list", *base)
		if err != nil {
			return err
		}
		roots := make([]string, len(targets))
		for i, t := range targets {
			roots[i] = t.Path
		}
		return renderWorkspaceGraph(ctx, ws, graphRenderOptions{
			Upstream: *upstream,
			Depth:    *graphDepth,
			Roots:    roots,
		})
	}

	if *stdin {
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
		if *null {
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
		targets, source, _, err := ws.ExpandAffected(ctx, "list", *base)
		if err != nil {
			return err
		}
		listTargets("affected:list", targets, source)
		return nil
	}

	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	targets, source, _, err := m.ExpandAffected(ctx, target, *base)
	if err != nil {
		return err
	}
	var scopeLabel string
	if len(targets) == 1 {
		scopeLabel = targets[0].Path
	} else {
		scopeLabel = fmt.Sprintf("%d projects", len(targets))
	}
	m.LogScope(scopeLabel, source)
	// Merge magus.yaml default_charms with any explicit charm on the target - the same
	// as `magus run` does. Previously `affected` used only the explicit charms, so
	// default_charms (e.g. rw) silently did NOT apply to `affected`, unlike `run`.
	charms := withDefaultCharms(parsed.Charms, globalCfg.DefaultCharms, *noDefaultCharms)
	m.LogCharms(strings.Join(charms, ","))
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
	race, err := resolveRace(*raceFlag)
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
	if *step {
		runOpts = append(runOpts, magus.WithStep())
	}
	if *noCache {
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
	cwd, _ := os.Getwd()
	liveBC, stopLive := beginLive(ctx, *live)
	defer stopLive()
	// An adopted affected run (dispatched by the daemon) also feeds the daemon's live-run
	// registry, carried on ctx; a plain CLI run has no sink, so this is empty there.
	captureHandlers := append(liveHandlers(liveBC), console.RunSinkHandlers(ctx)...)
	invCtx, endInvocation := m.BeginInvocation(ctx, journal.Command{
		Verb: "affected", Args: args, Cwd: cwd, Trigger: trigger,
	}, version, captureHandlers...)
	defer func() { endInvocation(err) }()

	if target == "ci" {
		err = m.RunCI(invCtx, targets, runOpts...)
	} else {
		err = m.Run(invCtx, targets, runOpts...)
	}
	if *timeout > 0 && errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("affected %s: timed out after %s", target, *timeout)
	}
	if reportedRunErr(err) {
		return errSilent{exitCode: 1}
	}
	return err
}

func affectedUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus affected <target> [flags]")
	fmt.Fprintln(os.Stderr, "       magus affected --explain <project> [--base <ref>]")
	fmt.Fprintln(os.Stderr, "       magus affected <target> --plan [--max-shards N]")
	fmt.Fprintln(os.Stderr, "       magus affected --bisect <project> [--target <target>] [--good <sha>]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Targets (same as 'run' but project set comes from VCS diff):")
	fmt.Fprintln(os.Stderr, "  list      print affected projects (no execution)")
	fmt.Fprintln(os.Stderr, "  ci        full pipeline for affected projects")
	fmt.Fprintln(os.Stderr, "  <target>    any target supported by the project's tool (build, test, lint, …)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Forensic modes (reason about the affected set instead of executing):")
	fmt.Fprintln(os.Stderr, "  --explain <project>  show why a project is in the affected set")
	fmt.Fprintln(os.Stderr, "  <target> --plan      emit a provider-neutral JSON CI shard plan for <target> (e.g. ci)")
	fmt.Fprintln(os.Stderr, "  --bisect <project>   drive VCS bisect to find a regression's culprit commit")
	fmt.Fprintln(os.Stderr, "  --base <ref>         override VCS base ref (default: MAGUS_VCS_BASE_REF or origin/main)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Use MAGUS_VCS_BASE_REF or --base to set the comparison ref.")
	fmt.Fprintf(os.Stderr, "Use --stdin to read changed paths from a pipe (e.g. `%s | %s`).\n", clihint.Watch, clihint.Affected.With("--stdin", "build"))
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
}

type planShard struct {
	Shard    string `json:"shard"`
	Projects string `json:"projects"`
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
	var target string
	flagArgs := planless
	if len(planless) > 0 && !strings.HasPrefix(planless[0], "-") {
		target = planless[0]
		flagArgs = planless[1:]
	}
	if target == "" {
		return fmt.Errorf("magus affected --plan: a target is required (e.g. `%s`); run `%s` to list available targets",
			clihint.Affected.With("ci", "--plan"), clihint.DescribeTargets)
	}
	target = canonicalTarget(target) // expand short aliases at the CLI edge, mirroring `magus run`

	var (
		maxShards        *int
		runnerPoolBudget *int
	)
	if _, err := cmdParse("affected "+target+" --plan", flagArgs, func(fs *flag.FlagSet) {
		maxShards = fs.Int("max-shards", globalCfg.CI.MaxShards, "Maximum CI shards (-1 = unlimited)")
		runnerPoolBudget = fs.Int("max-parallel-budget", globalCfg.CI.RunnerPoolBudget, "Cross-shard concurrency cap; 0 = unlimited")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus affected <target> --plan [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Emit a provider-neutral JSON shard plan for the affected project set of")
			fmt.Fprintln(os.Stderr, "<target> (required, e.g. ci). Does NOT execute the pipeline; CI wrappers")
			fmt.Fprintln(os.Stderr, "(e.g. GitHub Actions) translate the matrix into their own format.")
			fmt.Fprintln(os.Stderr, "Adaptive sharding is always enabled; set MAGUS_HISTORY_PATH or history_path")
			fmt.Fprintln(os.Stderr, "in magus.yaml to override the history file location.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags:")
			fs.PrintDefaults()
		}
	}); err != nil {
		return err
	}

	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}

	plan, err := m.Plan(ctx, target, magus.PlanOptions{
		MaxShards:        *maxShards,
		RunnerPoolBudget: *runnerPoolBudget,
	})
	if err != nil {
		return err
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

	b, err := codec.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if _, err := os.Stdout.Write(b); err != nil {
		return err
	}
	_, err = os.Stdout.Write([]byte{'\n'})
	return err
}

// parseExplainArgs scans args for --explain[=<project>] and optionally --base.
// Returns (project, base, true) when --explain is present; otherwise ("", "", false).
func parseExplainArgs(args []string) (project, base string, ok bool) {
	for i, a := range args {
		if a == "--explain" {
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				project = args[i+1]
			}
			ok = true
		} else if strings.HasPrefix(a, "--explain=") {
			project = strings.TrimPrefix(a, "--explain=")
			ok = true
		} else if a == "--base" && i+1 < len(args) {
			base = args[i+1]
		} else if strings.HasPrefix(a, "--base=") {
			base = strings.TrimPrefix(a, "--base=")
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
				Seed:  ap.Seed,
				Chain: ap.Chain,
				Files: r.FilesBySeed[ap.Seed],
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
	if !out.Affected {
		fmt.Printf("%s is not affected (base: %s)\n", out.Project, out.Base)
		return nil
	}
	fmt.Printf("%s\n", out.Project)
	for _, ap := range out.Paths {
		if len(ap.Chain) == 1 {
			fmt.Printf("  changed files:\n")
		} else {
			fmt.Printf("  via %s:\n", strings.Join(ap.Chain, " → "))
		}
		for _, f := range ap.Files {
			fmt.Printf("    %s\n", f)
		}
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
