package main

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

// TestDescribeWorkspacesOutput_MultiDeclared verifies that `describe workspaces`
// enumerates every declared daemon workspace, not just the active one.
func TestDescribeWorkspacesOutput_MultiDeclared(t *testing.T) {
	base := t.TempDir()
	mkWorkspace := func(name string) string {
		dir := filepath.Join(base, name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		// An empty magusfile.buzz marks the directory as a workspace root.
		require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), nil, 0o644))
		return dir
	}
	wsA, wsB := mkWorkspace("a"), mkWorkspace("b")

	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })
	globalCfg = config.Config{}
	globalCfg.Daemon.Workspaces = []string{wsA, wsB}

	out, err := describeWorkspacesOutput(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.NotEqual(t, out[1].Root, out[0].Root, "expected two distinct workspace roots")
}

// TestNewTargetCacheLastRun covers the four verdicts `describe target --cache` can reach
// about the last run recorded here. Every explanation has to name the mechanism behind
// the verdict and the next command, since that section is often the only thing a reader
// takes away from a miss.
func TestNewTargetCacheLastRun(t *testing.T) {
	t.Parallel()
	recorded := []string{"keyVersion:3", "target:build", "src:api/main.go:aaa:0", "tool:go:version=1.25.0"}

	t.Run("nothing recorded", func(t *testing.T) {
		t.Parallel()
		got := newTargetCacheLastRun("api", "build", cache.RecordedRun{}, fs.ErrNotExist, "cafe", nil)
		assert.False(t, got.Recorded)
		assert.Empty(t, got.Ref, "no entry, so nothing to point at")
		assert.Contains(t, got.Explanation, "nothing to compare against")
		assert.Contains(t, got.Explanation, "magus run build api")
	})

	t.Run("matches", func(t *testing.T) {
		t.Parallel()
		rec := cache.RecordedRun{Key: "cafe", KeyInputs: recorded}
		got := newTargetCacheLastRun("api", "build", rec, nil, "cafe", recorded)
		assert.True(t, got.Recorded)
		assert.True(t, got.Matches)
		assert.Equal(t, 0, got.Differences)
		assert.Nil(t, got.First)
		assert.Contains(t, got.Explanation, "replays that entry")
	})

	t.Run("names the first differing input", func(t *testing.T) {
		t.Parallel()
		rec := cache.RecordedRun{Key: "cafe", KeyInputs: recorded}
		live := []string{"keyVersion:3", "target:build", "src:api/main.go:bbb:0", "tool:go:version=1.26.0"}
		got := newTargetCacheLastRun("api", "build", rec, nil, "f00d", live)
		assert.False(t, got.Matches)
		assert.Equal(t, 2, got.Differences)
		require.NotNil(t, got.First)
		assert.Equal(t, "src:api/main.go", got.First.Input, "the earliest class in live key order leads")
		assert.Contains(t, got.Explanation, "MISSES")
		assert.Contains(t, got.Explanation, "hash of these inputs")
		assert.Contains(t, got.Explanation, "magus describe target build api --cache --inputs")
	})

	t.Run("entry predates key inputs", func(t *testing.T) {
		t.Parallel()
		got := newTargetCacheLastRun("api", "build", cache.RecordedRun{Key: "cafe"}, nil, "f00d", recorded)
		assert.True(t, got.Recorded)
		assert.False(t, got.Matches)
		assert.Nil(t, got.First, "there is no recorded line to blame")
		assert.Contains(t, got.Explanation, "predates key-input persistence")
		assert.Contains(t, got.Explanation, "magus run build api")
	})
}

// TestLastRunLines pins the text section's shape: one header line carrying the verdict,
// the recorded and live values of the first differing input, and the explanation last.
func TestLastRunLines(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)

	got := lastRunLines(targetCacheLastRun{Explanation: "nothing recorded."})
	assert.Equal(t, []string{"  last recorded run: none", "    nothing recorded."}, got)

	got = lastRunLines(targetCacheLastRun{Recorded: true, Ref: "mgs_abc123", At: at, Matches: true, Explanation: "replays."})
	assert.Equal(t, []string{"  last recorded run: 2026-08-20T10:00:00Z  mgs_abc123  MATCHES", "    replays."}, got)

	got = lastRunLines(targetCacheLastRun{
		Recorded:    true,
		Ref:         "mgs_abc123",
		At:          at,
		Differences: 1,
		First:       &cache.KeyInputChange{Class: "src", Input: "src:api/main.go", Recorded: "aaa:0", Live: "bbb:0"},
		Explanation: "misses.",
	})
	assert.Equal(t, []string{
		"  last recorded run: 2026-08-20T10:00:00Z  mgs_abc123  DIFFERS",
		"    first differing input: src:api/main.go",
		"      - recorded  aaa:0",
		"      + live      bbb:0",
		"    misses.",
	}, got)
}

// TestLastRunLinesMarksAbsence: a class with no value slot differs only by appearing or
// disappearing, and the section must say which side is missing rather than print a blank.
func TestLastRunLinesMarksAbsence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		first cache.KeyInputChange
		want  []string
	}{
		{
			name:  "added",
			first: cache.KeyInputChange{Class: "charm", Input: "charm:rw", RecordedAbsent: true},
			want:  []string{"      - recorded  (absent)", "      + live      (present)"},
		},
		{
			name:  "dropped",
			first: cache.KeyInputChange{Class: "dep", Input: "dep:libs/x", LiveAbsent: true},
			want:  []string{"      - recorded  (present)", "      + live      (absent)"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := lastRunLines(targetCacheLastRun{Recorded: true, Differences: 1, First: &tc.first, Explanation: "x"})
			assert.Contains(t, got, tc.want[0])
			assert.Contains(t, got, tc.want[1])
		})
	}
}

// TestClaimLabel pins the one spelling `describe file` prints a claim's declarer
// as: the path:target ref every other magus command accepts, so a reader can run
// the target that wrote the file without translating anything.
func TestClaimLabel(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		claim types.FileClaim
		want  string
	}{
		{name: "per-target glob", claim: types.FileClaim{Project: "docs", Target: "generate"}, want: "docs:generate"},
		{name: "project-wide glob", claim: types.FileClaim{Project: "docs"}, want: "docs"},
		{name: "root project", claim: types.FileClaim{Project: ".", Target: "generate"}, want: ".:generate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, claimLabel(tc.claim))
		})
	}
}
