package main

import (
	"testing"

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
	if got[0] != "apps/web/dashboard" {
		t.Fatalf("expected leaf-match first, got %v", got)
	}
}

func TestScoreProjects_PrefixBeatsInfix(t *testing.T) {
	all := mkProjects(
		"apps/web/my-dashboard",
		"apps/web/dashboard",
	)
	got := paths(interactive.ScoreProjects(all, []string{"dash"}))
	if got[0] != "apps/web/dashboard" {
		t.Fatalf("expected prefix-on-leaf first, got %v", got)
	}
}

func TestScoreProjects_AND(t *testing.T) {
	all := mkProjects(
		"apps/web/dashboard",
		"apps/mobile/dashboard",
		"services/api",
	)
	got := paths(interactive.ScoreProjects(all, []string{"dash", "mobile"}))
	if len(got) != 1 || got[0] != "apps/mobile/dashboard" {
		t.Fatalf("AND filter wrong: %v", got)
	}
}

func TestScoreProjects_NoFilters(t *testing.T) {
	all := mkProjects("c", "a", "b")
	got := paths(interactive.ScoreProjects(all, nil))
	want := []string{"a", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected alphabetical %v, got %v", want, got)
		}
	}
}

func TestScoreProjects_NoMatchEmpty(t *testing.T) {
	all := mkProjects("apps/web/dashboard")
	got := interactive.ScoreProjects(all, []string{"zzznope"})
	if len(got) != 0 {
		t.Fatalf("expected empty result, got %v", paths(got))
	}
}

func TestLeafScore_QueryNotInPath(t *testing.T) {
	if interactive.LeafScore("apps/web/dashboard", "zzz") != 0 {
		t.Fatal("non-matching query should score 0")
	}
}

func TestLeafScore_DenserOnShorterLeaf(t *testing.T) {
	short := interactive.LeafScore("apps/web/dash", "dash")
	long := interactive.LeafScore("apps/web/dashboard", "dash")
	if short <= long {
		t.Fatalf("denser match (%d) should beat sparser (%d)", short, long)
	}
}
