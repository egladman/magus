package forecast

import (
	"testing"

	"github.com/egladman/magus/types"
)

func TestOptimalShardCount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                     string
		workMs, setupMs, alphaMs Millis
		maxN                     int
		want                     int
	}{
		{"trivial PR triggers circuit breaker", 30_000, 30_000, 5_000, 8, 1},
		{"zero work", 0, 30_000, 5_000, 8, 1},
		{"alpha=0 means fan out fully", 1_000_000, 30_000, 0, 8, 8},
		{"sqrt picks middle when both substantial", 500_000, 30_000, 5_000, 16, 10},
		{"big work clamps to maxN", 100_000_000, 30_000, 1_000, 8, 8},
		{"maxN floor of 1", 60_000, 30_000, 5_000, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := optimalShardCount(tt.workMs, tt.setupMs, tt.alphaMs, tt.maxN)
			if got != tt.want {
				t.Errorf("optimalShardCount(W=%d, setup=%d, α=%d, maxN=%d) = %d, want %d",
					tt.workMs, tt.setupMs, tt.alphaMs, tt.maxN, got, tt.want)
			}
		})
	}
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
	if len(shards) != 3 {
		t.Fatalf("want 3 shards, got %d", len(shards))
	}
	for i, s := range shards {
		slow := 0
		for _, p := range s {
			if p.Path == "slow1" || p.Path == "slow2" || p.Path == "slow3" {
				slow++
			}
		}
		if slow != 1 {
			t.Errorf("shard %d has %d slow projects, want exactly 1", i, slow)
		}
	}
}

func TestLPT_emptyAndDegenerate(t *testing.T) {
	t.Parallel()

	if got := lpt(nil, nil, 4); got != nil {
		t.Errorf("lpt(nil) = %v, want nil", got)
	}

	ps := testProjects("a")
	got := lpt(ps, []int64{1_000}, 8)
	if len(got) != 1 {
		t.Fatalf("lpt(1 project, 8 shards) returned %d shards, want 1 (empty shards pruned)", len(got))
	}
}
