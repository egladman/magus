package ci

import (
	"fmt"
	"testing"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

type BuildSuite struct{ suite.Suite }

func TestBuildSuite(t *testing.T) { suite.Run(t, new(BuildSuite)) }

func units(n int) []types.Unit {
	out := make([]types.Unit, n)
	for i := range out {
		out[i] = types.Unit{Project: fmt.Sprintf("p%03d", i), Target: "test"}
	}
	return out
}

func shardSizes(shards []Shard) []int {
	sizes := make([]int, len(shards))
	for i, s := range shards {
		sizes[i] = len(s.Units)
	}
	return sizes
}

func (s *BuildSuite) TestEmptyPlansNothing() {
	plan, err := Build(nil, "test")
	s.Require().NoError(err)
	s.Empty(plan.Shards)
}

func (s *BuildSuite) TestOneUnitIsOneShard() {
	plan, err := Build(units(1), "test", WithMaxShards(8))
	s.Require().NoError(err)
	s.Require().Len(plan.Shards, 1)
	s.Equal("1a", plan.Shards[0].ID)
}

func (s *BuildSuite) TestOneOverTheCapDoublesTheFirstShard() {
	plan, err := Build(units(9), "test", WithMaxShards(8))
	s.Require().NoError(err)
	s.Equal([]int{2, 1, 1, 1, 1, 1, 1, 1}, shardSizes(plan.Shards))
}

func (s *BuildSuite) TestZeroMaxShardsIsAnError() {
	_, err := Build(units(1), "test", WithMaxShards(0))
	s.ErrorIs(err, ErrInvalidMaxShards)
}

func (s *BuildSuite) TestNegativeMaxShardsBelowUnlimitedIsAnError() {
	_, err := Build(units(1), "test", WithMaxShards(-2))
	s.ErrorIs(err, ErrInvalidMaxShards)
}

func (s *BuildSuite) TestUnlimitedIsOneShardPerUnit() {
	plan, err := Build(units(5), "test", WithMaxShards(-1))
	s.Require().NoError(err)
	s.Len(plan.Shards, 5)
}

func (s *BuildSuite) TestMaxShardsClampsToTheUnitCount() {
	plan, err := Build(units(1), "test", WithMaxShards(500))
	s.Require().NoError(err)
	s.Len(plan.Shards, 1)
}

func (s *BuildSuite) TestIDsTakeOneLetterUpTo26Shards() {
	plan, err := Build(units(26), "test", WithMaxShards(26))
	s.Require().NoError(err)
	s.Equal("1a", plan.Shards[0].ID)
	s.Equal("1z", plan.Shards[25].ID)
}

func (s *BuildSuite) TestIDsPadToTheLastIndex() {
	plan, err := Build(units(100), "test", WithMaxShards(100))
	s.Require().NoError(err)
	s.Equal("1aa", plan.Shards[0].ID)
	s.Equal("1az", plan.Shards[25].ID)
	s.Equal("1ba", plan.Shards[26].ID)
	s.Equal("1dv", plan.Shards[99].ID)
}

func (s *BuildSuite) TestAModelReplacesCeilDivision() {
	us := units(4)
	m := forecast.Model{
		History:           forecast.History{Constants: forecast.Constants{SetupP50Ms: forecast.Millis(600_000)}},
		MemoryBudgetBytes: 1 << 62,
	}

	plan, err := Build(us, "test", WithMaxShards(8), WithModel(&m))
	s.Require().NoError(err)
	s.Equal([]int{4}, shardSizes(plan.Shards))
}

func (s *BuildSuite) TestAnUnlimitedRequestPastTheCeilingSaysItWasCapped() {
	plan, err := Build(units(hardCeiling+1), "test", WithMaxShards(-1))
	s.Require().NoError(err)
	s.Len(plan.Shards, hardCeiling)
	s.Len(plan.Fallbacks, 1)
	s.Contains(plan.Fallbacks[0].What, "At most 256 shards")
}

func (s *BuildSuite) TestARequestUnderTheCeilingFallsBackOnNothing() {
	plan, err := Build(units(5), "test", WithMaxShards(-1))
	s.Require().NoError(err)
	s.Empty(plan.Fallbacks)
}

type PlacedOnceSuite struct{ suite.Suite }

func TestPlacedOnceSuite(t *testing.T) { suite.Run(t, new(PlacedOnceSuite)) }

func (s *PlacedOnceSuite) TestAnEmptyAssignmentIsAnError() {
	s.ErrorContains(placedOnce(units(2), nil, 2), "dropped p000:test")
}

func (s *PlacedOnceSuite) TestAssignmentThatDropsAUnitIsAnError() {
	us := units(2)
	s.ErrorContains(placedOnce(us, [][]types.Unit{us[:1]}, 2), "dropped p001:test")
}

func (s *PlacedOnceSuite) TestAssignmentThatRepeatsAUnitIsAnError() {
	us := units(2)
	s.ErrorContains(placedOnce(us, [][]types.Unit{us, us[:1]}, 2), "placed p000:test on shards 0 and 1")
}

func (s *PlacedOnceSuite) TestAssignmentThatInventsAUnitIsAnError() {
	us := units(1)
	stray := types.Unit{Project: "x", Target: "lint"}
	s.ErrorContains(placedOnce(us, [][]types.Unit{{us[0], stray}}, 1), "placed x:lint, which is not a unit")
}

func (s *PlacedOnceSuite) TestAssignmentWithAnEmptyShardIsAnError() {
	us := units(2)
	s.ErrorContains(placedOnce(us, [][]types.Unit{us, {}}, 2), "left shard 1 empty")
}

func (s *PlacedOnceSuite) TestAssignmentOverTheLimitIsAnError() {
	us := units(2)
	s.ErrorContains(placedOnce(us, [][]types.Unit{us[:1], us[1:], us[:0]}, 2), "opened 3 shards, over the limit of 2")
}

func (s *BuildSuite) TestSourcePassesThrough() {
	plan, err := Build(units(1), "git diff vs origin/main")
	s.Require().NoError(err)
	s.Equal("git diff vs origin/main", plan.Source)
}

// StageZeroSuite plans a 200s lint and a 200s test that both need a 10s
// build, so they split and the build runs once ahead of them.
type StageZeroSuite struct {
	suite.Suite
	model forecast.Model
	build types.Unit
	lint  types.Unit
	test  types.Unit
}

func TestStageZeroSuite(t *testing.T) { suite.Run(t, new(StageZeroSuite)) }

func (s *StageZeroSuite) SetupTest() {
	s.build = types.Unit{Project: ".", Target: "build"}
	s.lint = types.Unit{Project: ".", Target: "lint"}
	s.test = types.Unit{Project: ".", Target: "test"}
	s.model = forecast.Model{
		History: forecast.History{
			Constants: forecast.Constants{SetupP50Ms: forecast.Millis(1_000)},
			Projects: map[string]map[string]forecast.Stats{".": {
				"build": {P75Ms: 10_000, Samples: 50},
				"lint":  {P75Ms: 200_000, Samples: 50},
				"test":  {P75Ms: 200_000, Samples: 50},
			}},
		},
		MemoryBudgetBytes: 1 << 62,
		Needs: func(u types.Unit) []types.Unit {
			if u == s.build {
				return []types.Unit{u}
			}
			return []types.Unit{u, s.build}
		},
	}
}

func (s *StageZeroSuite) TestASharedPrerequisiteIsPlannedOnceInStageZero() {
	plan, err := Build([]types.Unit{s.lint, s.test}, "test", WithMaxShards(8), WithModel(&s.model))
	s.Require().NoError(err)
	s.Equal(Plan{
		StageZero: []Shard{{ID: "0a", Units: []types.Unit{s.build}}},
		Shards:    []Shard{{ID: "1a", Units: []types.Unit{s.lint}}, {ID: "1b", Units: []types.Unit{s.test}}},
		Source:    "test",
	}, plan)
}

func (s *StageZeroSuite) TestAUnitAnotherNeedsMovesToStageZero() {
	plan, err := Build([]types.Unit{s.build, s.lint, s.test}, "test", WithMaxShards(8), WithModel(&s.model))
	s.Require().NoError(err)
	s.Equal(Plan{
		StageZero: []Shard{{ID: "0a", Units: []types.Unit{s.build}}},
		Shards:    []Shard{{ID: "1a", Units: []types.Unit{s.lint}}, {ID: "1b", Units: []types.Unit{s.test}}},
		Source:    "test",
	}, plan)
}

func (s *StageZeroSuite) TestAPlanWithNoSharedWorkIsTheSingleStagePlan() {
	s.model.Needs = nil
	us := []types.Unit{s.lint, s.test}
	plan, err := Build(us, "test", WithMaxShards(8), WithModel(&s.model))
	s.Require().NoError(err)
	s.Equal(Plan{Shards: numbered(1, s.model.Plan(us, 2)), Source: "test"}, plan)
}

func (s *StageZeroSuite) TestAReplayedPrerequisiteNeedsNoStageZero() {
	s.model.Repeatable = func(u types.Unit) bool { return u == s.build }
	plan, err := Build([]types.Unit{s.lint, s.test}, "test", WithMaxShards(8), WithModel(&s.model))
	s.Require().NoError(err)
	s.Equal(Plan{
		Shards: []Shard{{ID: "1a", Units: []types.Unit{s.lint}}, {ID: "1b", Units: []types.Unit{s.test}}},
		Source: "test",
	}, plan)
}

type UncrossedSuite struct {
	suite.Suite
	f forecast.Model
}

func TestUncrossedSuite(t *testing.T) { suite.Run(t, new(UncrossedSuite)) }

func (s *UncrossedSuite) SetupTest() {
	edges := map[types.Unit][]types.Unit{
		{Project: "docs", Target: "build"}: {{Project: "docs", Target: "build"}, {Project: ".", Target: "build"}},
	}
	s.f = forecast.Model{Needs: func(u types.Unit) []types.Unit { return edges[u] }}
}

func (s *UncrossedSuite) TestAStageOneComputingAnotherProjectsPrerequisiteRefusesWithMGS3054() {
	err := uncrossed(s.f, []types.Unit{{Project: "docs", Target: "build"}})
	s.Require().ErrorIs(err, types.ShardCrossesProjects)
	s.Contains(err.Error(), ".:build")
}

func (s *UncrossedSuite) TestAStageOneReplayingWhatStageZeroComputedPasses() {
	f := s.f.Replaying([]types.Unit{{Project: ".", Target: "build"}})
	s.NoError(uncrossed(f, []types.Unit{{Project: "docs", Target: "build"}}))
}
