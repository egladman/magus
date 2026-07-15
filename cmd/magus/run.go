package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/types"
)

// runTarget dispatches `magus run <target> [projects...]`.
func runTarget(ctx context.Context, root string, _ runConfig, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		return targetUsage()
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
	if parseErr != nil {
		return parseErr
	}
	targetName := canonicalTarget(parsedTarget.Name) // expand short aliases at the CLI edge
	parsedTarget.Name = targetName

	startedAt := time.Now()

	flagArgs, extraArgs := splitOnDashDash(rest)

	var (
		timeout           *time.Duration
		shardID           *string
		nShards           *int
		noVolatilityRetry *bool
		raceFlag          *string
		graphView         *bool
		upstream          *bool
		graphDepth        *int
		step              *bool
		live              *bool
		noCache           *bool

		noDefaultCharms *bool
	)
	projectArgs, err := cmdParse("run "+targetName, flagArgs, func(fs *flag.FlagSet) {
		timeout = fs.Duration("timeout", 0, "Abort if not finished within this duration (e.g. 5m, 1h30m); 0 = no limit")
		shardID = fs.String("shard", os.Getenv("MAGUS_SHARD"), "Shard ID within this CI matrix run (e.g. \"0\"); enables ci.shard.total reporting")
		var nShardsDefault int
		if s := os.Getenv("MAGUS_N_SHARDS"); s != "" {
			nShardsDefault, _ = strconv.Atoi(s)
		}
		nShards = fs.Int("n-shards", nShardsDefault, "Total shard count for this CI matrix run; paired with --shard")
		noVolatilityRetry = fs.Bool("no-volatility-retry", false, "Disable volatility auto-retry for this run (used by magus affected --bisect)")
		raceFlag = fs.String("race", "", raceFormatHelp)
		graphView = fs.Bool("graph", false, "Render the dependency graph for the selected scope instead of executing")
		upstream = fs.Bool("upstream", false, "With --graph: show dependents instead of dependencies")
		graphDepth = fs.Int("depth", 0, "With --graph: cap displayed depth (0 = unlimited)")
		step = fs.Bool("step", false, "Pause before each subprocess for interactive stepping (requires TTY; implies --concurrency=1)")
		noDefaultCharms = fs.Bool("no-default-charms", false, "Ignore magus.yaml default_charms for this run")
		live = fs.Bool("live", false, "Print a local log-viewer link and stream this run's output to it live over an ephemeral loopback server (127.0.0.1); the link and data never leave your machine")
		noCache = fs.Bool("no-cache", false, "Force a fresh run even on a cache hit; still refreshes the entry (unlike a skip_cache target, which never snapshots)")
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

	if *step && !isInteractiveTTY() {
		fmt.Fprintln(os.Stderr, "magus: --step requires an interactive terminal")
		return errSilent{exitCode: 2}
	}

	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = withTimeout(ctx, *timeout, "run:"+targetName)
		defer cancel()
	}

	if *step {
		ctx = withStepGate(ctx)
	}

	if spellFilter != "" && targetName == "ci" {
		return fmt.Errorf("spell-qualified syntax (e.g. %q) is not supported for the ci target", rawTarget)
	}

	if *graphView {
		ws, err := inspectWorkspace(ctx, root)
		if err != nil {
			return err
		}
		return renderWorkspaceGraph(ctx, ws, graphRenderOptions{
			Upstream: *upstream,
			Depth:    *graphDepth,
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
		targets, source, err := resolveTargets(ws, listTarget, projectArgs)
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
	targets, source, err := resolveTargets(m, parsedTarget, projectArgs)
	if err != nil {
		return err
	}
	// Fault tolerance by design: a target only some projects serve should skip - not
	// error - the projects that lack it when the scope is the workspace or several
	// projects, but a single project that does not serve it (or a name no project serves
	// at all) is an error. This is the run counterpart to affected's tolerance.
	if len(targets) > 0 {
		targets, err = filterServedTargets(m, targets, targetName)
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
	m.LogScope(scopeLabel, source)
	// Surface the active charms up front, next to the projects header, so the run's
	// state ("here's what's in effect") is visible before any work - and so a missing
	// default charm (e.g. rw not applied) is obvious rather than silent.
	charms := withDefaultCharms(parsedTarget.Charms, globalCfg.DefaultCharms, *noDefaultCharms)
	m.LogCharms(strings.Join(charms, ","))
	if len(targets) == 0 {
		slog.InfoContext(ctx, "run: no projects selected", slog.String("target", targetName))
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
	if globalCfg.DryRun {
		runOpts = append(runOpts, magus.WithDryRun())
	}
	if len(charms) > 0 {
		runOpts = append(runOpts, magus.WithCharms(charms...))
	}
	if *noVolatilityRetry {
		runOpts = append(runOpts, magus.WithNoVolatilityRetry())
	}
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
	if *step {
		runOpts = append(runOpts, magus.WithStep())
	}
	if *noCache {
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
	cwd, _ := os.Getwd()
	liveBC, stopLive := beginLive(ctx, *live)
	defer stopLive()
	// An adopted run (dispatched by the daemon) also feeds the daemon's live-run registry,
	// carried on ctx; a plain CLI run has no sink, so this is empty there.
	captureHandlers := append(liveHandlers(liveBC), console.RunSinkHandlers(ctx)...)
	invCtx, endInvocation := m.BeginInvocation(ctx, journal.Command{
		Arguments: append([]string{"run"}, args...), Cwd: cwd, Trigger: trigger,
	}, version, captureHandlers...)
	defer func() { endInvocation(err) }()

	if targetName == "ci" {
		err = m.RunCI(invCtx, targets, runOpts...)
	} else {
		err = m.Run(invCtx, targets, runOpts...)
	}
	if *timeout > 0 && errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("run %s: timed out after %s", targetName, *timeout)
	}
	if rw != nil && *shardID != "" && *nShards > 0 {
		_ = rw.RecordShardTotal(*shardID, *nShards, time.Since(startedAt))
	}
	if reportedRunErr(err) {
		return errSilent{exitCode: 1}
	}
	return err
}

// resolveTargets resolves targets from the workspace: by path, explicit args, cwd-scope, or all.
func resolveTargets(ws types.WorkspaceRepository, t types.Target, projectArgs []string) ([]types.Target, string, error) {
	anchor := cwdAnchor(ws.Root())
	if t.Path != "" {
		resolved, err := resolveProjectArg(t.Path, anchor)
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
			resolved, err := resolveProjectArg(arg, anchor)
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
	cwdTargets, found, err := ws.ExpandCwd(t)
	if err != nil {
		return nil, "", err
	}
	if found {
		return cwdTargets, "cwd", nil
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
func filterServedTargets(m *magus.Magus, targets []types.Target, targetName string) ([]types.Target, error) {
	return applyTargetFilter(targets, targetName, buildDefinesTarget(m), func(path string) string { return projectLabelFor(m, path) })
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
func buildDefinesTarget(m *magus.Magus) func(path, target string) bool {
	byProject := map[string]map[string]bool{}
	add := func(path, name string) {
		set := byProject[path]
		if set == nil {
			set = map[string]bool{}
			byProject[path] = set
		}
		set[name] = true
	}
	for _, p := range m.DescribeGraph().Projects {
		for _, n := range p.Nodes {
			add(p.Path, n.Name)
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

// resolveProjectArg canonicalises a CLI project argument to a workspace-relative
// path. Dot-relative paths resolve against anchor, bare paths stay
// workspace-relative, and absolute or escaping paths are rejected. The ""
// and "/" all-projects sentinels pass through for ExpandPath to fan out.
func resolveProjectArg(arg, anchor string) (string, error) {
	if arg == "" || arg == "/" {
		return arg, nil
	}
	return file.Resolve(arg, anchor)
}

// cwdAnchor returns the working directory as a slash path relative to root.
// It falls back to "." when the cwd cannot be located.
func cwdAnchor(root string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	rel, err := filepath.Rel(root, cwd)
	if err != nil {
		return "."
	}
	return filepath.ToSlash(rel)
}

func targetUsage() error {
	fmt.Fprintln(os.Stderr, "Usage: magus run <target> [flags] [project...]")
	fmt.Fprintln(os.Stderr, "       magus run <spell>::<target> [flags] [project...]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Targets:")
	fmt.Fprintln(os.Stderr, "  <name>   any target exported by a project's magusfile")
	fmt.Fprintln(os.Stderr, "  list     print selected projects (no execution)")
	fmt.Fprintln(os.Stderr, "  ci       the magusfile's ci target, run read-only (affected/pipeline anchor)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Conventional lifecycle names (you compose these in your magusfile from a")
	fmt.Fprintln(os.Stderr, "spell's tool-native ops, e.g. global function build(_a) go.build() end):")
	fmt.Fprintln(os.Stderr, "  build / test / lint / format / clean / generate / ci")
	fmt.Fprintln(os.Stderr, "  (fmt → format and gen → generate are accepted as aliases)")
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
