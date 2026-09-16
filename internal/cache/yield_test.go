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

// A failing run still executed, so it still proves the cache did not replay; otherwise a
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

func exec(project, target, text string) string {
	return fmt.Sprintf(`{"kind":"exec","project":%q,"target":%q,"text":%q}`, project, target, text)
}

// TestStalledTargetsNeedsTheSameWorkTwice pins the condition the finding rests on. Never
// replaying is evidence of a broken footprint only when the runs WERE the same work; a
// different command is supposed to miss, and counting those as misses accuses a target of
// a footprint the journal says nothing about.
//
// Measured on this repo: go-test reported 43 runs and 0 replays, which was twenty distinct
// `-run` filters, most of them carrying `-count=1`.
func TestStalledTargetsNeedsTheSameWorkTwice(t *testing.T) {
	t.Parallel()

	t.Run("one command run many times is still the finding", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		recs := repeatResult("docs", "generate", "pass", 80_000, MinRunsForYield)
		recs = append(recs, exec("docs", "generate", "go test ./..."))
		writeJournal(t, dir, "a.jsonl", recs...)

		require.Len(t, StalledTargets(dir, nil), 1,
			"identical work that never replays is what this check is for")
	})

	t.Run("two different commands are not evidence of anything", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		recs := repeatResult("docs", "generate", "pass", 80_000, MinRunsForYield)
		recs = append(recs,
			exec("docs", "generate", "go test ./... -run TestOne"),
			exec("docs", "generate", "go test ./... -run TestTwo"))
		writeJournal(t, dir, "a.jsonl", recs...)

		require.Empty(t, StalledTargets(dir, nil),
			"a different command SHOULD miss, so the misses say nothing about the footprint")
	})

	t.Run("a journal with no exec record still reports", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeJournal(t, dir, "a.jsonl", repeatResult("docs", "generate", "pass", 80_000, MinRunsForYield)...)

		require.Len(t, StalledTargets(dir, nil), 1,
			"a target that execs nothing observable must keep the behaviour it had")
	})
}

func resultKeyed(project, target, status, key string, durMs int) string {
	return fmt.Sprintf(`{"kind":"result","project":%q,"target":%q,"status":%q,"cache_key":%q,"dur_ms":%d}`,
		project, target, status, key, durMs)
}

// TestStalledTargetsNeedsTheSameKeyTwice is the command test one level down. Runs under
// DIFFERENT keys mean the inputs moved, so every miss was correct and the cache is working;
// only a key that repeats and still never replays accuses the footprint of anything.
//
// Measured on this repo: two generators reported 10 runs and 0 replays across ten edits to
// the Go sources they read, which is an ordinary edit-and-rebuild session.
func TestStalledTargetsNeedsTheSameKeyTwice(t *testing.T) {
	t.Parallel()

	t.Run("one key run many times is still the finding", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		recs := make([]string, 0, MinRunsForYield)
		for range MinRunsForYield {
			recs = append(recs, resultKeyed("docs", "generate", "pass", "same-key", 80_000))
		}
		writeJournal(t, dir, "a.jsonl", recs...)

		require.Len(t, StalledTargets(dir, nil), 1,
			"identical inputs that never replay is exactly what this check is for")
	})

	t.Run("a key that moves every run is the cache working", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		recs := make([]string, 0, MinRunsForYield)
		for i := range MinRunsForYield {
			recs = append(recs, resultKeyed("docs", "generate", "pass", fmt.Sprintf("key-%d", i), 80_000))
		}
		writeJournal(t, dir, "a.jsonl", recs...)

		require.Empty(t, StalledTargets(dir, nil),
			"the inputs moved, so the misses were correct and say nothing about the footprint")
	})

	t.Run("a journal with no key recorded keeps the old behaviour", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeJournal(t, dir, "a.jsonl", repeatResult("docs", "generate", "pass", 80_000, MinRunsForYield)...)

		require.Len(t, StalledTargets(dir, nil), 1,
			"a journal written before results carried a key must not silently stop reporting")
	})
}
