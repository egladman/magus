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
