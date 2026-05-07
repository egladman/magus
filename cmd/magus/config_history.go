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
	"sort"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/report"
)

func configHistoryCmd(ctx context.Context, root string, cfg config.Config, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprintln(os.Stderr, "Usage: magus config history <subcommand>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Subcommands:")
		fmt.Fprintln(os.Stderr, "  import   merge runtime-history JSON files into the history (e.g. per-shard CI histories)")
		fmt.Fprintln(os.Stderr, "  dedup    measure cross-shard redundant builds from per-shard JSONL report files")
		return flag.ErrHelp
	}

	sub, rest := args[0], args[1:]
	switch sub {
	case "import":
		return runHistoryImport(ctx, cfg, rest)
	case "dedup":
		return runHistoryDedup(ctx, rest)
	default:
		return fmt.Errorf("magus config history: unknown subcommand %q (choose: import, dedup)", sub)
	}
}

// runHistoryImport folds one or more runtime-history JSON files into --history
// (created if absent), per (project, target) freshest-wins. Passing several files
// merges them — how the per-shard histories of one sharded CI run combine into the
// single history the next run's forecaster and flake detector read. Each input is
// the history `magus run` wrote; there is no separate "merge" mode.
func runHistoryImport(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("config history import", flag.ContinueOnError)
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
// redundant builds: when the same (project, target, hash) is a cache miss on
// more than one shard, those extra builds are waste that a shared remote cache
// would eliminate. Outputs a summary including total redundant build-seconds.
func runHistoryDedup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("config history dedup", flag.ContinueOnError)
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

	// Per hash: list of misses (one per shard that built it, keyed by file path
	// to avoid double-counting within a single shard's report).
	type dedupKey struct {
		project string
		target  string
		hash    string
	}
	type shardMiss struct {
		durationMs int64
		file       string
	}
	byKey := make(map[dedupKey][]shardMiss)

	var totalMisses int

	for _, p := range reportPaths {
		matches, err := filepath.Glob(p)
		if err != nil {
			return fmt.Errorf("config history dedup: bad glob %q: %w", p, err)
		}
		if len(matches) == 0 {
			matches = []string{p}
		}
		for _, m := range matches {
			misses, err := readMisses(ctx, m)
			if err != nil {
				return fmt.Errorf("config history dedup: read %q: %w", m, err)
			}
			totalMisses += len(misses)
			for _, miss := range misses {
				k := dedupKey{miss.project, miss.target, miss.hash}
				byKey[k] = append(byKey[k], shardMiss{miss.durationMs, m})
			}
		}
	}

	// Count redundant builds: for each key with N>1 misses across different files,
	// (N-1) builds were redundant. If hash is empty (old reports without Hash field),
	// fall back to (project,target) grouping but flag it as approximate.
	var (
		redundantBuilds int
		redundantMs     int64
		approx          bool
	)
	type topEntry struct {
		key         dedupKey
		extraBuilds int
		extraMs     int64
	}
	var top []topEntry

	for k, misses := range byKey {
		if k.hash == "" {
			approx = true
		}
		// Deduplicate within a single file (a target can appear once per shard run).
		seen := make(map[string]int64, len(misses))
		for _, sm := range misses {
			if _, ok := seen[sm.file]; !ok {
				seen[sm.file] = sm.durationMs
			}
		}
		if len(seen) <= 1 {
			continue
		}
		// First build is necessary; rest are redundant.
		extra := len(seen) - 1
		var maxMs int64
		var totalMs int64
		for _, d := range seen {
			totalMs += d
			if d > maxMs {
				maxMs = d
			}
		}
		// Redundant ms = total - max (keep the longest as the "necessary" one).
		extraMs := totalMs - maxMs
		redundantBuilds += extra
		redundantMs += extraMs
		top = append(top, topEntry{k, extra, extraMs})
	}

	sort.Slice(top, func(i, j int) bool { return top[i].extraMs > top[j].extraMs })

	approxNote := ""
	if approx {
		approxNote = " (approximate: some events missing hash field — upgrade magus to get exact dedup)"
	}

	fmt.Printf("cross-shard dedup analysis%s\n", approxNote)
	fmt.Printf("  report files:    %d\n", len(reportPaths))
	fmt.Printf("  total misses:    %d\n", totalMisses)
	fmt.Printf("  unique keys:     %d\n", len(byKey))
	fmt.Printf("  redundant builds: %d\n", redundantBuilds)
	fmt.Printf("  redundant time:  %dms (%.1fs)\n", redundantMs, float64(redundantMs)/1000)

	if len(top) > 0 {
		limit := 10
		if len(top) < limit {
			limit = len(top)
		}
		fmt.Printf("\n  top %d redundant builds by wasted time:\n", limit)
		for _, e := range top[:limit] {
			hashDisp := e.key.hash
			if len(hashDisp) > 8 {
				hashDisp = hashDisp[:8]
			}
			fmt.Printf("    %s %s (%s): +%d builds, %dms wasted\n",
				e.key.project, e.key.target, hashDisp, e.extraBuilds, e.extraMs)
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
		if err := codec.Unmarshal(line, &head); err != nil {
			continue
		}
		if head.Type != report.TypeCacheMiss {
			continue
		}
		var ev report.CacheMiss
		if err := codec.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.DurationMs <= 0 {
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
