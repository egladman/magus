package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/ci"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/libs/clikit/exit"
)

func configHistoryUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus config history <subcommand>")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Subcommands:")
	fmt.Fprintln(os.Stderr, "  import   merge runtime-history JSON files into the history (e.g. per-shard CI histories)")
	fmt.Fprintln(os.Stderr, "  passed   record the commit a ref last completed a fully passing run at")
	fmt.Fprintln(os.Stderr, "  dedup    measure cross-shard redundant builds from per-shard JSONL report files")
}

func configHistoryCommand(ctx context.Context, _ string, cfg config.Config, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		configHistoryUsage()
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
		return exit.Usagef("magus config history: unknown subcommand %q (choose: import, passed, dedup)", sub)
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
	hf := flaggen.BindConfigHistoryPassed(fs, flaggen.ConfigHistoryPassedDefaults{
		History: cfg.HistoryPath,
		Status:  string(forecast.RunPassed),
	})
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
		printOwnDefaults(fs)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if hf.History == "" {
		return errors.New("magus config history passed: --history is required (or set history_path in magus.yaml)")
	}
	if hf.Ref == "" || hf.Commit == "" {
		return errors.New("magus config history passed: --ref and --commit are both required")
	}
	st := forecast.RunStatus(hf.Status)
	if st != forecast.RunPassed && st != forecast.RunFailed {
		return exit.Usagef("magus config history passed: --status must be %q or %q, got %q",
			forecast.RunPassed, forecast.RunFailed, hf.Status)
	}
	// Skipped, not failed. A workspace that turned the run log off wired this step in
	// anyway (it is inside the shipped action), and erroring would paint every run red
	// for honoring its own configuration.
	if !cfg.CI.RecordRuns {
		if opts.Format != outputText {
			return emitRecord(opts, report.PassedRecord{Ref: hf.Ref, Commit: hf.Commit, Target: hf.Target, Status: string(st), HistoryPath: hf.History}, []string{hf.Ref})
		}
		slog.InfoContext(ctx, "config history passed: run log disabled by ci.record_runs; recording nothing",
			slog.String("ref", hf.Ref), slog.String("commit", hf.Commit))
		return nil
	}

	err = report.RecordPassed(ctx, hf.History, forecast.Run{Commit: hf.Commit, Ref: hf.Ref, Target: hf.Target, Status: st}, time.Now())
	if err != nil {
		return err
	}
	if opts.Format != outputText {
		return emitRecord(opts, report.PassedRecord{Recorded: true, Ref: hf.Ref, Commit: hf.Commit, Target: hf.Target, Status: string(st), HistoryPath: hf.History}, []string{hf.Ref})
	}
	slog.InfoContext(ctx, "config history run recorded",
		slog.String("ref", hf.Ref),
		slog.String("commit", hf.Commit),
		slog.String("target", hf.Target),
		slog.String("status", string(st)),
		slog.String("history_path", hf.History))
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
	imf := flaggen.BindConfigHistoryImport(fs, flaggen.ConfigHistoryImportDefaults{History: cfg.HistoryPath})
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config history import [--history <path>] <history.jsonl>...")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Merge runtime-history JSON files into the history (created if absent). For each")
		fmt.Fprintln(os.Stderr, "(project, target) the freshest entry wins, so the per-shard histories one sharded")
		fmt.Fprintln(os.Stderr, "CI run produces combine into a single history. Inputs may be globs.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		printOwnDefaults(fs)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if imf.History == "" {
		return errors.New("magus config history import: --history is required (or set history_path in magus.yaml)")
	}
	inputs := fs.Args()
	if len(inputs) == 0 {
		return errors.New("magus config history import: pass at least one history file")
	}

	files, projects, err := report.ImportHistories(ctx, imf.History, inputs)
	if err != nil {
		return fmt.Errorf("config history import: %w", err)
	}
	if opts.Format != outputText {
		return emitRecord(opts, report.HistoryImportRecord{Files: files, Projects: projects, HistoryPath: imf.History}, []string{imf.History})
	}
	slog.InfoContext(ctx, "config history import complete",
		slog.Int("files", files),
		slog.Int("projects", projects),
		slog.String("history_path", imf.History))
	return nil
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
		fmt.Fprintln(os.Stderr, "miss in more than one shard - work a shared remote cache would eliminate.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		printOwnDefaults(fs)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	reportPaths := fs.Args()
	if len(reportPaths) == 0 {
		return errors.New("magus config history dedup: pass at least one report path")
	}

	events, err := report.ReadMissFiles(ctx, reportPaths)
	if err != nil {
		return fmt.Errorf("config history dedup: %w", err)
	}
	misses := make([]ci.MissBuild, 0, len(events))
	for _, e := range events {
		misses = append(misses, ci.MissBuild{Project: e.Project, Target: e.Target, Hash: e.Hash, DurationMs: e.DurationMs, File: e.File})
	}
	res := ci.Dedup(misses)

	approxNote := ""
	if res.Approx {
		approxNote = " (approximate: some events missing hash field - upgrade magus to get exact dedup)"
	}

	if opts.Format != outputText {
		rec := report.DedupRecord{
			ReportFiles: len(reportPaths), TotalMisses: res.TotalMisses, UniqueKeys: res.UniqueKeys,
			RedundantBuilds: res.RedundantBuilds, RedundantMs: res.RedundantMs, Approximate: res.Approx,
			Top: []report.DedupItem{},
		}
		names := []string{}
		for _, e := range res.Top {
			rec.Top = append(rec.Top, report.DedupItem{Project: e.Project, Target: e.Target, Hash: e.Hash, ExtraBuilds: e.ExtraBuilds, ExtraMs: e.ExtraMs})
			names = append(names, fmt.Sprintf("%s:%s", e.Project, e.Target))
		}
		return emitRecord(opts, rec, names)
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
