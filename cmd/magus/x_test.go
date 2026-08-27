package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/types"
)

func mkProjects(paths ...string) []*types.Project {
	out := make([]*types.Project, len(paths))
	for i, p := range paths {
		out[i] = &types.Project{Path: p, Dir: "/tmp/" + p}
	}
	return out
}

func paths(in []interactive.ScoredProject) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = s.P.Path
	}
	return out
}

func TestScoreProjects_LeafBeatsParent(t *testing.T) {
	all := mkProjects(
		"apps/web/dashboard",
		"apps/dashboard-deprecated/foo",
	)
	got := paths(interactive.ScoreProjects(all, []string{"dash"}))
	require.NotEmpty(t, got)
	assert.Equal(t, "apps/web/dashboard", got[0], "expected leaf-match first")
}

func TestScoreProjects_PrefixBeatsInfix(t *testing.T) {
	all := mkProjects(
		"apps/web/my-dashboard",
		"apps/web/dashboard",
	)
	got := paths(interactive.ScoreProjects(all, []string{"dash"}))
	require.NotEmpty(t, got)
	assert.Equal(t, "apps/web/dashboard", got[0], "expected prefix-on-leaf first")
}

func TestScoreProjects_AND(t *testing.T) {
	all := mkProjects(
		"apps/web/dashboard",
		"apps/mobile/dashboard",
		"services/api",
	)
	got := paths(interactive.ScoreProjects(all, []string{"dash", "mobile"}))
	assert.Equal(t, []string{"apps/mobile/dashboard"}, got)
}

func TestScoreProjects_NoFilters(t *testing.T) {
	all := mkProjects("c", "a", "b")
	got := paths(interactive.ScoreProjects(all, nil))
	assert.Equal(t, []string{"a", "b", "c"}, got, "expected alphabetical")
}

func TestScoreProjects_NoMatchEmpty(t *testing.T) {
	all := mkProjects("apps/web/dashboard")
	got := interactive.ScoreProjects(all, []string{"zzznope"})
	assert.Empty(t, got)
}

func TestLeafScore_QueryNotInPath(t *testing.T) {
	assert.Equal(t, 0, interactive.LeafScore("apps/web/dashboard", "zzz"), "non-matching query should score 0")
}

func TestLeafScore_DenserOnShorterLeaf(t *testing.T) {
	short := interactive.LeafScore("apps/web/dash", "dash")
	long := interactive.LeafScore("apps/web/dashboard", "dash")
	assert.Greater(t, short, long, "denser match should beat sparser")
}

// outputRefShape decides which of x's two modes an argument selects, so a filter
// mistaken for a ref would skip the picker and a ref mistaken for a filter would hit
// the TTY gate and be refused - the bug that made `magus x <ref>` report "requires an
// interactive terminal" before this existed.
func TestOutputRefShapeSelectsTheReproduceMode(t *testing.T) {
	for _, ref := range []string{
		"out13f5a539577d",
		"out1a2b3c4d",
		"out0ac012f97486fe219811a2b08f8155378632ac17f42d2f6dfee3caeda24c8151",
	} {
		assert.True(t, outputRefShape.MatchString(ref), "%q is a ref and must reproduce", ref)
	}

	// Project filters, target names and near-misses all stay on the picker path.
	for _, filter := range []string{
		"out",          // the bare prefix names no ref
		"outputs",      // a plausible project directory
		"out13F5A539",  // refs are lower-case hex
		"outbound/api", // a path that merely starts with the prefix
		"build",        // an ordinary target
		"",             // no argument at all
		"out13f5a5",    // shorter than the minimum a ref carries
	} {
		assert.False(t, outputRefShape.MatchString(filter), "%q is a filter and must reach the picker", filter)
	}
}

// elide keeps MGS8004's columns aligned. Trimming from the LEFT is the point: a
// describe-style version differs in its tail, so a right-trim would render two
// different versions as the same string.
func TestElideKeepsTheDistinguishingTail(t *testing.T) {
	assert.Equal(t, "short", elide("short"))

	a := elide("backup/pre-frosty-improve-terminal-6-g594d75a08-dirty")
	b := elide("backup/pre-frosty-improve-terminal-8-g63eea2d31-dirty")
	assert.NotEqual(t, a, b, "two versions differing only in their tail must not elide to one string")
	assert.LessOrEqual(t, len(a), 22, "the value must fit the aligned column")
	assert.Contains(t, a, "g594d75a08", "the distinguishing part survives")
}

func TestShortRev(t *testing.T) {
	assert.Equal(t, "594d75a08a35", shortRev("594d75a08a358dfc080de4afbee71872f50a7390"))
	assert.Equal(t, "abc", shortRev("abc"), "a value already short is passed through")
	assert.Empty(t, shortRev(""), "an unrecorded revision stays empty rather than becoming a rendered blank")
}

func TestFilterPathsIsCaseInsensitiveSubstring(t *testing.T) {
	items := []string{"cmd/magus/diff.go", "internal/CACHE/log.go", "types/diff.go"}

	// An empty filter returns the input itself rather than a copy: the picker shows
	// everything before a keystroke arrives.
	assert.Equal(t, items, filterPaths(items, ""))
	assert.Equal(t, items, filterPaths(items, "   "))

	assert.Equal(t, []string{"cmd/magus/diff.go", "types/diff.go"}, filterPaths(items, "DIFF"))
	assert.Equal(t, []string{"internal/CACHE/log.go"}, filterPaths(items, "cache"))
	assert.Nil(t, filterPaths(items, "nothing-matches"))
}
