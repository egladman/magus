package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// writeJournal fabricates the run journal StalledTargets reads: one JSONL record per
// target result, with "pass" meaning it executed and "cached" meaning it replayed. root
// is the workspace root, so the journal lands under .magus/runs where runner.cacheDir
// looks for it.
func writeJournal(t *testing.T, root string, records []map[string]any) {
	t.Helper()
	dir := filepath.Join(root, ".magus", cache.RunsDir)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	var b strings.Builder
	for _, rec := range records {
		line, err := json.Marshal(rec)
		require.NoError(t, err)
		b.Write(line)
		b.WriteByte('\n')
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "run-1.jsonl"), []byte(b.String()), 0o644))
}

func ranNTimes(project, target string, n int) []map[string]any {
	out := make([]map[string]any, 0, n)
	for range n {
		out = append(out, map[string]any{
			"kind": "result", "project": project, "target": target,
			"status": "pass", "dur_ms": 3000, // above MinAvgMsForYield
		})
	}
	return out
}

// TestCacheYieldExemptsSkipCacheTargets is the MGS1009 fix. A drift gate declares
// skip_cache precisely so it always runs, so "0 cached" is the designed outcome and
// reporting it fails a workspace for being correct.
func TestCacheYieldExemptsSkipCacheTargets(t *testing.T) {
	dir := t.TempDir()
	writeJournal(t, dir, ranNTimes(".", "generate:rw", 12))

	projects := []*types.Project{{
		Path: ".",
		TargetPolicies: map[string]types.Target{
			"generate": {SkipCache: true, SkipCacheReason: "is the drift gate itself"},
		},
	}}
	r := &runner{root: dir, ws: stubWorkspace{}}

	got := r.checkCacheYield(projects)

	assert.Equal(t, Check{
		Name:    "cache yield",
		Status:  StatusOK,
		Message: "no target is running uncached (1 declared skip_cache)",
	}, got, "the charm suffix must not defeat the policy lookup, and the exemption stays visible")
}

// TestCacheYieldStillReportsUndeclaredTargets keeps the teeth: a target that never
// replays and never claimed it would is the finding the check exists for.
func TestCacheYieldStillReportsUndeclaredTargets(t *testing.T) {
	dir := t.TempDir()
	records := append(ranNTimes(".", "generate:rw", 12), ranNTimes("docs", "build", 9)...)
	writeJournal(t, dir, records)

	projects := []*types.Project{{
		Path:           ".",
		TargetPolicies: map[string]types.Target{"generate": {SkipCache: true, SkipCacheReason: "drift gate"}},
	}, {Path: "docs"}}
	r := &runner{root: dir, ws: stubWorkspace{}}

	got := r.checkCacheYield(projects)

	require.Equal(t, StatusFail, got.Status)
	require.Len(t, got.Details, 2, "one finding plus the advice line")
	assert.Contains(t, got.Details[0], "docs build: 9 runs, 0 cached")
	assert.NotContains(t, strings.Join(got.Details, "\n"), "generate",
		"the skip_cache target is excluded, not merely sorted below")
}

// TestCacheYieldAdviceNamesBothCauses pins the wording. The old line asserted a wide
// footprint, which is wrong for a version-stamped binary: go-build embeds `git describe`
// and the commit hash, so every commit mints a new key and no footprint change fixes it.
func TestCacheYieldAdviceNamesBothCauses(t *testing.T) {
	dir := t.TempDir()
	writeJournal(t, dir, ranNTimes(".", "go-build:rw", 9))

	r := &runner{root: dir, ws: stubWorkspace{}}
	got := r.checkCacheYield(nil)

	require.Equal(t, StatusFail, got.Status)
	advice := got.Details[len(got.Details)-1]
	assert.Contains(t, advice, "wider than it reads", "the footprint cause")
	assert.Contains(t, advice, "volatile state", "the version-stamp cause")
}
