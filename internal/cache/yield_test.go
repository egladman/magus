package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// journal writes one invocation journal holding the given result records.
func writeJournal(t *testing.T, cacheDir, name string, recs ...string) string {
	t.Helper()
	runs := filepath.Join(cacheDir, RunsDir)
	require.NoError(t, os.MkdirAll(runs, 0o755))
	path := filepath.Join(runs, name)
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(recs, "\n")+"\n"), 0o600))
	return path
}

func result(project, target, status string, durMs int) string {
	return fmt.Sprintf(`{"kind":"result","project":%q,"target":%q,"status":%q,"dur_ms":%d}`,
		project, target, status, durMs)
}

func repeatResult(project, target, status string, durMs, n int) []string {
	out := make([]string, 0, n)
	for range n {
		out = append(out, result(project, target, status, durMs))
	}
	return out
}

func TestStalledTargetsFlagsNeverCached(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeJournal(t, dir, "a.jsonl", repeatResult("docs", "generate", "pass", 80_000, MinRunsForYield)...)

	got := StalledTargets(dir, nil)

	require.Len(t, got, 1)
	require.Equal(t, "docs", got[0].Project)
	require.Equal(t, MinRunsForYield, got[0].Runs)
	require.Equal(t, int64(80_000), got[0].AvgMs())
}

// One replay proves the cache works for that target; the remaining misses are ordinary
// edit-rebuild cycles and are none of this check's business.
func TestStalledTargetsIgnoresTargetThatEverCached(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	recs := repeatResult("docs", "generate", "pass", 80_000, MinRunsForYield)
	recs = append(recs, result("docs", "generate", "cached", 12))
	writeJournal(t, dir, "a.jsonl", recs...)

	require.Empty(t, StalledTargets(dir, nil))
}

func TestStalledTargetsStaysQuietBelowThresholds(t *testing.T) {
	t.Parallel()

	t.Run("too few runs to be evidence", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeJournal(t, dir, "a.jsonl", repeatResult("docs", "generate", "pass", 80_000, MinRunsForYield-1)...)
		require.Empty(t, StalledTargets(dir, nil))
	})

	t.Run("cheap enough not to matter", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeJournal(t, dir, "a.jsonl", repeatResult("libs", "fmt", "pass", 50, MinRunsForYield*4)...)
		require.Empty(t, StalledTargets(dir, nil))
	})

	t.Run("no history at all", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, StalledTargets(t.TempDir(), nil))
	})
}

// A failing run still executed, so it still proves the cache did not replay - otherwise a
// target that is both broken and uncacheable would hide behind its own failures.
func TestStalledTargetsCountsFailuresAsExecutions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeJournal(t, dir, "a.jsonl", repeatResult("api", "test", "fail", 30_000, MinRunsForYield)...)

	require.Len(t, StalledTargets(dir, nil), 1)
}

// Journals are append-only and a killed run leaves a truncated final line. One bad line
// must not discard the rest of the file.
func TestStalledTargetsSkipsUnparsableLines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	recs := append([]string{`{"kind":"result","project":"docs","tar`},
		repeatResult("docs", "generate", "pass", 80_000, MinRunsForYield)...)
	writeJournal(t, dir, "a.jsonl", recs...)

	require.Len(t, StalledTargets(dir, nil), 1)
}

func TestStalledTargetsRanksWorstWallClockFirst(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	recs := repeatResult("small", "build", "pass", 3_000, MinRunsForYield)
	recs = append(recs, repeatResult("big", "generate", "pass", 90_000, MinRunsForYield)...)
	writeJournal(t, dir, "a.jsonl", recs...)

	got := StalledTargets(dir, nil)

	require.Len(t, got, 2)
	require.Equal(t, "big", got[0].Project, "the expensive one is what to fix today")
}

// The `only` filter is what keeps the per-run check affordable: it must actually narrow.
func TestStalledTargetsHonorsOnlyFilter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	recs := repeatResult("a", "build", "pass", 9_000, MinRunsForYield)
	recs = append(recs, repeatResult("b", "build", "pass", 9_000, MinRunsForYield)...)
	writeJournal(t, dir, "a.jsonl", recs...)

	got := StalledTargets(dir, map[string]bool{"b\x00build": true})

	require.Len(t, got, 1)
	require.Equal(t, "b", got[0].Project)
}

func TestSlowExecutionsSelectsOnlySlowRealRuns(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeJournal(t, dir, "a.jsonl",
		result("slow", "build", "pass", 9_000),
		result("quick", "build", "pass", 100),
		result("replayed", "build", "cached", 90_000),
	)

	got := SlowExecutions(path, slowThresholdForTest)

	require.Equal(t, map[string]bool{"slow\x00build": true}, got,
		"a cached replay proves the cache works, so it is never a candidate")
}

const slowThresholdForTest = 5_000
