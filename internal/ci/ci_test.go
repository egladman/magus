package ci_test

import (
	"testing"

	"github.com/egladman/magus/internal/ci"
	"github.com/egladman/magus/types"
)

// makeProjects builds a []*types.Project from paths for use in Build calls.
func makeProjects(paths ...string) []*types.Project {
	ps := make([]*types.Project, len(paths))
	for i, p := range paths {
		ps[i] = &types.Project{Path: p}
	}
	return ps
}

func shardSizes(shards []ci.Shard) []int {
	sizes := make([]int, len(shards))
	for i, s := range shards {
		sizes[i] = len(s.Projects)
	}
	return sizes
}

func TestBuild_empty(t *testing.T) {
	t.Parallel()
	plan, err := ci.Build(nil, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Shards) != 0 {
		t.Fatalf("want 0 shards, got %d", len(plan.Shards))
	}
}

func TestBuild_oneProject(t *testing.T) {
	t.Parallel()
	plan, err := ci.Build(makeProjects("a"), "test", ci.WithMaxShards(8))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Shards) != 1 {
		t.Fatalf("want 1 shard, got %d", len(plan.Shards))
	}
	if plan.Shards[0].ID != "0" {
		t.Fatalf("want ID '0', got %q", plan.Shards[0].ID)
	}
}

func TestBuild_exactlyCap(t *testing.T) {
	t.Parallel()
	paths := make([]string, 8)
	for i := range paths {
		paths[i] = string(rune('a' + i))
	}
	plan, err := ci.Build(makeProjects(paths...), "test", ci.WithMaxShards(8))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Shards) != 8 {
		t.Fatalf("want 8 shards, got %d", len(plan.Shards))
	}
	for _, s := range plan.Shards {
		if len(s.Projects) != 1 {
			t.Fatalf("want 1 project per shard, got %d in shard %s", len(s.Projects), s.ID)
		}
	}
}

func TestBuild_oneOverCap(t *testing.T) {
	t.Parallel()
	// 9 projects, cap=8 → 8 shards, first shard gets 2, rest get 1
	paths := make([]string, 9)
	for i := range paths {
		paths[i] = string(rune('a' + i))
	}
	plan, err := ci.Build(makeProjects(paths...), "test", ci.WithMaxShards(8))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Shards) != 8 {
		t.Fatalf("want 8 shards, got %d", len(plan.Shards))
	}
	sizes := shardSizes(plan.Shards)
	want := []int{2, 1, 1, 1, 1, 1, 1, 1}
	for i, s := range sizes {
		if s != want[i] {
			t.Fatalf("shard %d: want size %d, got %d", i, want[i], s)
		}
	}
}

func TestBuild_100projects(t *testing.T) {
	t.Parallel()
	paths := make([]string, 100)
	for i := range paths {
		paths[i] = string(rune(i + 1)) // non-zero rune
	}
	plan, err := ci.Build(makeProjects(paths...), "test", ci.WithMaxShards(8))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Shards) != 8 {
		t.Fatalf("want 8 shards, got %d", len(plan.Shards))
	}
	sizes := shardSizes(plan.Shards)
	want := []int{13, 13, 13, 13, 12, 12, 12, 12}
	for i, s := range sizes {
		if s != want[i] {
			t.Fatalf("shard %d: want size %d, got %d", i, want[i], s)
		}
	}
}

func TestBuild_invalidMaxShards_zero(t *testing.T) {
	t.Parallel()
	_, err := ci.Build(makeProjects("a"), "test", ci.WithMaxShards(0))
	if err == nil {
		t.Fatal("expected error for WithMaxShards(0), got nil")
	}
}

func TestBuild_invalidMaxShards_negativeTwoOrLess(t *testing.T) {
	t.Parallel()
	for _, n := range []int{-2, -3, -100} {
		_, err := ci.Build(makeProjects("a"), "test", ci.WithMaxShards(n))
		if err == nil {
			t.Fatalf("expected error for WithMaxShards(%d), got nil", n)
		}
	}
}

func TestBuild_unlimited(t *testing.T) {
	t.Parallel()
	// -1 = unlimited: 5 projects → 5 shards
	paths := []string{"a", "b", "c", "d", "e"}
	plan, err := ci.Build(makeProjects(paths...), "test", ci.WithMaxShards(-1))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Shards) != 5 {
		t.Fatalf("want 5 shards (unlimited), got %d", len(plan.Shards))
	}
}

func TestBuild_clampAboveHardCeiling(t *testing.T) {
	t.Parallel()
	// 500 > hardCeiling(256): should clamp to 256, not error.
	plan, err := ci.Build(makeProjects("a"), "test", ci.WithMaxShards(500))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Shards) != 1 {
		t.Fatalf("want 1 shard (only 1 project), got %d", len(plan.Shards))
	}
}

func TestBuild_IDWidth(t *testing.T) {
	t.Parallel()
	tests := []struct {
		maxShards int
		wantWidth int
	}{
		{8, 1},   // IDs "0".."7"   — last=7, 1 digit
		{10, 1},  // IDs "0".."9"   — last=9, 1 digit
		{100, 2}, // IDs "00".."99" — last=99, 2 digits
		{256, 3}, // IDs "000".."255" — last=255, 3 digits
	}
	for _, tt := range tests {
		paths := make([]string, tt.maxShards)
		for i := range paths {
			paths[i] = string(rune(i + 1))
		}
		plan, err := ci.Build(makeProjects(paths...), "test", ci.WithMaxShards(tt.maxShards))
		if err != nil {
			t.Fatalf("maxShards=%d: unexpected error: %v", tt.maxShards, err)
		}
		for _, s := range plan.Shards {
			if len(s.ID) != tt.wantWidth {
				t.Fatalf("maxShards=%d: want ID width %d, got %q (len %d)",
					tt.maxShards, tt.wantWidth, s.ID, len(s.ID))
			}
		}
	}
}

// stubForecaster is a ci.Forecaster that always returns a fixed
// partition. Used to verify Build delegates correctly when the
// forecaster option is set.
type stubForecaster struct {
	calls    int
	maxSeen  int
	override [][]*types.Project
}

func (s *stubForecaster) Plan(projects []*types.Project, maxShards int) [][]*types.Project {
	s.calls++
	s.maxSeen = maxShards
	return s.override
}

func TestBuild_withForecaster_replacesCeilDivision(t *testing.T) {
	t.Parallel()
	projects := makeProjects("a", "b", "c", "d")
	// Forecaster groups everything into one shard regardless of count.
	f := &stubForecaster{override: [][]*types.Project{projects}}

	plan, err := ci.Build(projects, "test", ci.WithMaxShards(8), ci.WithForecaster(f))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if f.calls != 1 {
		t.Fatalf("forecaster.Plan called %d times, want 1", f.calls)
	}
	if len(plan.Shards) != 1 {
		t.Fatalf("want 1 shard from forecaster, got %d", len(plan.Shards))
	}
	if len(plan.Shards[0].Projects) != 4 {
		t.Fatalf("want all 4 projects in single shard, got %d", len(plan.Shards[0].Projects))
	}
}

func TestBuild_withForecaster_emptyResultFallsBack(t *testing.T) {
	t.Parallel()
	projects := makeProjects("a", "b")
	// Misbehaving forecaster: returns empty for non-empty input.
	f := &stubForecaster{override: nil}

	plan, err := ci.Build(projects, "test", ci.WithMaxShards(8), ci.WithForecaster(f))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Defensive fallback to ceil-division: 2 projects, max 8 shards → 2 shards.
	if len(plan.Shards) != 2 {
		t.Fatalf("want 2 shards (ceil-division fallback), got %d", len(plan.Shards))
	}
}

func TestBuild_sourcePassthrough(t *testing.T) {
	t.Parallel()
	plan, err := ci.Build(makeProjects("a"), "git diff vs origin/main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Source != "git diff vs origin/main" {
		t.Fatalf("want source 'git diff vs origin/main', got %q", plan.Source)
	}
}
