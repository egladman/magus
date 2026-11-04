package ci

import (
	"fmt"
	"testing"

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
	s.Equal("0", plan.Shards[0].ID)
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

func (s *BuildSuite) TestIDsPadToTheLastIndex() {
	plan, err := Build(units(100), "test", WithMaxShards(100))
	s.Require().NoError(err)
	s.Equal("00", plan.Shards[0].ID)
	s.Equal("99", plan.Shards[99].ID)
}

func (s *BuildSuite) TestAForecasterReplacesCeilDivision() {
	us := units(4)
	f := NewMockForecaster(s.T())
	f.EXPECT().Plan(us, 4).Return([][]types.Unit{us}).Once()

	plan, err := Build(us, "test", WithMaxShards(8), WithForecaster(f))
	s.Require().NoError(err)
	s.Equal([]int{4}, shardSizes(plan.Shards))
}

func (s *BuildSuite) forecast(us []types.Unit, max int, assignments [][]types.Unit) error {
	f := NewMockForecaster(s.T())
	f.EXPECT().Plan(us, max).Return(assignments).Once()
	_, err := Build(us, "test", WithMaxShards(8), WithForecaster(f))
	return err
}

func (s *BuildSuite) TestAnEmptyForecastIsAnError() {
	us := units(2)
	s.ErrorContains(s.forecast(us, 2, nil), "dropped p000:test")
}

func (s *BuildSuite) TestAForecastThatDropsAUnitIsAnError() {
	us := units(2)
	s.ErrorContains(s.forecast(us, 2, [][]types.Unit{us[:1]}), "dropped p001:test")
}

func (s *BuildSuite) TestAForecastThatRepeatsAUnitIsAnError() {
	us := units(2)
	s.ErrorContains(s.forecast(us, 2, [][]types.Unit{us, us[:1]}), "placed p000:test on shards 0 and 1")
}

func (s *BuildSuite) TestAForecastThatInventsAUnitIsAnError() {
	us := units(1)
	stray := types.Unit{Project: "x", Target: "lint"}
	s.ErrorContains(s.forecast(us, 1, [][]types.Unit{{us[0], stray}}), "placed x:lint, which is not a unit")
}

func (s *BuildSuite) TestAForecastWithAnEmptyShardIsAnError() {
	us := units(2)
	s.ErrorContains(s.forecast(us, 2, [][]types.Unit{us, {}}), "left shard 1 empty")
}

func (s *BuildSuite) TestAForecastOverTheLimitIsAnError() {
	us := units(2)
	s.ErrorContains(s.forecast(us, 2, [][]types.Unit{us[:1], us[1:], us[:0]}), "opened 3 shards, over the limit of 2")
}

func (s *BuildSuite) TestSourcePassesThrough() {
	plan, err := Build(units(1), "git diff vs origin/main")
	s.Require().NoError(err)
	s.Equal("git diff vs origin/main", plan.Source)
}
