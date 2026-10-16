//go:build retrievaleval

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
	configgen "github.com/egladman/magus/internal/config/gen"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/rankeval"
	"github.com/egladman/magus/internal/workspace"
)

func TestRetrievalEval(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	root, err := workspace.FindRoot("")
	require.NoError(t, err)
	judgments := readRetrievalJudgments(t, filepath.Join(root, "testdata", "retrieval", "judgments.jsonl"))

	cfg, err := config.LoadWithRoot("", root)
	require.NoError(t, err)
	require.NoError(t, configgen.ApplyEnv(&cfg, os.Getenv))
	// A running daemon may be another build; every read here measures this one.
	cfg.Server.Enabled = false
	globalCfg = cfg

	ctx := context.Background()
	m, err := magus.Open(ctx, root, workspace.WithLoadedConfig(cfg), magus.WithSpawnRule(magus.TierCeiling))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })
	ctx = withMagus(ctx, m)

	r := replayer{root: root, m: m, cfg: cfg, next: newNextGate(root), coverage: memoCoverage(ctx, m, cfg)}
	r.domain, err = loadWorkspaceGraph(ctx, m, cfg, false, false, false)
	require.NoError(t, err)
	r.symbols, err = loadWorkspaceGraph(ctx, m, cfg, false, false, true)
	require.NoError(t, err)
	r.known = make(map[string]string)
	for _, n := range r.symbols.Nodes() {
		r.known[n.ID] = n.Source
	}

	workers := runtime.GOMAXPROCS(0)
	start := time.Now()
	runs, err := rankeval.Replay(ctx, judgments, workers, r.replay)
	require.NoError(t, err)
	elapsed := time.Since(start)
	dropped := 0
	for _, run := range runs {
		dropped += run.Dropped
	}

	report := rankeval.Summarize(runs)
	fmt.Print(report.Text())
	fmt.Printf("dropped gold: %d\n", dropped)
	fmt.Printf("replayed %d judgments in %s on %d workers\n", len(runs), elapsed.Round(time.Millisecond), workers)
	printBaselineDeltas(t, filepath.Join(root, "testdata", "retrieval", "baseline.json"), report)
	if dest := os.Getenv("MAGUS_RETRIEVAL_EVAL_BASELINE"); dest != "" {
		writeRetrievalBaseline(t, root, dest, report)
	}
}

func readRetrievalJudgments(t *testing.T, file string) []rankeval.Judgment {
	t.Helper()
	f, err := os.Open(file)
	if errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s does not exist; harvest it with ./magus buzz hack/retrieval/harvest.buzz <holdout-after date>", file)
	}
	require.NoError(t, err)
	defer f.Close()
	judgments, err := rankeval.ReadJudgments(f)
	require.NoError(t, err)
	return judgments
}

func printBaselineDeltas(t *testing.T, file string, now rankeval.Report) {
	t.Helper()
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Printf("no baseline at %s; nothing to compare against\n", file)
		return
	}
	require.NoError(t, err)
	var base rankeval.Report
	require.NoError(t, json.Unmarshal(data, &base), "decode %s", file)
	deltas := rankeval.Compare(base, now)
	fmt.Printf("against %s: %d metric(s) moved\n", file, len(deltas))
	for _, d := range deltas {
		fmt.Printf("  %s %s: %.3f -> %.3f\n", d.Slice, d.Metric, d.Base, d.Now)
	}
}

// writeRetrievalBaseline writes now for a person to review and commit as the baseline. A
// relative dest is from the workspace root, since go test runs in the package dir.
func writeRetrievalBaseline(t *testing.T, root, dest string, now rankeval.Report) {
	t.Helper()
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(root, dest)
	}
	data, err := json.MarshalIndent(now, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(dest, append(data, '\n'), 0o644))
	fmt.Printf("wrote this report to %s\n", dest)
}
