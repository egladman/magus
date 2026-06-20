package project

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func day(d int) time.Time { return time.Date(2026, 6, d, 12, 0, 0, 0, time.UTC) }

// scanFixture builds a small attributed scan across two projects.
func scanFixture() []ScannedCommit {
	return []ScannedCommit{
		{Author: "ada", Date: day(1), Files: []string{"api/a.go", "api/b.go"}, Projects: []string{"api"}},
		{Author: "lin", Date: day(3), Files: []string{"api/a.go", "web/studio/y.go"}, Projects: []string{"api", "web/studio"}},
		{Author: "ada", Date: day(5), Files: []string{"web/studio/y.go"}, Projects: []string{"web/studio"}},
	}
}

func TestProjectStats(t *testing.T) {
	stats := ProjectStats(scanFixture())
	assert.Equal(t, ProjectStat{Commits: 2, Authors: 2, Last: day(3)}, stats["api"])
	assert.Equal(t, ProjectStat{Commits: 2, Authors: 2, Last: day(5)}, stats["web/studio"])
}

func TestFileHotspots(t *testing.T) {
	// Constant complexity isolates the churn ranking: api/a.go (2 commits) leads.
	out := FileHotspots(scanFixture(), func(string) int { return 10 })
	require.NotEmpty(t, out)
	assert.Equal(t, "api/a.go", out[0].Path)
	assert.Equal(t, 2, out[0].Commits)
	assert.Equal(t, 10, out[0].Complexity)
	assert.Equal(t, 20, out[0].Score)
	assert.Equal(t, 2, out[0].Authors)
}

func TestAffinity(t *testing.T) {
	out := Affinity(scanFixture())
	require.Len(t, out, 1)
	assert.Equal(t, types.CoChange{A: "api", B: "web/studio", Count: 1}, out[0])
}

func TestOwnership(t *testing.T) {
	out := Ownership(scanFixture(), day(4))
	byPath := map[string]types.Ownership{}
	for _, o := range out {
		byPath[o.Path] = o
	}
	api := byPath["api"]
	assert.Equal(t, 2, api.Authors)
	assert.Equal(t, 50, api.PrimaryShare, "ada and lin each have one of api's two commits")
	assert.False(t, api.BusFactor1)
	assert.True(t, api.Stale, "api's last commit (day 3) predates the day-4 cutoff")
	assert.False(t, byPath["web/studio"].Stale)
}

func TestTrend(t *testing.T) {
	// Span is day1..day5, midpoint day3; api is earlier-weighted, web/studio rising.
	out := Trend(scanFixture())
	byPath := map[string]types.Trend{}
	for _, tr := range out {
		byPath[tr.Path] = tr
	}
	assert.LessOrEqual(t, byPath["api"].Delta, 0, "api is flat or cooling")
	assert.Positive(t, byPath["web/studio"].Delta, "web/studio is rising")
}

func TestParseSince(t *testing.T) {
	empty, err := parseSince("")
	require.NoError(t, err)
	assert.Equal(t, "", empty, "no window means no lower bound")

	got, err := parseSince("7d")
	require.NoError(t, err)
	parsed, err := time.Parse(time.RFC3339, got)
	require.NoError(t, err, "7d yields an RFC3339 cutoff")
	assert.WithinDuration(t, time.Now().Add(-7*24*time.Hour), parsed, time.Minute)

	for _, bad := range []string{"d", "10", "5x", "abc"} {
		_, err := parseSince(bad)
		assert.Errorf(t, err, "%q should be rejected", bad)
	}
}

func TestComplexity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.go")
	require.NoError(t, os.WriteFile(path, []byte("package x\n\nfunc f() {\n    return\n}\n"), 0o644))
	// non-blank lines: package x(1), func f() {(1), `    return`(1+1 indent), }(1) = 5
	assert.Equal(t, 5, Complexity(path))
	assert.Zero(t, Complexity(filepath.Join(dir, "missing.go")), "unreadable file scores 0")
}
