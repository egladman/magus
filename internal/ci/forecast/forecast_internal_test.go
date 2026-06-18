package forecast

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptimalShardCount(t *testing.T) {
	t.Parallel()

	t.Run("trivial PR triggers circuit breaker", func(t *testing.T) {
		assert.Equal(t, 1, optimalShardCount(30_000, 30_000, 5_000, 8))
	})
	t.Run("zero work", func(t *testing.T) {
		assert.Equal(t, 1, optimalShardCount(0, 30_000, 5_000, 8))
	})
	t.Run("alpha=0 means fan out fully", func(t *testing.T) {
		assert.Equal(t, 8, optimalShardCount(1_000_000, 30_000, 0, 8))
	})
	t.Run("sqrt picks middle when both substantial", func(t *testing.T) {
		assert.Equal(t, 10, optimalShardCount(500_000, 30_000, 5_000, 16))
	})
	t.Run("big work clamps to maxN", func(t *testing.T) {
		assert.Equal(t, 8, optimalShardCount(100_000_000, 30_000, 1_000, 8))
	})
	t.Run("maxN floor of 1", func(t *testing.T) {
		assert.Equal(t, 1, optimalShardCount(60_000, 30_000, 5_000, 0))
	})
}

func testProjects(paths ...string) []*types.Project {
	out := make([]*types.Project, len(paths))
	for i, p := range paths {
		out[i] = &types.Project{Path: p}
	}
	return out
}

func TestLPT_balancesByDuration(t *testing.T) {
	t.Parallel()
	ps := testProjects("slow1", "slow2", "slow3", "fast1", "fast2", "fast3")
	durs := []int64{60_000, 60_000, 60_000, 1_000, 1_000, 1_000}

	shards := lpt(ps, durs, 3)
	require.Len(t, shards, 3)
	for i, s := range shards {
		slow := 0
		for _, p := range s {
			if p.Path == "slow1" || p.Path == "slow2" || p.Path == "slow3" {
				slow++
			}
		}
		assert.Equalf(t, 1, slow, "shard %d should have exactly 1 slow project", i)
	}
}

func TestLPT_emptyAndDegenerate(t *testing.T) {
	t.Parallel()

	assert.Nil(t, lpt(nil, nil, 4))

	ps := testProjects("a")
	got := lpt(ps, []int64{1_000}, 8)
	assert.Len(t, got, 1, "1 project, 8 shards → 1 shard (empty shards pruned)")
}
