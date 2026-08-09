package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/egladman/magus/internal/ci"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/report"
)

func configHistoryCmd(ctx context.Context, _ string, cfg config.Config, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprintln(os.Stderr, "Usage: magus config history <subcommand>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Subcommands:")
		fmt.Fprintln(os.Stderr, "  import   merge runtime-history JSON files into the history (e.g. per-shard CI histories)")
		fmt.Fprintln(os.Stderr, "  passed   record the commit a ref last completed a fully passing run at")
		fmt.Fprintln(os.Stderr, "  dedup    measure cross-shard redundant builds from per-shard JSONL report files")
		return flag.ErrHelp
	}

	sub, rest := args[0], args[1:]
	switch sub {
	case "import":
		return runHistoryImport(ctx, cfg, rest)
	case "passed":
		return runHistoryPassed(ctx, cfg, rest)
	case "dedup":
		return runHistoryDedup(ctx, rest)
	default:
		return usagef("magus config history: unknown subcommand %q (choose: import, passed, dedup)", sub)
	}
}

// runHistoryPassed records the commit a ref last completed a fully passing run at,
// which `magus affected --base last-passed` then measures from.
//
// It is a SEPARATE subcommand from import rather than a flag on it, because the two are
// asserted by different things. import merges what the shards measured, and each shard
// only knows its own result; that the whole run passed is a fact only the aggregation
// job holds. Folding it into import would also tie recording to having files to merge,
// and a run whose affected set was empty has none while still being a passing run whose
// commit the marker must advance past.
func runHistoryPassed(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("config history passed", flag.ContinueOnError)
	bindDisplayFlags(fs)
	historyPath := fs.String("history", cfg.HistoryPath, "Path to the history JSON to write (default: configured history_path)")
	ref := fs.String("ref", "", "Ref the run was on (git branch, hg named branch, jj bookmark)")
	commit := fs.String("commit", "", "Commit the run was at")
	target := fs.String("target", "ci", "Target that ran")
	status := fs.String("status", string(forecast.RunPassed), "How the run came out: passed or failed")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config history passed --ref <name> --commit <id> [--target <name>] [--history <path>]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Record that ref completed a fully passing run of target at commit. Only a")
		fmt.Fprintln(os.Stderr, "caller that knows the WHOLE run passed may say so: in a sharded CI run that is")
		fmt.Fprintln(os.Stderr, "the aggregation job, never an individual shard.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "`magus affected <target> --base last-passed` then diffs from that commit, so a")
		fmt.Fprintln(os.Stderr, "run that was cancelled or failed leaves its commits in the NEXT run's diff")
		fmt.Fprintln(os.Stderr, "rather than being stepped over.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *historyPath == "" {
		return errors.New("magus config history passed: --history is required (or set history_path in magus.yaml)")
	}
	if *ref == "" || *commit == "" {
		return errors.New("magus config history passed: --ref and --commit are both required")
	}
	st := forecast.RunStatus(*status)
	if st != forecast.RunPassed && st != forecast.RunFailed {
		return usagef("magus config history passed: --status must be %q or %q, got %q",
			forecast.RunPassed, forecast.RunFailed, *status)
	}
	// Skipped, not failed. A workspace that turned the run log off wired this step in
	// anyway (it is inside the shipped action), and erroring would paint every run red
	// for honoring its own configuration.
	if !cfg.CI.RecordRuns {
		slog.InfoContext(ctx, "config history passed: run log disabled by ci.record_runs; recording nothing",
			slog.String("ref", *ref), slog.String("commit", *commit))
		return nil
	}

	var hist forecast.History
	if err := hist.Load(ctx, *historyPath); err != nil {
		return err
	}
	hist.RecordRun(forecast.Run{Commit: *commit, Ref: *ref, Target: *target, Status: st}, time.Now())
	if err := hist.Save(ctx, *historyPath); err != nil {
		return err
	}
	slog.InfoContext(ctx, "config history run recorded",
		slog.String("ref", *ref),
		slog.String("commit", *commit),
		slog.String("target", *target),
		slog.String("status", string(st)),
		slog.String("history_path", *historyPath))
	return nil
}

// runHistoryImport folds one or more runtime-history JSON files into --history
// (created if absent), freshest-wins per (project, target). Passing several files
// merges them: this is how the per-shard histories of one sharded CI run combine
// into the single history the next run's forecaster and volatility detector read. Each
// input is the history `magus run` wrote; there is no separate "merge" mode.
func runHistoryImport(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("config history import", flag.ContinueOnError)
	bindDisplayFlags(fs)
	historyPath := fs.String("history", cfg.HistoryPath, "Path to the history JSON to write (default: configured history_path)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config history import [--history <path>] <history.json>...")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Merge runtime-history JSON files into the history (created if absent). For each")
		fmt.Fprintln(os.Stderr, "(project, target) the freshest entry wins, so the per-shard histories one sharded")
		fmt.Fprintln(os.Stderr, "CI run produces combine into a single history. Inputs may be globs.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *historyPath == "" {
		return errors.New("magus config history import: --history is required (or set history_path in magus.yaml)")
	}
	inputs := fs.Args()
	if len(inputs) == 0 {
		return errors.New("magus config history import: pass at least one history file")
	}

	var hist forecast.History
	if err := hist.Load(ctx, *historyPath); err != nil {
		return err
	}
	files := 0
	for _, p := range inputs {
		matches, err := filepath.Glob(p)
		if err != nil {
			return fmt.Errorf("config history import: bad glob %q: %w", p, err)
		}
		if len(matches) == 0 {
			matches = []string{p}
		}
		for _, m := range matches {
			var in forecast.History
			if err := in.Load(ctx, m); err != nil {
				return fmt.Errorf("config history import: load %q: %w", m, err)
			}
			hist.Merge(&in)
			files++
		}
	}
	if err := hist.Save(ctx, *historyPath); err != nil {
		return err
	}
	slog.InfoContext(ctx, "config history import complete",
		slog.Int("files", files),
		slog.Int("projects", len(hist.Projects)),
		slog.String("history_path", *historyPath))
	return nil
}

// missBuild is one cache-miss event parsed from a shard JSONL report.
type missBuild struct {
	project    string
	target     string
	hash       string
	durationMs int64
}

// runHistoryDedup reads per-shard JSONL report files and measures cross-shard
// redundant builds: when the same (project, target, hash) is a cache miss on more
// than one shard, those extra builds are waste a shared remote cache would
// eliminate. Outputs a summary including total redundant build-seconds.
func runHistoryDedup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("config history dedup", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config history dedup <report.jsonl>...")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Reads per-shard JSONL report files and reports cross-shard redundant builds.")
		fmt.Fprintln(os.Stderr, "A build is redundant if the same (project, target, hash) appears as a cache")
		fmt.Fprintln(os.Stderr, "miss in more than one shard — work a shared remote cache would eliminate.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	reportPaths := fs.Args()
	if len(reportPaths) == 0 {
		return errors.New("magus config history dedup: pass at least one report path")
	}

	// Read every shard report into a flat miss list (tagged with its file), then
	// hand the pure cross-shard aggregation to ci.Dedup.
	var misses []ci.MissBuild
	for _, p := range reportPaths {
		matches, err := filepath.Glob(p)
		if err != nil {
			return fmt.Errorf("config history dedup: bad glob %q: %w", p, err)
		}
		if len(matches) == 0 {
			matches = []string{p}
		}
		for _, m := range matches {
			ms, err := readMisses(ctx, m)
			if err != nil {
				return fmt.Errorf("config history dedup: read %q: %w", m, err)
			}
			for _, miss := range ms {
				misses = append(misses, ci.MissBuild{
					Project: miss.project, Target: miss.target, Hash: miss.hash,
					DurationMs: miss.durationMs, File: m,
				})
			}
		}
	}

	res := ci.Dedup(misses)

	approxNote := ""
	if res.Approx {
		approxNote = " (approximate: some events missing hash field — upgrade magus to get exact dedup)"
	}

	fmt.Printf("cross-shard dedup analysis%s\n", approxNote)
	fmt.Printf("  report files:    %d\n", len(reportPaths))
	fmt.Printf("  total misses:    %d\n", res.TotalMisses)
	fmt.Printf("  unique keys:     %d\n", res.UniqueKeys)
	fmt.Printf("  redundant builds: %d\n", res.RedundantBuilds)
	fmt.Printf("  redundant time:  %dms (%.1fs)\n", res.RedundantMs, float64(res.RedundantMs)/1000)

	if len(res.Top) > 0 {
		limit := 10
		if len(res.Top) < limit {
			limit = len(res.Top)
		}
		fmt.Printf("\n  top %d redundant builds by wasted time:\n", limit)
		for _, e := range res.Top[:limit] {
			hashDisp := e.Hash
			if len(hashDisp) > 8 {
				hashDisp = hashDisp[:8]
			}
			fmt.Printf("    %s %s (%s): +%d builds, %dms wasted\n",
				e.Project, e.Target, hashDisp, e.ExtraBuilds, e.ExtraMs)
		}
	}

	return nil
}

// readMisses parses cache-miss events from one JSONL report file.
func readMisses(ctx context.Context, path string) ([]missBuild, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []missBuild
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &head); err != nil {
			continue
		}
		if head.Type != report.TypeTargetResult {
			continue
		}
		var ev report.TargetResult
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		// A "miss" is a fresh, successful run (not a cache replay) with a real duration.
		if ev.CacheHit || ev.Status != "ok" || ev.DurationMs <= 0 {
			continue
		}
		out = append(out, missBuild{
			project:    ev.Project,
			target:     ev.Target,
			hash:       ev.Hash,
			durationMs: ev.DurationMs,
		})
	}
	return out, sc.Err()
}
