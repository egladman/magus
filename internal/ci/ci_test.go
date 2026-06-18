package ci

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeProjects builds a []*types.Project from paths for use in Build calls.
func makeProjects(paths ...string) []*types.Project {
	ps := make([]*types.Project, len(paths))
	for i, p := range paths {
		ps[i] = &types.Project{Path: p}
	}
	return ps
}

func shardSizes(shards []Shard) []int {
	sizes := make([]int, len(shards))
	for i, s := range shards {
		sizes[i] = len(s.Projects)
	}
	return sizes
}

func TestBuild_empty(t *testing.T) {
	t.Parallel()
	plan, err := Build(nil, "test")
	require.NoError(t, err)
	assert.Empty(t, plan.Shards)
}

func TestBuild_oneProject(t *testing.T) {
	t.Parallel()
	plan, err := Build(makeProjects("a"), "test", WithMaxShards(8))
	require.NoError(t, err)
	require.Len(t, plan.Shards, 1)
	assert.Equal(t, "0", plan.Shards[0].ID)
}

func TestBuild_exactlyCap(t *testing.T) {
	t.Parallel()
	paths := make([]string, 8)
	for i := range paths {
		paths[i] = string(rune('a' + i))
	}
	plan, err := Build(makeProjects(paths...), "test", WithMaxShards(8))
	require.NoError(t, err)
	require.Len(t, plan.Shards, 8)
	for _, s := range plan.Shards {
		assert.Lenf(t, s.Projects, 1, "want 1 project per shard in shard %s", s.ID)
	}
}

func TestBuild_oneOverCap(t *testing.T) {
	t.Parallel()
	// 9 projects, cap=8 → 8 shards, first shard gets 2, rest get 1
	paths := make([]string, 9)
	for i := range paths {
		paths[i] = string(rune('a' + i))
	}
	plan, err := Build(makeProjects(paths...), "test", WithMaxShards(8))
	require.NoError(t, err)
	require.Len(t, plan.Shards, 8)
	assert.Equal(t, []int{2, 1, 1, 1, 1, 1, 1, 1}, shardSizes(plan.Shards))
}

func TestBuild_100projects(t *testing.T) {
	t.Parallel()
	paths := make([]string, 100)
	for i := range paths {
		paths[i] = string(rune(i + 1)) // non-zero rune
	}
	plan, err := Build(makeProjects(paths...), "test", WithMaxShards(8))
	require.NoError(t, err)
	require.Len(t, plan.Shards, 8)
	assert.Equal(t, []int{13, 13, 13, 13, 12, 12, 12, 12}, shardSizes(plan.Shards))
}

func TestBuild_invalidMaxShards_zero(t *testing.T) {
	t.Parallel()
	_, err := Build(makeProjects("a"), "test", WithMaxShards(0))
	assert.Error(t, err)
}

func TestBuild_invalidMaxShards_negativeTwoOrLess(t *testing.T) {
	t.Parallel()
	for _, n := range []int{-2, -3, -100} {
		_, err := Build(makeProjects("a"), "test", WithMaxShards(n))
		assert.Errorf(t, err, "expected error for WithMaxShards(%d)", n)
	}
}

func TestBuild_unlimited(t *testing.T) {
	t.Parallel()
	// -1 = unlimited: 5 projects → 5 shards
	paths := []string{"a", "b", "c", "d", "e"}
	plan, err := Build(makeProjects(paths...), "test", WithMaxShards(-1))
	require.NoError(t, err)
	assert.Len(t, plan.Shards, 5)
}

func TestBuild_clampAboveHardCeiling(t *testing.T) {
	t.Parallel()
	// 500 > hardCeiling(256): should clamp to 256, not error.
	plan, err := Build(makeProjects("a"), "test", WithMaxShards(500))
	require.NoError(t, err)
	assert.Len(t, plan.Shards, 1)
}

func TestBuild_IDWidth(t *testing.T) {
	t.Parallel()

	// IDs "0".."7" — last=7, 1 digit
	t.Run("maxShards=8 width=1", func(t *testing.T) {
		assertIDWidth(t, 8, 1)
	})
	// IDs "0".."9" — last=9, 1 digit
	t.Run("maxShards=10 width=1", func(t *testing.T) {
		assertIDWidth(t, 10, 1)
	})
	// IDs "00".."99" — last=99, 2 digits
	t.Run("maxShards=100 width=2", func(t *testing.T) {
		assertIDWidth(t, 100, 2)
	})
	// IDs "000".."255" — last=255, 3 digits
	t.Run("maxShards=256 width=3", func(t *testing.T) {
		assertIDWidth(t, 256, 3)
	})
}

func assertIDWidth(t *testing.T, maxShards, wantWidth int) {
	t.Helper()
	paths := make([]string, maxShards)
	for i := range paths {
		paths[i] = string(rune(i + 1))
	}
	plan, err := Build(makeProjects(paths...), "test", WithMaxShards(maxShards))
	require.NoError(t, err)
	for _, s := range plan.Shards {
		assert.Lenf(t, s.ID, wantWidth, "maxShards=%d: ID %q", maxShards, s.ID)
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

	plan, err := Build(projects, "test", WithMaxShards(8), WithForecaster(f))
	require.NoError(t, err)
	assert.Equal(t, 1, f.calls)
	require.Len(t, plan.Shards, 1)
	assert.Len(t, plan.Shards[0].Projects, 4)
}

func TestBuild_withForecaster_emptyResultFallsBack(t *testing.T) {
	t.Parallel()
	projects := makeProjects("a", "b")
	// Misbehaving forecaster: returns empty for non-empty input.
	f := &stubForecaster{override: nil}

	plan, err := Build(projects, "test", WithMaxShards(8), WithForecaster(f))
	require.NoError(t, err)
	// Defensive fallback to ceil-division: 2 projects, max 8 shards → 2 shards.
	assert.Len(t, plan.Shards, 2)
}

func TestBuild_sourcePassthrough(t *testing.T) {
	t.Parallel()
	plan, err := Build(makeProjects("a"), "git diff vs origin/main")
	require.NoError(t, err)
	assert.Equal(t, "git diff vs origin/main", plan.Source)
}
