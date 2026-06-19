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
	"github.com/egladman/magus/types"
)

// runTarget dispatches `magus run <target> [projects...]`.
func runTarget(ctx context.Context, root string, _ runConfig, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		return targetUsage()
	}

	rawTarget, rest := args[0], args[1:]
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
		timeout      *time.Duration
		shardID      *string
		nShards      *int
		noFlakeRetry *bool
		raceFlag     *string
		graphView    *bool
		upstream     *bool
		graphDepth   *int
		step         *bool
	)
	projectArgs, err := cmdParse("run "+targetName, flagArgs, func(fs *flag.FlagSet) {
		timeout = fs.Duration("timeout", 0, "Abort if not finished within this duration (e.g. 5m, 1h30m); 0 = no limit")
		shardID = fs.String("shard", os.Getenv("MAGUS_SHARD"), "Shard ID within this CI matrix run (e.g. \"0\"); enables ci.shard.total reporting")
		var nShardsDefault int
		if s := os.Getenv("MAGUS_N_SHARDS"); s != "" {
			nShardsDefault, _ = strconv.Atoi(s)
		}
		nShards = fs.Int("n-shards", nShardsDefault, "Total shard count for this CI matrix run; paired with --shard")
		noFlakeRetry = fs.Bool("no-flake-retry", false, "Disable flake auto-retry for this run (used by magus affected --bisect)")
		raceFlag = fs.String("race", "", raceFormatHelp)
		graphView = fs.Bool("graph", false, "Render the dependency graph for the selected scope instead of executing")
		upstream = fs.Bool("upstream", false, "With --graph: show dependents instead of dependencies")
		graphDepth = fs.Int("depth", 0, "With --graph: cap displayed depth (0 = unlimited)")
		step = fs.Bool("step", false, "Pause before each subprocess for interactive stepping (requires TTY; implies --concurrency=1)")
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
	var scopeLabel string
	if len(targets) == 1 {
		scopeLabel = targets[0].Path
	} else {
		scopeLabel = fmt.Sprintf("%d projects", len(targets))
	}
	m.LogScope(scopeLabel, source)
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
	if len(parsedTarget.Charms) > 0 {
		runOpts = append(runOpts, magus.WithCharms(parsedTarget.Charms...))
	}
	if *noFlakeRetry {
		runOpts = append(runOpts, magus.WithNoFlakeRetry())
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
	if rw != nil {
		runOpts = append(runOpts, magus.WithReport(rw))
	}
	if spellFilter != "" {
		runOpts = append(runOpts, magus.WithSpellFilter(spellFilter))
	}
	if len(extraArgs) > 0 {
		runOpts = append(runOpts, magus.WithExtraArgs(extraArgs))
	}
	if targetName == "ci" {
		err = m.RunCI(ctx, targets, runOpts...)
	} else {
		err = m.Run(ctx, targets, runOpts...)
	}
	if *timeout > 0 && errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("run %s: timed out after %s", targetName, *timeout)
	}
	if rw != nil && *shardID != "" && *nShards > 0 {
		_ = rw.RecordShardTotal(*shardID, *nShards, time.Since(startedAt))
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
